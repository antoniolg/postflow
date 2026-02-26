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
	"regexp"
	"strconv"
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

func TestFailedViewUsesStyledCheckboxes(t *testing.T) {
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
	if !strings.Contains(body, ".failed-checkbox {\n      margin-top: 2px;\n      width: 16px;\n      height: 16px;\n      appearance: none;") {
		t.Fatalf("expected custom failed checkbox base style")
	}
	if !strings.Contains(body, ".failed-checkbox:checked::before {\n      transform: scale(1);") {
		t.Fatalf("expected custom failed checkbox checked style")
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

func TestPublicationsViewShowsOnlyScheduledInNext14Days(t *testing.T) {
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
		"text":         "scheduled in next 14 days",
		"scheduled_at": time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339),
	})
	createScheduledReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createScheduledBody))
	createScheduledW := httptest.NewRecorder()
	h.ServeHTTP(createScheduledW, createScheduledReq)
	if createScheduledW.Code != http.StatusCreated {
		t.Fatalf("expected 201 for scheduled, got %d", createScheduledW.Code)
	}
	var scheduledResp map[string]any
	if err := json.Unmarshal(createScheduledW.Body.Bytes(), &scheduledResp); err != nil {
		t.Fatalf("decode scheduled create response: %v", err)
	}
	scheduledID, _ := scheduledResp["id"].(string)
	if scheduledID == "" {
		t.Fatalf("expected scheduled post id")
	}

	createFutureBody, _ := json.Marshal(map[string]any{
		"platform":     "x",
		"text":         "scheduled after 14 days",
		"scheduled_at": time.Now().UTC().Add(20 * 24 * time.Hour).Format(time.RFC3339),
	})
	createFutureReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createFutureBody))
	createFutureW := httptest.NewRecorder()
	h.ServeHTTP(createFutureW, createFutureReq)
	if createFutureW.Code != http.StatusCreated {
		t.Fatalf("expected 201 for future scheduled, got %d", createFutureW.Code)
	}

	createToPublishBody, _ := json.Marshal(map[string]any{
		"platform":     "x",
		"text":         "this published post should not appear in publications",
		"scheduled_at": time.Now().UTC().Add(4 * time.Hour).Format(time.RFC3339),
	})
	createToPublishReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createToPublishBody))
	createToPublishW := httptest.NewRecorder()
	h.ServeHTTP(createToPublishW, createToPublishReq)
	if createToPublishW.Code != http.StatusCreated {
		t.Fatalf("expected 201 for post-to-publish, got %d", createToPublishW.Code)
	}
	var toPublishResp map[string]any
	if err := json.Unmarshal(createToPublishW.Body.Bytes(), &toPublishResp); err != nil {
		t.Fatalf("decode post-to-publish response: %v", err)
	}
	toPublishID, _ := toPublishResp["id"].(string)
	if toPublishID == "" {
		t.Fatalf("expected post-to-publish id")
	}
	if err := store.MarkPublished(t.Context(), toPublishID, "x-post-123"); err != nil {
		t.Fatalf("mark published: %v", err)
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
	if !strings.Contains(body, "scheduled in next 14 days") {
		t.Fatalf("scheduled-in-window text should appear in publications view")
	}
	if strings.Contains(body, "scheduled after 14 days") {
		t.Fatalf("scheduled-outside-window text should not appear in publications view")
	}
	if strings.Contains(body, "this published post should not appear in publications") {
		t.Fatalf("published text should not appear in publications view")
	}
	if !strings.Contains(body, scheduledID) {
		t.Fatalf("expected in-window scheduled post id in rendered html")
	}
	if strings.Contains(body, "scheduled (14d)") {
		t.Fatalf("legacy publications stats bar should not appear")
	}
	if strings.Contains(body, "next run") {
		t.Fatalf("legacy publications stats bar should not appear")
	}
	if !strings.Contains(body, "nav-icon nav-icon-scheduled") || !strings.Contains(body, "<span>scheduled</span>") {
		t.Fatalf("expected scheduled nav badge with count")
	}
	if !strings.Contains(body, "nav-icon nav-icon-drafts") || !strings.Contains(body, "<span>drafts</span>") {
		t.Fatalf("expected drafts nav badge with count")
	}
	if strings.Count(body, "class=\"nav-badge\">1</span>") < 2 {
		t.Fatalf("expected neutral nav badges for scheduled and drafts")
	}
}

