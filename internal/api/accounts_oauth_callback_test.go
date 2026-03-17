package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/db"
	"github.com/antoniolg/postflow/internal/domain"
	"github.com/antoniolg/postflow/internal/postflow"
)

type oauthReplayTestProvider struct {
	callbackErr       error
	connectedAccounts []postflow.ConnectedAccount
}

func (p *oauthReplayTestProvider) Platform() domain.Platform {
	return domain.PlatformLinkedIn
}

func (p *oauthReplayTestProvider) ValidateDraft(_ context.Context, _ domain.SocialAccount, _ postflow.Draft) ([]string, error) {
	return nil, nil
}

func (p *oauthReplayTestProvider) Publish(_ context.Context, _ domain.SocialAccount, _ postflow.Credentials, _ domain.Post, _ postflow.PublishOptions) (postflow.PublishResult, error) {
	return postflow.PublishResult{ExternalID: "ok"}, nil
}

func (p *oauthReplayTestProvider) RefreshIfNeeded(_ context.Context, _ domain.SocialAccount, credentials postflow.Credentials) (postflow.Credentials, bool, error) {
	return credentials, false, nil
}

func (p *oauthReplayTestProvider) StartOAuth(_ context.Context, in postflow.OAuthStartInput) (postflow.OAuthStartOutput, error) {
	return postflow.OAuthStartOutput{AuthURL: "https://example.com/oauth?state=" + url.QueryEscape(in.State)}, nil
}

func (p *oauthReplayTestProvider) HandleOAuthCallback(_ context.Context, _ postflow.OAuthCallbackInput) ([]postflow.ConnectedAccount, error) {
	if p.callbackErr != nil {
		return nil, p.callbackErr
	}
	if len(p.connectedAccounts) > 0 {
		return append([]postflow.ConnectedAccount(nil), p.connectedAccounts...), nil
	}
	return []postflow.ConnectedAccount{
		{
			Platform:          domain.PlatformLinkedIn,
			AccountKind:       domain.AccountKindPersonal,
			DisplayName:       "LinkedIn Test",
			ExternalAccountID: "linkedin_test_id",
			Credentials: postflow.Credentials{
				AccessToken: "token",
				TokenType:   "Bearer",
			},
		},
	}, nil
}

func oauthReplaySelectionAccounts() []postflow.ConnectedAccount {
	return []postflow.ConnectedAccount{
		{
			Platform:          domain.PlatformLinkedIn,
			AccountKind:       domain.AccountKindPersonal,
			DisplayName:       "LinkedIn Personal",
			ExternalAccountID: "linkedin-personal-test-id",
			Credentials: postflow.Credentials{
				AccessToken: "personal-token",
				TokenType:   "Bearer",
			},
		},
		{
			Platform:          domain.PlatformLinkedIn,
			AccountKind:       domain.AccountKindOrganization,
			DisplayName:       "LinkedIn Org",
			ExternalAccountID: "linkedin-org-test-id",
			Credentials: postflow.Credentials{
				AccessToken: "org-token",
				TokenType:   "Bearer",
			},
		},
	}
}

type oauthContextResilientProvider struct{}

func (p *oauthContextResilientProvider) Platform() domain.Platform {
	return domain.PlatformLinkedIn
}

func (p *oauthContextResilientProvider) ValidateDraft(_ context.Context, _ domain.SocialAccount, _ postflow.Draft) ([]string, error) {
	return nil, nil
}

func (p *oauthContextResilientProvider) Publish(_ context.Context, _ domain.SocialAccount, _ postflow.Credentials, _ domain.Post, _ postflow.PublishOptions) (postflow.PublishResult, error) {
	return postflow.PublishResult{ExternalID: "ok"}, nil
}

func (p *oauthContextResilientProvider) RefreshIfNeeded(_ context.Context, _ domain.SocialAccount, credentials postflow.Credentials) (postflow.Credentials, bool, error) {
	return credentials, false, nil
}

func (p *oauthContextResilientProvider) StartOAuth(_ context.Context, in postflow.OAuthStartInput) (postflow.OAuthStartOutput, error) {
	return postflow.OAuthStartOutput{AuthURL: "https://example.com/oauth?state=" + url.QueryEscape(in.State)}, nil
}

