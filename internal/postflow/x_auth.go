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
const xOAuth1CodeVerifierPrefix = "oauth1:"

type xTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
}

func (p *XProvider) RefreshIfNeeded(ctx context.Context, _ domain.SocialAccount, credentials Credentials) (Credentials, bool, error) {
	if strings.TrimSpace(credentials.AccessTokenSecret) != "" || strings.EqualFold(strings.TrimSpace(credentials.TokenType), "oauth1") {
		return credentials, false, nil
	}
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

func (p *XProvider) StartOAuth(ctx context.Context, in OAuthStartInput) (OAuthStartOutput, error) {
	if p.hasOAuth1ConnectConfig() {
		return p.startOAuth1(ctx, in)
	}
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
	if requestToken, requestTokenSecret, ok := parseXOAuth1CodeVerifier(in.CodeVerifier); ok {
		return p.handleOAuth1Callback(ctx, in, requestToken, requestTokenSecret)
	}
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

func (p *XProvider) hasOAuth1ConnectConfig() bool {
	return strings.TrimSpace(p.cfg.APIKey) != "" && strings.TrimSpace(p.cfg.APIKeySecret) != ""
}

func (p *XProvider) oauth1BaseURL() string {
	base := strings.TrimSpace(p.cfg.APIBaseURL)
	if base == "" || base == "https://api.x.com" {
		return "https://api.twitter.com"
	}
	return strings.TrimRight(base, "/")
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

func (p *XProvider) startOAuth1(ctx context.Context, in OAuthStartInput) (OAuthStartOutput, error) {
	callbackURL, err := appendQueryValue(strings.TrimSpace(in.RedirectURL), "state", strings.TrimSpace(in.State))
	if err != nil {
		return OAuthStartOutput{}, err
	}
	responseValues, err := p.oauth1FormExchange(ctx, "/oauth/request_token", oauth1Credentials{
		ConsumerKey:    strings.TrimSpace(p.cfg.APIKey),
		ConsumerSecret: strings.TrimSpace(p.cfg.APIKeySecret),
	}, map[string]string{
		"oauth_callback": callbackURL,
	})
	if err != nil {
		return OAuthStartOutput{}, err
	}
	requestToken := strings.TrimSpace(responseValues.Get("oauth_token"))
	requestTokenSecret := strings.TrimSpace(responseValues.Get("oauth_token_secret"))
	if requestToken == "" || requestTokenSecret == "" {
		return OAuthStartOutput{}, fmt.Errorf("x oauth request token response missing oauth token")
	}
	return OAuthStartOutput{
		AuthURL:      p.oauth1BaseURL() + "/oauth/authenticate?oauth_token=" + url.QueryEscape(requestToken),
		CodeVerifier: formatXOAuth1CodeVerifier(requestToken, requestTokenSecret),
	}, nil
}

func (p *XProvider) handleOAuth1Callback(ctx context.Context, in OAuthCallbackInput, requestToken, requestTokenSecret string) ([]ConnectedAccount, error) {
	values, err := p.oauth1FormExchange(ctx, "/oauth/access_token", oauth1Credentials{
		ConsumerKey:    strings.TrimSpace(p.cfg.APIKey),
		ConsumerSecret: strings.TrimSpace(p.cfg.APIKeySecret),
		Token:          requestToken,
		TokenSecret:    requestTokenSecret,
	}, map[string]string{
		"oauth_verifier": strings.TrimSpace(in.Code),
	})
	if err != nil {
		return nil, err
	}
	accessToken := strings.TrimSpace(values.Get("oauth_token"))
	accessTokenSecret := strings.TrimSpace(values.Get("oauth_token_secret"))
	if accessToken == "" || accessTokenSecret == "" {
		return nil, fmt.Errorf("x oauth access token response missing oauth token")
	}
	user, err := p.fetchCurrentUserOAuth1(ctx, accessToken, accessTokenSecret)
	if err != nil {
		return nil, err
	}
	displayName := "X " + user.ID
	if strings.TrimSpace(user.Username) != "" {
		displayName = "@" + strings.TrimSpace(user.Username)
	} else if strings.TrimSpace(user.Name) != "" {
		displayName = strings.TrimSpace(user.Name)
	}
	return []ConnectedAccount{{
		Platform:          domain.PlatformX,
		AccountKind:       domain.AccountKindDefault,
		DisplayName:       displayName,
		ExternalAccountID: strings.TrimSpace(user.ID),
		Credentials: Credentials{
			AccessToken:       accessToken,
			AccessTokenSecret: accessTokenSecret,
			TokenType:         "oauth1",
			Extra: map[string]string{
				"username": strings.TrimSpace(user.Username),
				"name":     strings.TrimSpace(user.Name),
			},
		},
	}}, nil
}

func (p *XProvider) oauth1FormExchange(ctx context.Context, path string, creds oauth1Credentials, oauthParams map[string]string) (url.Values, error) {
	endpoint := p.oauth1BaseURL() + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/x-www-form-urlencoded")
	if err := signRequest(req, newOAuth1Signer(creds), oauthParams); err != nil {
		return nil, err
	}
	resp, err := p.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("x oauth request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	values, err := url.ParseQuery(strings.TrimSpace(string(body)))
	if err != nil {
		return nil, err
	}
	return values, nil
}

func (p *XProvider) fetchCurrentUserOAuth1(ctx context.Context, accessToken, accessTokenSecret string) (xCurrentUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.apiBaseURL(), "/")+"/2/users/me", nil)
	if err != nil {
		return xCurrentUser{}, err
	}
	req.Header.Set("Accept", "application/json")
	if err := signRequest(req, newOAuth1Signer(oauth1Credentials{
		ConsumerKey:    strings.TrimSpace(p.cfg.APIKey),
		ConsumerSecret: strings.TrimSpace(p.cfg.APIKeySecret),
		Token:          strings.TrimSpace(accessToken),
		TokenSecret:    strings.TrimSpace(accessTokenSecret),
	}), nil); err != nil {
		return xCurrentUser{}, err
	}
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

func appendQueryValue(rawURL, key, value string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set(strings.TrimSpace(key), strings.TrimSpace(value))
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func formatXOAuth1CodeVerifier(requestToken, requestTokenSecret string) string {
	return xOAuth1CodeVerifierPrefix + strings.TrimSpace(requestToken) + ":" + strings.TrimSpace(requestTokenSecret)
}

func parseXOAuth1CodeVerifier(raw string) (requestToken, requestTokenSecret string, ok bool) {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, xOAuth1CodeVerifierPrefix) {
		return "", "", false
	}
	parts := strings.SplitN(strings.TrimPrefix(trimmed, xOAuth1CodeVerifierPrefix), ":", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
}
