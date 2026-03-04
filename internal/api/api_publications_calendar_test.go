package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/db"
)

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
		"account_id": testAccountID(t, store),
		"text":       "this draft must stay out of publications",
	})
	createDraftReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createDraftBody))
	createDraftW := httptest.NewRecorder()
	h.ServeHTTP(createDraftW, createDraftReq)
	if createDraftW.Code != http.StatusCreated {
		t.Fatalf("expected 201 for draft, got %d", createDraftW.Code)
	}

	createScheduledBody, _ := json.Marshal(map[string]any{
		"account_id":   testAccountID(t, store),
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
		"account_id":   testAccountID(t, store),
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
		"account_id":   testAccountID(t, store),
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
	if !strings.Contains(body, "href=\"/?view=publications\"") {
		t.Fatalf("expected publications navigation link")
	}
	if !strings.Contains(body, "href=\"/?view=drafts\"") {
		t.Fatalf("expected drafts navigation link")
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
		"account_id": testAccountID(t, store),
		"text":       "draft badge",
	})
	createDraftReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createDraftBody))
	createDraftW := httptest.NewRecorder()
	h.ServeHTTP(createDraftW, createDraftReq)
	if createDraftW.Code != http.StatusCreated {
		t.Fatalf("expected 201 for draft, got %d", createDraftW.Code)
	}

	createScheduledBody, _ := json.Marshal(map[string]any{
		"account_id":   testAccountID(t, store),
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
		"account_id":   testAccountID(t, store),
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
	if !strings.Contains(body, "href=\"/?view=calendar") {
		t.Fatalf("expected calendar navigation link")
	}
	if !strings.Contains(body, "href=\"/?view=settings\"") {
		t.Fatalf("expected settings navigation link")
	}
	if !strings.Contains(body, "nav-item-settings") {
		t.Fatalf("expected settings nav item class for desktop bottom placement")
	}
	if !strings.Contains(body, "href=\"/?view=publications\"") {
		t.Fatalf("expected publications navigation link")
	}
	if !strings.Contains(body, "href=\"/?view=drafts\"") {
		t.Fatalf("expected drafts navigation link")
	}
	if !strings.Contains(body, "href=\"/?view=failed\"") {
		t.Fatalf("expected failed navigation link")
	}
	if strings.Count(body, "class=\"nav-badge\">1</span>") < 2 {
		t.Fatalf("expected neutral nav badges for scheduled and drafts")
	}
	if !strings.Contains(body, "class=\"nav-badge nav-badge-danger\">1</span>") {
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
		"account_id":   testAccountID(t, store),
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
		"account_id": testAccountID(t, store),
		"text":       "draft without badge",
	})
	createDraftReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createDraftBody))
	createDraftW := httptest.NewRecorder()
	h.ServeHTTP(createDraftW, createDraftReq)
	if createDraftW.Code != http.StatusCreated {
		t.Fatalf("expected 201 for draft post, got %d", createDraftW.Code)
	}

	createFailedBody, _ := json.Marshal(map[string]any{
		"account_id":   testAccountID(t, store),
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
