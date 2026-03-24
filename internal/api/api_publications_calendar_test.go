package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/db"
	"github.com/antoniolg/postflow/internal/domain"
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
	if err := store.MarkPublished(t.Context(), toPublishID, "x-post-123", ""); err != nil {
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

func TestPublicationsViewGroupsSameContentByScheduleAcrossNetworks(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	xAccount := createConnectedAccountForPlatform(t, store, domain.PlatformX, "x-grouped")
	linkedinAccount := createConnectedAccountForPlatform(t, store, domain.PlatformLinkedIn, "li-grouped")
	instagramAccount := createConnectedAccountForPlatform(t, store, domain.PlatformInstagram, "ig-grouped")

	scheduledAt := time.Now().UTC().Add(3 * time.Hour).Truncate(time.Minute).Format(time.RFC3339)
	text := "same grouped text"
	for _, accountID := range []string{xAccount.ID, linkedinAccount.ID, instagramAccount.ID} {
		createBody, _ := json.Marshal(map[string]any{
			"account_id":   accountID,
			"text":         text,
			"scheduled_at": scheduledAt,
		})
		req := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createBody))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201 for grouped publication seed post, got %d", w.Code)
		}
	}

	publicationsReq := httptest.NewRequest(http.MethodGet, "/?view=publications", nil)
	publicationsW := httptest.NewRecorder()
	h.ServeHTTP(publicationsW, publicationsReq)
	if publicationsW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", publicationsW.Code)
	}

	body := publicationsW.Body.String()
	if strings.Count(body, "data-publication-group=\"3\"") != 1 {
		t.Fatalf("expected one grouped publication card with 3 posts")
	}
	if strings.Count(body, "class=\"card scheduled publication-card card-editable\"") != 1 {
		t.Fatalf("expected a single scheduled publication card in grouped view")
	}
	if !strings.Contains(body, "data-publication-platform-count=\"3\"") {
		t.Fatalf("expected grouped publication card to include three unique platforms")
	}

	editURLRe := regexp.MustCompile(`data-publication-group="3"[^>]*data-edit-url="([^"]+)"`)
	match := editURLRe.FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("expected grouped publication card with edit url")
	}
	groupEditURL := html.UnescapeString(match[1])
	if !strings.Contains(groupEditURL, "account_ids=") {
		t.Fatalf("expected grouped edit url to include account_ids query param")
	}
	if !strings.Contains(groupEditURL, "post_ids=") {
		t.Fatalf("expected grouped edit url to include grouped post_ids query param")
	}

	createReq := httptest.NewRequest(http.MethodGet, groupEditURL, nil)
	createW := httptest.NewRecorder()
	h.ServeHTTP(createW, createReq)
	if createW.Code != http.StatusOK {
		t.Fatalf("expected 200 for grouped edit create view, got %d", createW.Code)
	}
	createBody := createW.Body.String()
	for _, accountID := range []string{xAccount.ID, linkedinAccount.ID, instagramAccount.ID} {
		expected := "value=\"" + accountID + "\""
		idx := strings.Index(createBody, expected)
		if idx < 0 {
			t.Fatalf("expected create view options to include account id %s", accountID)
		}
		windowEnd := idx + 160
		if windowEnd > len(createBody) {
			windowEnd = len(createBody)
		}
		if !strings.Contains(createBody[idx:windowEnd], "selected") {
			t.Fatalf("expected account id %s to be selected in create view", accountID)
		}
	}
}

