package api

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/db"
	"github.com/antoniolg/postflow/internal/domain"
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

func TestRequestClientLabelDoesNotExposeCredentials(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/schedule", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	got := requestClientLabel(req)
	if strings.Contains(got, "secret-token") {
		t.Fatalf("requestClientLabel exposed bearer token: %q", got)
	}
	if !strings.HasPrefix(got, "bearer:") {
		t.Fatalf("expected bearer fingerprint label, got %q", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/schedule", nil)
	req.Header.Set("X-API-Key", "secret-token")
	got = requestClientLabel(req)
	if strings.Contains(got, "secret-token") {
		t.Fatalf("requestClientLabel exposed api key: %q", got)
	}
	if !strings.HasPrefix(got, "key:") {
		t.Fatalf("expected api key fingerprint label, got %q", got)
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

func TestAuthMiddlewareAllowsSignedMediaContentURL(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	mediaPath := filepath.Join(tempDir, "clip.txt")
	if err := os.WriteFile(mediaPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write media file: %v", err)
	}
	created, err := store.CreateMedia(t.Context(), domain.Media{
		Kind:         "image",
		OriginalName: "clip.txt",
		StoragePath:  mediaPath,
		MimeType:     "text/plain",
		SizeBytes:    5,
	})
	if err != nil {
		t.Fatalf("create media: %v", err)
	}

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3, APIToken: "secret-token"}
	h := srv.Handler()

	exp := time.Now().UTC().Add(10 * time.Minute).Unix()
	payload := fmt.Sprintf("%s:%d", created.ID, exp)
	sig := srv.credentialsCipher().SignString(payload)

	req := httptest.NewRequest(http.MethodGet, "/media/"+created.ID+"/content?exp="+fmt.Sprintf("%d", exp)+"&sig="+sig, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with signed media url, got %d", w.Code)
	}
}

func TestAuthMiddlewareAllowsSignedMediaContentURLWithFilename(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	mediaPath := filepath.Join(tempDir, "review.jpg")
	if err := os.WriteFile(mediaPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write media file: %v", err)
	}
	created, err := store.CreateMedia(t.Context(), domain.Media{
		Kind:         "image",
		OriginalName: "review.jpg",
		StoragePath:  mediaPath,
		MimeType:     "image/jpeg",
		SizeBytes:    5,
	})
	if err != nil {
		t.Fatalf("create media: %v", err)
	}

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3, APIToken: "secret-token"}
	h := srv.Handler()

	exp := time.Now().UTC().Add(10 * time.Minute).Unix()
	payload := fmt.Sprintf("%s:%d", created.ID, exp)
	sig := srv.credentialsCipher().SignString(payload)

	req := httptest.NewRequest(http.MethodGet, "/media/"+created.ID+"/content/"+created.ID+".jpg?exp="+fmt.Sprintf("%d", exp)+"&sig="+sig, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with signed media url filename, got %d", w.Code)
	}
	if got := w.Header().Get("Content-Type"); !strings.Contains(got, "image/jpeg") {
		t.Fatalf("expected image/jpeg content type, got %q", got)
	}
}

func TestAuthMiddlewareAllowsPathSignedMediaContentURL(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	mediaPath := filepath.Join(tempDir, "review.jpg")
	if err := os.WriteFile(mediaPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write media file: %v", err)
	}
	created, err := store.CreateMedia(t.Context(), domain.Media{
		Kind:         "image",
		OriginalName: "review.jpg",
		StoragePath:  mediaPath,
		MimeType:     "image/jpeg",
		SizeBytes:    5,
	})
	if err != nil {
		t.Fatalf("create media: %v", err)
	}

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3, APIToken: "secret-token"}
	h := srv.Handler()

	exp := time.Now().UTC().Add(24 * time.Hour).Unix()
	payload := fmt.Sprintf("%s:%d", created.ID, exp)
	sig := srv.credentialsCipher().SignString(payload)

	req := httptest.NewRequest(http.MethodGet, "/media/"+created.ID+"/content/"+fmt.Sprintf("%d", exp)+"/"+sig+"/"+created.ID+".jpg", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with path signed media url, got %d", w.Code)
	}
	if got := w.Header().Get("Content-Type"); !strings.Contains(got, "image/jpeg") {
		t.Fatalf("expected image/jpeg content type, got %q", got)
	}
}

func TestRobotsTXTIsPublicWhenAuthEnabled(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{
		Store:             store,
		DataDir:           tempDir,
		DefaultMaxRetries: 3,
		APIToken:          "secret-token",
		UIBasicUser:       "antonio",
		UIBasicPass:       "pass123",
	}
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/robots.txt", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected public robots.txt, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Allow: /") {
		t.Fatalf("expected robots.txt to allow crawlers, got %q", body)
	}
}

func TestAuthMiddlewareAllowsPublicAssetsWithoutCredentials(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{
		Store:             store,
		DataDir:           tempDir,
		DefaultMaxRetries: 3,
		APIToken:          "secret-token",
		UIBasicUser:       "antonio",
		UIBasicPass:       "pass123",
	}
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/assets/icons/site.webmanifest", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for public asset without credentials, got %d", w.Code)
	}
}
