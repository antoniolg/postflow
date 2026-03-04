package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/db"
	"github.com/antoniolg/postflow/internal/domain"
)

func TestDLQListAndRequeueFlow(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	account := createTestAccount(t, store)

	created, err := store.CreatePost(t.Context(), db.CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "goes to dlq",
			Status:      domain.PostStatusScheduled,
			ScheduledAt: time.Now().UTC().Add(-1 * time.Minute),
			MaxAttempts: 1,
		},
	})
	if err != nil {
		t.Fatalf("create post: %v", err)
	}

	claimed, err := store.ClaimDuePosts(t.Context(), 1)
	if err != nil {
		t.Fatalf("claim due: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("expected 1 claimed post, got %d", len(claimed))
	}

	if err := store.RecordPublishFailure(t.Context(), created.Post.ID, errors.New("boom"), time.Second); err != nil {
		t.Fatalf("record failure: %v", err)
	}

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/dlq?limit=10", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 from /dlq, got %d", w.Code)
	}

	var listResp struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode /dlq response: %v", err)
	}
	if len(listResp.Items) != 1 || listResp.Items[0].ID == "" {
		t.Fatalf("expected one dlq item with id")
	}
	dlqID := listResp.Items[0].ID

	req = httptest.NewRequest(http.MethodPost, "/dlq/"+dlqID+"/requeue", bytes.NewReader(nil))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 from requeue, got %d", w.Code)
	}

	post, err := store.GetPost(t.Context(), created.Post.ID)
	if err != nil {
		t.Fatalf("get post: %v", err)
	}
	if post.Status != domain.PostStatusScheduled {
		t.Fatalf("expected scheduled after requeue, got %s", post.Status)
	}
	if post.Attempts != 0 {
		t.Fatalf("expected attempts reset to 0, got %d", post.Attempts)
	}
}

func TestDLQDeleteFlow(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	account := createTestAccount(t, store)

	created, err := store.CreatePost(t.Context(), db.CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "goes to dlq delete",
			Status:      domain.PostStatusScheduled,
			ScheduledAt: time.Now().UTC().Add(-1 * time.Minute),
			MaxAttempts: 1,
		},
	})
	if err != nil {
		t.Fatalf("create post: %v", err)
	}

	if _, err := store.ClaimDuePosts(t.Context(), 1); err != nil {
		t.Fatalf("claim due: %v", err)
	}

	if err := store.RecordPublishFailure(t.Context(), created.Post.ID, errors.New("boom"), time.Second); err != nil {
		t.Fatalf("record failure: %v", err)
	}

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/dlq?limit=10", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 from /dlq, got %d", w.Code)
	}

	var listResp struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode /dlq response: %v", err)
	}
	if len(listResp.Items) != 1 || listResp.Items[0].ID == "" {
		t.Fatalf("expected one dlq item with id")
	}
	dlqID := listResp.Items[0].ID

	req = httptest.NewRequest(http.MethodPost, "/dlq/"+dlqID+"/delete", bytes.NewReader(nil))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 from delete, got %d", w.Code)
	}

	if _, err := store.GetPost(t.Context(), created.Post.ID); err == nil {
		t.Fatalf("expected post to be deleted")
	}

	dlqItems, err := store.ListDeadLetters(t.Context(), 10)
	if err != nil {
		t.Fatalf("list dead letters: %v", err)
	}
	if len(dlqItems) != 0 {
		t.Fatalf("expected no dead letters after delete, got %d", len(dlqItems))
	}
}
