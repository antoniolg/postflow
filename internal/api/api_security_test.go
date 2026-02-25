package api

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/antoniolg/publisher/internal/db"
)

func TestAuthMiddlewareRejectsWhenMissingCredentials(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3, APIToken: "secret-token"}
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/schedule", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestAuthMiddlewareAllowsBearerToken(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3, APIToken: "secret-token"}
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/schedule", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestAuthMiddlewareAllowsBasicAuth(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3, UIBasicUser: "antonio", UIBasicPass: "pass123"}
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	basic := base64.StdEncoding.EncodeToString([]byte("antonio:pass123"))
	req.Header.Set("Authorization", "Basic "+basic)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestRateLimitMiddlewareLimitsRequests(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3, RateLimitRPM: 1}
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/schedule", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected first request 200, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/schedule", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected second request 429, got %d", w.Code)
	}
}

func TestRequestIDHeaderIsAlwaysPresent(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/schedule", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Header().Get("X-Request-Id") == "" {
		t.Fatalf("expected X-Request-Id header to be set")
	}
}