func TestNavBadgesUseNeutralForScheduledAndDraftsAndRedForFailed(t *testing.T) {
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
		"text":     "draft badge",
	})
	createDraftReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createDraftBody))
	createDraftW := httptest.NewRecorder()
	h.ServeHTTP(createDraftW, createDraftReq)
	if createDraftW.Code != http.StatusCreated {
		t.Fatalf("expected 201 for draft, got %d", createDraftW.Code)
	}

	createScheduledBody, _ := json.Marshal(map[string]any{
		"platform":     "x",
		"text":         "scheduled badge",
		"scheduled_at": time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339),
	})
	createScheduledReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createScheduledBody))
	createScheduledW := httptest.NewRecorder()
	h.ServeHTTP(createScheduledW, createScheduledReq)
	if createScheduledW.Code != http.StatusCreated {
		t.Fatalf("expected 201 for scheduled, got %d", createScheduledW.Code)
	}

	createFailedBody, _ := json.Marshal(map[string]any{
		"platform":     "x",
		"text":         "failed badge",
		"scheduled_at": time.Now().UTC().Add(20 * 24 * time.Hour).Format(time.RFC3339),
		"max_attempts": 1,
	})
	createFailedReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createFailedBody))
	createFailedW := httptest.NewRecorder()
	h.ServeHTTP(createFailedW, createFailedReq)
	if createFailedW.Code != http.StatusCreated {
		t.Fatalf("expected 201 for failed seed post, got %d", createFailedW.Code)
	}
	var failedResp map[string]any
	if err := json.Unmarshal(createFailedW.Body.Bytes(), &failedResp); err != nil {
		t.Fatalf("decode failed seed response: %v", err)
	}
	failedPostID, _ := failedResp["id"].(string)
	if failedPostID == "" {
		t.Fatalf("expected failed seed post id")
	}
	if err := store.RecordPublishFailure(t.Context(), failedPostID, errors.New("boom"), 30*time.Second); err != nil {
		t.Fatalf("record publish failure: %v", err)
	}

	publicationsReq := httptest.NewRequest(http.MethodGet, "/?view=publications", nil)
	publicationsW := httptest.NewRecorder()
	h.ServeHTTP(publicationsW, publicationsReq)
	if publicationsW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", publicationsW.Code)
	}

	body := publicationsW.Body.String()
	if !strings.Contains(body, "nav-icon nav-icon-calendar") {
		t.Fatalf("expected calendar nav icon")
	}
	if !strings.Contains(body, "nav-icon nav-icon-settings") {
		t.Fatalf("expected settings nav icon")
	}
	if !strings.Contains(body, "nav-item-settings") {
		t.Fatalf("expected settings nav item class for desktop bottom placement")
	}
	if !strings.Contains(body, "nav-icon nav-icon-scheduled") || !strings.Contains(body, "<span>scheduled</span>") {
		t.Fatalf("expected scheduled nav badge with neutral style")
	}
	if !strings.Contains(body, "nav-icon nav-icon-drafts") || !strings.Contains(body, "<span>drafts</span>") {
		t.Fatalf("expected drafts nav badge with neutral style")
	}
	if strings.Count(body, "class=\"nav-badge\">1</span>") < 2 {
		t.Fatalf("expected neutral nav badges for scheduled and drafts")
	}
	if !strings.Contains(body, "nav-icon nav-icon-failed") || !strings.Contains(body, "<span>failed</span>") || !strings.Contains(body, "class=\"nav-badge nav-badge-danger\">1</span>") {
		t.Fatalf("expected failed nav badge with danger style")
	}
}

