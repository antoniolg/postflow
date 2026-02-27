package publisher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	warnings := make([]string, 0)
	if len(draft.Media) > 0 {
		warnings = append(warnings, "linkedin provider currently supports text-only publish in this release")
	}
	return warnings, nil
}

func (p *LinkedInProvider) Publish(ctx context.Context, account domain.SocialAccount, credentials Credentials, post domain.Post) (string, error) {
	if len(post.Media) > 0 {
		return "", fmt.Errorf("linkedin media publishing is not enabled yet")
	}
	token := strings.TrimSpace(credentials.AccessToken)
	if token == "" {
		return "", fmt.Errorf("linkedin access token missing")
	}
	memberID := strings.TrimSpace(account.ExternalAccountID)
	if memberID == "" {
		return "", fmt.Errorf("linkedin external account id is required")
	}
	payload := map[string]any{
		"author":         "urn:li:person:" + memberID,
		"lifecycleState": "PUBLISHED",
		"specificContent": map[string]any{
			"com.linkedin.ugc.ShareContent": map[string]any{
				"shareCommentary": map[string]any{"text": post.Text},
				"shareMediaCategory": "NONE",
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
	values.Set("scope", "w_member_social offline_access")
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.cfg.APIBaseURL, "/")+"/v2/me", nil)
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
		return "", "", fmt.Errorf("linkedin profile fetch failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
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
