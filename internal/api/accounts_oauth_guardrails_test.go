package api

import (
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

func TestOAuthCompletedStateCacheBehavior(t *testing.T) {
	resetRecentCompletedOAuthStates(t)

	rememberCompletedOAuthState("state_recent")
	if !wasRecentlyCompletedOAuthState("state_recent") {
		t.Fatalf("expected remembered state to be considered recently completed")
	}
	if wasRecentlyCompletedOAuthState("missing") {
		t.Fatalf("did not expect missing state to be considered recently completed")
	}

	recentCompletedOAuthStates.Store("state_invalid_type", "not-a-time")
	if wasRecentlyCompletedOAuthState("state_invalid_type") {
		t.Fatalf("invalid typed state should not be considered recently completed")
	}

	recentCompletedOAuthStates.Store("state_expired", time.Now().UTC().Add(-1*time.Minute))
	if wasRecentlyCompletedOAuthState("state_expired") {
		t.Fatalf("expired state should not be considered recently completed")
	}
}

func resetRecentCompletedOAuthStates(t *testing.T) {
	t.Helper()
	recentCompletedOAuthStates.Range(func(key, _ any) bool {
		recentCompletedOAuthStates.Delete(key)
		return true
	})
	t.Cleanup(func() {
		recentCompletedOAuthStates.Range(func(key, _ any) bool {
			recentCompletedOAuthStates.Delete(key)
			return true
		})
	})
}
