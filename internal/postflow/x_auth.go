package postflow

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/antoniolg/postflow/internal/domain"
)

const xOAuthScope = "tweet.read tweet.write users.read media.write offline.access"

type xTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
}

func (p *XProvider) RefreshIfNeeded(ctx context.Context, _ domain.SocialAccount, credentials Credentials) (Credentials, bool, error) {
	if credentials.ExpiresAt == nil || strings.TrimSpace(credentials.RefreshToken) == "" {
		return credentials, false, nil
	}
	if credentials.ExpiresAt.After(time.Now().UTC().Add(5 * time.Minute)) {
		return credentials, false, nil
	}
	tokenResp, err := p.exchangeToken(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {strings.TrimSpace(credentials.RefreshToken)},
		"client_id":     {strings.TrimSpace(p.cfg.ClientID)},
	})
	if err != nil {
		return credentials, false, err
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

func (p *XProvider) StartOAuth(_ context.Context, in OAuthStartInput) (OAuthStartOutput, error) {
	if strings.TrimSpace(p.cfg.ClientID) == "" {
		return OAuthStartOutput{}, fmt.Errorf("x oauth not configured")
	}
	values := url.Values{}
	values.Set("response_type", "code")
	values.Set("client_id", strings.TrimSpace(p.cfg.ClientID))
	values.Set("redirect_uri", strings.TrimSpace(in.RedirectURL))
	values.Set("state", strings.TrimSpace(in.State))
	values.Set("scope", xOAuthScope)
	values.Set("code_challenge", xCodeChallengeS256(in.CodeVerifier))
	values.Set("code_challenge_method", "S256")
	return OAuthStartOutput{
		AuthURL: strings.TrimRight(p.authBaseURL(), "/") + "/i/oauth2/authorize?" + values.Encode(),
	}, nil
}

func (p *XProvider) HandleOAuthCallback(ctx context.Context, in OAuthCallbackInput) ([]ConnectedAccount, error) {
	tokenResp, err := p.exchangeToken(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {strings.TrimSpace(in.Code)},
		"redirect_uri":  {strings.TrimSpace(in.RedirectURL)},
		"code_verifier": {strings.TrimSpace(in.CodeVerifier)},
		"client_id":     {strings.TrimSpace(p.cfg.ClientID)},
	})
	if err != nil {
		return nil, err
	}
	user, err := p.fetchCurrentUser(ctx, tokenResp.AccessToken)
	if err != nil {
		return nil, err
	}
	displayName := "X " + user.ID
	if strings.TrimSpace(user.Username) != "" {
		displayName = "@" + strings.TrimSpace(user.Username)
	} else if strings.TrimSpace(user.Name) != "" {
		displayName = strings.TrimSpace(user.Name)
	}
	creds := Credentials{
		AccessToken:  strings.TrimSpace(tokenResp.AccessToken),
		RefreshToken: strings.TrimSpace(tokenResp.RefreshToken),
		Scope:        strings.TrimSpace(tokenResp.Scope),
		TokenType:    strings.TrimSpace(tokenResp.TokenType),
		Extra: map[string]string{
			"username": strings.TrimSpace(user.Username),
			"name":     strings.TrimSpace(user.Name),
		},
	}
	if tokenResp.ExpiresIn > 0 {
		expiresAt := time.Now().UTC().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
		creds.ExpiresAt = &expiresAt
	}
	return []ConnectedAccount{{
		Platform:          domain.PlatformX,
		AccountKind:       domain.AccountKindDefault,
		DisplayName:       displayName,
		ExternalAccountID: strings.TrimSpace(user.ID),
		Credentials:       creds,
	}}, nil
}

func (p *XProvider) exchangeToken(ctx context.Context, values url.Values) (xTokenResponse, error) {
	if strings.TrimSpace(p.cfg.ClientID) == "" {
		return xTokenResponse{}, fmt.Errorf("x oauth not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenURL(), strings.NewReader(values.Encode()))
	if err != nil {
		return xTokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if strings.TrimSpace(p.cfg.ClientSecret) != "" {
		req.SetBasicAuth(strings.TrimSpace(p.cfg.ClientID), strings.TrimSpace(p.cfg.ClientSecret))
	}
	resp, err := p.httpClient().Do(req)
	if err != nil {
		return xTokenResponse{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 300 {
		return xTokenResponse{}, fmt.Errorf("x token exchange failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var tokenResp xTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return xTokenResponse{}, err
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" {
		return xTokenResponse{}, fmt.Errorf("x token exchange returned empty access token")
	}
	return tokenResp, nil
}

type xCurrentUser struct {
	ID       string
	Name     string
	Username string
}

func (p *XProvider) fetchCurrentUser(ctx context.Context, accessToken string) (xCurrentUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.apiBaseURL(), "/")+"/2/users/me", nil)
	if err != nil {
		return xCurrentUser{}, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	req.Header.Set("Accept", "application/json")
	resp, err := p.httpClient().Do(req)
	if err != nil {
		return xCurrentUser{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 300 {
		return xCurrentUser{}, fmt.Errorf("x profile fetch failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		Data struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Username string `json:"username"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return xCurrentUser{}, err
	}
	user := xCurrentUser{
		ID:       strings.TrimSpace(out.Data.ID),
		Name:     strings.TrimSpace(out.Data.Name),
		Username: strings.TrimSpace(out.Data.Username),
	}
	if user.ID == "" {
		return xCurrentUser{}, fmt.Errorf("x profile response missing user id")
	}
	return user, nil
}

func (p *XProvider) authBaseURL() string {
	if strings.TrimSpace(p.cfg.AuthBaseURL) != "" {
		return strings.TrimRight(strings.TrimSpace(p.cfg.AuthBaseURL), "/")
	}
	return "https://x.com"
}

func (p *XProvider) tokenURL() string {
	if strings.TrimSpace(p.cfg.TokenURL) != "" {
		return strings.TrimSpace(p.cfg.TokenURL)
	}
	return "https://api.x.com/2/oauth2/token"
}

func (p *XProvider) apiBaseURL() string {
	if strings.TrimSpace(p.cfg.APIBaseURL) != "" {
		return strings.TrimRight(strings.TrimSpace(p.cfg.APIBaseURL), "/")
	}
	return "https://api.x.com"
}

func xCodeChallengeS256(codeVerifier string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(codeVerifier)))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
