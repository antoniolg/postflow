package publisher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/antoniolg/publisher/internal/domain"
)

type LinkedInProviderConfig struct {
	ClientID     string
	ClientSecret string
	AuthBaseURL  string
	APIBaseURL   string
}

type LinkedInProvider struct {
	cfg    LinkedInProviderConfig
	client *http.Client
}

func NewLinkedInProvider(cfg LinkedInProviderConfig) *LinkedInProvider {
	if strings.TrimSpace(cfg.AuthBaseURL) == "" {
		cfg.AuthBaseURL = "https://www.linkedin.com"
	}
	if strings.TrimSpace(cfg.APIBaseURL) == "" {
		cfg.APIBaseURL = "https://api.linkedin.com"
	}
	return &LinkedInProvider{
		cfg:    cfg,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

func (p *LinkedInProvider) Platform() domain.Platform {
	return domain.PlatformLinkedIn
}

func (p *LinkedInProvider) ValidateDraft(_ context.Context, _ domain.SocialAccount, draft Draft) ([]string, error) {
	if len(draft.Media) > 9 {
		return nil, fmt.Errorf("linkedin supports up to 9 image attachments per post")
	}
	imageCount := 0
	videoCount := 0
	for _, media := range draft.Media {
		if isImageMedia(media) {
			imageCount++
			continue
		}
		if isVideoMedia(media) {
			videoCount++
			continue
		}
		return nil, fmt.Errorf("linkedin requires image or video media")
	}
	if videoCount > 1 {
		return nil, fmt.Errorf("linkedin supports a single video per post in this release")
	}
	if videoCount > 0 && imageCount > 0 {
		return nil, fmt.Errorf("linkedin does not support mixing images and video in this release")
	}
	return nil, nil
}

func (p *LinkedInProvider) Publish(ctx context.Context, account domain.SocialAccount, credentials Credentials, post domain.Post, opts PublishOptions) (string, error) {
	postText := formatPostTextForPublish(post.Text)
	token := strings.TrimSpace(credentials.AccessToken)
	if token == "" {
		return "", fmt.Errorf("linkedin access token missing")
	}
	memberID := strings.TrimSpace(account.ExternalAccountID)
	if memberID == "" {
		return "", fmt.Errorf("linkedin external account id is required")
	}
	if opts.Mode == PublishModeComment {
		if len(post.Media) > 0 {
			return "", fmt.Errorf("linkedin thread comments do not support media in this release")
		}
		parentExternalID := strings.TrimSpace(opts.ParentExternalID)
		if parentExternalID == "" {
			return "", fmt.Errorf("linkedin parent external id is required for comment mode")
		}
		return p.publishComment(ctx, token, memberID, parentExternalID, postText)
	}
	assetURNs := make([]string, 0, len(post.Media))
	videoCount := 0
	for _, media := range post.Media {
		if isVideoMedia(media) {
			videoCount++
		}
	}
	if videoCount > 1 {
		return "", fmt.Errorf("linkedin supports a single video per post in this release")
	}
	if videoCount > 0 && len(post.Media) > 1 {
		return "", fmt.Errorf("linkedin does not support mixing images and video in this release")
	}
	for _, media := range post.Media {
		var (
			assetURN string
			err      error
		)
		switch {
		case isImageMedia(media):
			assetURN, err = p.uploadAsset(ctx, memberID, token, media, "urn:li:digitalmediaRecipe:feedshare-image")
		case isVideoMedia(media):
			assetURN, err = p.uploadAsset(ctx, memberID, token, media, "urn:li:digitalmediaRecipe:feedshare-video")
		default:
			return "", fmt.Errorf("linkedin requires image or video media")
		}
		if err != nil {
			return "", err
		}
		assetURNs = append(assetURNs, assetURN)
	}
	shareCategory := "NONE"
	mediaPayload := make([]map[string]any, 0, len(assetURNs))
	if len(assetURNs) > 0 {
		shareCategory = "IMAGE"
		if videoCount > 0 {
			shareCategory = "VIDEO"
		}
		for _, urn := range assetURNs {
			mediaPayload = append(mediaPayload, map[string]any{
				"status": "READY",
				"media":  urn,
				"title":  map[string]any{"text": firstNonEmpty(strings.TrimSpace(postText), "LinkedIn media post")},
			})
		}
	}
	payload := map[string]any{
		"author":         "urn:li:person:" + memberID,
		"lifecycleState": "PUBLISHED",
		"specificContent": map[string]any{
			"com.linkedin.ugc.ShareContent": map[string]any{
				"shareCommentary":    map[string]any{"text": postText},
				"shareMediaCategory": shareCategory,
				"media":              mediaPayload,
			},
		},
		"visibility": map[string]any{"com.linkedin.ugc.MemberNetworkVisibility": "PUBLIC"},
	}
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.cfg.APIBaseURL, "/")+"/v2/ugcPosts", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Restli-Protocol-Version", "2.0.0")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("linkedin publish failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	externalID := strings.TrimSpace(resp.Header.Get("x-restli-id"))
	if externalID != "" {
		return externalID, nil
	}
	var out struct {
		ID string `json:"id"`
	}
	if len(body) > 0 {
		_ = json.Unmarshal(body, &out)
	}
	if strings.TrimSpace(out.ID) == "" {
		return fmt.Sprintf("linkedin_%d", time.Now().Unix()), nil
	}
	return strings.TrimSpace(out.ID), nil
}

func (p *LinkedInProvider) publishComment(ctx context.Context, accessToken, memberID, parentExternalID, text string) (string, error) {
	target := strings.TrimSpace(parentExternalID)
	if target == "" {
		return "", fmt.Errorf("linkedin comment target is required")
	}
	payload := map[string]any{
		"actor":   "urn:li:person:" + strings.TrimSpace(memberID),
		"message": map[string]any{"text": strings.TrimSpace(text)},
	}
	raw, _ := json.Marshal(payload)
	endpoint := strings.TrimRight(p.cfg.APIBaseURL, "/") + "/v2/socialActions/" + url.PathEscape(target) + "/comments"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Restli-Protocol-Version", "2.0.0")
	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("linkedin comment failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		ID string `json:"id"`
	}
	if len(body) > 0 {
		_ = json.Unmarshal(body, &out)
	}
	if strings.TrimSpace(out.ID) == "" {
		return fmt.Sprintf("linkedin_comment_%d", time.Now().Unix()), nil
	}
	return strings.TrimSpace(out.ID), nil
}

func (p *LinkedInProvider) uploadAsset(ctx context.Context, memberID, accessToken string, media domain.Media, recipe string) (string, error) {
	ownerURN := "urn:li:person:" + strings.TrimSpace(memberID)
	registerPayload := map[string]any{
		"registerUploadRequest": map[string]any{
			"owner":   ownerURN,
			"recipes": []string{strings.TrimSpace(recipe)},
			"serviceRelationships": []map[string]string{{
				"relationshipType": "OWNER",
				"identifier":       "urn:li:userGeneratedContent",
			}},
		},
	}
	raw, _ := json.Marshal(registerPayload)
	registerReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.cfg.APIBaseURL, "/")+"/v2/assets?action=registerUpload", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	registerReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	registerReq.Header.Set("Content-Type", "application/json")
	registerReq.Header.Set("X-Restli-Protocol-Version", "2.0.0")
	registerResp, err := p.client.Do(registerReq)
	if err != nil {
		return "", err
	}
	defer registerResp.Body.Close()
	registerBody, _ := io.ReadAll(io.LimitReader(registerResp.Body, 2<<20))
	if registerResp.StatusCode >= 300 {
		return "", fmt.Errorf("linkedin register upload failed: status=%d body=%s", registerResp.StatusCode, strings.TrimSpace(string(registerBody)))
	}
	var registerOut struct {
		Value struct {
			Asset           string `json:"asset"`
			UploadMechanism map[string]struct {
				UploadURL string `json:"uploadUrl"`
			} `json:"uploadMechanism"`
		} `json:"value"`
	}
	if err := json.Unmarshal(registerBody, &registerOut); err != nil {
		return "", err
	}
	assetURN := strings.TrimSpace(registerOut.Value.Asset)
	if assetURN == "" {
		return "", fmt.Errorf("linkedin register upload missing asset urn")
	}
	uploadURL := ""
	for _, mechanism := range registerOut.Value.UploadMechanism {
		uploadURL = strings.TrimSpace(mechanism.UploadURL)
		if uploadURL != "" {
			break
		}
	}
	if uploadURL == "" {
		return "", fmt.Errorf("linkedin register upload missing upload url")
	}

	content, contentType, err := readLinkedInMedia(media)
	if err != nil {
		return "", err
	}
	uploadReq, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(content))
	if err != nil {
		return "", err
	}
	if contentType != "" {
		uploadReq.Header.Set("Content-Type", contentType)
	}
	uploadResp, err := p.client.Do(uploadReq)
	if err != nil {
		return "", err
	}
	defer uploadResp.Body.Close()
	uploadBody, _ := io.ReadAll(io.LimitReader(uploadResp.Body, 2<<20))
	if uploadResp.StatusCode >= 300 {
		return "", fmt.Errorf("linkedin media upload failed: status=%d body=%s", uploadResp.StatusCode, strings.TrimSpace(string(uploadBody)))
	}
	return assetURN, nil
}

