package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/db"
	"github.com/antoniolg/postflow/internal/domain"
	"github.com/antoniolg/postflow/internal/postflow"
)

type pkceCaptureProvider struct {
	lastVerifier string
}

func (p *pkceCaptureProvider) Platform() domain.Platform {
	return domain.PlatformX
}

func (p *pkceCaptureProvider) ValidateDraft(_ context.Context, _ domain.SocialAccount, _ postflow.Draft) ([]string, error) {
	return nil, nil
}

func (p *pkceCaptureProvider) Publish(_ context.Context, _ domain.SocialAccount, _ postflow.Credentials, _ domain.Post, _ postflow.PublishOptions) (string, error) {
	return "ok", nil
}

func (p *pkceCaptureProvider) RefreshIfNeeded(_ context.Context, _ domain.SocialAccount, credentials postflow.Credentials) (postflow.Credentials, bool, error) {
	return credentials, false, nil
}

func (p *pkceCaptureProvider) StartOAuth(_ context.Context, in postflow.OAuthStartInput) (postflow.OAuthStartOutput, error) {
	p.lastVerifier = in.CodeVerifier
	return postflow.OAuthStartOutput{AuthURL: "https://x.example/auth"}, nil
}

func (p *pkceCaptureProvider) HandleOAuthCallback(_ context.Context, _ postflow.OAuthCallbackInput) ([]postflow.ConnectedAccount, error) {
	return nil, nil
}

type oauth1CaptureProvider struct {
	lastStartInput    postflow.OAuthStartInput
	lastCallbackInput postflow.OAuthCallbackInput
}

func (p *oauth1CaptureProvider) Platform() domain.Platform {
	return domain.PlatformX
}

func (p *oauth1CaptureProvider) ValidateDraft(_ context.Context, _ domain.SocialAccount, _ postflow.Draft) ([]string, error) {
	return nil, nil
}

func (p *oauth1CaptureProvider) Publish(_ context.Context, _ domain.SocialAccount, _ postflow.Credentials, _ domain.Post, _ postflow.PublishOptions) (string, error) {
	return "ok", nil
}

func (p *oauth1CaptureProvider) RefreshIfNeeded(_ context.Context, _ domain.SocialAccount, credentials postflow.Credentials) (postflow.Credentials, bool, error) {
	return credentials, false, nil
}

func (p *oauth1CaptureProvider) StartOAuth(_ context.Context, in postflow.OAuthStartInput) (postflow.OAuthStartOutput, error) {
	p.lastStartInput = in
	return postflow.OAuthStartOutput{
		AuthURL:      "https://x.example/auth",
		CodeVerifier: "oauth1:req-token:req-secret",
	}, nil
}

func (p *oauth1CaptureProvider) HandleOAuthCallback(_ context.Context, in postflow.OAuthCallbackInput) ([]postflow.ConnectedAccount, error) {
	p.lastCallbackInput = in
	return []postflow.ConnectedAccount{{
		Platform:          domain.PlatformX,
		AccountKind:       domain.AccountKindDefault,
		DisplayName:       "@postflowbot",
		ExternalAccountID: "2244994945",
		Credentials: postflow.Credentials{
			AccessToken:       "user-token",
			AccessTokenSecret: "user-secret",
			TokenType:         "oauth1",
		},
	}}, nil
}

func TestAccountReturnToSanitizesUnsafeTargets(t *testing.T) {
	testCases := []struct {
		name     string
		raw      string
		expected string
	}{
		{name: "empty defaults to settings", raw: "", expected: settingsViewURL},
		{name: "relative view path allowed", raw: "/?view=calendar", expected: "/?view=calendar"},
		{name: "absolute url rejected", raw: "https://evil.example/steal", expected: settingsViewURL},
		{name: "scheme-relative url rejected", raw: "//evil.example/steal", expected: settingsViewURL},
	}
	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			target := "/?return_to=" + url.QueryEscape(tc.raw)
			req := httptest.NewRequest(http.MethodGet, target, nil)
			if got := accountReturnTo(req); got != tc.expected {
				t.Fatalf("unexpected return_to resolution: got=%q want=%q", got, tc.expected)
			}
		})
	}
}

func TestOAuthCallbackURLSelectionOrder(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://internal.local/path", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "postflow.example")

	withPublic := Server{PublicBaseURL: "https://public.example"}
	if got := withPublic.oauthCallbackURL(req, domain.PlatformLinkedIn); got != "https://public.example/oauth/linkedin/callback" {
		t.Fatalf("expected callback URL from public base, got %q", got)
	}

	withForwarded := Server{}
	if got := withForwarded.oauthCallbackURL(req, domain.PlatformLinkedIn); got != "https://postflow.example/oauth/linkedin/callback" {
		t.Fatalf("expected callback URL from forwarded headers, got %q", got)
	}

	reqNoForwarded := httptest.NewRequest(http.MethodGet, "http://localhost:9090/path", nil)
	if got := withForwarded.oauthCallbackURL(reqNoForwarded, domain.PlatformLinkedIn); got != "http://localhost:9090/oauth/linkedin/callback" {
		t.Fatalf("expected callback URL from request host, got %q", got)
	}
}

