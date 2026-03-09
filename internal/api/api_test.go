package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

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
