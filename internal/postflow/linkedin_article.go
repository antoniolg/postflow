package postflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	linkedInArticleHTMLReadLimit  = 2 << 20
	linkedInArticleImageReadLimit = 8 << 20
	linkedInArticleRedirectLimit  = 10
	linkedInArticleTitleMaxRunes  = 400
	linkedInArticleTextMaxRunes   = 4086
)

var (
	firstURLPattern = regexp.MustCompile(`https?://[^\s<>"']+`)
	metaTagPattern  = regexp.MustCompile(`(?is)<meta\b[^>]*>`)
	titleTagPattern = regexp.MustCompile(`(?is)<title\b[^>]*>(.*?)</title>`)
	attrPattern     = regexp.MustCompile(`(?is)([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*("([^"]*)"|'([^']*)'|([^\s"'=<>` + "`" + `]+))`)
	spacePattern    = regexp.MustCompile(`\s+`)
)

const linkedInTrailingURLTrim = ".,;:!?)]}\"'>"

type linkedInArticleMetadata struct {
	Source       string
	Title        string
	Description  string
	ImageURL     string
	ImageAlt     string
	ThumbnailURN string
	ThumbnailAlt string
}

func extractFirstURL(text string) string {
	matches := firstURLPattern.FindAllString(strings.TrimSpace(text), -1)
	for _, match := range matches {
		trimmed := strings.TrimRight(strings.TrimSpace(match), linkedInTrailingURLTrim)
		parsed, err := url.Parse(trimmed)
		if err != nil || parsed == nil {
			continue
		}
		scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
		if (scheme == "http" || scheme == "https") && strings.TrimSpace(parsed.Host) != "" {
			return trimmed
		}
	}
	return ""
}

func (p *LinkedInProvider) tryPublishArticlePost(ctx context.Context, accessToken, actorURN, postText string) (PublishResult, bool, error) {
	firstURL := extractFirstURL(postText)
	if firstURL == "" {
		return PublishResult{}, false, nil
	}
	meta, ok := p.fetchArticleMetadata(ctx, firstURL)
	if !ok || strings.TrimSpace(meta.Title) == "" {
		return PublishResult{}, false, nil
	}
	if strings.TrimSpace(meta.ImageURL) != "" {
		thumbnailURN, thumbnailAlt, err := p.uploadArticleThumbnail(ctx, actorURN, accessToken, meta.ImageURL)
		if err == nil && strings.TrimSpace(thumbnailURN) != "" {
			meta.ThumbnailURN = strings.TrimSpace(thumbnailURN)
			meta.ThumbnailAlt = firstNonEmpty(strings.TrimSpace(meta.ImageAlt), strings.TrimSpace(thumbnailAlt))
		}
	}
	result, err := p.publishArticlePost(ctx, actorURN, accessToken, postText, meta)
	if err != nil {
		return PublishResult{}, false, nil
	}
	return result, true, nil
}

func (p *LinkedInProvider) fetchArticleMetadata(ctx context.Context, rawURL string) (linkedInArticleMetadata, bool) {
	req, client, err := p.newArticleMetadataFetchRequest(ctx, rawURL, "text/html,application/xhtml+xml")
	if err != nil {
		return linkedInArticleMetadata{}, false
	}
	resp, err := client.Do(req)
	if err != nil {
		return linkedInArticleMetadata{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return linkedInArticleMetadata{}, false
	}
	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	if contentType != "" && !strings.Contains(contentType, "text/html") && !strings.Contains(contentType, "application/xhtml+xml") {
		return linkedInArticleMetadata{}, false
	}
	if resp.ContentLength > linkedInArticleHTMLReadLimit {
		return linkedInArticleMetadata{}, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, linkedInArticleHTMLReadLimit+1))
	if err != nil {
		return linkedInArticleMetadata{}, false
	}
	if len(body) > linkedInArticleHTMLReadLimit {
		return linkedInArticleMetadata{}, false
	}
	source := strings.TrimSpace(rawURL)
	if resp.Request != nil && resp.Request.URL != nil {
		source = strings.TrimSpace(resp.Request.URL.String())
	}
	title, description, imageURL, imageAlt := parseLinkedInArticleHTML(body)
	if strings.TrimSpace(source) == "" || strings.TrimSpace(title) == "" {
		return linkedInArticleMetadata{}, false
	}
	return linkedInArticleMetadata{
		Source:      source,
		Title:       truncateRunes(title, linkedInArticleTitleMaxRunes),
		Description: truncateRunes(description, linkedInArticleTextMaxRunes),
		ImageURL:    strings.TrimSpace(resolveLinkedInArticleURL(source, imageURL)),
		ImageAlt:    truncateRunes(imageAlt, linkedInArticleTextMaxRunes),
	}, true
}