func TestDraftsViewGroupsSameContentAcrossNetworks(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	xAccount := createConnectedAccountForPlatform(t, store, domain.PlatformX, "x-draft-grouped")
	linkedinAccount := createConnectedAccountForPlatform(t, store, domain.PlatformLinkedIn, "li-draft-grouped")
	facebookAccount := createConnectedAccountForPlatform(t, store, domain.PlatformFacebook, "fb-draft-grouped")

	text := "same grouped draft text"
	for _, accountID := range []string{xAccount.ID, linkedinAccount.ID, facebookAccount.ID} {
		createBody, _ := json.Marshal(map[string]any{
			"account_id": accountID,
			"text":       text,
		})
		req := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createBody))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201 for grouped draft seed post, got %d", w.Code)
		}
	}

	draftsReq := httptest.NewRequest(http.MethodGet, "/?view=drafts", nil)
	draftsW := httptest.NewRecorder()
	h.ServeHTTP(draftsW, draftsReq)
	if draftsW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", draftsW.Code)
	}

	body := draftsW.Body.String()
	if strings.Count(body, "data-draft-group=\"3\"") != 1 {
		t.Fatalf("expected one grouped draft card with 3 posts")
	}
	if strings.Count(body, "class=\"card draft publication-card card-editable\"") != 1 {
		t.Fatalf("expected a single draft card in grouped view")
	}
	if !strings.Contains(body, "data-draft-platform-count=\"3\"") {
		t.Fatalf("expected grouped draft card to include three unique platforms")
	}

	editURLRe := regexp.MustCompile(`data-draft-group="3"[^>]*data-edit-url="([^"]+)"`)
	match := editURLRe.FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("expected grouped draft card with edit url")
	}
	groupEditURL := html.UnescapeString(match[1])
	if !strings.Contains(groupEditURL, "account_ids=") {
		t.Fatalf("expected grouped draft edit url to include account_ids query param")
	}

	createReq := httptest.NewRequest(http.MethodGet, groupEditURL, nil)
	createW := httptest.NewRecorder()
	h.ServeHTTP(createW, createReq)
	if createW.Code != http.StatusOK {
		t.Fatalf("expected 200 for grouped draft edit create view, got %d", createW.Code)
	}
	createBody := createW.Body.String()
	for _, accountID := range []string{xAccount.ID, linkedinAccount.ID, facebookAccount.ID} {
		expected := "value=\"" + accountID + "\""
		idx := strings.Index(createBody, expected)
		if idx < 0 {
			t.Fatalf("expected create view options to include account id %s", accountID)
		}
		windowEnd := idx + 160
		if windowEnd > len(createBody) {
			windowEnd = len(createBody)
		}
		if !strings.Contains(createBody[idx:windowEnd], "selected") {
			t.Fatalf("expected account id %s to be selected in create view", accountID)
		}
	}
}

func TestCalendarDayPanelGroupsSameContentAcrossNetworks(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	day := time.Date(2026, time.March, 6, 0, 0, 0, 0, time.UTC)
	scheduledAt := day.Add(13*time.Hour + 24*time.Minute).Format(time.RFC3339)
	xAccount := createConnectedAccountForPlatform(t, store, domain.PlatformX, "x-calendar-grouped")
	linkedinAccount := createConnectedAccountForPlatform(t, store, domain.PlatformLinkedIn, "li-calendar-grouped")
	facebookAccount := createConnectedAccountForPlatform(t, store, domain.PlatformFacebook, "fb-calendar-grouped")

	for _, accountID := range []string{xAccount.ID, linkedinAccount.ID, facebookAccount.ID} {
		createBody, _ := json.Marshal(map[string]any{
			"account_id":   accountID,
			"text":         "same grouped calendar text",
			"scheduled_at": scheduledAt,
		})
		req := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createBody))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201 for grouped calendar post, got %d", w.Code)
		}
	}

	calendarReq := httptest.NewRequest(http.MethodGet, "/?view=calendar&month=2026-03&day=2026-03-06", nil)
	calendarW := httptest.NewRecorder()
	h.ServeHTTP(calendarW, calendarReq)
	if calendarW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", calendarW.Code)
	}

	body := calendarW.Body.String()
	if strings.Count(body, "data-calendar-group=\"3\"") != 1 {
		t.Fatalf("expected one grouped calendar card with 3 posts")
	}
	if !strings.Contains(body, "data-calendar-platform-count=\"3\"") {
		t.Fatalf("expected grouped calendar card to include three unique platforms")
	}
	if !strings.Contains(body, "to publish (1)") {
		t.Fatalf("expected day panel pending count to reflect grouped logical publications")
	}

	cardRe := regexp.MustCompile(`(?s)<article class="card scheduled publication-card day-panel-publication-card[^>]*data-calendar-group="3"[^>]*>.*?</article>`)
	card := cardRe.FindString(body)
	if card == "" {
		t.Fatalf("expected grouped scheduled card in calendar side panel")
	}
	if strings.Contains(card, "publication-time-date") {
		t.Fatalf("expected calendar side panel card to render time only without date label")
	}
	if strings.Contains(card, "UTC") || strings.Contains(card, "CET") {
		t.Fatalf("expected calendar side panel card to avoid timezone suffix")
	}
}

