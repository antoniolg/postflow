package postflow

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/domain"
)

func TestXProviderValidateDraftUsesDefaultLimit(t *testing.T) {
	provider := NewXProvider(XConfig{})
	warnings, err := provider.ValidateDraft(context.Background(), domain.SocialAccount{Platform: domain.PlatformX}, Draft{
		Text: strings.Repeat("a", 281),
	})
	if err != nil {
		t.Fatalf("validate draft: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d (%v)", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "280") {
		t.Fatalf("expected warning to mention 280 chars, got %q", warnings[0])
	}
}

func TestXProviderValidateDraftUsesPremiumLimit(t *testing.T) {
	provider := NewXProvider(XConfig{})
	account := domain.SocialAccount{Platform: domain.PlatformX, XPremium: true}

	warnings, err := provider.ValidateDraft(context.Background(), account, Draft{
		Text: strings.Repeat("a", 300),
	})
	if err != nil {
		t.Fatalf("validate draft: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for 300 chars on premium x account, got %v", warnings)
	}

	warnings, err = provider.ValidateDraft(context.Background(), account, Draft{
		Text: strings.Repeat("a", 25001),
	})
	if err != nil {
		t.Fatalf("validate draft with overflow: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for premium overflow, got %d (%v)", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "25000") {
		t.Fatalf("expected warning to mention 25000 chars, got %q", warnings[0])
	}
}

func TestXProviderPlatformAndRefreshNoopWithoutExpiry(t *testing.T) {
	provider := NewXProvider(XConfig{})
	if provider.Platform() != domain.PlatformX {
		t.Fatalf("expected platform x, got %s", provider.Platform())
	}

	original := Credentials{AccessToken: "token-1", TokenType: "bearer"}
	updated, changed, err := provider.RefreshIfNeeded(context.Background(), domain.SocialAccount{}, original)
	if err != nil {
		t.Fatalf("refresh if needed: %v", err)
	}
	if changed {
		t.Fatalf("expected x refresh noop")
	}
	if updated.AccessToken != original.AccessToken {
		t.Fatalf("expected unchanged credentials, got %+v", updated)
	}
}

func TestXProviderStartOAuthIncludesPKCEAndScopes(t *testing.T) {
	provider := NewXProvider(XConfig{
		ClientID:    "x-client-id",
		AuthBaseURL: "https://x.example",
	})

	out, err := provider.StartOAuth(context.Background(), OAuthStartInput{
		State:        "state-123",
		CodeVerifier: "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMN",
		RedirectURL:  "https://postflow.example/oauth/x/callback",
	})
	if err != nil {
		t.Fatalf("StartOAuth() error = %v", err)
	}
	parsed, err := url.Parse(out.AuthURL)
	if err != nil {
		t.Fatalf("parse auth url: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "x.example" || parsed.Path != "/i/oauth2/authorize" {
		t.Fatalf("unexpected auth url %q", out.AuthURL)
	}
	if got := parsed.Query().Get("client_id"); got != "x-client-id" {
		t.Fatalf("client_id = %q, want %q", got, "x-client-id")
	}
	if got := parsed.Query().Get("state"); got != "state-123" {
		t.Fatalf("state = %q, want %q", got, "state-123")
	}
	if got := parsed.Query().Get("scope"); got != xOAuthScope {
		t.Fatalf("scope = %q, want %q", got, xOAuthScope)
	}
	if got := parsed.Query().Get("code_challenge_method"); got != "S256" {
		t.Fatalf("code_challenge_method = %q, want S256", got)
	}
	if got := parsed.Query().Get("code_challenge"); strings.TrimSpace(got) == "" {
		t.Fatalf("expected code challenge in auth url")
	}
}

func TestXProviderHandleOAuthCallbackConnectsOAuth2Account(t *testing.T) {
	var tokenForm url.Values
	var tokenAuthHeader string
	var profileAuthHeader string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/2/oauth2/token":
			body, _ := io.ReadAll(r.Body)
			tokenAuthHeader = strings.TrimSpace(r.Header.Get("Authorization"))
			tokenForm, _ = url.ParseQuery(string(body))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"x-access","refresh_token":"x-refresh","expires_in":7200,"scope":"tweet.read tweet.write users.read media.write offline.access","token_type":"bearer"}`))
		case "/2/users/me":
			profileAuthHeader = strings.TrimSpace(r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"id":"2244994945","name":"PostFlow Bot","username":"postflowbot"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	provider := NewXProvider(XConfig{
		ClientID:     "x-client-id",
		ClientSecret: "x-client-secret",
		APIBaseURL:   srv.URL,
		TokenURL:     srv.URL + "/2/oauth2/token",
	})

	accounts, err := provider.HandleOAuthCallback(context.Background(), OAuthCallbackInput{
		Code:         "oauth-code",
		CodeVerifier: "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMN",
		RedirectURL:  "https://postflow.example/oauth/x/callback",
	})
	if err != nil {
		t.Fatalf("HandleOAuthCallback() error = %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("expected 1 connected account, got %d", len(accounts))
	}
	account := accounts[0]
	if account.Platform != domain.PlatformX {
		t.Fatalf("platform = %s, want x", account.Platform)
	}
	if account.DisplayName != "@postflowbot" {
		t.Fatalf("display name = %q, want %q", account.DisplayName, "@postflowbot")
	}
	if account.ExternalAccountID != "2244994945" {
		t.Fatalf("external account id = %q, want %q", account.ExternalAccountID, "2244994945")
	}
	if account.Credentials.AccessToken != "x-access" || account.Credentials.RefreshToken != "x-refresh" {
		t.Fatalf("unexpected credentials: %+v", account.Credentials)
	}
	if account.Credentials.ExpiresAt == nil {
		t.Fatalf("expected oauth credentials to include expiry")
	}
	if got := tokenForm.Get("grant_type"); got != "authorization_code" {
		t.Fatalf("grant_type = %q, want authorization_code", got)
	}
	if got := tokenForm.Get("code"); got != "oauth-code" {
		t.Fatalf("code = %q, want oauth-code", got)
	}
	if got := tokenForm.Get("code_verifier"); got == "" {
		t.Fatalf("expected code_verifier in token exchange")
	}
	if tokenAuthHeader == "" {
		t.Fatalf("expected token exchange basic auth header")
	}
	if profileAuthHeader != "Bearer x-access" {
		t.Fatalf("profile authorization = %q, want %q", profileAuthHeader, "Bearer x-access")
	}
}

func TestXProviderRefreshIfNeededRefreshesExpiringOAuth2Token(t *testing.T) {
	var tokenForm url.Values

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/2/oauth2/token" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		tokenForm, _ = url.ParseQuery(string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"refreshed-access","refresh_token":"refreshed-refresh","expires_in":3600,"scope":"tweet.read tweet.write","token_type":"bearer"}`))
	}))
	defer srv.Close()

	provider := NewXProvider(XConfig{
		ClientID: "x-client-id",
		TokenURL: srv.URL + "/2/oauth2/token",
	})
	expiresSoon := time.Now().UTC().Add(2 * time.Minute)
	updated, changed, err := provider.RefreshIfNeeded(context.Background(), domain.SocialAccount{}, Credentials{
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
		ExpiresAt:    &expiresSoon,
		TokenType:    "bearer",
	})
	if err != nil {
		t.Fatalf("RefreshIfNeeded() error = %v", err)
	}
	if !changed {
		t.Fatalf("expected refresh to report changed credentials")
	}
	if updated.AccessToken != "refreshed-access" || updated.RefreshToken != "refreshed-refresh" {
		t.Fatalf("unexpected refreshed credentials: %+v", updated)
	}
	if updated.ExpiresAt == nil || !updated.ExpiresAt.After(time.Now().UTC().Add(30*time.Minute)) {
		t.Fatalf("expected refreshed expiry to be updated, got %+v", updated.ExpiresAt)
	}
	if got := tokenForm.Get("grant_type"); got != "refresh_token" {
		t.Fatalf("grant_type = %q, want refresh_token", got)
	}
	if got := tokenForm.Get("refresh_token"); got != "old-refresh" {
		t.Fatalf("refresh_token = %q, want old-refresh", got)
	}
}

