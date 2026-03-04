package postflow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/antoniolg/postflow/internal/domain"
)

func (p *FacebookProvider) RefreshIfNeeded(ctx context.Context, _ domain.SocialAccount, credentials Credentials) (Credentials, bool, error) {
	if credentials.ExpiresAt == nil {
		return credentials, false, nil
	}
	if credentials.ExpiresAt.After(time.Now().UTC().Add(5 * time.Minute)) {
		return credentials, false, nil
	}
	token := strings.TrimSpace(credentials.AccessToken)
	if token == "" {
		return credentials, false, fmt.Errorf("meta refresh requires access token")
	}
	if strings.TrimSpace(p.cfg.AppID) == "" || strings.TrimSpace(p.cfg.AppSecret) == "" {
		return credentials, false, fmt.Errorf("meta refresh requires app credentials")
	}
	refreshed, err := p.exchangeLongLivedToken(ctx, token)
	if err != nil {
		return credentials, false, err
	}
	updated := credentials
	updated.AccessToken = strings.TrimSpace(refreshed.AccessToken)
	if strings.TrimSpace(refreshed.TokenType) != "" {
		updated.TokenType = strings.TrimSpace(refreshed.TokenType)
	}
	if refreshed.ExpiresIn > 0 {
		expiresAt := time.Now().UTC().Add(time.Duration(refreshed.ExpiresIn) * time.Second)
		updated.ExpiresAt = &expiresAt
	}
	return updated, true, nil
}

func (p *InstagramProvider) RefreshIfNeeded(ctx context.Context, account domain.SocialAccount, credentials Credentials) (Credentials, bool, error) {
	fb := FacebookProvider{cfg: p.cfg, client: p.client}
	return fb.RefreshIfNeeded(ctx, account, credentials)
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
		creds := Credentials{
			AccessToken: strings.TrimSpace(page.AccessToken),
			TokenType:   "Bearer",
			Extra: map[string]string{
				"page_id": externalID,
			},
		}
		if token.ExpiresIn > 0 {
			expiresAt := time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second)
			creds.ExpiresAt = &expiresAt
		}
		accounts = append(accounts, ConnectedAccount{
			Platform:          domain.PlatformFacebook,
			DisplayName:       firstNonEmpty(strings.TrimSpace(page.Name), "Facebook Page "+externalID),
			ExternalAccountID: externalID,
			Credentials:       creds,
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
		creds := Credentials{
			AccessToken: strings.TrimSpace(page.AccessToken),
			TokenType:   "Bearer",
			Extra: map[string]string{
				"ig_user_id": igID,
				"page_id":    strings.TrimSpace(page.ID),
			},
		}
		if token.ExpiresIn > 0 {
			expiresAt := time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second)
			creds.ExpiresAt = &expiresAt
		}
		accounts = append(accounts, ConnectedAccount{
			Platform:          domain.PlatformInstagram,
			DisplayName:       display,
			ExternalAccountID: igID,
			Credentials:       creds,
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
	ID                       string `json:"id"`
	Name                     string `json:"name"`
	AccessToken              string `json:"access_token"`
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

func (p *FacebookProvider) exchangeLongLivedToken(ctx context.Context, accessToken string) (metaTokenResponse, error) {
	values := url.Values{}
	values.Set("grant_type", "fb_exchange_token")
	values.Set("client_id", strings.TrimSpace(p.cfg.AppID))
	values.Set("client_secret", strings.TrimSpace(p.cfg.AppSecret))
	values.Set("fb_exchange_token", strings.TrimSpace(accessToken))
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
		return metaTokenResponse{}, fmt.Errorf("meta token refresh failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out metaTokenResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return metaTokenResponse{}, err
	}
	if strings.TrimSpace(out.AccessToken) == "" {
		return metaTokenResponse{}, fmt.Errorf("meta token refresh returned empty access_token")
	}
	return out, nil
}
