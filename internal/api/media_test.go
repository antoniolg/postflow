package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antoniolg/publisher/internal/db"
	"github.com/antoniolg/publisher/internal/domain"
)

func TestMediaAPIListAndDeleteLifecycle(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	mediaPath := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(mediaPath, []byte("hello media"), 0o644); err != nil {
		t.Fatalf("seed media file: %v", err)
	}
	created, err := store.CreateMedia(t.Context(), domain.Media{
		Kind:         "image",
		OriginalName: "sample.txt",
		StoragePath:  mediaPath,
		MimeType:     "text/plain; charset=utf-8",
		SizeBytes:    11,
	})
	if err != nil {
		t.Fatalf("create media row: %v", err)
	}

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	listReq := httptest.NewRequest(http.MethodGet, "/media?limit=10", nil)
	listW := httptest.NewRecorder()
	h.ServeHTTP(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("expected list status 200, got %d", listW.Code)
	}
	var listResp struct {
		Count int `json:"count"`
		Items []struct {
			ID         string `json:"id"`
			PreviewURL string `json:"preview_url"`
			UsageCount int    `json:"usage_count"`
			InUse      bool   `json:"in_use"`
		} `json:"items"`
	}
	if err := json.Unmarshal(listW.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if listResp.Count != 1 || len(listResp.Items) != 1 {
		t.Fatalf("expected 1 media item in list, got count=%d items=%d", listResp.Count, len(listResp.Items))
	}
	if listResp.Items[0].ID != created.ID {
		t.Fatalf("expected listed media id %q, got %q", created.ID, listResp.Items[0].ID)
	}
	if listResp.Items[0].UsageCount != 0 || listResp.Items[0].InUse {
		t.Fatalf("expected listed media to be unused")
	}
	if !strings.Contains(listResp.Items[0].PreviewURL, "/media/"+created.ID+"/content") {
		t.Fatalf("expected preview url for media content endpoint, got %q", listResp.Items[0].PreviewURL)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/media/"+created.ID, nil)
	deleteW := httptest.NewRecorder()
	h.ServeHTTP(deleteW, deleteReq)
	if deleteW.Code != http.StatusOK {
		t.Fatalf("expected delete status 200, got %d: %s", deleteW.Code, deleteW.Body.String())
	}
	if _, err := os.Stat(mediaPath); !os.IsNotExist(err) {
		t.Fatalf("expected media file to be removed from disk")
	}

	listAfterReq := httptest.NewRequest(http.MethodGet, "/media?limit=10", nil)
	listAfterW := httptest.NewRecorder()
	h.ServeHTTP(listAfterW, listAfterReq)
	if listAfterW.Code != http.StatusOK {
		t.Fatalf("expected list-after-delete status 200, got %d", listAfterW.Code)
	}
	var listAfterResp struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(listAfterW.Body.Bytes(), &listAfterResp); err != nil {
		t.Fatalf("decode list-after-delete response: %v", err)
	}
	if listAfterResp.Count != 0 {
		t.Fatalf("expected no media after delete, got count=%d", listAfterResp.Count)
	}
}

func TestMediaAPIDeleteRejectsInUseMedia(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	mediaPath := filepath.Join(tempDir, "in-use.png")
	if err := os.WriteFile(mediaPath, []byte("in use"), 0o644); err != nil {
		t.Fatalf("seed media file: %v", err)
	}
	createdMedia, err := store.CreateMedia(t.Context(), domain.Media{
		Kind:         "image",
		OriginalName: "in-use.png",
		StoragePath:  mediaPath,
		MimeType:     "image/png",
		SizeBytes:    6,
	})
	if err != nil {
		t.Fatalf("create media: %v", err)
	}
	if _, err := store.CreatePost(t.Context(), db.CreatePostParams{
		Post: domain.Post{
			AccountID:   createTestAccount(t, store).ID,
			Text:        "post with in-use media",
			Status:      domain.PostStatusDraft,
			MaxAttempts: 3,
		},
		MediaIDs: []string{createdMedia.ID},
	}); err != nil {
		t.Fatalf("create post: %v", err)
	}

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	deleteReq := httptest.NewRequest(http.MethodDelete, "/media/"+createdMedia.ID, nil)
	deleteW := httptest.NewRecorder()
	h.ServeHTTP(deleteW, deleteReq)
	if deleteW.Code != http.StatusConflict {
		t.Fatalf("expected delete status 409 for in-use media, got %d: %s", deleteW.Code, deleteW.Body.String())
	}
	if _, err := os.Stat(mediaPath); err != nil {
		t.Fatalf("expected in-use file to remain on disk, stat err=%v", err)
	}
}

func TestCreateAndSettingsViewsRenderMediaManagementSections(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	mediaPath := filepath.Join(tempDir, "preview.png")
	if err := os.WriteFile(mediaPath, []byte("preview"), 0o644); err != nil {
		t.Fatalf("seed media file: %v", err)
	}
	createdMedia, err := store.CreateMedia(t.Context(), domain.Media{
		Kind:         "image",
		OriginalName: "preview.png",
		StoragePath:  mediaPath,
		MimeType:     "image/png",
		SizeBytes:    7,
	})
	if err != nil {
		t.Fatalf("create media: %v", err)
	}

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	createReq := httptest.NewRequest(http.MethodGet, "/?view=create", nil)
	createW := httptest.NewRecorder()
	h.ServeHTTP(createW, createReq)
	if createW.Code != http.StatusOK {
		t.Fatalf("expected create view status 200, got %d", createW.Code)
	}
	createBody := createW.Body.String()
	if !strings.Contains(createBody, `id="create-media-library"`) {
		t.Fatalf("expected create media library section")
	}
	if !strings.Contains(createBody, `data-media-open="`+createdMedia.ID+`"`) {
		t.Fatalf("expected create media library to include open action button for uploaded media")
	}

	settingsReq := httptest.NewRequest(http.MethodGet, "/?view=settings", nil)
	settingsW := httptest.NewRecorder()
	h.ServeHTTP(settingsW, settingsReq)
	if settingsW.Code != http.StatusOK {
		t.Fatalf("expected settings view status 200, got %d", settingsW.Code)
	}
	settingsBody := settingsW.Body.String()
	if !strings.Contains(settingsBody, "media library") || !strings.Contains(settingsBody, "files ·") {
		t.Fatalf("expected settings media section with totals")
	}
	if !strings.Contains(settingsBody, `/media/`+createdMedia.ID+`/delete`) {
		t.Fatalf("expected settings media delete form action for media item")
	}

	deleteForm := bytes.NewBufferString("return_to=%2F%3Fview%3Dsettings")
	deleteFormReq := httptest.NewRequest(http.MethodPost, "/media/"+createdMedia.ID+"/delete", deleteForm)
	deleteFormReq.Header.Set("content-type", "application/x-www-form-urlencoded")
	deleteFormW := httptest.NewRecorder()
	h.ServeHTTP(deleteFormW, deleteFormReq)
	if deleteFormW.Code != http.StatusSeeOther {
		t.Fatalf("expected form delete redirect, got %d", deleteFormW.Code)
	}
	if loc := deleteFormW.Header().Get("Location"); !strings.Contains(loc, "media_success=") {
		t.Fatalf("expected success redirect query, got %q", loc)
	}
}

func TestUploadMediaAcceptsOutOfOrderMultipartFields(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	filePart, err := writer.CreateFormFile("file", "clip.txt")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	payload := []byte("hello streaming upload")
	if _, err := filePart.Write(payload); err != nil {
		t.Fatalf("write file payload: %v", err)
	}
	if err := writer.WriteField("kind", "image"); err != nil {
		t.Fatalf("write kind field: %v", err)
	}
	if err := writer.WriteField("platform", "x"); err != nil {
		t.Fatalf("write platform field: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/media", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		ID           string `json:"id"`
		Kind         string `json:"kind"`
		OriginalName string `json:"original_name"`
		StoragePath  string `json:"storage_path"`
		SizeBytes    int64  `json:"size_bytes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ID == "" {
		t.Fatalf("expected media id in response")
	}
	if resp.Kind != "image" {
		t.Fatalf("expected kind=image, got %q", resp.Kind)
	}
	if resp.OriginalName != "clip.txt" {
		t.Fatalf("expected original_name=clip.txt, got %q", resp.OriginalName)
	}
	if resp.SizeBytes != int64(len(payload)) {
		t.Fatalf("expected size_bytes=%d, got %d", len(payload), resp.SizeBytes)
	}

	stored, err := os.ReadFile(resp.StoragePath)
	if err != nil {
		t.Fatalf("read stored file: %v", err)
	}
	if !bytes.Equal(stored, payload) {
		t.Fatalf("stored file contents mismatch")
	}
}

func TestUploadMediaMissingFileReturnsBadRequest(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("platform", "x"); err != nil {
		t.Fatalf("write platform field: %v", err)
	}
	if err := writer.WriteField("kind", "video"); err != nil {
		t.Fatalf("write kind field: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/media", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUploadMediaIgnoresLegacyPlatformField(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("platform", "linkedin"); err != nil {
		t.Fatalf("write platform field: %v", err)
	}
	filePart, err := writer.CreateFormFile("file", "tmp.txt")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := filePart.Write([]byte("will be discarded")); err != nil {
		t.Fatalf("write file payload: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/media", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	mediaDir := filepath.Join(tempDir, "media")
	entries, err := os.ReadDir(mediaDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read media dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("expected uploaded media file to be persisted")
	}
}