func TestXProviderRefreshIfNeededDoesNotRefreshHealthyOAuth2Token(t *testing.T) {
	provider := NewXProvider(XConfig{ClientID: "x-client-id"})
	expiresLater := time.Now().UTC().Add(30 * time.Minute)
	updated, changed, err := provider.RefreshIfNeeded(context.Background(), domain.SocialAccount{}, Credentials{
		AccessToken:  "still-good",
		RefreshToken: "refresh-token",
		ExpiresAt:    &expiresLater,
		TokenType:    "bearer",
	})
	if err != nil {
		t.Fatalf("RefreshIfNeeded() error = %v", err)
	}
	if changed {
		t.Fatalf("did not expect refresh for healthy token")
	}
	if updated.AccessToken != "still-good" {
		t.Fatalf("expected unchanged token, got %+v", updated)
	}
}

func TestXProviderPublishBuildsPublishedURLFromCredentialsUsername(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/2/tweets" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"id":"x_post_1"}}`))
	}))
	defer srv.Close()

	provider := NewXProvider(XConfig{
		APIBaseURL:    srv.URL,
		UploadBaseURL: srv.URL,
	})

	result, err := provider.Publish(context.Background(), domain.SocialAccount{Platform: domain.PlatformX}, Credentials{
		AccessToken: "token",
		Extra:       map[string]string{"username": "postflowbot"},
	}, domain.Post{
		Platform: domain.PlatformX,
		Text:     "hello",
	}, PublishOptions{Mode: PublishModeRoot})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if result.PublishedURL != "https://x.com/postflowbot/status/x_post_1" {
		t.Fatalf("unexpected published url %q", result.PublishedURL)
	}
}

