package api

import (
	"bytes"
	"encoding/json"
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
)

func TestRequeueDeadLetterFromFormRedirectsToFailedView(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	payload, _ := json.Marshal(map[string]any{
		"account_id":   testAccountID(t, store),
		"text":         "will fail once",
		"scheduled_at": time.Now().UTC().Add(1 * time.Minute).Format(time.RFC3339),
		"max_attempts": 1,
	})
	createReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(payload))
	createW := httptest.NewRecorder()
	h.ServeHTTP(createW, createReq)
	if createW.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", createW.Code)
	}

	var created map[string]any
	if err := json.Unmarshal(createW.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	postID, _ := created["id"].(string)
	if postID == "" {
		t.Fatalf("missing post id")
	}

	if err := store.RecordPublishFailure(t.Context(), postID, errors.New("boom"), 30*time.Second); err != nil {
		t.Fatalf("record publish failure: %v", err)
	}

	dlqItems, err := store.ListDeadLetters(t.Context(), 10)
	if err != nil {
		t.Fatalf("list dead letters: %v", err)
	}
	if len(dlqItems) != 1 {
		t.Fatalf("expected 1 dead letter, got %d", len(dlqItems))
	}

	requeueReq := httptest.NewRequest(http.MethodPost, "/dlq/"+dlqItems[0].ID+"/requeue", bytes.NewBufferString(""))
	requeueReq.Header.Set("content-type", "application/x-www-form-urlencoded")
	requeueW := httptest.NewRecorder()
	h.ServeHTTP(requeueW, requeueReq)
	if requeueW.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", requeueW.Code)
	}
	if loc := requeueW.Header().Get("Location"); loc != "/?view=failed&failed_success=requeued" {
		t.Fatalf("unexpected redirect location: %q", loc)
	}

	post, err := store.GetPost(t.Context(), postID)
	if err != nil {
		t.Fatalf("get post: %v", err)
	}
	if post.Status != domain.PostStatusScheduled {
		t.Fatalf("expected scheduled status, got %s", post.Status)
	}
}

func TestBulkRequeueDeadLettersFromForm(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	makeFailed := func(text string) string {
		payload, _ := json.Marshal(map[string]any{
			"account_id":   testAccountID(t, store),
			"text":         text,
			"scheduled_at": time.Now().UTC().Add(1 * time.Minute).Format(time.RFC3339),
			"max_attempts": 1,
		})
		createReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(payload))
		createW := httptest.NewRecorder()
		h.ServeHTTP(createW, createReq)
		if createW.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d", createW.Code)
		}
		var created map[string]any
		if err := json.Unmarshal(createW.Body.Bytes(), &created); err != nil {
			t.Fatalf("decode create response: %v", err)
		}
		postID, _ := created["id"].(string)
		if postID == "" {
			t.Fatalf("missing post id")
		}
		if err := store.RecordPublishFailure(t.Context(), postID, errors.New("boom"), 30*time.Second); err != nil {
			t.Fatalf("record publish failure: %v", err)
		}
		return postID
	}

	postA := makeFailed("failed a")
	postB := makeFailed("failed b")

	dlqItems, err := store.ListDeadLetters(t.Context(), 10)
	if err != nil {
		t.Fatalf("list dead letters: %v", err)
	}
	if len(dlqItems) < 2 {
		t.Fatalf("expected at least 2 dead letters, got %d", len(dlqItems))
	}

	form := url.Values{}
	form.Add("ids", dlqItems[0].ID)
	form.Add("ids", dlqItems[1].ID)
	req := httptest.NewRequest(http.MethodPost, "/dlq/requeue", bytes.NewBufferString(form.Encode()))
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}

	post1, err := store.GetPost(t.Context(), postA)
	if err != nil {
		t.Fatalf("get postA: %v", err)
	}
	post2, err := store.GetPost(t.Context(), postB)
	if err != nil {
		t.Fatalf("get postB: %v", err)
	}
	if post1.Status != domain.PostStatusScheduled || post2.Status != domain.PostStatusScheduled {
		t.Fatalf("expected both posts scheduled after bulk requeue")
	}
}

