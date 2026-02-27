package publisher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/antoniolg/publisher/internal/domain"
)

type MetaProviderConfig struct {
	AppID      string
	AppSecret  string
	GraphURL   string
	DialogURL  string
	APIVersion string
}

type FacebookProvider struct {
	cfg    MetaProviderConfig
	client *http.Client
}

type InstagramProvider struct {
	cfg    MetaProviderConfig
	client *http.Client
}

func normalizeMetaConfig(cfg MetaProviderConfig) MetaProviderConfig {
	if strings.TrimSpace(cfg.GraphURL) == "" {
		cfg.GraphURL = "https://graph.facebook.com"
	}
	if strings.TrimSpace(cfg.DialogURL) == "" {
		cfg.DialogURL = "https://www.facebook.com"
	}
	if strings.TrimSpace(cfg.APIVersion) == "" {
		cfg.APIVersion = "v22.0"
	}
	return cfg
}

func NewFacebookProvider(cfg MetaProviderConfig) *FacebookProvider {
	cfg = normalizeMetaConfig(cfg)
	return &FacebookProvider{cfg: cfg, client: &http.Client{Timeout: 60 * time.Second}}
}

func NewInstagramProvider(cfg MetaProviderConfig) *InstagramProvider {
	cfg = normalizeMetaConfig(cfg)
	return &InstagramProvider{cfg: cfg, client: &http.Client{Timeout: 60 * time.Second}}
}

func (p *FacebookProvider) Platform() domain.Platform {
	return domain.PlatformFacebook
}

func (p *InstagramProvider) Platform() domain.Platform {
	return domain.PlatformInstagram
}

func (p *FacebookProvider) ValidateDraft(_ context.Context, _ domain.SocialAccount, draft Draft) ([]string, error) {
	warnings := make([]string, 0)
	if len(draft.Media) > 0 {
		warnings = append(warnings, "facebook provider currently sends text-only posts")
	}
	return warnings, nil
}

func (p *InstagramProvider) ValidateDraft(_ context.Context, _ domain.SocialAccount, draft Draft) ([]string, error) {
	warnings := make([]string, 0)
	if len(draft.Media) == 0 {
		warnings = append(warnings, "instagram publish requires at least one image")
	}
	return warnings, nil
}

