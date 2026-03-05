package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antoniolg/postflow/internal/db"
	"github.com/antoniolg/postflow/internal/domain"
	"github.com/antoniolg/postflow/internal/postflow"
)

func TestListAccountsHTMLRedirectsToSettings(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	_ = testAccountID(t, store)

	req := httptest.NewRequest(http.MethodGet, "/accounts", nil)
	req.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect for html /accounts, got %d", w.Code)
	}
	if got := w.Header().Get("Location"); got != "/?view=settings" {
		t.Fatalf("expected settings redirect location, got %q", got)
	}
}

func TestSettingsViewRendersAccountsBlockWithActions(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	connectedID := testAccountID(t, store)
	liAccount, err := store.UpsertAccount(t.Context(), db.UpsertAccountParams{
		Platform:          domain.PlatformLinkedIn,
		DisplayName:       "LinkedIn Test",
		ExternalAccountID: "li-test-account",
		AuthMethod:        domain.AuthMethodOAuth,
		Status:            domain.AccountStatusConnected,
	})
	if err != nil {
		t.Fatalf("create linkedin account: %v", err)
	}
	if err := store.DisconnectAccount(t.Context(), liAccount.ID); err != nil {
		t.Fatalf("disconnect linkedin account: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/?view=settings", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for settings view, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "action=\"/accounts/"+connectedID+"/disconnect\"") {
		t.Fatalf("expected disconnect action for connected account")
	}
	if !strings.Contains(body, "action=\"/accounts/"+connectedID+"/x-premium\"") {
		t.Fatalf("expected x premium toggle action for x account")
	}
	if strings.Contains(body, "action=\"/accounts/"+liAccount.ID+"/x-premium\"") {
		t.Fatalf("did not expect x premium toggle for non-x account")
	}
	if !strings.Contains(body, "action=\"/accounts/"+liAccount.ID+"/connect\"") {
		t.Fatalf("expected connect action for disconnected account")
	}
	if !strings.Contains(body, "action=\"/accounts/"+liAccount.ID+"/delete\"") {
		t.Fatalf("expected delete action for disconnected account")
	}
	for _, oauthStartPath := range []string{
		"action=\"/oauth/x/start\"",
		"action=\"/oauth/linkedin/start\"",
		"action=\"/oauth/facebook/start\"",
		"action=\"/oauth/instagram/start\"",
	} {
		if !strings.Contains(body, oauthStartPath) {
			t.Fatalf("expected oauth start action %s in settings", oauthStartPath)
		}
	}
	if !strings.Contains(body, "@media (max-width: 520px)") {
		t.Fatalf("expected mobile breakpoint css in settings view")
	}
	if !strings.Contains(body, ".settings-accounts { grid-template-columns: 1fr; }") {
		t.Fatalf("expected settings accounts to collapse to one column on small screens")
	}
}

func TestDisconnectAccountFormRedirectsBackToSettings(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	accountID := testAccountID(t, store)
	form := url.Values{}
	form.Set("return_to", "/?view=settings")

	req := httptest.NewRequest(http.MethodPost, "/accounts/"+accountID+"/disconnect", bytes.NewBufferString(form.Encode()))
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect for html account disconnect, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	parsed, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse redirect location: %v", err)
	}
	if got := parsed.Query().Get("view"); got != "settings" {
		t.Fatalf("expected view=settings in redirect, got %q", got)
	}
	if got := parsed.Query().Get("accounts_success"); got != "account disconnected" {
		t.Fatalf("expected account disconnect success message, got %q", got)
	}
}

func TestConnectAccountFormRedirectsBackToSettings(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	accountID := testAccountID(t, store)
	if err := srv.saveCredentials(t.Context(), accountID, postflow.Credentials{
		AccessToken:       "token-connect-test",
		AccessTokenSecret: "secret-connect-test",
		TokenType:         "oauth1",
	}); err != nil {
		t.Fatalf("save account credentials: %v", err)
	}
	if err := store.DisconnectAccount(t.Context(), accountID); err != nil {
		t.Fatalf("disconnect account: %v", err)
	}

	form := url.Values{}
	form.Set("return_to", "/?view=settings")
	req := httptest.NewRequest(http.MethodPost, "/accounts/"+accountID+"/connect", bytes.NewBufferString(form.Encode()))
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect for html account connect, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	parsed, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse redirect location: %v", err)
	}
	if got := parsed.Query().Get("view"); got != "settings" {
		t.Fatalf("expected view=settings in redirect, got %q", got)
	}
	if got := parsed.Query().Get("accounts_success"); got != "account connected" {
		t.Fatalf("expected account connect success message, got %q", got)
	}

	account, err := store.GetAccount(t.Context(), accountID)
	if err != nil {
		t.Fatalf("get account after connect: %v", err)
	}
	if account.Status != domain.AccountStatusConnected {
		t.Fatalf("expected account to be connected after connect action, got %s", account.Status)
	}
}

func TestSetXPremiumFormRedirectsBackToSettings(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	accountID := testAccountID(t, store)
	form := url.Values{}
	form.Set("return_to", "/?view=settings")
	form.Add("x_premium", "0")
	form.Add("x_premium", "1")

	req := httptest.NewRequest(http.MethodPost, "/accounts/"+accountID+"/x-premium", bytes.NewBufferString(form.Encode()))
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect for html x premium update, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	parsed, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse redirect location: %v", err)
	}
	if got := parsed.Query().Get("view"); got != "settings" {
		t.Fatalf("expected view=settings in redirect, got %q", got)
	}
	if got := parsed.Query().Get("accounts_success"); got != "x premium updated" {
		t.Fatalf("expected x premium success message, got %q", got)
	}

	account, err := store.GetAccount(t.Context(), accountID)
	if err != nil {
		t.Fatalf("get account after x premium update: %v", err)
	}
	if !account.XPremium {
		t.Fatalf("expected x premium to be enabled")
	}
}

func TestSetXPremiumRejectsNonXAccount(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	liAccount, err := store.UpsertAccount(t.Context(), db.UpsertAccountParams{
		Platform:          domain.PlatformLinkedIn,
		DisplayName:       "LinkedIn Premium Toggle",
		ExternalAccountID: "li-premium-toggle",
		AuthMethod:        domain.AuthMethodOAuth,
		Status:            domain.AccountStatusConnected,
	})
	if err != nil {
		t.Fatalf("create linkedin account: %v", err)
	}

	form := url.Values{}
	form.Set("return_to", "/?view=settings")
	form.Add("x_premium", "1")
	req := httptest.NewRequest(http.MethodPost, "/accounts/"+liAccount.ID+"/x-premium", bytes.NewBufferString(form.Encode()))
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect for html non-x premium update, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	parsed, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse redirect location: %v", err)
	}
	if got := parsed.Query().Get("accounts_error"); got == "" {
		t.Fatalf("expected accounts_error in redirect when updating non-x premium")
	}
}
