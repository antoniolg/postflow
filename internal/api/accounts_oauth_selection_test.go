package api

import (
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

func TestOAuthCallbackHTMLMultipleAccountsRedirectsToSelectionAndReplaysSameURL(t *testing.T) {
	resetRecentOAuthCallbackOutcomes(t)

	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	provider := &oauthReplayTestProvider{connectedAccounts: oauthReplaySelectionAccounts()}
	srv := Server{
		Store:             store,
		DataDir:           tempDir,
		DefaultMaxRetries: 3,
		Registry:          postflow.NewProviderRegistry(provider),
		PublicBaseURL:     "https://postflow.example",
	}
	h := srv.Handler()

	state := "state_selection_" + strings.ReplaceAll(t.Name(), "/", "_")
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
		t.Fatalf("expected first callback redirect, got %d", w1.Code)
	}
	location1 := w1.Header().Get("Location")
	parsed1, err := url.Parse(location1)
	if err != nil {
		t.Fatalf("parse first redirect location: %v", err)
	}
	if got := parsed1.Query().Get("view"); got != "settings" {
		t.Fatalf("expected settings selection redirect, got %q", location1)
	}
	selectionID := parsed1.Query().Get("oauth_select")
	if selectionID == "" {
		t.Fatalf("expected oauth_select query in redirect, got %q", location1)
	}
	if parsed1.Query().Get("accounts_success") != "" || parsed1.Query().Get("accounts_error") != "" {
		t.Fatalf("expected pending selection redirect without final status, got %q", location1)
	}

	accountsAfterFirst, err := store.ListAccounts(t.Context())
	if err != nil {
		t.Fatalf("list accounts after first callback: %v", err)
	}
	if len(accountsAfterFirst) != 0 {
		t.Fatalf("expected no persisted accounts before selection, got %d", len(accountsAfterFirst))
	}
	pending, err := store.GetOAuthPendingAccountSelection(t.Context(), selectionID)
	if err != nil {
		t.Fatalf("expected pending selection to be stored: %v", err)
	}
	if pending.Platform != domain.PlatformLinkedIn {
		t.Fatalf("expected linkedin pending selection, got %s", pending.Platform)
	}
	outcome, ok := recentOAuthCallbackOutcome(state)
	if !ok || !outcome.Success || outcome.SelectionID != selectionID {
		t.Fatalf("expected callback outcome cache to point at selection, got ok=%v outcome=%+v", ok, outcome)
	}

	req2 := httptest.NewRequest(http.MethodGet, callbackURL, nil)
	req2.Header.Set("Accept", "text/html")
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	if w2.Code != http.StatusSeeOther {
		t.Fatalf("expected replay callback redirect, got %d", w2.Code)
	}
	location2 := w2.Header().Get("Location")
	parsed2, err := url.Parse(location2)
	if err != nil {
		t.Fatalf("parse replay redirect location: %v", err)
	}
	if got := parsed2.Query().Get("oauth_select"); got != selectionID {
		t.Fatalf("expected replay redirect to same selection, got %q want %q", got, selectionID)
	}
	if parsed2.Query().Get("accounts_error") != "" {
		t.Fatalf("expected replay to avoid invalid state error, got %q", location2)
	}
}

func TestHandleOAuthSelectPersistsOnlyCheckedAccounts(t *testing.T) {
	resetRecentOAuthCallbackOutcomes(t)

	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	accounts := oauthReplaySelectionAccounts()
	state := "state_selection_submit_" + strings.ReplaceAll(t.Name(), "/", "_")
	selectionID, err := srv.createOAuthPendingSelection(t.Context(), oauthPendingSelectionPayload{
		OAuthState: state,
		Platform:   domain.PlatformLinkedIn,
		Accounts:   accounts,
	})
	if err != nil {
		t.Fatalf("create pending selection: %v", err)
	}
	rememberOAuthCallbackOutcome(state, true, "", selectionID)

	form := url.Values{}
	form.Set("selection_id", selectionID)
	form.Set("return_to", "/?view=settings")
	form.Add("account_key", oauthConnectedAccountKey(accounts[0]))

	req := httptest.NewRequest(http.MethodPost, "/oauth/select", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected selection submit redirect, got %d", w.Code)
	}
	location := w.Header().Get("Location")
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse selection submit redirect: %v", err)
	}
	if got := parsed.Query().Get("view"); got != "settings" {
		t.Fatalf("expected redirect back to settings, got %q", location)
	}
	if got := parsed.Query().Get("accounts_success"); got != "1 account connected" {
		t.Fatalf("expected success message after selection, got %q", got)
	}

	persistedPersonal, err := store.GetAccountByPlatformExternalID(t.Context(), domain.PlatformLinkedIn, domain.AccountKindPersonal, accounts[0].ExternalAccountID)
	if err != nil {
		t.Fatalf("expected selected personal account to be persisted: %v", err)
	}
	if persistedPersonal.DisplayName != accounts[0].DisplayName {
		t.Fatalf("unexpected persisted personal account: %+v", persistedPersonal)
	}
	if _, err := store.GetAccountByPlatformExternalID(t.Context(), domain.PlatformLinkedIn, domain.AccountKindOrganization, accounts[1].ExternalAccountID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected organization account to stay disconnected, got %v", err)
	}
	if _, err := store.GetOAuthPendingAccountSelection(t.Context(), selectionID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected pending selection to be deleted, got %v", err)
	}
	outcome, ok := recentOAuthCallbackOutcome(state)
	if !ok || !outcome.Success || outcome.Message != "1 account connected" || outcome.SelectionID != "" {
		t.Fatalf("expected callback outcome cache to be finalized, got ok=%v outcome=%+v", ok, outcome)
	}
}

func TestSettingsViewRendersOAuthPendingSelectionChoices(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	accounts := oauthReplaySelectionAccounts()
	selectionID, err := srv.createOAuthPendingSelection(t.Context(), oauthPendingSelectionPayload{
		Platform: domain.PlatformLinkedIn,
		Accounts: accounts,
	})
	if err != nil {
		t.Fatalf("create pending selection: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/?view=settings&oauth_select="+url.QueryEscape(selectionID), nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected settings view ok, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "choose accounts to connect") {
		t.Fatalf("expected oauth selection copy in settings view")
	}
	if !strings.Contains(body, "action=\"/oauth/select\"") {
		t.Fatalf("expected oauth selection form action in settings view")
	}
	for _, account := range accounts {
		if !strings.Contains(body, account.DisplayName) {
			t.Fatalf("expected pending account %q in settings view", account.DisplayName)
		}
	}
	personalKey := oauthConnectedAccountKey(accounts[0])
	if !strings.Contains(body, "value=\""+personalKey+"\" checked") {
		t.Fatalf("expected personal account to be preselected in settings view")
	}
	orgKey := oauthConnectedAccountKey(accounts[1])
	if strings.Contains(body, "value=\""+orgKey+"\" checked") {
		t.Fatalf("expected organization account to start unchecked in settings view")
	}
}