func TestCalendarGridGroupsSameContentAcrossNetworks(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	day := time.Date(2026, time.March, 6, 0, 0, 0, 0, time.UTC)
	scheduledAt := day.Add(13*time.Hour + 24*time.Minute).Format(time.RFC3339)
	xAccount := createConnectedAccountForPlatform(t, store, domain.PlatformX, "x-calendar-grid-grouped")
	linkedinAccount := createConnectedAccountForPlatform(t, store, domain.PlatformLinkedIn, "li-calendar-grid-grouped")
	facebookAccount := createConnectedAccountForPlatform(t, store, domain.PlatformFacebook, "fb-calendar-grid-grouped")
	instagramAccount := createConnectedAccountForPlatform(t, store, domain.PlatformInstagram, "ig-calendar-grid-grouped")

	for _, accountID := range []string{xAccount.ID, linkedinAccount.ID, facebookAccount.ID, instagramAccount.ID} {
		createBody, _ := json.Marshal(map[string]any{
			"account_id":   accountID,
			"text":         "same grouped calendar grid text",
			"scheduled_at": scheduledAt,
		})
		req := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createBody))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201 for grouped calendar post, got %d", w.Code)
		}
	}

	calendarReq := httptest.NewRequest(http.MethodGet, "/?view=calendar&month=2026-03&day=2026-03-06", nil)
	calendarW := httptest.NewRecorder()
	h.ServeHTTP(calendarW, calendarReq)
	if calendarW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", calendarW.Code)
	}

	body := calendarW.Body.String()
	if strings.Count(body, "data-day-event data-status=") != 1 {
		t.Fatalf("expected grouped calendar grid to render one event row for duplicated content")
	}
	if strings.Count(body, "data-event-group=\"4\"") != 1 {
		t.Fatalf("expected grouped calendar grid row to expose group size 4")
	}
	if !strings.Contains(body, "data-event-platform-count=\"4\"") {
		t.Fatalf("expected grouped calendar grid row to include four unique platforms")
	}
	selectedDayCountRe := regexp.MustCompile(`(?s)<div class="day-cell [^"]*selected[^"]*">.*?<span class="day-count">1</span>`)
	if !selectedDayCountRe.MatchString(body) {
		t.Fatalf("expected selected day cell badge to reflect grouped logical publications")
	}
}

func TestPartiallyPublishedThreadStaysGroupedInPublishingSection(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	accountID := testAccountID(t, store)
	scheduledAt := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Minute)
	createBody, _ := json.Marshal(map[string]any{
		"account_id":   accountID,
		"scheduled_at": scheduledAt.Format(time.RFC3339),
		"segments": []map[string]any{
			{"text": "thread root"},
			{"text": "thread follow up"},
		},
	})
	createReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createBody))
	createW := httptest.NewRecorder()
	h.ServeHTTP(createW, createReq)
	if createW.Code != http.StatusCreated {
		t.Fatalf("expected 201 for thread create, got %d", createW.Code)
	}

	var createResp struct {
		Items []domain.Post `json:"items"`
	}
	if err := json.Unmarshal(createW.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("decode thread create response: %v", err)
	}
	if len(createResp.Items) != 2 {
		t.Fatalf("expected 2 thread items, got %d", len(createResp.Items))
	}

	rootID := ""
	for _, item := range createResp.Items {
		if item.ThreadPosition == 1 {
			rootID = item.ID
			break
		}
	}
	if rootID == "" {
		t.Fatalf("expected root post id in thread create response")
	}
	if err := store.MarkPublished(t.Context(), rootID, "x-root-123", ""); err != nil {
		t.Fatalf("mark root published: %v", err)
	}

	publicationsReq := httptest.NewRequest(http.MethodGet, "/?view=publications", nil)
	publicationsW := httptest.NewRecorder()
	h.ServeHTTP(publicationsW, publicationsReq)
	if publicationsW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", publicationsW.Code)
	}

	body := publicationsW.Body.String()
	if !strings.Contains(body, "publishing (1)") {
		t.Fatalf("expected partially published thread to appear in publishing section")
	}
	if strings.Count(body, "class=\"card publishing publication-card card-editable\"") != 1 {
		t.Fatalf("expected a single grouped publishing card for partial thread")
	}
	if !strings.Contains(body, "data-thread-steps=\"2\"") {
		t.Fatalf("expected grouped publishing card to expose total step count")
	}
	if !strings.Contains(body, ">2 steps<") {
		t.Fatalf("expected grouped publishing card to show total steps badge")
	}
	if !strings.Contains(body, ">1/2 published<") {
		t.Fatalf("expected grouped publishing card to show partial progress badge")
	}
	if strings.Contains(body, "published (1)") {
		t.Fatalf("partially published thread should not move to published section yet")
	}
}