func (p *LinkedInProvider) newArticleMetadataFetchRequest(ctx context.Context, rawURL, accept string) (*http.Request, *http.Client, error) {
	return p.newArticleFetchRequestWithDialGuard(ctx, rawURL, accept, true)
}

func parseLinkedInArticleHTML(body []byte) (title, description, imageURL, imageAlt string) {
	rawHTML := string(body)
	metaValues := make(map[string]string)
	for _, tag := range metaTagPattern.FindAllString(rawHTML, -1) {
		attrs := parseHTMLAttributes(tag)
		key := strings.ToLower(firstNonEmpty(attrs["property"], attrs["name"]))
		if key == "" {
			continue
		}
		content := normalizeHTMLText(attrs["content"])
		if content == "" {
			continue
		}
		if _, exists := metaValues[key]; !exists {
			metaValues[key] = content
		}
	}
	title = firstNonEmpty(
		metaValues["og:title"],
		metaValues["twitter:title"],
		parseHTMLTitle(rawHTML),
	)
	description = firstNonEmpty(
		metaValues["og:description"],
		metaValues["twitter:description"],
		metaValues["description"],
	)
	imageURL = firstNonEmpty(
		metaValues["og:image:secure_url"],
		metaValues["og:image"],
		metaValues["twitter:image"],
		metaValues["twitter:image:src"],
	)
	imageAlt = firstNonEmpty(
		metaValues["og:image:alt"],
		metaValues["twitter:image:alt"],
	)
	return strings.TrimSpace(title), strings.TrimSpace(description), strings.TrimSpace(imageURL), strings.TrimSpace(imageAlt)
}

func parseHTMLTitle(rawHTML string) string {
	match := titleTagPattern.FindStringSubmatch(rawHTML)
	if len(match) < 2 {
		return ""
	}
	return normalizeHTMLText(match[1])
}

func parseHTMLAttributes(tag string) map[string]string {
	out := make(map[string]string)
	for _, match := range attrPattern.FindAllStringSubmatch(tag, -1) {
		if len(match) < 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(match[1]))
		value := firstNonEmpty(match[3], match[4], match[5])
		out[key] = html.UnescapeString(strings.TrimSpace(value))
	}
	return out
}

func normalizeHTMLText(value string) string {
	unescaped := html.UnescapeString(strings.TrimSpace(value))
	return strings.TrimSpace(spacePattern.ReplaceAllString(unescaped, " "))
}

func resolveLinkedInArticleURL(baseURL, rawRef string) string {
	baseURL = strings.TrimSpace(baseURL)
	rawRef = strings.TrimSpace(rawRef)
	if baseURL == "" || rawRef == "" {
		return rawRef
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return rawRef
	}
	ref, err := url.Parse(rawRef)
	if err != nil {
		return rawRef
	}
	return base.ResolveReference(ref).String()
}

