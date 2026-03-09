package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/db"
	"github.com/antoniolg/postflow/internal/domain"
)

func TestCreateViewRendersAllThreadStepsInPreviewWhenEditing(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	createBody, _ := json.Marshal(map[string]any{
		"account_id":   testAccountID(t, store),
		"scheduled_at": time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339),
		"segments": []map[string]any{
			{"text": "thread root preview"},
			{"text": "thread follow up preview"},
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

	req := httptest.NewRequest(http.MethodGet, "/?view=create&edit_id="+rootID, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for create edit view, got %d", w.Code)
	}

	body := w.Body.String()
	if !regexp.MustCompile(`EDIT POST</h1>`).MatchString(body) {
		t.Fatalf("expected create header title to switch to edit mode")
	}
	if regexp.MustCompile(`preview-step-count|preview-summary`).MatchString(body) {
		t.Fatalf("did not expect preview sequence summary copy in edit mode")
	}

	previewThreadRe := regexp.MustCompile(`(?s)id="preview-thread".*preview-step-root.*thread root preview.*preview-step-followup.*thread follow up preview`)
	if !previewThreadRe.MatchString(body) {
		t.Fatalf("expected live preview to render root and follow-up steps when editing a thread")
	}
}