func (p *FacebookProvider) Publish(ctx context.Context, account domain.SocialAccount, credentials Credentials, post domain.Post) (string, error) {
	pageID := strings.TrimSpace(account.ExternalAccountID)
	if pageID == "" {
		pageID = strings.TrimSpace(credentials.Extra["page_id"])
	}
	if pageID == "" {
		return "", fmt.Errorf("facebook page id is required")
	}
	if strings.TrimSpace(credentials.AccessToken) == "" {
		return "", fmt.Errorf("facebook access token missing")
	}
	values := url.Values{}
	values.Set("message", strings.TrimSpace(post.Text))
	values.Set("access_token", strings.TrimSpace(credentials.AccessToken))
	reqURL := fmt.Sprintf("%s/%s/%s/feed", strings.TrimRight(p.cfg.GraphURL, "/"), p.cfg.APIVersion, pageID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(values.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("facebook publish failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	if strings.TrimSpace(out.ID) == "" {
		return "", fmt.Errorf("facebook publish response missing id")
	}
	return strings.TrimSpace(out.ID), nil
}

func (p *InstagramProvider) Publish(ctx context.Context, account domain.SocialAccount, credentials Credentials, post domain.Post) (string, error) {
	igUserID := strings.TrimSpace(account.ExternalAccountID)
	if igUserID == "" {
		igUserID = strings.TrimSpace(credentials.Extra["ig_user_id"])
	}
	if igUserID == "" {
		return "", fmt.Errorf("instagram user id is required")
	}
	if strings.TrimSpace(credentials.AccessToken) == "" {
		return "", fmt.Errorf("instagram access token missing")
	}
	if len(post.Media) == 0 {
		return "", fmt.Errorf("instagram requires at least one media item")
	}
	imageURL := strings.TrimSpace(credentials.Extra["image_url"])
	if imageURL == "" {
		candidate := strings.TrimSpace(post.Media[0].StoragePath)
		if strings.HasPrefix(strings.ToLower(candidate), "http://") || strings.HasPrefix(strings.ToLower(candidate), "https://") {
			imageURL = candidate
		}
	}
	if imageURL == "" {
		return "", fmt.Errorf("instagram requires a public image URL; media %s at %s is not public", post.Media[0].ID, filepath.Base(post.Media[0].StoragePath))
	}
	createValues := url.Values{}
	createValues.Set("image_url", imageURL)
	createValues.Set("caption", strings.TrimSpace(post.Text))
	createValues.Set("access_token", strings.TrimSpace(credentials.AccessToken))
	createURL := fmt.Sprintf("%s/%s/%s/media", strings.TrimRight(p.cfg.GraphURL, "/"), p.cfg.APIVersion, igUserID)
	createReq, err := http.NewRequestWithContext(ctx, http.MethodPost, createURL, strings.NewReader(createValues.Encode()))
	if err != nil {
		return "", err
	}
	createReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	createResp, err := p.client.Do(createReq)
	if err != nil {
		return "", err
	}
	defer createResp.Body.Close()
	createBody, _ := io.ReadAll(io.LimitReader(createResp.Body, 2<<20))
	if createResp.StatusCode >= 300 {
		return "", fmt.Errorf("instagram create media failed: status=%d body=%s", createResp.StatusCode, strings.TrimSpace(string(createBody)))
	}
	var container struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(createBody, &container); err != nil {
		return "", err
	}
	if strings.TrimSpace(container.ID) == "" {
		return "", fmt.Errorf("instagram create media missing container id")
	}

	publishValues := url.Values{}
	publishValues.Set("creation_id", strings.TrimSpace(container.ID))
	publishValues.Set("access_token", strings.TrimSpace(credentials.AccessToken))
	publishURL := fmt.Sprintf("%s/%s/%s/media_publish", strings.TrimRight(p.cfg.GraphURL, "/"), p.cfg.APIVersion, igUserID)
	publishReq, err := http.NewRequestWithContext(ctx, http.MethodPost, publishURL, strings.NewReader(publishValues.Encode()))
	if err != nil {
		return "", err
	}
	publishReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	publishResp, err := p.client.Do(publishReq)
	if err != nil {
		return "", err
	}
	defer publishResp.Body.Close()
	publishBody, _ := io.ReadAll(io.LimitReader(publishResp.Body, 2<<20))
	if publishResp.StatusCode >= 300 {
		return "", fmt.Errorf("instagram publish failed: status=%d body=%s", publishResp.StatusCode, strings.TrimSpace(string(publishBody)))
	}
	var publishOut struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(publishBody, &publishOut); err != nil {
		return "", err
	}
	if strings.TrimSpace(publishOut.ID) == "" {
		return "", fmt.Errorf("instagram publish response missing id")
	}
	return strings.TrimSpace(publishOut.ID), nil
}

func (p *FacebookProvider) RefreshIfNeeded(_ context.Context, _ domain.SocialAccount, credentials Credentials) (Credentials, bool, error) {
	return credentials, false, nil
}

func (p *InstagramProvider) RefreshIfNeeded(_ context.Context, _ domain.SocialAccount, credentials Credentials) (Credentials, bool, error) {
	return credentials, false, nil
}

func (p *FacebookProvider) StartOAuth(_ context.Context, in OAuthStartInput) (OAuthStartOutput, error) {
	return p.startOAuth(in, "pages_manage_posts,pages_read_engagement,pages_show_list")
}

func (p *InstagramProvider) StartOAuth(_ context.Context, in OAuthStartInput) (OAuthStartOutput, error) {
	return p.startOAuth(in, "pages_show_list,pages_read_engagement,instagram_content_publish,instagram_basic")
}

func (p *FacebookProvider) startOAuth(in OAuthStartInput, scope string) (OAuthStartOutput, error) {
	if strings.TrimSpace(p.cfg.AppID) == "" || strings.TrimSpace(p.cfg.AppSecret) == "" {
		return OAuthStartOutput{}, fmt.Errorf("meta oauth not configured")
	}
	values := url.Values{}
	values.Set("client_id", p.cfg.AppID)
	values.Set("redirect_uri", in.RedirectURL)
	values.Set("state", in.State)
	values.Set("scope", scope)
	values.Set("response_type", "code")
	return OAuthStartOutput{AuthURL: fmt.Sprintf("%s/%s/dialog/oauth?%s", strings.TrimRight(p.cfg.DialogURL, "/"), p.cfg.APIVersion, values.Encode())}, nil
}

func (p *InstagramProvider) startOAuth(in OAuthStartInput, scope string) (OAuthStartOutput, error) {
	fb := FacebookProvider{cfg: p.cfg, client: p.client}
	return fb.startOAuth(in, scope)
}

func (p *FacebookProvider) HandleOAuthCallback(ctx context.Context, in OAuthCallbackInput) ([]ConnectedAccount, error) {
	token, err := p.exchangeCode(ctx, in)
	if err != nil {
		return nil, err
	}
	pages, err := p.fetchPages(ctx, token)
	if err != nil {
		return nil, err
	}
	accounts := make([]ConnectedAccount, 0, len(pages))
	for _, page := range pages {
		externalID := strings.TrimSpace(page.ID)
		if externalID == "" || strings.TrimSpace(page.AccessToken) == "" {
			continue
		}
		accounts = append(accounts, ConnectedAccount{
			Platform:          domain.PlatformFacebook,
			DisplayName:       firstNonEmpty(strings.TrimSpace(page.Name), "Facebook Page "+externalID),
			ExternalAccountID: externalID,
			Credentials: Credentials{
				AccessToken: strings.TrimSpace(page.AccessToken),
				TokenType:   "Bearer",
				Extra: map[string]string{
					"page_id": externalID,
				},
			},
		})
	}
	if len(accounts) == 0 {
		return nil, fmt.Errorf("meta oauth returned no publishable facebook pages")
	}
	return accounts, nil
}

func (p *InstagramProvider) HandleOAuthCallback(ctx context.Context, in OAuthCallbackInput) ([]ConnectedAccount, error) {
	token, err := p.exchangeCode(ctx, in)
	if err != nil {
		return nil, err
	}
	pages, err := p.fetchPages(ctx, token)
	if err != nil {
		return nil, err
	}
	accounts := make([]ConnectedAccount, 0)
	for _, page := range pages {
		ig := page.InstagramBusinessAccount
		if ig == nil {
			continue
		}
		igID := strings.TrimSpace(ig.ID)
		if igID == "" || strings.TrimSpace(page.AccessToken) == "" {
			continue
		}
		display := strings.TrimSpace(ig.Username)
		if display == "" {
			display = "Instagram " + igID
		}
		accounts = append(accounts, ConnectedAccount{
			Platform:          domain.PlatformInstagram,
			DisplayName:       display,
			ExternalAccountID: igID,
			Credentials: Credentials{
				AccessToken: strings.TrimSpace(page.AccessToken),
				TokenType:   "Bearer",
				Extra: map[string]string{
					"ig_user_id": igID,
					"page_id":    strings.TrimSpace(page.ID),
				},
			},
		})
	}
	if len(accounts) == 0 {
		return nil, fmt.Errorf("meta oauth returned no instagram business accounts")
	}
	return accounts, nil
}

type metaTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

type metaPage struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	AccessToken string `json:"access_token"`
	InstagramBusinessAccount *struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	} `json:"instagram_business_account"`
}

