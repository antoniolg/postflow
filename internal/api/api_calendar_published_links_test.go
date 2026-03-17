package api

import (
	"bytes"
	"encoding/json"
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

func TestCalendarPublishedPanelRendersClickablePlatformLogo(t *testing.T) {
	store, handler, selectedDay := openCalendarPublishedLinksTestEnv(t)

	payload, _ := json.Marshal(map[string]any{
		"account_id":   testAccountID(t, store),
		"text":         "published with permalink",
		"scheduled_at": selectedDay.Add(10 * time.Hour).Format(time.RFC3339),
	})
	postID := createCalendarPublishedLinksPost(t, handler, payload)
	if err := store.MarkPublished(t.Context(), postID, "x-post-123", "https://x.com/postflowbot/status/x-post-123"); err != nil {
		t.Fatalf("mark published: %v", err)
	}

	panel := fetchCalendarPublishedLinksPanel(t, handler, selectedDay)
	if !strings.Contains(panel, `href="https://x.com/postflowbot/status/x-post-123"`) {
		t.Fatalf("expected published platform logo link in panel, got %s", panel)
	}
	if !strings.Contains(panel, `target="_blank"`) {
		t.Fatalf("expected published platform link to open in new tab")
	}
}

func TestCalendarPublishedPanelRendersMenuForSameNetworkMultiAccountGroup(t *testing.T) {
	store, handler, selectedDay := openCalendarPublishedLinksTestEnv(t)

	accountA := createCalendarPublishedLinksAccount(t, store, domain.PlatformX, "x-menu-a", "X alpha")
	accountB := createCalendarPublishedLinksAccount(t, store, domain.PlatformX, "x-menu-b", "X beta")
	scheduledAt := selectedDay.Add(11 * time.Hour).Format(time.RFC3339)

	firstID := createCalendarPublishedLinksPost(t, handler, mustJSON(t, map[string]any{
		"account_id":   accountA.ID,
		"text":         "same grouped published content",
		"scheduled_at": scheduledAt,
	}))
	secondID := createCalendarPublishedLinksPost(t, handler, mustJSON(t, map[string]any{
		"account_id":   accountB.ID,
		"text":         "same grouped published content",
		"scheduled_at": scheduledAt,
	}))

	if err := store.MarkPublished(t.Context(), firstID, "x-menu-post-a", "https://x.com/x_alpha/status/x-menu-post-a"); err != nil {
		t.Fatalf("mark published first: %v", err)
	}
	if err := store.MarkPublished(t.Context(), secondID, "x-menu-post-b", "https://x.com/x_beta/status/x-menu-post-b"); err != nil {
		t.Fatalf("mark published second: %v", err)
	}

	panel := fetchCalendarPublishedLinksPanel(t, handler, selectedDay)
	if !strings.Contains(panel, `class="publication-platform-menu"`) {
		t.Fatalf("expected same-network published group to render a menu, got %s", panel)
	}
	if !strings.Contains(panel, `>X alpha</a>`) || !strings.Contains(panel, `>X beta</a>`) {
		t.Fatalf("expected menu links labelled by account display name, got %s", panel)
	}
}

func TestCalendarPublishedPanelRendersDisabledPlatformChipWhenPublishedURLMissing(t *testing.T) {
	store, handler, selectedDay := openCalendarPublishedLinksTestEnv(t)

	payload, _ := json.Marshal(map[string]any{
		"account_id":   testAccountID(t, store),
		"text":         "published without permalink",
		"scheduled_at": selectedDay.Add(12 * time.Hour).Format(time.RFC3339),
	})
	postID := createCalendarPublishedLinksPost(t, handler, payload)
	if err := store.MarkPublished(t.Context(), postID, "x-post-no-link", ""); err != nil {
		t.Fatalf("mark published: %v", err)
	}

	panel := fetchCalendarPublishedLinksPanel(t, handler, selectedDay)
	if strings.Contains(panel, `href="https://x.com/`) {
		t.Fatalf("did not expect clickable x permalink when published_url is missing, got %s", panel)
	}
	if !strings.Contains(panel, `publication-platform-chip-disabled`) {
		t.Fatalf("expected disabled platform chip when published_url is missing")
	}
}

func openCalendarPublishedLinksTestEnv(t *testing.T) (*db.Store, http.Handler, time.Time) {
	t.Helper()

	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	handler := srv.Handler()

	settingsForm := url.Values{}
	settingsForm.Set("timezone", "UTC")
	settingsReq := httptest.NewRequest(http.MethodPost, "/settings/timezone", bytes.NewBufferString(settingsForm.Encode()))
	settingsReq.Header.Set("content-type", "application/x-www-form-urlencoded")
	settingsW := httptest.NewRecorder()
	handler.ServeHTTP(settingsW, settingsReq)
	if settingsW.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 on timezone update, got %d", settingsW.Code)
	}

	return store, handler, time.Date(2026, time.February, 26, 0, 0, 0, 0, time.UTC)
}

func createCalendarPublishedLinksAccount(t *testing.T, store *db.Store, platform domain.Platform, externalID, displayName string) domain.SocialAccount {
	t.Helper()
	account, err := store.UpsertAccount(t.Context(), db.UpsertAccountParams{
		Platform:          platform,
		DisplayName:       displayName,
		ExternalAccountID: externalID,
		AuthMethod:        domain.AuthMethodStatic,
		Status:            domain.AccountStatusConnected,
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	return account
}

func createCalendarPublishedLinksPost(t *testing.T, handler http.Handler, body []byte) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 for seed post, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	postID, _ := resp["id"].(string)
	if strings.TrimSpace(postID) == "" {
		t.Fatalf("expected post id in response")
	}
	return postID
}

func fetchCalendarPublishedLinksPanel(t *testing.T, handler http.Handler, selectedDay time.Time) string {
	t.Helper()
	monthParam := selectedDay.Format("2006-01")
	dayParam := selectedDay.Format("2006-01-02")
	req := httptest.NewRequest(http.MethodGet, "/?view=calendar&month="+monthParam+"&day="+dayParam, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	panelStart := strings.Index(body, "<aside class=\"day-panel\" aria-label=\"Day detail\">")
	if panelStart == -1 {
		t.Fatalf("expected day panel in calendar view")
	}
	panelEndRel := strings.Index(body[panelStart:], "</aside>")
	if panelEndRel == -1 {
		t.Fatalf("expected day panel closing tag")
	}
	return body[panelStart : panelStart+panelEndRel]
}

func mustJSON(t *testing.T, payload map[string]any) []byte {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return body
}
