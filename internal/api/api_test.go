package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/publisher/internal/db"
	"github.com/antoniolg/publisher/internal/domain"
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

func TestCreatePostFromFormRedirects(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	form := url.Values{}
	form.Set("platform", "x")
	form.Set("text", "draft from form")
	req := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewBufferString(form.Encode()))
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}

	drafts, err := store.ListDrafts(t.Context())
	if err != nil {
		t.Fatalf("list drafts: %v", err)
	}
	if len(drafts) != 1 {
		t.Fatalf("expected 1 draft, got %d", len(drafts))
	}
}

func TestEditPostFromForm(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	createPayload, _ := json.Marshal(map[string]any{
		"platform":     "x",
		"text":         "initial scheduled",
		"scheduled_at": time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339),
	})
	createReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createPayload))
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

	editForm := url.Values{}
	editForm.Set("text", "edited as draft")
	editReq := httptest.NewRequest(http.MethodPost, "/posts/"+postID+"/edit", bytes.NewBufferString(editForm.Encode()))
	editReq.Header.Set("content-type", "application/x-www-form-urlencoded")
	editW := httptest.NewRecorder()
	h.ServeHTTP(editW, editReq)
	if editW.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", editW.Code)
	}

	post, err := store.GetPost(t.Context(), postID)
	if err != nil {
		t.Fatalf("get post: %v", err)
	}
	if post.Text != "edited as draft" {
		t.Fatalf("expected updated text, got %q", post.Text)
	}
	if status := string(post.Status); status != "draft" {
		t.Fatalf("expected draft status after clearing date, got %s", status)
	}
}

func TestCreateDraftWithoutScheduledAt(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	body := map[string]any{
		"platform": "x",
		"text":     "idea de borrador",
	}
	payload, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(payload))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if status, _ := resp["status"].(string); status != "draft" {
		t.Fatalf("expected status=draft, got %q", status)
	}
}

func TestScheduleDraftPost(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	createPayload, _ := json.Marshal(map[string]any{
		"platform": "x",
		"text":     "draft to schedule",
	})
	createReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createPayload))
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

	schedulePayload, _ := json.Marshal(map[string]any{
		"scheduled_at": time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339),
	})
	scheduleReq := httptest.NewRequest(http.MethodPost, "/posts/"+postID+"/schedule", bytes.NewReader(schedulePayload))
	scheduleReq.Header.Set("content-type", "application/json")
	scheduleW := httptest.NewRecorder()
	h.ServeHTTP(scheduleW, scheduleReq)
	if scheduleW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", scheduleW.Code)
	}
	var scheduled map[string]any
	if err := json.Unmarshal(scheduleW.Body.Bytes(), &scheduled); err != nil {
		t.Fatalf("decode schedule response: %v", err)
	}
	if status, _ := scheduled["status"].(string); status != "scheduled" {
		t.Fatalf("expected status=scheduled, got %q", status)
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
		"platform":     "x",
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
			"platform":     "x",
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
	form.Set("platform", "x")
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

func TestPublicationsViewDoesNotRenderDrafts(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	createDraftBody, _ := json.Marshal(map[string]any{
		"platform": "x",
		"text":     "this draft must stay out of publications",
	})
	createDraftReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createDraftBody))
	createDraftW := httptest.NewRecorder()
	h.ServeHTTP(createDraftW, createDraftReq)
	if createDraftW.Code != http.StatusCreated {
		t.Fatalf("expected 201 for draft, got %d", createDraftW.Code)
	}

	createScheduledBody, _ := json.Marshal(map[string]any{
		"platform":     "x",
		"text":         "this scheduled post must appear in publications",
		"scheduled_at": time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339),
	})
	createScheduledReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createScheduledBody))
	createScheduledW := httptest.NewRecorder()
	h.ServeHTTP(createScheduledW, createScheduledReq)
	if createScheduledW.Code != http.StatusCreated {
		t.Fatalf("expected 201 for scheduled, got %d", createScheduledW.Code)
	}

	publicationsReq := httptest.NewRequest(http.MethodGet, "/?view=publications", nil)
	publicationsW := httptest.NewRecorder()
	h.ServeHTTP(publicationsW, publicationsReq)
	if publicationsW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", publicationsW.Code)
	}

	body := publicationsW.Body.String()
	if strings.Contains(body, "this draft must stay out of publications") {
		t.Fatalf("draft text should not appear in publications view")
	}
	if !strings.Contains(body, "this scheduled post must appear in publications") {
		t.Fatalf("scheduled text should appear in publications view")
	}
}