func (p *FacebookProvider) exchangeCode(ctx context.Context, in OAuthCallbackInput) (metaTokenResponse, error) {
	values := url.Values{}
	values.Set("client_id", p.cfg.AppID)
	values.Set("client_secret", p.cfg.AppSecret)
	values.Set("redirect_uri", in.RedirectURL)
	values.Set("code", in.Code)
	reqURL := fmt.Sprintf("%s/%s/oauth/access_token?%s", strings.TrimRight(p.cfg.GraphURL, "/"), p.cfg.APIVersion, values.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return metaTokenResponse{}, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return metaTokenResponse{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 300 {
		return metaTokenResponse{}, fmt.Errorf("meta token exchange failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out metaTokenResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return metaTokenResponse{}, err
	}
	if strings.TrimSpace(out.AccessToken) == "" {
		return metaTokenResponse{}, fmt.Errorf("meta token exchange returned empty access_token")
	}
	return out, nil
}

func (p *InstagramProvider) exchangeCode(ctx context.Context, in OAuthCallbackInput) (metaTokenResponse, error) {
	fb := FacebookProvider{cfg: p.cfg, client: p.client}
	return fb.exchangeCode(ctx, in)
}

func (p *FacebookProvider) fetchPages(ctx context.Context, token metaTokenResponse) ([]metaPage, error) {
	values := url.Values{}
	values.Set("fields", "id,name,access_token,instagram_business_account{id,username}")
	values.Set("access_token", strings.TrimSpace(token.AccessToken))
	reqURL := fmt.Sprintf("%s/%s/me/accounts?%s", strings.TrimRight(p.cfg.GraphURL, "/"), p.cfg.APIVersion, values.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("meta pages fetch failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		Data []metaPage `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func (p *InstagramProvider) fetchPages(ctx context.Context, token metaTokenResponse) ([]metaPage, error) {
	fb := FacebookProvider{cfg: p.cfg, client: p.client}
	return fb.fetchPages(ctx, token)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func marshalBody(v any) io.Reader {
	raw, _ := json.Marshal(v)
	return bytes.NewReader(raw)
}
