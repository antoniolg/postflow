package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/antoniolg/postflow/internal/db"
	"github.com/antoniolg/postflow/internal/domain"
)

func TestListDraftsEndpoint(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	accountID := testAccountID(t, store)
	_, err = store.CreatePost(t.Context(), db.CreatePostParams{
		Post: domain.Post{
			AccountID:   accountID,
			Platform:    domain.PlatformX,
			Text:        "draft from endpoint",
			Status:      domain.PostStatusDraft,
			MaxAttempts: 3,
		},
	})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/drafts?limit=10", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var out struct {
		Count  int `json:"count"`
		Drafts []struct {
			ID string `json:"id"`
		} `json:"drafts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Count != 1 || len(out.Drafts) != 1 {
		t.Fatalf("expected one draft, got count=%d len=%d", out.Count, len(out.Drafts))
	}
}

func TestListDraftsEndpointRejectsInvalidLimit(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/drafts?limit=invalid", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}