func TestQueueCardsHideRedundantStatusFlags(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	createScheduledBody, _ := json.Marshal(map[string]any{
		"platform":     "x",
		"text":         "scheduled without badge",
		"scheduled_at": time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339),
	})
	createScheduledReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createScheduledBody))
	createScheduledW := httptest.NewRecorder()
	h.ServeHTTP(createScheduledW, createScheduledReq)
	if createScheduledW.Code != http.StatusCreated {
		t.Fatalf("expected 201 for scheduled post, got %d", createScheduledW.Code)
	}

	createDraftBody, _ := json.Marshal(map[string]any{
		"platform": "x",
		"text":     "draft without badge",
	})
	createDraftReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createDraftBody))
	createDraftW := httptest.NewRecorder()
	h.ServeHTTP(createDraftW, createDraftReq)
	if createDraftW.Code != http.StatusCreated {
		t.Fatalf("expected 201 for draft post, got %d", createDraftW.Code)
	}

	createFailedBody, _ := json.Marshal(map[string]any{
		"platform":     "x",
		"text":         "failed without badge",
		"scheduled_at": time.Now().UTC().Add(3 * time.Hour).Format(time.RFC3339),
		"max_attempts": 1,
	})
	createFailedReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createFailedBody))
	createFailedW := httptest.NewRecorder()
	h.ServeHTTP(createFailedW, createFailedReq)
	if createFailedW.Code != http.StatusCreated {
		t.Fatalf("expected 201 for failed seed post, got %d", createFailedW.Code)
	}
	var failedResp map[string]any
	if err := json.Unmarshal(createFailedW.Body.Bytes(), &failedResp); err != nil {
		t.Fatalf("decode failed seed response: %v", err)
	}
	failedID, _ := failedResp["id"].(string)
	if failedID == "" {
		t.Fatalf("expected failed post id")
	}
	if err := store.RecordPublishFailure(t.Context(), failedID, errors.New("boom"), 30*time.Second); err != nil {
		t.Fatalf("record publish failure: %v", err)
	}

	publicationsReq := httptest.NewRequest(http.MethodGet, "/?view=publications", nil)
	publicationsW := httptest.NewRecorder()
	h.ServeHTTP(publicationsW, publicationsReq)
	if publicationsW.Code != http.StatusOK {
		t.Fatalf("expected 200 for publications view, got %d", publicationsW.Code)
	}
	publicationsBody := publicationsW.Body.String()
	if strings.Contains(publicationsBody, "<span class=\"dot scheduled\"></span>") || strings.Contains(publicationsBody, "status-schd\">SCHD") {
		t.Fatalf("scheduled queue cards should not render status dot/label")
	}

	draftsReq := httptest.NewRequest(http.MethodGet, "/?view=drafts", nil)
	draftsW := httptest.NewRecorder()
	h.ServeHTTP(draftsW, draftsReq)
	if draftsW.Code != http.StatusOK {
		t.Fatalf("expected 200 for drafts view, got %d", draftsW.Code)
	}
	draftsBody := draftsW.Body.String()
	if strings.Contains(draftsBody, "<span class=\"dot draft\"></span>") || strings.Contains(draftsBody, "status-drft\">DRFT") {
		t.Fatalf("draft queue cards should not render status dot/label")
	}

	failedReq := httptest.NewRequest(http.MethodGet, "/?view=failed", nil)
	failedW := httptest.NewRecorder()
	h.ServeHTTP(failedW, failedReq)
	if failedW.Code != http.StatusOK {
		t.Fatalf("expected 200 for failed view, got %d", failedW.Code)
	}
	failedBody := failedW.Body.String()
	if strings.Contains(failedBody, "<span class=\"dot fail\"></span>") || strings.Contains(failedBody, "status-fail\">FAIL") {
		t.Fatalf("failed queue cards should not render status dot/label")
	}
	if !strings.Contains(failedBody, "class=\"failed-checkbox\"") {
		t.Fatalf("failed queue cards should keep selection checkbox")
	}
}