func (p *LinkedInProvider) uploadArticleThumbnail(ctx context.Context, ownerURN, accessToken, imageURL string) (string, string, error) {
	content, contentType, err := p.fetchArticleImage(ctx, imageURL)
	if err != nil {
		return "", "", err
	}
	registerPayload := map[string]any{
		"initializeUploadRequest": map[string]any{
			"owner": strings.TrimSpace(ownerURN),
		},
	}
	raw, _ := json.Marshal(registerPayload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.cfg.APIBaseURL, "/")+"/rest/images?action=initializeUpload", bytes.NewReader(raw))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("LinkedIn-Version", linkedInRESTVersion)
	req.Header.Set("X-Restli-Protocol-Version", "2.0.0")
	resp, err := p.client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("linkedin image initialize failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		Value struct {
			Image           string `json:"image"`
			UploadURL       string `json:"uploadUrl"`
			UploadMechanism map[string]struct {
				UploadURL string `json:"uploadUrl"`
			} `json:"uploadMechanism"`
		} `json:"value"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", "", err
	}
	imageURN := strings.TrimSpace(out.Value.Image)
	uploadURL := strings.TrimSpace(out.Value.UploadURL)
	if uploadURL == "" {
		for _, mechanism := range out.Value.UploadMechanism {
			uploadURL = strings.TrimSpace(mechanism.UploadURL)
			if uploadURL != "" {
				break
			}
		}
	}
	if imageURN == "" || uploadURL == "" {
		return "", "", fmt.Errorf("linkedin image initialize response missing upload data")
	}
	uploadReq, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(content))
	if err != nil {
		return "", "", err
	}
	if contentType != "" {
		uploadReq.Header.Set("Content-Type", contentType)
	}
	uploadResp, err := p.client.Do(uploadReq)
	if err != nil {
		return "", "", err
	}
	defer uploadResp.Body.Close()
	uploadBody, _ := io.ReadAll(io.LimitReader(uploadResp.Body, 2<<20))
	if uploadResp.StatusCode >= 300 {
		return "", "", fmt.Errorf("linkedin image upload failed: status=%d body=%s", uploadResp.StatusCode, strings.TrimSpace(string(uploadBody)))
	}
	return imageURN, "", nil
}

func (p *LinkedInProvider) fetchArticleImage(ctx context.Context, imageURL string) ([]byte, string, error) {
	req, client, err := p.newArticleFetchRequest(ctx, imageURL, "image/*")
	if err != nil {
		return nil, "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("thumbnail fetch status=%d", resp.StatusCode)
	}
	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	if contentType != "" && !strings.HasPrefix(contentType, "image/") {
		return nil, "", fmt.Errorf("thumbnail fetch returned non-image content type %q", contentType)
	}
	if resp.ContentLength > linkedInArticleImageReadLimit {
		return nil, "", fmt.Errorf("thumbnail fetch exceeds max size")
	}
	content, err := io.ReadAll(io.LimitReader(resp.Body, linkedInArticleImageReadLimit+1))
	if err != nil {
		return nil, "", err
	}
	if len(content) > linkedInArticleImageReadLimit {
		return nil, "", fmt.Errorf("thumbnail fetch exceeds max size")
	}
	if len(content) == 0 {
		return nil, "", fmt.Errorf("thumbnail fetch returned empty body")
	}
	if contentType == "" {
		contentType = http.DetectContentType(content)
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "image/") {
		return nil, "", fmt.Errorf("thumbnail content is not an image")
	}
	return content, contentType, nil
}

func (p *LinkedInProvider) newArticleFetchRequest(ctx context.Context, rawURL, accept string) (*http.Request, *http.Client, error) {
	return p.newArticleFetchRequestWithDialGuard(ctx, rawURL, accept, false)
}

func (p *LinkedInProvider) newArticleFetchRequestWithDialGuard(ctx context.Context, rawURL, accept string, guardDial bool) (*http.Request, *http.Client, error) {
	rawURL = strings.TrimSpace(rawURL)
	if !p.allowUnsafeArticleFetches {
		if err := validateLinkedInArticleFetchURL(ctx, rawURL); err != nil {
			return nil, nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	return req, p.articleFetchClient(guardDial), nil
}

func (p *LinkedInProvider) articleFetchClient(guardDial bool) *http.Client {
	if p.allowUnsafeArticleFetches {
		return p.client
	}
	base := p.client
	if base == nil {
		base = http.DefaultClient
	}
	client := *base
	if guardDial {
		client.Transport = safeLinkedInArticleTransport(base.Transport)
	}
	previousCheckRedirect := base.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= linkedInArticleRedirectLimit {
			return fmt.Errorf("article fetch stopped after %d redirects", linkedInArticleRedirectLimit)
		}
		if req == nil || req.URL == nil {
			return fmt.Errorf("article fetch redirect missing url")
		}
		if err := validateLinkedInArticleFetchURL(req.Context(), req.URL.String()); err != nil {
			return err
		}
		if previousCheckRedirect != nil {
			return previousCheckRedirect(req, via)
		}
		return nil
	}
	return &client
}

var (
	linkedInArticleLookupIPAddr = net.DefaultResolver.LookupIPAddr
	linkedInArticleDialContext  = (&net.Dialer{}).DialContext
)

func safeLinkedInArticleTransport(base http.RoundTripper) http.RoundTripper {
	transport, ok := base.(*http.Transport)
	if !ok || transport == nil {
		transport = http.DefaultTransport.(*http.Transport)
	}
	clone := transport.Clone()
	clone.DialContext = safeLinkedInArticleDialContext
	return clone
}

func safeLinkedInArticleDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addrs, err := linkedInArticleLookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, addr := range addrs {
		if isUnsafeLinkedInArticleIP(addr.IP) {
			lastErr = fmt.Errorf("article fetch host resolves to private address")
			continue
		}
		conn, err := linkedInArticleDialContext(ctx, network, net.JoinHostPort(addr.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("article fetch host has no resolved addresses")
}

func validateLinkedInArticleFetchURL(ctx context.Context, rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return err
	}
	if parsed == nil {
		return fmt.Errorf("article fetch url is empty")
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("article fetch url scheme %q is not allowed", parsed.Scheme)
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return fmt.Errorf("article fetch url host is empty")
	}
	return validateLinkedInArticleHost(ctx, host)
}

func validateLinkedInArticleHost(ctx context.Context, host string) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("article fetch url host is empty")
	}
	if ip := net.ParseIP(host); ip != nil {
		if isUnsafeLinkedInArticleIP(ip) {
			return fmt.Errorf("article fetch host resolves to private address")
		}
		return nil
	}
	addrs, err := linkedInArticleLookupIPAddr(ctx, host)
	if err != nil {
		return err
	}
	if len(addrs) == 0 {
		return fmt.Errorf("article fetch host has no resolved addresses")
	}
	for _, addr := range addrs {
		if isUnsafeLinkedInArticleIP(addr.IP) {
			return fmt.Errorf("article fetch host resolves to private address")
		}
	}
	return nil
}

func isUnsafeLinkedInArticleIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsUnspecified() ||
		ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast()
}

func (p *LinkedInProvider) publishArticlePost(ctx context.Context, actorURN, accessToken, postText string, meta linkedInArticleMetadata) (PublishResult, error) {
	if strings.TrimSpace(meta.Source) == "" || strings.TrimSpace(meta.Title) == "" {
		return PublishResult{}, fmt.Errorf("linkedin article metadata incomplete")
	}
	article := map[string]any{
		"source": strings.TrimSpace(meta.Source),
		"title":  truncateRunes(meta.Title, linkedInArticleTitleMaxRunes),
	}
	if desc := truncateRunes(meta.Description, linkedInArticleTextMaxRunes); desc != "" {
		article["description"] = desc
	}
	if thumbnailURN := strings.TrimSpace(meta.ThumbnailURN); thumbnailURN != "" {
		article["thumbnail"] = thumbnailURN
		if alt := truncateRunes(firstNonEmpty(meta.ThumbnailAlt, meta.ImageAlt), linkedInArticleTextMaxRunes); alt != "" {
			article["thumbnailAltText"] = alt
		}
	}
	payload := map[string]any{
		"author":     strings.TrimSpace(actorURN),
		"commentary": strings.TrimSpace(postText),
		"visibility": "PUBLIC",
		"distribution": map[string]any{
			"feedDistribution":               "MAIN_FEED",
			"targetEntities":                 []any{},
			"thirdPartyDistributionChannels": []any{},
		},
		"content": map[string]any{
			"article": article,
		},
		"lifecycleState":            "PUBLISHED",
		"isReshareDisabledByAuthor": false,
	}
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.cfg.APIBaseURL, "/")+"/rest/posts", bytes.NewReader(raw))
	if err != nil {
		return PublishResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("LinkedIn-Version", linkedInRESTVersion)
	req.Header.Set("X-Restli-Protocol-Version", "2.0.0")
	resp, err := p.client.Do(req)
	if err != nil {
		return PublishResult{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 300 {
		return PublishResult{}, fmt.Errorf("linkedin article publish failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	externalID := strings.TrimSpace(resp.Header.Get("x-restli-id"))
	if externalID == "" {
		var out struct {
			ID string `json:"id"`
		}
		if len(body) > 0 {
			_ = json.Unmarshal(body, &out)
		}
		externalID = strings.TrimSpace(out.ID)
	}
	if externalID == "" {
		externalID = fmt.Sprintf("linkedin_%d", time.Now().Unix())
	}
	return PublishResult{
		ExternalID:   externalID,
		PublishedURL: p.bestEffortLinkedInPermalink(ctx, accessToken, externalID),
	}, nil
}

func truncateRunes(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || value == "" {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return strings.TrimSpace(string(runes[:limit]))
}
