package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/antoniolg/publisher/internal/db"
)

func TestCreatePostValidation(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	body := map[string]any{
		"platform":     "x",
		"text":         "hola",
		"scheduled_at": "not-a-date",
	}
	payload, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(payload))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestScheduleEndpointReturnsCreatedPost(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	scheduled := time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339)
	body := map[string]any{
		"platform":     "x",
		"text":         "post de prueba",
		"scheduled_at": scheduled,
	}
	payload, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(payload))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/schedule", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	if !bytes.Contains(w.Body.Bytes(), []byte("post de prueba")) {
		_ = os.WriteFile(filepath.Join(tempDir, "schedule_response.json"), w.Body.Bytes(), 0o644)
		t.Fatalf("expected schedule to include created post")
	}
}

func TestCreatePostWithIdempotencyKeyIsReplayed(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	scheduled := time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339)
	body := map[string]any{
		"platform":     "x",
		"text":         "idempotent post",
		"scheduled_at": scheduled,
	}
	payload, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(payload))
	req.Header.Set("Idempotency-Key", "same-request-123")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", w.Code)
	}
	var firstResp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &firstResp); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	firstID, _ := firstResp["id"].(string)
	if firstID == "" {
		t.Fatalf("expected first response to include post id")
	}

	req = httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(payload))
	req.Header.Set("Idempotency-Key", "same-request-123")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 replay, got %d", w.Code)
	}
	var secondResp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &secondResp); err != nil {
		t.Fatalf("decode second response: %v", err)
	}
	secondID, _ := secondResp["id"].(string)
	if secondID != firstID {
		t.Fatalf("expected same id on replay, got %q and %q", firstID, secondID)
	}
}
