package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/publisher/internal/db"
	"github.com/antoniolg/publisher/internal/domain"
)

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
		"account_id": testAccountID(t, store),
		"text":       "draft for accessibility labels",
	})
	createDraftReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createDraftBody))
	createDraftW := httptest.NewRecorder()
	h.ServeHTTP(createDraftW, createDraftReq)
	if createDraftW.Code != http.StatusCreated {
		t.Fatalf("expected 201 for draft seed post, got %d", createDraftW.Code)
	}

	createFailedBody, _ := json.Marshal(map[string]any{
		"account_id":   testAccountID(t, store),
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
	if !strings.Contains(calendarBody, "href=\"/assets/icons/favicon.ico\"") {
		t.Fatalf("expected favicon link in html head")
	}
	if !strings.Contains(calendarBody, "src=\"/assets/icons/postflow-logo-header-transparent-64.png\"") {
		t.Fatalf("expected sidebar logo image")
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
	if !strings.Contains(createBody, "for=\"create-scheduled-at\"") || !strings.Contains(createBody, "id=\"create-scheduled-at\"") {
		t.Fatalf("expected create scheduled datetime label association")
	}

	settingsReq := httptest.NewRequest(http.MethodGet, "/?view=settings", nil)
	settingsW := httptest.NewRecorder()
	h.ServeHTTP(settingsW, settingsReq)
	if settingsW.Code != http.StatusOK {
		t.Fatalf("expected 200 for settings view, got %d", settingsW.Code)
	}
	settingsBody := settingsW.Body.String()
	if !strings.Contains(settingsBody, "<label for=\"timezone-select\">zone (IANA)</label>") {
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
	accountID := testAccountID(t, store)

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
	if !strings.Contains(body, "class=\"network-chip-icon\"") || !strings.Contains(body, "viewBox=\"0 0 24 24\"") {
		t.Fatalf("expected network chips to include platform icons")
	}
	if !strings.Contains(body, "data-account-id=\""+accountID+"\"") {
		t.Fatalf("expected network chip account binding for unique selection")
	}
	accountSelectRe := regexp.MustCompile(`(?s)<select[^>]*id="create-account-select"[^>]*name="account_ids"[^>]*multiple[^>]*data-account-select`)
	if !accountSelectRe.MatchString(body) {
		t.Fatalf("expected hidden multi-account select backing field for selected networks")
	}
	if !strings.Contains(body, "id=\"create-primary-account-id\" name=\"account_id\"") {
		t.Fatalf("expected hidden primary account_id field for compatibility")
	}
	if !strings.Contains(body, "id=\"create-post-form\"") {
		t.Fatalf("expected create composer form")
	}
	if !strings.Contains(body, "data-editing-post=\"0\"") {
		t.Fatalf("expected create form to expose non-editing state")
	}
	if !strings.Contains(body, "class=\"create-header-actions\"") || !strings.Contains(body, "form=\"create-post-form\"") || !strings.Contains(body, "name=\"intent\" value=\"publish_now\"") {
		t.Fatalf("expected create actions in header and connected to composer form")
	}
	if !strings.Contains(body, "NEW POST</h1>") {
		t.Fatalf("expected create title in header")
	}
	if !strings.Contains(body, "id=\"create-media-input\"") || !strings.Contains(body, "id=\"create-media-list\"") {
		t.Fatalf("expected media upload controls in create view")
	}
	if !strings.Contains(body, "id=\"create-scheduled-at\" type=\"datetime-local\" name=\"scheduled_at_local\" data-date-picker") {
		t.Fatalf("expected create datetime input to use reusable date picker component")
	}
	if !strings.Contains(body, "class=\"preview-panel\"") || !strings.Contains(body, "id=\"preview-text\"") {
		t.Fatalf("expected live preview panel in create view")
	}
	if !strings.Contains(body, "id=\"preview-media\" hidden") {
		t.Fatalf("expected media preview block to be hidden by default when there is no media")
	}
	if !strings.Contains(body, "id=\"create-char-count\"") || !strings.Contains(body, "char-count-line") {
		t.Fatalf("expected multi-network char count lines in create view")
	}
	if !strings.Contains(body, "postflow.create.selected_account_ids") || !strings.Contains(body, "window.localStorage.setItem") {
		t.Fatalf("expected create view to persist selected networks in browser storage")
	}
	if !strings.Contains(body, "date-picker-popover") || !strings.Contains(body, "date-display") {
		t.Fatalf("expected custom dark date picker UI instead of native browser picker")
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
	form.Set("account_id", testAccountID(t, store))
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

func TestCreatePostFromFormSupportsMultipleAccounts(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	primaryID := testAccountID(t, store)
	secondary, err := store.UpsertAccount(t.Context(), db.UpsertAccountParams{
		Platform:          domain.PlatformLinkedIn,
		DisplayName:       "LinkedIn Secondary",
		ExternalAccountID: "li-secondary",
		AuthMethod:        domain.AuthMethodStatic,
		Status:            domain.AccountStatusConnected,
	})
	if err != nil {
		t.Fatalf("create secondary account: %v", err)
	}

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	form := url.Values{}
	form.Set("account_id", primaryID)
	form.Add("account_ids", primaryID)
	form.Add("account_ids", secondary.ID)
	form.Set("text", "multi-account post")
	form.Set("intent", "draft")

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
	if len(drafts) != 2 {
		t.Fatalf("expected 2 drafts for two selected accounts, got %d", len(drafts))
	}
	seen := make(map[string]bool)
	for _, item := range drafts {
		seen[item.AccountID] = true
	}
	if !seen[primaryID] || !seen[secondary.ID] {
		t.Fatalf("expected drafts for both selected accounts")
	}
}

func TestCreatePostFromFormMultipleAccountsFailsAllWhenOneAccountInvalid(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	primaryID := testAccountID(t, store)
	disconnected, err := store.UpsertAccount(t.Context(), db.UpsertAccountParams{
		Platform:          domain.PlatformLinkedIn,
		DisplayName:       "LinkedIn Disconnected",
		ExternalAccountID: "li-disconnected",
		AuthMethod:        domain.AuthMethodStatic,
		Status:            domain.AccountStatusDisconnected,
	})
	if err != nil {
		t.Fatalf("create disconnected account: %v", err)
	}

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	form := url.Values{}
	form.Set("account_id", primaryID)
	form.Add("account_ids", primaryID)
	form.Add("account_ids", disconnected.ID)
	form.Set("text", "must fail all")
	form.Set("intent", "draft")

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
	if len(drafts) != 0 {
		t.Fatalf("expected fail-all behavior with zero drafts created, got %d", len(drafts))
	}
}

func TestCreateViewPreviewRendersMarkdownFormatting(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	_ = testAccountID(t, store)

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	previewText := url.QueryEscape("Hola **mundo** y _equipo_")
	req := httptest.NewRequest(http.MethodGet, "/?view=create&text="+previewText, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for create view, got %d", w.Code)
	}

	body := w.Body.String()
	previewHTMLRe := regexp.MustCompile(`id="preview-text">[\s\n]*Hola <strong>mundo</strong> y <em>equipo</em>`)
	if !previewHTMLRe.MatchString(body) {
		t.Fatalf("expected markdown formatting in preview html")
	}
}