func TestThreadStepPreviewTextUsesVisualClamp(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()
	_ = testAccountID(t, store)

	req := httptest.NewRequest(http.MethodGet, "/?view=publications", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, ".publication-thread-step-text {\n      min-width: 0;\n      flex: 1 1 auto;\n      display: -webkit-box;") {
		t.Fatalf("expected thread step preview styles to use clamped box layout")
	}
	if !strings.Contains(body, "-webkit-line-clamp: 3;") {
		t.Fatalf("expected thread step preview styles to clamp to three lines")
	}
	if !strings.Contains(body, "overflow-wrap: anywhere;") {
		t.Fatalf("expected thread step preview styles to wrap long unbroken tokens")
	}
}

func TestGroupedCardsExposeDeleteControlInScheduledAndDrafts(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	xAccount := createConnectedAccountForPlatform(t, store, domain.PlatformX, "x-delete-grouped")
	linkedinAccount := createConnectedAccountForPlatform(t, store, domain.PlatformLinkedIn, "li-delete-grouped")
	scheduledAt := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Minute).Format(time.RFC3339)

	for _, accountID := range []string{xAccount.ID, linkedinAccount.ID} {
		createScheduledBody, _ := json.Marshal(map[string]any{
			"account_id":   accountID,
			"text":         "grouped scheduled delete card",
			"scheduled_at": scheduledAt,
		})
		createScheduledReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createScheduledBody))
		createScheduledW := httptest.NewRecorder()
		h.ServeHTTP(createScheduledW, createScheduledReq)
		if createScheduledW.Code != http.StatusCreated {
			t.Fatalf("expected 201 for grouped scheduled post, got %d", createScheduledW.Code)
		}
	}

	publicationsReq := httptest.NewRequest(http.MethodGet, "/?view=publications", nil)
	publicationsW := httptest.NewRecorder()
	h.ServeHTTP(publicationsW, publicationsReq)
	if publicationsW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", publicationsW.Code)
	}
	publicationsBody := publicationsW.Body.String()
	publicationCardRe := regexp.MustCompile(`(?s)<article class="card scheduled publication-card card-editable"[^>]*data-publication-group="2"[^>]*>.*?</article>`)
	publicationCard := publicationCardRe.FindString(publicationsBody)
	if publicationCard == "" {
		t.Fatalf("expected grouped scheduled card")
	}
	if !strings.Contains(publicationCard, "class=\"publication-delete-form\"") {
		t.Fatalf("expected grouped scheduled card to expose delete control in top-right")
	}
	if strings.Count(publicationCard, "name=\"ids\" value=\"") != 2 {
		t.Fatalf("expected grouped scheduled delete form to submit both post ids")
	}

	for _, accountID := range []string{xAccount.ID, linkedinAccount.ID} {
		createDraftBody, _ := json.Marshal(map[string]any{
			"account_id": accountID,
			"text":       "grouped draft delete card",
		})
		createDraftReq := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createDraftBody))
		createDraftW := httptest.NewRecorder()
		h.ServeHTTP(createDraftW, createDraftReq)
		if createDraftW.Code != http.StatusCreated {
			t.Fatalf("expected 201 for grouped draft post, got %d", createDraftW.Code)
		}
	}

	draftsReq := httptest.NewRequest(http.MethodGet, "/?view=drafts", nil)
	draftsW := httptest.NewRecorder()
	h.ServeHTTP(draftsW, draftsReq)
	if draftsW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", draftsW.Code)
	}
	draftsBody := draftsW.Body.String()
	draftCardRe := regexp.MustCompile(`(?s)<article class="card draft publication-card card-editable"[^>]*data-draft-group="2"[^>]*>.*?</article>`)
	draftCard := draftCardRe.FindString(draftsBody)
	if draftCard == "" {
		t.Fatalf("expected grouped draft card")
	}
	if !strings.Contains(draftCard, "class=\"publication-delete-form\"") {
		t.Fatalf("expected grouped draft card to expose delete control in top-right")
	}
	if strings.Count(draftCard, "name=\"ids\" value=\"") != 2 {
		t.Fatalf("expected grouped draft delete form to submit both post ids")
	}
}