func TestBulkDeleteDeadLettersFromForm(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	makeFailed := func(text string) string {
		payload, _ := json.Marshal(map[string]any{
			"account_id":   testAccountID(t, store),
			"text":         text,
			"scheduled_at": time.Now().UTC().Add(1 * time.Minute).Format(time.RFC3339),
			"max_attempts": 1,
		})
		createReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(payload))
		createW := httptest.NewRecorder()
		h.ServeHTTP(createW, createReq)
		if createW.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d", createW.Code)
		}
		var created map[string]any
		if err := json.Unmarshal(createW.Body.Bytes(), &created); err != nil {
			t.Fatalf("decode create response: %v", err)
		}
		postID, _ := created["id"].(string)
		if postID == "" {
			t.Fatalf("missing post id")
		}
		if err := store.RecordPublishFailure(t.Context(), postID, errors.New("boom"), 30*time.Second); err != nil {
			t.Fatalf("record publish failure: %v", err)
		}
		return postID
	}

	postA := makeFailed("failed delete a")
	postB := makeFailed("failed delete b")

	dlqItems, err := store.ListDeadLetters(t.Context(), 10)
	if err != nil {
		t.Fatalf("list dead letters: %v", err)
	}
	if len(dlqItems) < 2 {
		t.Fatalf("expected at least 2 dead letters, got %d", len(dlqItems))
	}

	form := url.Values{}
	form.Add("ids", dlqItems[0].ID)
	form.Add("ids", dlqItems[1].ID)
	req := httptest.NewRequest(http.MethodPost, "/dlq/delete", bytes.NewBufferString(form.Encode()))
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}

	if _, err := store.GetPost(t.Context(), postA); err == nil {
		t.Fatalf("expected postA deleted")
	}
	if _, err := store.GetPost(t.Context(), postB); err == nil {
		t.Fatalf("expected postB deleted")
	}

	dlqItems, err = store.ListDeadLetters(t.Context(), 10)
	if err != nil {
		t.Fatalf("list dead letters after bulk delete: %v", err)
	}
	if len(dlqItems) != 0 {
		t.Fatalf("expected no dead letters after bulk delete, got %d", len(dlqItems))
	}
}

func TestFailedViewRendersBulkSelectionControls(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	payload, _ := json.Marshal(map[string]any{
		"account_id":   testAccountID(t, store),
		"text":         "failed checkbox style",
		"scheduled_at": time.Now().UTC().Add(2 * time.Minute).Format(time.RFC3339),
		"max_attempts": 1,
	})
	createReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(payload))
	createW := httptest.NewRecorder()
	h.ServeHTTP(createW, createReq)
	if createW.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", createW.Code)
	}
	var created map[string]any
	if err := json.Unmarshal(createW.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	postID, _ := created["id"].(string)
	if postID == "" {
		t.Fatalf("missing post id")
	}
	if err := store.RecordPublishFailure(t.Context(), postID, errors.New("boom"), 30*time.Second); err != nil {
		t.Fatalf("record publish failure: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/?view=failed", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "data-failed-checkbox") {
		t.Fatalf("expected failed rows to expose bulk selection checkboxes")
	}
	if !strings.Contains(body, "id=\"failed-delete-selected\"") {
		t.Fatalf("expected bulk delete button in failed view")
	}
	if !strings.Contains(body, "id=\"failed-requeue-selected\"") {
		t.Fatalf("expected bulk requeue button in failed view")
	}
	if !strings.Contains(body, "/dlq/") || !strings.Contains(body, "/delete") {
		t.Fatalf("expected delete actions in failed view")
	}
}

func TestCreatePostFromFormUsesConfiguredTimezone(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	settingsForm := url.Values{}
	settingsForm.Set("timezone", "Europe/Madrid")
	settingsReq := httptest.NewRequest(http.MethodPost, "/settings/timezone", bytes.NewBufferString(settingsForm.Encode()))
	settingsReq.Header.Set("content-type", "application/x-www-form-urlencoded")
	settingsW := httptest.NewRecorder()
	h.ServeHTTP(settingsW, settingsReq)
	if settingsW.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 on timezone update, got %d", settingsW.Code)
	}

	form := url.Values{}
	form.Set("account_id", testAccountID(t, store))
	form.Set("text", "scheduled with timezone")
	form.Set("scheduled_at_local", "2026-02-26T10:00")
	req := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewBufferString(form.Encode()))
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}

	loc, err := time.LoadLocation("Europe/Madrid")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	localTime, err := time.ParseInLocation("2006-01-02T15:04", "2026-02-26T10:00", loc)
	if err != nil {
		t.Fatalf("parse local time: %v", err)
	}
	expectedUTC := localTime.UTC()

	items, err := store.ListSchedule(t.Context(), expectedUTC.Add(-time.Minute), expectedUTC.Add(time.Minute))
	if err != nil {
		t.Fatalf("list schedule: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 scheduled item, got %d", len(items))
	}
	if !items[0].ScheduledAt.Equal(expectedUTC) {
		t.Fatalf("expected UTC %s, got %s", expectedUTC.Format(time.RFC3339), items[0].ScheduledAt.Format(time.RFC3339))
	}
}