func TestCalendarDayDetailShowsPendingBeforePublished(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	settingsForm := url.Values{}
	settingsForm.Set("timezone", "UTC")
	settingsReq := httptest.NewRequest(http.MethodPost, "/settings/timezone", bytes.NewBufferString(settingsForm.Encode()))
	settingsReq.Header.Set("content-type", "application/x-www-form-urlencoded")
	settingsW := httptest.NewRecorder()
	h.ServeHTTP(settingsW, settingsReq)
	if settingsW.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 on timezone update, got %d", settingsW.Code)
	}

	selectedDay := time.Date(2026, time.February, 26, 0, 0, 0, 0, time.UTC)
	publishedAt := selectedDay.Add(9 * time.Hour)
	pendingAt := selectedDay.Add(10 * time.Hour)

	createPublishedBody, _ := json.Marshal(map[string]any{
		"platform":     "x",
		"text":         "published item should be below",
		"scheduled_at": publishedAt.Format(time.RFC3339),
	})
	createPublishedReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createPublishedBody))
	createPublishedW := httptest.NewRecorder()
	h.ServeHTTP(createPublishedW, createPublishedReq)
	if createPublishedW.Code != http.StatusCreated {
		t.Fatalf("expected 201 for published seed post, got %d", createPublishedW.Code)
	}
	var publishedResp map[string]any
	if err := json.Unmarshal(createPublishedW.Body.Bytes(), &publishedResp); err != nil {
		t.Fatalf("decode published seed response: %v", err)
	}
	publishedID, _ := publishedResp["id"].(string)
	if publishedID == "" {
		t.Fatalf("expected published post id")
	}
	if err := store.MarkPublished(t.Context(), publishedID, "x-published-seed"); err != nil {
		t.Fatalf("mark published: %v", err)
	}

	createPendingBody, _ := json.Marshal(map[string]any{
		"platform":     "x",
		"text":         "pending item should be first",
		"scheduled_at": pendingAt.Format(time.RFC3339),
	})
	createPendingReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createPendingBody))
	createPendingW := httptest.NewRecorder()
	h.ServeHTTP(createPendingW, createPendingReq)
	if createPendingW.Code != http.StatusCreated {
		t.Fatalf("expected 201 for pending post, got %d", createPendingW.Code)
	}

	monthParam := selectedDay.Format("2006-01")
	dayParam := selectedDay.Format("2006-01-02")
	calendarReq := httptest.NewRequest(http.MethodGet, "/?view=calendar&month="+monthParam+"&day="+dayParam, nil)
	calendarW := httptest.NewRecorder()
	h.ServeHTTP(calendarW, calendarReq)
	if calendarW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", calendarW.Code)
	}

	body := calendarW.Body.String()
	panelStart := strings.Index(body, "<aside class=\"day-panel\" aria-label=\"Day detail\">")
	if panelStart == -1 {
		t.Fatalf("expected day panel in calendar view")
	}
	panelEndRel := strings.Index(body[panelStart:], "</aside>")
	if panelEndRel == -1 {
		t.Fatalf("expected day panel closing tag")
	}
	panel := body[panelStart : panelStart+panelEndRel]

	if !strings.Contains(panel, "to publish (1)") {
		t.Fatalf("expected pending section header")
	}
	if !strings.Contains(panel, "published (1)") {
		t.Fatalf("expected published section header")
	}

	pendingIdx := strings.Index(panel, "pending item should be first")
	separatorIdx := strings.Index(panel, "class=\"day-separator\">published</div>")
	publishedIdx := strings.Index(panel, "published item should be below")
	if pendingIdx == -1 || separatorIdx == -1 || publishedIdx == -1 {
		t.Fatalf("expected pending item, separator, and published item in day panel")
	}
	if !(pendingIdx < separatorIdx && separatorIdx < publishedIdx) {
		t.Fatalf("expected pending section before separator and published section")
	}
}