func TestXProviderPublishRejectsLegacyOAuth1Credentials(t *testing.T) {
	provider := NewXProvider(XConfig{})

	_, err := provider.Publish(context.Background(), domain.SocialAccount{Platform: domain.PlatformX}, Credentials{
		AccessToken:       "token",
		AccessTokenSecret: "legacy-secret",
		TokenType:         "oauth1",
	}, domain.Post{
		Platform: domain.PlatformX,
		Text:     "hello",
	}, PublishOptions{})
	if err == nil {
		t.Fatalf("expected legacy oauth1 credentials to be rejected")
	}
	if !strings.Contains(err.Error(), "reconnect via oauth") {
		t.Fatalf("expected reconnect guidance, got %v", err)
	}
}

func TestXProviderPublishLeavesPublishedURLEmptyWhenUsernameUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/2/tweets":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"id":"x_post_2"}}`))
		case "/2/users/me":
			http.Error(w, "boom", http.StatusBadGateway)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	provider := NewXProvider(XConfig{
		APIBaseURL:    srv.URL,
		UploadBaseURL: srv.URL,
		ClientID:      "x-client-id",
	})
	result, err := provider.Publish(context.Background(), domain.SocialAccount{Platform: domain.PlatformX}, Credentials{
		AccessToken: "bearer-token",
		TokenType:   "bearer",
	}, domain.Post{
		Platform: domain.PlatformX,
		Text:     "hello",
	}, PublishOptions{Mode: PublishModeRoot})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if result.PublishedURL != "" {
		t.Fatalf("expected empty published url, got %q", result.PublishedURL)
	}
}

func TestXCodeChallengeS256UsesBase64URLEncoding(t *testing.T) {
	challenge := xCodeChallengeS256("abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMN")
	if strings.TrimSpace(challenge) == "" {
		t.Fatalf("expected code challenge")
	}
	if strings.ContainsAny(challenge, "+/=") {
		t.Fatalf("challenge contains non-url-safe characters: %q", challenge)
	}
}