func readLinkedInMedia(media domain.Media) ([]byte, string, error) {
	path := strings.TrimSpace(media.StoragePath)
	if path == "" {
		return nil, "", fmt.Errorf("linkedin media %s has empty storage path", strings.TrimSpace(media.ID))
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	contentType := strings.TrimSpace(media.MimeType)
	if contentType == "" {
		ext := strings.ToLower(strings.TrimSpace(filepath.Ext(firstNonEmpty(strings.TrimSpace(media.OriginalName), filepath.Base(path)))))
		if ext != "" {
			contentType = strings.TrimSpace(mime.TypeByExtension(ext))
		}
	}
	if contentType == "" {
		contentType = http.DetectContentType(content)
	}
	return content, contentType, nil
}

func (p *LinkedInProvider) RefreshIfNeeded(ctx context.Context, _ domain.SocialAccount, credentials Credentials) (Credentials, bool, error) {
	if credentials.ExpiresAt == nil {
		return credentials, false, nil
	}
	if credentials.RefreshToken == "" {
		return credentials, false, nil
	}
	if credentials.ExpiresAt.After(time.Now().UTC().Add(5 * time.Minute)) {
		return credentials, false, nil
	}
	values := url.Values{}
	values.Set("grant_type", "refresh_token")
	values.Set("refresh_token", credentials.RefreshToken)
	values.Set("client_id", p.cfg.ClientID)
	values.Set("client_secret", p.cfg.ClientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.cfg.AuthBaseURL, "/")+"/oauth/v2/accessToken", strings.NewReader(values.Encode()))
	if err != nil {
		return credentials, false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.client.Do(req)
	if err != nil {
		return credentials, false, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 300 {
		return credentials, false, fmt.Errorf("linkedin refresh failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
		TokenType    string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return credentials, false, err
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" {
		return credentials, false, fmt.Errorf("linkedin refresh returned empty access token")
	}
	updated := credentials
	updated.AccessToken = strings.TrimSpace(tokenResp.AccessToken)
	if strings.TrimSpace(tokenResp.RefreshToken) != "" {
		updated.RefreshToken = strings.TrimSpace(tokenResp.RefreshToken)
	}
	if tokenResp.ExpiresIn > 0 {
		expiresAt := time.Now().UTC().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
		updated.ExpiresAt = &expiresAt
	}
	updated.Scope = strings.TrimSpace(tokenResp.Scope)
	updated.TokenType = strings.TrimSpace(tokenResp.TokenType)
	return updated, true, nil
}

func (p *LinkedInProvider) StartOAuth(_ context.Context, in OAuthStartInput) (OAuthStartOutput, error) {
	if strings.TrimSpace(p.cfg.ClientID) == "" || strings.TrimSpace(p.cfg.ClientSecret) == "" {
		return OAuthStartOutput{}, fmt.Errorf("linkedin oauth not configured")
	}
	values := url.Values{}
	values.Set("response_type", "code")
	values.Set("client_id", p.cfg.ClientID)
	values.Set("redirect_uri", in.RedirectURL)
	values.Set("state", in.State)
	values.Set("scope", "w_member_social openid profile")
	values.Set("prompt", "consent")
	return OAuthStartOutput{AuthURL: strings.TrimRight(p.cfg.AuthBaseURL, "/") + "/oauth/v2/authorization?" + values.Encode()}, nil
}

func (p *LinkedInProvider) HandleOAuthCallback(ctx context.Context, in OAuthCallbackInput) ([]ConnectedAccount, error) {
	values := url.Values{}
	values.Set("grant_type", "authorization_code")
	values.Set("code", in.Code)
	values.Set("redirect_uri", in.RedirectURL)
	values.Set("client_id", p.cfg.ClientID)
	values.Set("client_secret", p.cfg.ClientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.cfg.AuthBaseURL, "/")+"/oauth/v2/accessToken", strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("linkedin token exchange failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
		TokenType    string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, err
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" {
		return nil, fmt.Errorf("linkedin token exchange returned empty access token")
	}

	memberID, displayName, err := p.fetchMemberProfile(ctx, tokenResp.AccessToken)
	if err != nil {
		return nil, err
	}
	creds := Credentials{
		AccessToken:  strings.TrimSpace(tokenResp.AccessToken),
		RefreshToken: strings.TrimSpace(tokenResp.RefreshToken),
		Scope:        strings.TrimSpace(tokenResp.Scope),
		TokenType:    strings.TrimSpace(tokenResp.TokenType),
	}
	if tokenResp.ExpiresIn > 0 {
		expiresAt := time.Now().UTC().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
		creds.ExpiresAt = &expiresAt
	}
	return []ConnectedAccount{{
		Platform:          domain.PlatformLinkedIn,
		DisplayName:       displayName,
		ExternalAccountID: memberID,
		Credentials:       creds,
	}}, nil
}

func (p *LinkedInProvider) fetchMemberProfile(ctx context.Context, accessToken string) (memberID, displayName string, err error) {
	memberID, displayName, err = p.fetchMemberProfileFromUserInfo(ctx, accessToken)
	if err == nil {
		return memberID, displayName, nil
	}

	legacyID, legacyName, legacyErr := p.fetchMemberProfileFromMe(ctx, accessToken)
	if legacyErr == nil {
		return legacyID, legacyName, nil
	}

	return "", "", fmt.Errorf("linkedin profile fetch failed (userinfo and me): userinfo_error=%v me_error=%v", err, legacyErr)
}

func (p *LinkedInProvider) fetchMemberProfileFromUserInfo(ctx context.Context, accessToken string) (memberID, displayName string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.cfg.APIBaseURL, "/")+"/v2/userinfo", nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	resp, err := p.client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("userinfo status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var userinfo struct {
		Sub       string `json:"sub"`
		Name      string `json:"name"`
		GivenName string `json:"given_name"`
		Family    string `json:"family_name"`
	}
	if err := json.Unmarshal(body, &userinfo); err != nil {
		return "", "", err
	}
	memberID = strings.TrimSpace(userinfo.Sub)
	if memberID == "" {
		return "", "", fmt.Errorf("userinfo response missing sub")
	}
	displayName = strings.TrimSpace(userinfo.Name)
	if displayName == "" {
		displayName = strings.TrimSpace(strings.TrimSpace(userinfo.GivenName) + " " + strings.TrimSpace(userinfo.Family))
	}
	if displayName == "" {
		displayName = "LinkedIn " + memberID
	}
	return memberID, displayName, nil
}

func (p *LinkedInProvider) fetchMemberProfileFromMe(ctx context.Context, accessToken string) (memberID, displayName string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.cfg.APIBaseURL, "/")+"/v2/me", nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	req.Header.Set("X-Restli-Protocol-Version", "2.0.0")
	resp, err := p.client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("me status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var me struct {
		ID                 string `json:"id"`
		LocalizedFirstName string `json:"localizedFirstName"`
		LocalizedLastName  string `json:"localizedLastName"`
	}
	if err := json.Unmarshal(body, &me); err != nil {
		return "", "", err
	}
	memberID = strings.TrimSpace(me.ID)
	if memberID == "" {
		return "", "", fmt.Errorf("linkedin profile response missing id")
	}
	displayName = strings.TrimSpace(strings.TrimSpace(me.LocalizedFirstName) + " " + strings.TrimSpace(me.LocalizedLastName))
	if displayName == "" {
		displayName = "LinkedIn " + memberID
	}
	return memberID, displayName, nil
}