func TestFailedViewGroupsSameContentAcrossNetworks(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	xAccount := createConnectedAccountForPlatform(t, store, domain.PlatformX, "x-failed-grouped")
	linkedinAccount := createConnectedAccountForPlatform(t, store, domain.PlatformLinkedIn, "li-failed-grouped")

	scheduledAt := time.Now().UTC().Add(5 * time.Minute).Truncate(time.Minute).Format(time.RFC3339)
	text := "same grouped failed text"
	for _, accountID := range []string{xAccount.ID, linkedinAccount.ID} {
		createBody, _ := json.Marshal(map[string]any{
			"account_id":   accountID,
			"text":         text,
			"scheduled_at": scheduledAt,
			"max_attempts": 1,
		})
		req := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(createBody))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201 for grouped failed seed post, got %d", w.Code)
		}
		var created map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
			t.Fatalf("decode create response: %v", err)
		}
		postID, _ := created["id"].(string)
		if postID == "" {
			t.Fatalf("expected failed seed post id")
		}
		if err := store.RecordPublishFailure(t.Context(), postID, errors.New("boom"), 30*time.Second); err != nil {
			t.Fatalf("record publish failure: %v", err)
		}
	}

	failedReq := httptest.NewRequest(http.MethodGet, "/?view=failed", nil)
	failedW := httptest.NewRecorder()
	h.ServeHTTP(failedW, failedReq)
	if failedW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", failedW.Code)
	}

	body := failedW.Body.String()
	if strings.Count(body, "data-failed-group=\"2\"") != 1 {
		t.Fatalf("expected one grouped failed card with 2 posts")
	}
	if !strings.Contains(body, "data-failed-platform-count=\"2\"") {
		t.Fatalf("expected grouped failed card to include two unique platforms")
	}
	csvValueRe := regexp.MustCompile(`value="[^"]+,[^"]+"[^>]*data-failed-checkbox|data-failed-checkbox[^>]*value="[^"]+,[^"]+"`)
	if !csvValueRe.MatchString(body) {
		t.Fatalf("expected grouped failed card checkbox to include csv dead letter ids")
	}

	editURLRe := regexp.MustCompile(`data-failed-group="2"[^>]*data-edit-url="([^"]+)"`)
	match := editURLRe.FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("expected grouped failed card with edit url")
	}
	groupEditURL := html.UnescapeString(match[1])
	if !strings.Contains(groupEditURL, "account_ids=") {
		t.Fatalf("expected grouped failed edit url to include account_ids query param")
	}

	createReq := httptest.NewRequest(http.MethodGet, groupEditURL, nil)
	createW := httptest.NewRecorder()
	h.ServeHTTP(createW, createReq)
	if createW.Code != http.StatusOK {
		t.Fatalf("expected 200 for grouped failed edit create view, got %d", createW.Code)
	}
	createBody := createW.Body.String()
	for _, accountID := range []string{xAccount.ID, linkedinAccount.ID} {
		expected := "value=\"" + accountID + "\""
		idx := strings.Index(createBody, expected)
		if idx < 0 {
			t.Fatalf("expected create view options to include account id %s", accountID)
		}
		windowEnd := idx + 160
		if windowEnd > len(createBody) {
			windowEnd = len(createBody)
		}
		if !strings.Contains(createBody[idx:windowEnd], "selected") {
			t.Fatalf("expected account id %s to be selected in create view", accountID)
		}
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
	mobileNavGridRe := regexp.MustCompile(`(?s)@media \(max-width: 980px\)\s*\{.*?\.nav\s*\{[^}]*display:\s*grid;[^}]*grid-template-columns:\s*repeat\(5,\s*minmax\(0,\s*1fr\)\);[^}]*overflow-x:\s*hidden;`)
	if !mobileNavGridRe.MatchString(body) {
		t.Fatalf("expected mobile bottom nav to use fixed grid layout without horizontal scrolling")
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