func TestDefaultViewIsCalendar(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, ">CALENDAR</h1>") {
		t.Fatalf("expected CALENDAR as default view title")
	}
	if !strings.Contains(body, "body[data-view=\"calendar\"] .main") {
		t.Fatalf("expected calendar-specific main width rule")
	}
	if !strings.Contains(body, "body[data-view=\"calendar\"] .calendar-layout") {
		t.Fatalf("expected calendar-specific centered layout rule")
	}
	if !strings.Contains(body, "width: min(100%, 1540px);") {
		t.Fatalf("expected calendar layout max width cap")
	}
	if !strings.Contains(body, "padding-top: 6px;") {
		t.Fatalf("expected calendar/day-detail top spacing")
	}
	if !strings.Contains(body, "body[data-view=\"calendar\"] .calendar-wrap {\n      display: flex;\n      flex-direction: column;") {
		t.Fatalf("expected calendar container to support viewport-height stretching")
	}
	if !strings.Contains(body, "body[data-view=\"calendar\"] .day-panel-body {\n      flex: 1;\n      min-height: 0;\n      max-height: none;") {
		t.Fatalf("expected day detail body to stretch and fill calendar height")
	}
	if !strings.Contains(body, "const syncDayPanelHeightToCalendar = () => {") || !strings.Contains(body, "dayPanel.style.height = calendarWrap.offsetHeight + \"px\";") {
		t.Fatalf("expected day detail height to be synced from calendar height")
	}
	if !strings.Contains(body, "body[data-view=\"calendar\"] .week-row {\n      flex: 1 1 0;\n      min-height: 0;") {
		t.Fatalf("expected calendar week rows to distribute vertical space")
	}
	if !strings.Contains(body, "body[data-view=\"calendar\"] .day-cell {\n      min-height: 0;\n      height: 100%;") {
		t.Fatalf("expected calendar cells to stretch vertically with available height")
	}
	if !strings.Contains(body, ".weekday {\n      padding: 8px 8px;\n      font-size: 11px;") {
		t.Fatalf("expected larger calendar weekday labels for accessibility")
	}
	if !strings.Contains(body, ".content .text {\n      font-size: 13px;") {
		t.Fatalf("expected larger body text size for accessibility")
	}
	if !strings.Contains(body, ".nav-item.active .nav-badge {\n      background: #2f384d;\n      color: #d9e4ff;") {
		t.Fatalf("expected improved active nav badge contrast")
	}
	if !strings.Contains(body, ".day-event {\n      flex: 0 0 auto;") {
		t.Fatalf("expected calendar event rows to keep fixed height without shrinking")
	}
	if !strings.Contains(body, "const bottomPadding = mainStyles ? parseFloat(mainStyles.paddingBottom || \"0\") : 0;") {
		t.Fatalf("expected calendar height calculation to account for main bottom padding")
	}
	if !strings.Contains(body, "calendarWrap.style.height = availableHeight + \"px\";") {
		t.Fatalf("expected calendar to expand to available viewport height")
	}
	if strings.Contains(body, ".calendar-wrap {\n      margin-top: 12px;") {
		t.Fatalf("calendar and day detail cards should align at the same top edge")
	}
}