func (p *oauthContextResilientProvider) HandleOAuthCallback(ctx context.Context, _ postflow.OAuthCallbackInput) ([]postflow.ConnectedAccount, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(15 * time.Millisecond):
	}
	return []postflow.ConnectedAccount{
		{
			Platform:          domain.PlatformLinkedIn,
			AccountKind:       domain.AccountKindPersonal,
			DisplayName:       "LinkedIn Resilient",
			ExternalAccountID: "linkedin_resilient_id",
			Credentials: postflow.Credentials{
				AccessToken: "token",
				TokenType:   "Bearer",
			},
		},
	}, nil
}

func TestOAuthCallbackReplayInHTMLFlowReturnsSuccess(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	provider := &oauthReplayTestProvider{}
	srv := Server{
		Store:             store,
		DataDir:           tempDir,
		DefaultMaxRetries: 3,
		Registry:          postflow.NewProviderRegistry(provider),
		PublicBaseURL:     "https://postflow.example",
	}
	h := srv.Handler()

	state := "state_replay_" + strings.ReplaceAll(t.Name(), "/", "_")
	_, err = store.CreateOAuthState(t.Context(), domain.OauthState{
		Platform:     domain.PlatformLinkedIn,
		State:        state,
		CodeVerifier: "verifier",
		ExpiresAt:    time.Now().UTC().Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create oauth state: %v", err)
	}

	callbackURL := "/oauth/linkedin/callback?state=" + url.QueryEscape(state) + "&code=auth_code"

	req1 := httptest.NewRequest(http.MethodGet, callbackURL, nil)
	req1.Header.Set("Accept", "text/html")
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, req1)
	if w1.Code != http.StatusSeeOther {
		t.Fatalf("expected first callback to redirect, got %d", w1.Code)
	}
	location1 := w1.Header().Get("Location")
	if !strings.Contains(location1, "accounts_success=") {
		t.Fatalf("expected success redirect in first callback, got %q", location1)
	}
	linkedAfterFirst, err := store.GetAccountByPlatformExternalID(t.Context(), domain.PlatformLinkedIn, domain.AccountKindPersonal, "linkedin_test_id")
	if err != nil {
		t.Fatalf("expected linkedin account persisted after first callback: %v", err)
	}
	if linkedAfterFirst.Status != domain.AccountStatusConnected {
		t.Fatalf("expected linked account to be connected after first callback, got %s", linkedAfterFirst.Status)
	}
	accountsAfterFirst, err := store.ListAccounts(t.Context())
	if err != nil {
		t.Fatalf("list accounts after first callback: %v", err)
	}
	if len(accountsAfterFirst) != 1 {
		t.Fatalf("expected exactly one linked account after first callback, got %d", len(accountsAfterFirst))
	}
	_, err = store.ConsumeOAuthState(t.Context(), state)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected oauth state to be consumed after first callback, got %v", err)
	}

	req2 := httptest.NewRequest(http.MethodGet, callbackURL, nil)
	req2.Header.Set("Accept", "text/html")
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	if w2.Code != http.StatusSeeOther {
		t.Fatalf("expected replay callback to redirect, got %d", w2.Code)
	}
	location2 := w2.Header().Get("Location")
	if strings.Contains(location2, "accounts_error=") {
		t.Fatalf("expected replay callback to avoid invalid state error, got %q", location2)
	}
	if !strings.Contains(location2, "accounts_success=1+account+connected") {
		t.Fatalf("expected replay callback to preserve original success message, got %q", location2)
	}
	linkedAfterReplay, err := store.GetAccountByPlatformExternalID(t.Context(), domain.PlatformLinkedIn, domain.AccountKindPersonal, "linkedin_test_id")
	if err != nil {
		t.Fatalf("expected linkedin account to remain persisted after replay: %v", err)
	}
	if linkedAfterReplay.ID != linkedAfterFirst.ID {
		t.Fatalf("expected replay to keep same linked account id, got first=%s replay=%s", linkedAfterFirst.ID, linkedAfterReplay.ID)
	}
	accountsAfterReplay, err := store.ListAccounts(t.Context())
	if err != nil {
		t.Fatalf("list accounts after replay callback: %v", err)
	}
	if len(accountsAfterReplay) != 1 {
		t.Fatalf("expected replay to keep exactly one linked account, got %d", len(accountsAfterReplay))
	}
}

