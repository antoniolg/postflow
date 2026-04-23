package api

import (
	"bytes"
	"encoding/json"
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

func TestValidatePostEndpoint(t *testing.T) {
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
		Registry:          testRegistryWithRealLinkedIn(),
	}
	h := srv.Handler()

	payload, _ := json.Marshal(map[string]any{
		"account_id":   testAccountID(t, store),
		"text":         "validar",
		"scheduled_at": time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339),
	})
	req := httptest.NewRequest(http.MethodPost, "/posts/validate", bytes.NewReader(payload))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	valid, _ := resp["valid"].(bool)
	if !valid {
		t.Fatalf("expected valid=true")
	}
}

func TestValidatePostEndpointDraftMode(t *testing.T) {
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
		Registry:          testRegistryWithRealLinkedIn(),
	}
	h := srv.Handler()

	payload, _ := json.Marshal(map[string]any{
		"account_id": testAccountID(t, store),
		"text":       "idea",
	})
	req := httptest.NewRequest(http.MethodPost, "/posts/validate", bytes.NewReader(payload))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	normalized, _ := resp["normalized"].(map[string]any)
	if scheduledAt, _ := normalized["scheduled_at"].(string); scheduledAt != "" {
		t.Fatalf("expected empty scheduled_at in draft mode, got %q", scheduledAt)
	}
}

func TestValidatePostEndpointRejectsTooManySegments(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	segments := make([]map[string]any, 0, 501)
	for i := 0; i < 501; i++ {
		segments = append(segments, map[string]any{"text": "segment"})
	}
	payload, _ := json.Marshal(map[string]any{
		"account_id": testAccountID(t, store),
		"segments":   segments,
	})
	req := httptest.NewRequest(http.MethodPost, "/posts/validate", bytes.NewReader(payload))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestValidatePostEndpointWarnsLinkedInArticleUnfurl(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	account := createConnectedAccountForPlatform(t, store, domain.PlatformLinkedIn, "li-validate-unfurl")
	srv := Server{
		Store:             store,
		DataDir:           tempDir,
		DefaultMaxRetries: 3,
		Registry:          testRegistryWithRealLinkedIn(),
	}
	h := srv.Handler()

	payload, _ := json.Marshal(map[string]any{
		"account_id": account.ID,
		"text":       "look at https://example.com/post",
	})
	req := httptest.NewRequest(http.MethodPost, "/posts/validate", bytes.NewReader(payload))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "LinkedIn will try article unfurl at publish time") {
		t.Fatalf("expected linkedin unfurl warning, got %s", w.Body.String())
	}
}

func TestValidatePostEndpointWarnsLinkedInMediaWinsOverUnfurl(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	account := createConnectedAccountForPlatform(t, store, domain.PlatformLinkedIn, "li-validate-media-wins")
	mediaPath := filepath.Join(tempDir, "cover.jpg")
	if err := os.WriteFile(mediaPath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("write media file: %v", err)
	}
	media, err := store.CreateMedia(t.Context(), domain.Media{
		Kind:         "image",
		OriginalName: "cover.jpg",
		StoragePath:  mediaPath,
		MimeType:     "image/jpeg",
		SizeBytes:    4,
	})
	if err != nil {
		t.Fatalf("create media: %v", err)
	}

	srv := Server{
		Store:             store,
		DataDir:           tempDir,
		DefaultMaxRetries: 3,
		Registry:          testRegistryWithRealLinkedIn(),
	}
	h := srv.Handler()

	payload, _ := json.Marshal(map[string]any{
		"account_id": account.ID,
		"text":       "look at https://example.com/post",
		"media_ids":  []string{media.ID},
	})
	req := httptest.NewRequest(http.MethodPost, "/posts/validate", bytes.NewReader(payload))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "LinkedIn media takes precedence; link unfurl will be skipped") {
		t.Fatalf("expected linkedin media warning, got %s", w.Body.String())
	}
}