func TestAccessibilityMarkupAddsLabelsAndLandmarks(t *testing.T) {
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
		"text":     "draft for accessibility labels",
	})
	createDraftReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createDraftBody))
	createDraftW := httptest.NewRecorder()
	h.ServeHTTP(createDraftW, createDraftReq)
	if createDraftW.Code != http.StatusCreated {
		t.Fatalf("expected 201 for draft seed post, got %d", createDraftW.Code)
	}

	createFailedBody, _ := json.Marshal(map[string]any{
		"platform":     "x",
		"text":         "failed for accessibility labels",
		"scheduled_at": time.Now().UTC().Add(1 * time.Minute).Format(time.RFC3339),
		"max_attempts": 1,
	})
	createFailedReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createFailedBody))
	createFailedW := httptest.NewRecorder()
	h.ServeHTTP(createFailedW, createFailedReq)
	if createFailedW.Code != http.StatusCreated {
		t.Fatalf("expected 201 for failed seed post, got %d", createFailedW.Code)
	}
	var failedResp map[string]any
	if err := json.Unmarshal(createFailedW.Body.Bytes(), &failedResp); err != nil {
		t.Fatalf("decode failed seed response: %v", err)
	}
	failedPostID, _ := failedResp["id"].(string)
	if failedPostID == "" {
		t.Fatalf("expected failed seed post id")
	}
	if err := store.RecordPublishFailure(t.Context(), failedPostID, errors.New("boom"), 30*time.Second); err != nil {
		t.Fatalf("record publish failure: %v", err)
	}

	calendarReq := httptest.NewRequest(http.MethodGet, "/?view=calendar", nil)
	calendarW := httptest.NewRecorder()
	h.ServeHTTP(calendarW, calendarReq)
	if calendarW.Code != http.StatusOK {
		t.Fatalf("expected 200 for calendar view, got %d", calendarW.Code)
	}
	calendarBody := calendarW.Body.String()
	if !strings.Contains(calendarBody, "<aside class=\"sidebar\" aria-label=\"Primary navigation\">") {
		t.Fatalf("expected labeled primary navigation landmark")
	}
	if !strings.Contains(calendarBody, "<aside class=\"day-panel\" aria-label=\"Day detail\">") {
		t.Fatalf("expected labeled day detail landmark")
	}

	draftsReq := httptest.NewRequest(http.MethodGet, "/?view=drafts", nil)
	draftsW := httptest.NewRecorder()
	h.ServeHTTP(draftsW, draftsReq)
	if draftsW.Code != http.StatusOK {
		t.Fatalf("expected 200 for drafts view, got %d", draftsW.Code)
	}
	draftsBody := draftsW.Body.String()
	if !strings.Contains(draftsBody, "aria-label=\"scheduled at for draft ") {
		t.Fatalf("expected accessible label on draft schedule datetime input")
	}

	failedReq := httptest.NewRequest(http.MethodGet, "/?view=failed", nil)
	failedW := httptest.NewRecorder()
	h.ServeHTTP(failedW, failedReq)
	if failedW.Code != http.StatusOK {
		t.Fatalf("expected 200 for failed view, got %d", failedW.Code)
	}
	failedBody := failedW.Body.String()
	failedLabelRe := regexp.MustCompile(`data-failed-checkbox[^>]*aria-label="select failed publication[^"]*"`)
	if !failedLabelRe.MatchString(failedBody) {
		t.Fatalf("expected accessible label on failed selection checkbox")
	}

	createReq := httptest.NewRequest(http.MethodGet, "/?view=create", nil)
	createW := httptest.NewRecorder()
	h.ServeHTTP(createW, createReq)
	if createW.Code != http.StatusOK {
		t.Fatalf("expected 200 for create view, got %d", createW.Code)
	}
	createBody := createW.Body.String()
	if !strings.Contains(createBody, "<label for=\"create-scheduled-at\">") || !strings.Contains(createBody, "id=\"create-scheduled-at\"") {
		t.Fatalf("expected create scheduled datetime label association")
	}

	settingsReq := httptest.NewRequest(http.MethodGet, "/?view=settings", nil)
	settingsW := httptest.NewRecorder()
	h.ServeHTTP(settingsW, settingsReq)
	if settingsW.Code != http.StatusOK {
		t.Fatalf("expected 200 for settings view, got %d", settingsW.Code)
	}
	settingsBody := settingsW.Body.String()
	if !strings.Contains(settingsBody, "<label for=\"timezone-select\">Timezone (IANA)</label>") {
		t.Fatalf("expected explicit label association for timezone select")
	}
}

func TestCreateViewIncludesComposerPreviewUploadAndNetworks(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/?view=create", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for create view, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "id=\"create-network-picker\"") || !strings.Contains(body, "data-network-chip") {
		t.Fatalf("expected network picker in create view")
	}
	if !strings.Contains(body, "id=\"create-media-input\"") || !strings.Contains(body, "id=\"create-media-list\"") {
		t.Fatalf("expected media upload controls in create view")
	}
	if !strings.Contains(body, "class=\"preview-panel\"") || !strings.Contains(body, "id=\"preview-text\"") {
		t.Fatalf("expected live preview panel in create view")
	}
	if !strings.Contains(body, "id=\"preview-media\" hidden") {
		t.Fatalf("expected media preview block to be hidden by default when there is no media")
	}
	if !strings.Contains(body, ".composer-text-wrap textarea {") || !strings.Contains(body, "width: 100%;") {
		t.Fatalf("expected create textarea to span full composer width")
	}
	if !strings.Contains(body, "name=\"intent\" value=\"publish_now\"") {
		t.Fatalf("expected publish_now action in create view")
	}
}