func TestOAuthCallbackHTMLMissingCodeUsesSafeRedirect(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{
		Store:             store,
		DefaultMaxRetries: 3,
		Registry:          postflow.NewProviderRegistry(&oauthReplayTestProvider{}),
	}
	handler := srv.Handler()

	testCases := []struct {
		name        string
		path        string
		expectError string
	}{
		{
			name:        "uses explicit error_description",
			path:        "/oauth/linkedin/callback?state=s1&error_description=Access+Denied&return_to=%2F%3Fview%3Dcalendar",
			expectError: "Access Denied",
		},
		{
			name:        "rejects unsafe return_to host",
			path:        "/oauth/linkedin/callback?state=s2&error_description=Nope&return_to=https%3A%2F%2Fevil.example%2Fsteal",
			expectError: "Nope",
		},
		{
			name:        "falls back to missing authorization code when no description",
			path:        "/oauth/linkedin/callback?state=s3&return_to=%2F%3Fview%3Dcalendar",
			expectError: "missing authorization code",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Accept", "text/html")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("expected 303 redirect, got %d", rec.Code)
			}
			location := strings.TrimSpace(rec.Header().Get("Location"))
			redirectURL, err := url.Parse(location)
			if err != nil {
				t.Fatalf("parse redirect location: %v", err)
			}
			if redirectURL.Path != "/" {
				t.Fatalf("expected redirect path '/', got %q", redirectURL.Path)
			}
			if view := redirectURL.Query().Get("view"); view != "settings" {
				t.Fatalf("expected redirect view=settings, got %q", view)
			}
			actualError := redirectURL.Query().Get("accounts_error")
			if actualError == "" {
				t.Fatalf("expected accounts_error query in redirect, got %q", location)
			}
			if !strings.EqualFold(actualError, tc.expectError) {
				t.Fatalf("expected redirect error %q, got %q", tc.expectError, actualError)
			}
		})
	}
}

func TestOAuthCallbackOutcomeCacheBehavior(t *testing.T) {
	resetRecentOAuthCallbackOutcomes(t)

	rememberOAuthCallbackOutcome("state_success", true, "1 account connected", "")
	success, ok := recentOAuthCallbackOutcome("state_success")
	if !ok || !success.Success || success.Message != "1 account connected" {
		t.Fatalf("expected remembered success outcome, got ok=%v outcome=%+v", ok, success)
	}
	if _, ok := recentOAuthCallbackOutcome("missing"); ok {
		t.Fatalf("did not expect missing state to have cached outcome")
	}

	recentOAuthCallbackOutcomes.Store("state_invalid_type", "not-an-outcome")
	if _, ok := recentOAuthCallbackOutcome("state_invalid_type"); ok {
		t.Fatalf("invalid typed state should not have cached outcome")
	}

	recentOAuthCallbackOutcomes.Store("state_expired", oauthCallbackOutcome{
		Success:   false,
		Message:   "oauth callback failed",
		ExpiresAt: time.Now().UTC().Add(-1 * time.Minute),
	})
	if _, ok := recentOAuthCallbackOutcome("state_expired"); ok {
		t.Fatalf("expired state should not have cached outcome")
	}
}

func TestHandleOAuthStartGeneratesPKCECompatibleVerifier(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	provider := &pkceCaptureProvider{}
	srv := Server{
		Store:             store,
		DefaultMaxRetries: 3,
		Registry:          postflow.NewProviderRegistry(provider),
		PublicBaseURL:     "https://postflow.example",
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/x/start", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 starting oauth, got %d", rec.Code)
	}
	if got := len(provider.lastVerifier); got < 43 || got > 128 {
		t.Fatalf("code_verifier length = %d, want 43..128", got)
	}
	if ok, _ := regexp.MatchString(`^[A-Za-z0-9._~-]{43,128}$`, provider.lastVerifier); !ok {
		t.Fatalf("code_verifier contains invalid characters: %q", provider.lastVerifier)
	}
}

func TestOAuthCallbackAcceptsOAuth1VerifierParameter(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	provider := &oauth1CaptureProvider{}
	srv := Server{
		Store:             store,
		DefaultMaxRetries: 3,
		Registry:          postflow.NewProviderRegistry(provider),
		PublicBaseURL:     "https://postflow.example",
	}
	handler := srv.Handler()

	startRec := httptest.NewRecorder()
	startReq := httptest.NewRequest(http.MethodPost, "/oauth/x/start", nil)
	handler.ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusOK {
		t.Fatalf("expected 200 oauth start, got %d body=%s", startRec.Code, startRec.Body.String())
	}
	var started struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(startRec.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode start payload: %v", err)
	}
	if strings.TrimSpace(started.State) == "" {
		t.Fatalf("expected state in oauth start payload")
	}

	callbackRec := httptest.NewRecorder()
	callbackReq := httptest.NewRequest(http.MethodGet, "/oauth/x/callback?state="+url.QueryEscape(started.State)+"&oauth_verifier=verifier_1", nil)
	handler.ServeHTTP(callbackRec, callbackReq)
	if callbackRec.Code != http.StatusOK {
		t.Fatalf("expected 200 oauth callback, got %d body=%s", callbackRec.Code, callbackRec.Body.String())
	}
	if provider.lastCallbackInput.Code != "verifier_1" {
		t.Fatalf("callback code = %q, want verifier_1", provider.lastCallbackInput.Code)
	}
	if provider.lastCallbackInput.CodeVerifier != "oauth1:req-token:req-secret" {
		t.Fatalf("callback code verifier = %q, want oauth1 request token payload", provider.lastCallbackInput.CodeVerifier)
	}
}

func resetRecentOAuthCallbackOutcomes(t *testing.T) {
	t.Helper()
	recentOAuthCallbackOutcomes.Range(func(key, _ any) bool {
		recentOAuthCallbackOutcomes.Delete(key)
		return true
	})
	t.Cleanup(func() {
		recentOAuthCallbackOutcomes.Range(func(key, _ any) bool {
			recentOAuthCallbackOutcomes.Delete(key)
			return true
		})
	})
}
