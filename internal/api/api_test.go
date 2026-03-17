package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/db"
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
		"account_id":   testAccountID(t, store),
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

func TestScheduleJSONDefaultsToPublicationsAndSupportsPostsView(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()
	accountID := testAccountID(t, store)
	scheduledAt := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)

	payload, _ := json.Marshal(map[string]any{
		"account_id":   accountID,
		"scheduled_at": scheduledAt.Format(time.RFC3339),
		"segments": []map[string]any{
			{"text": "root post"},
			{"text": "reply post"},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/posts", bytes.NewReader(payload))
	req.Header.Set("content-type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating thread, got %d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/schedule", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 default schedule list, got %d", w.Code)
	}
	var grouped struct {
		Count int `json:"count"`
		Items []struct {
			PublicationID string `json:"publication_id"`
			SegmentCount  int    `json:"segment_count"`
			Segments      []struct {
				PostID string `json:"post_id"`
			} `json:"segments"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &grouped); err != nil {
		t.Fatalf("decode grouped schedule response: %v", err)
	}
	if grouped.Count != 1 || len(grouped.Items) != 1 {
		t.Fatalf("expected one grouped publication, got count=%d items=%d", grouped.Count, len(grouped.Items))
	}
	if grouped.Items[0].PublicationID == "" || grouped.Items[0].SegmentCount != 2 || len(grouped.Items[0].Segments) != 2 {
		t.Fatalf("unexpected grouped publication payload: %+v", grouped.Items[0])
	}

	req = httptest.NewRequest(http.MethodGet, "/schedule?view=posts", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 posts view schedule list, got %d", w.Code)
	}
	var raw struct {
		Count int `json:"count"`
		Items []struct {
			ID             string `json:"id"`
			ThreadPosition int    `json:"thread_position"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw schedule response: %v", err)
	}
	if raw.Count != 2 || len(raw.Items) != 2 || raw.Items[1].ThreadPosition != 2 {
		t.Fatalf("unexpected raw schedule payload: %+v", raw)
	}
}

func TestPreferredUILanguage(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{
			name:   "empty fallback english",
			header: "",
			want:   "en",
		},
		{
			name:   "spanish preferred",
			header: "es-ES,es;q=0.9,en;q=0.8",
			want:   "es",
		},
		{
			name:   "unsupported falls back english",
			header: "fr-CA,fr;q=0.9",
			want:   "en",
		},
		{
			name:   "quality values respected",
			header: "en;q=0.4,es;q=0.9",
			want:   "es",
		},
		{
			name:   "wildcard falls back english",
			header: "*,fr;q=0.9",
			want:   "en",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := preferredUILanguage(tc.header)
			if got != tc.want {
				t.Fatalf("preferredUILanguage(%q) = %q, want %q", tc.header, got, tc.want)
			}
		})
	}
}

func TestScheduleHTMLUsesBrowserLanguageWithEnglishFallback(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()
	_ = testAccountID(t, store)

	reqES := httptest.NewRequest(http.MethodGet, "/?view=create", nil)
	reqES.Header.Set("Accept-Language", "es-ES,es;q=0.9,en;q=0.8")
	wES := httptest.NewRecorder()
	h.ServeHTTP(wES, reqES)
	if wES.Code != http.StatusOK {
		t.Fatalf("expected 200 for create view, got %d", wES.Code)
	}
	bodyES := wES.Body.String()
	if !strings.Contains(bodyES, "<html lang=\"es\">") {
		t.Fatalf("expected spanish html lang attribute from browser language")
	}
	if !strings.Contains(bodyES, "placeholder=\"Escribe tu publicación...\"") {
		t.Fatalf("expected spanish localized create placeholder")
	}
	if !strings.Contains(bodyES, "// editor del hilo") {
		t.Fatalf("expected spanish localized thread composer label")
	}
	reqESSettings := httptest.NewRequest(http.MethodGet, "/?view=settings", nil)
	reqESSettings.Header.Set("Accept-Language", "es-ES,es;q=0.9,en;q=0.8")
	wESSettings := httptest.NewRecorder()
	h.ServeHTTP(wESSettings, reqESSettings)
	if wESSettings.Code != http.StatusOK {
		t.Fatalf("expected 200 for spanish settings view, got %d", wESSettings.Code)
	}
	bodyESSettings := wESSettings.Body.String()
	if !strings.Contains(bodyESSettings, ">conectado<") {
		t.Fatalf("expected spanish translated account status label in settings view")
	}

	reqFallback := httptest.NewRequest(http.MethodGet, "/?view=create", nil)
	reqFallback.Header.Set("Accept-Language", "fr-CA,fr;q=0.9")
	wFallback := httptest.NewRecorder()
	h.ServeHTTP(wFallback, reqFallback)
	if wFallback.Code != http.StatusOK {
		t.Fatalf("expected 200 for create fallback view, got %d", wFallback.Code)
	}
	bodyFallback := wFallback.Body.String()
	if !strings.Contains(bodyFallback, "<html lang=\"en\">") {
		t.Fatalf("expected english fallback html lang attribute")
	}
	if !strings.Contains(bodyFallback, "placeholder=\"Write your post...\"") {
		t.Fatalf("expected english fallback create placeholder")
	}
	reqFallbackSettings := httptest.NewRequest(http.MethodGet, "/?view=settings", nil)
	reqFallbackSettings.Header.Set("Accept-Language", "fr-CA,fr;q=0.9")
	wFallbackSettings := httptest.NewRecorder()
	h.ServeHTTP(wFallbackSettings, reqFallbackSettings)
	if wFallbackSettings.Code != http.StatusOK {
		t.Fatalf("expected 200 for english fallback settings view, got %d", wFallbackSettings.Code)
	}
	bodyFallbackSettings := wFallbackSettings.Body.String()
	if !strings.Contains(bodyFallbackSettings, ">connected<") {
		t.Fatalf("expected english fallback account status label in settings view")
	}
}

func TestServesEmbeddedBrandingAssets(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/assets/icons/favicon-32x32.png", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 serving embedded icon, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/png") {
		t.Fatalf("expected image/png content type, got %q", ct)
	}
	if w.Body.Len() == 0 {
		t.Fatalf("expected non-empty icon payload")
	}
}