func TestCreatePostFromFormSupportsMediaIDs(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	mediaID, err := db.NewID("med")
	if err != nil {
		t.Fatalf("new media id: %v", err)
	}
	createdMedia, err := store.CreateMedia(t.Context(), domain.Media{
		ID:           mediaID,
		Platform:     domain.PlatformX,
		Kind:         "image",
		OriginalName: "preview.png",
		StoragePath:  filepath.Join(tempDir, "preview.png"),
		MimeType:     "image/png",
		SizeBytes:    1234,
	})
	if err != nil {
		t.Fatalf("create media: %v", err)
	}

	form := url.Values{}
	form.Set("platform", "x")
	form.Set("text", "form post with media ids")
	form.Set("intent", "draft")
	form.Add("media_ids", createdMedia.ID)

	req := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewBufferString(form.Encode()))
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 from form submit, got %d", w.Code)
	}

	drafts, err := store.ListDrafts(t.Context())
	if err != nil {
		t.Fatalf("list drafts: %v", err)
	}
	if len(drafts) != 1 {
		t.Fatalf("expected 1 draft, got %d", len(drafts))
	}
	if len(drafts[0].Media) != 1 || drafts[0].Media[0].ID != createdMedia.ID {
		t.Fatalf("expected draft to include submitted media id")
	}
}

func TestCalendarCellsRenderAllEventsForDynamicOverflow(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	settingsForm := url.Values{}
	settingsForm.Set("timezone", "UTC")
	settingsReq := httptest.NewRequest(http.MethodPost, "/settings/timezone", bytes.NewBufferString(settingsForm.Encode()))
	settingsReq.Header.Set("content-type", "application/x-www-form-urlencoded")
	settingsW := httptest.NewRecorder()
	h.ServeHTTP(settingsW, settingsReq)
	if settingsW.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 on timezone update, got %d", settingsW.Code)
	}

	selectedDay := time.Date(2026, time.February, 26, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 6; i++ {
		createBody, _ := json.Marshal(map[string]any{
			"platform":     "x",
			"text":         "dyn-overflow-" + strconv.Itoa(i+1),
			"scheduled_at": selectedDay.Add(time.Duration(9+i) * time.Hour).Format(time.RFC3339),
		})
		createReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createBody))
		createW := httptest.NewRecorder()
		h.ServeHTTP(createW, createReq)
		if createW.Code != http.StatusCreated {
			t.Fatalf("expected 201 for seed post %d, got %d", i+1, createW.Code)
		}
	}

	monthParam := selectedDay.Format("2006-01")
	dayParam := selectedDay.Format("2006-01-02")
	calendarReq := httptest.NewRequest(http.MethodGet, "/?view=calendar&month="+monthParam+"&day="+dayParam, nil)
	calendarW := httptest.NewRecorder()
	h.ServeHTTP(calendarW, calendarReq)
	if calendarW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", calendarW.Code)
	}

	body := calendarW.Body.String()
	selectedDayEventsRe := regexp.MustCompile(`(?s)<div class="day-cell[^"]*selected[^"]*">.*?<div class="day-events" data-day-events>(.*?)</div>\s*</a>`)
	selectedDayEventsMatch := selectedDayEventsRe.FindStringSubmatch(body)
	if len(selectedDayEventsMatch) != 2 {
		t.Fatalf("expected selected day events block in calendar grid")
	}
	selectedDayEvents := selectedDayEventsMatch[1]
	if got := strings.Count(selectedDayEvents, "dyn-overflow-"); got != 6 {
		t.Fatalf("expected all 6 selected-day events rendered for client-side fit, got %d", got)
	}
	if !strings.Contains(selectedDayEvents, "data-day-overflow hidden") {
		t.Fatalf("expected selected day overflow indicator placeholder for dynamic sizing")
	}
	if strings.Contains(body, "+3 more") {
		t.Fatalf("expected no server-side fixed +N more truncation")
	}
	if !strings.Contains(body, "const syncCalendarCellOverflow = () => {") {
		t.Fatalf("expected client-side overflow fit calculation script")
	}
}