func TestOAuthCallbackReplayInHTMLFlowReplaysOriginalError(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	provider := &oauthReplayTestProvider{callbackErr: errors.New("linkedin organization discovery failed")}
	srv := Server{
		Store:             store,
		DataDir:           tempDir,
		DefaultMaxRetries: 3,
		Registry:          postflow.NewProviderRegistry(provider),
		PublicBaseURL:     "https://postflow.example",
	}
	h := srv.Handler()

	state := "state_error_replay_" + strings.ReplaceAll(t.Name(), "/", "_")
	_, err = store.CreateOAuthState(t.Context(), domain.OauthState{
		Platform:     domain.PlatformLinkedIn,
		State:        state,
		CodeVerifier: "verifier",
		ExpiresAt:    time.Now().UTC().Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create oauth state: %v", err)
	}

	callbackURL := "/oauth/linkedin/callback?state=" + url.QueryEscape(state) + "&code=auth_code"

	req1 := httptest.NewRequest(http.MethodGet, callbackURL, nil)
	req1.Header.Set("Accept", "text/html")
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, req1)
	if w1.Code != http.StatusSeeOther {
		t.Fatalf("expected first callback to redirect, got %d", w1.Code)
	}
	location1 := w1.Header().Get("Location")
	if !strings.Contains(location1, "accounts_error=linkedin+organization+discovery+failed") {
		t.Fatalf("expected first callback to preserve original error, got %q", location1)
	}

	req2 := httptest.NewRequest(http.MethodGet, callbackURL, nil)
	req2.Header.Set("Accept", "text/html")
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	if w2.Code != http.StatusSeeOther {
		t.Fatalf("expected replay callback to redirect, got %d", w2.Code)
	}
	location2 := w2.Header().Get("Location")
	if !strings.Contains(location2, "accounts_error=linkedin+organization+discovery+failed") {
		t.Fatalf("expected replay callback to return original error, got %q", location2)
	}
	if strings.Contains(location2, "invalid+oauth+state") {
		t.Fatalf("expected replay callback to avoid invalid oauth state, got %q", location2)
	}
}

func TestOAuthCallbackHTMLDetachedFromRequestCancellation(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	provider := &oauthContextResilientProvider{}
	srv := Server{
		Store:             store,
		DataDir:           tempDir,
		DefaultMaxRetries: 3,
		Registry:          postflow.NewProviderRegistry(provider),
		PublicBaseURL:     "https://postflow.example",
	}
	h := srv.Handler()

	state := "state_ctx_" + strings.ReplaceAll(t.Name(), "/", "_")
	_, err = store.CreateOAuthState(t.Context(), domain.OauthState{
		Platform:     domain.PlatformLinkedIn,
		State:        state,
		CodeVerifier: "verifier",
		ExpiresAt:    time.Now().UTC().Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create oauth state: %v", err)
	}

	callbackURL := "/oauth/linkedin/callback?state=" + url.QueryEscape(state) + "&code=auth_code"
	baseReq := httptest.NewRequest(http.MethodGet, callbackURL, nil)
	baseReq.Header.Set("Accept", "text/html")
	ctx, cancel := context.WithCancel(baseReq.Context())
	req := baseReq.WithContext(ctx)
	cancel()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected callback redirect after canceled browser context, got %d", w.Code)
	}
	location := w.Header().Get("Location")
	if !strings.Contains(location, "accounts_success=1+account+connected") {
		t.Fatalf("expected success redirect after detached callback handling, got %q", location)
	}

	account, err := store.GetAccountByPlatformExternalID(t.Context(), domain.PlatformLinkedIn, domain.AccountKindPersonal, "linkedin_resilient_id")
	if err != nil {
		t.Fatalf("expected account persisted despite request cancellation: %v", err)
	}
	if account.Status != domain.AccountStatusConnected {
		t.Fatalf("expected account connected after callback, got %s", account.Status)
	}
}
