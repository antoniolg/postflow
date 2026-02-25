package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/antoniolg/publisher/internal/db"
)

func TestValidatePostEndpoint(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	payload, _ := json.Marshal(map[string]any{
		"platform":     "x",
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

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	payload, _ := json.Marshal(map[string]any{
		"platform": "x",
		"text":     "idea",
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
