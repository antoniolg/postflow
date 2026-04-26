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
	"time"

	"github.com/antoniolg/postflow/internal/db"
	"github.com/antoniolg/postflow/internal/domain"
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

func TestMediaAPIDeleteAllowsPublishedOnlyMedia(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	mediaPath := filepath.Join(tempDir, "published-only.png")
	if err := os.WriteFile(mediaPath, []byte("published"), 0o644); err != nil {
		t.Fatalf("seed media file: %v", err)
	}
	createdMedia, err := store.CreateMedia(t.Context(), domain.Media{
		Kind:         "image",
		OriginalName: "published-only.png",
		StoragePath:  mediaPath,
		MimeType:     "image/png",
		SizeBytes:    9,
	})
	if err != nil {
		t.Fatalf("create media: %v", err)
	}
	if _, err := store.CreatePost(t.Context(), db.CreatePostParams{
		Post: domain.Post{
			AccountID:   createTestAccount(t, store).ID,
			Text:        "published post with media",
			Status:      domain.PostStatusPublished,
			MaxAttempts: 3,
		},
		MediaIDs: []string{createdMedia.ID},
	}); err != nil {
		t.Fatalf("create published post: %v", err)
	}

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	deleteReq := httptest.NewRequest(http.MethodDelete, "/media/"+createdMedia.ID, nil)
	deleteW := httptest.NewRecorder()
	h.ServeHTTP(deleteW, deleteReq)
	if deleteW.Code != http.StatusOK {
		t.Fatalf("expected delete status 200 for published-only media, got %d: %s", deleteW.Code, deleteW.Body.String())
	}
	if _, err := os.Stat(mediaPath); !os.IsNotExist(err) {
		t.Fatalf("expected published-only media file to be removed from disk")
	}
}

func TestMediaAPIDeleteAllowsCanceledOnlyMedia(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	mediaPath := filepath.Join(tempDir, "canceled-only.png")
	if err := os.WriteFile(mediaPath, []byte("canceled"), 0o644); err != nil {
		t.Fatalf("seed media file: %v", err)
	}
	createdMedia, err := store.CreateMedia(t.Context(), domain.Media{
		Kind:         "image",
		OriginalName: "canceled-only.png",
		StoragePath:  mediaPath,
		MimeType:     "image/png",
		SizeBytes:    8,
	})
	if err != nil {
		t.Fatalf("create media: %v", err)
	}
	if _, err := store.CreatePost(t.Context(), db.CreatePostParams{
		Post: domain.Post{
			AccountID:   createTestAccount(t, store).ID,
			Text:        "canceled post with media",
			Status:      domain.PostStatusCanceled,
			MaxAttempts: 3,
		},
		MediaIDs: []string{createdMedia.ID},
	}); err != nil {
		t.Fatalf("create canceled post: %v", err)
	}

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	deleteReq := httptest.NewRequest(http.MethodDelete, "/media/"+createdMedia.ID, nil)
	deleteW := httptest.NewRecorder()
	h.ServeHTTP(deleteW, deleteReq)
	if deleteW.Code != http.StatusOK {
		t.Fatalf("expected delete status 200 for canceled-only media, got %d: %s", deleteW.Code, deleteW.Body.String())
	}
	if _, err := os.Stat(mediaPath); !os.IsNotExist(err) {
		t.Fatalf("expected canceled-only media file to be removed from disk")
	}
}

func TestMediaAPIDeleteRejectsFutureScheduledMedia(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	mediaPath := filepath.Join(tempDir, "future-scheduled.png")
	if err := os.WriteFile(mediaPath, []byte("future"), 0o644); err != nil {
		t.Fatalf("seed media file: %v", err)
	}
	createdMedia, err := store.CreateMedia(t.Context(), domain.Media{
		Kind:         "image",
		OriginalName: "future-scheduled.png",
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
			Text:        "future scheduled post with media",
			Status:      domain.PostStatusScheduled,
			ScheduledAt: time.Now().UTC().Add(90 * time.Minute),
			MaxAttempts: 3,
		},
		MediaIDs: []string{createdMedia.ID},
	}); err != nil {
		t.Fatalf("create scheduled post: %v", err)
	}

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	deleteReq := httptest.NewRequest(http.MethodDelete, "/media/"+createdMedia.ID, nil)
	deleteW := httptest.NewRecorder()
	h.ServeHTTP(deleteW, deleteReq)
	if deleteW.Code != http.StatusConflict {
		t.Fatalf("expected delete status 409 for future scheduled media, got %d: %s", deleteW.Code, deleteW.Body.String())
	}
	if _, err := os.Stat(mediaPath); err != nil {
		t.Fatalf("expected future scheduled file to remain on disk, stat err=%v", err)
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
	if strings.Contains(createBody, `data-media-delete="`) {
		t.Fatalf("did not expect media management delete controls in create recent library")
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
	if !strings.Contains(settingsBody, `action="/media/purge"`) {
		t.Fatalf("expected settings media purge action")
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

func TestCreateEditViewHydratesInitialAttachments(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	mediaPath := filepath.Join(tempDir, "edit-preview.png")
	if err := os.WriteFile(mediaPath, []byte("preview"), 0o644); err != nil {
		t.Fatalf("seed media file: %v", err)
	}
	createdMedia, err := store.CreateMedia(t.Context(), domain.Media{
		Kind:         "image",
		OriginalName: "edit-preview.png",
		StoragePath:  mediaPath,
		MimeType:     "image/png",
		SizeBytes:    7,
	})
	if err != nil {
		t.Fatalf("create media: %v", err)
	}

	account := createTestAccount(t, store)
	createdPost, err := store.CreatePost(t.Context(), db.CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "post with existing media",
			Status:      domain.PostStatusScheduled,
			ScheduledAt: time.Now().UTC().Add(30 * time.Minute),
			MaxAttempts: 3,
		},
		MediaIDs: []string{createdMedia.ID},
	})
	if err != nil {
		t.Fatalf("create post with media: %v", err)
	}

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	editReq := httptest.NewRequest(http.MethodGet, "/?view=create&edit_id="+createdPost.Post.ID, nil)
	editW := httptest.NewRecorder()
	h.ServeHTTP(editW, editReq)
	if editW.Code != http.StatusOK {
		t.Fatalf("expected edit create view status 200, got %d", editW.Code)
	}
	body := editW.Body.String()
	if !strings.Contains(body, `const initialAttachments = [{"id":"`+createdMedia.ID+`"`) {
		t.Fatalf("expected create edit view to include initial attachment hydration for media %q", createdMedia.ID)
	}
	if !strings.Contains(body, `"previewUrl":"`+mediaContentURL(createdMedia.ID)+`"`) {
		t.Fatalf("expected create edit view to include preview url for media %q", createdMedia.ID)
	}
}

func TestMediaPurgeFormDeletesOnlyPublishedCanceledOrUnusedMedia(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	makeMedia := func(name string) domain.Media {
		path := filepath.Join(tempDir, name)
		if err := os.WriteFile(path, []byte(name), 0o644); err != nil {
			t.Fatalf("seed media file %s: %v", name, err)
		}
		item, err := store.CreateMedia(t.Context(), domain.Media{
			Kind:         "image",
			OriginalName: name,
			StoragePath:  path,
			MimeType:     "image/png",
			SizeBytes:    int64(len(name)),
		})
		if err != nil {
			t.Fatalf("create media %s: %v", name, err)
		}
		return item
	}

	orphan := makeMedia("orphan.png")
	publishedOnly := makeMedia("published-only.png")
	canceledOnly := makeMedia("canceled-only.png")
	draftOnly := makeMedia("draft-only.png")
	failedOnly := makeMedia("failed-only.png")
	account := createTestAccount(t, store)

	publishedResult, err := store.CreatePost(t.Context(), db.CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "published with media",
			Status:      domain.PostStatusPublished,
			MaxAttempts: 3,
		},
		MediaIDs: []string{publishedOnly.ID},
	})
	if err != nil {
		t.Fatalf("create published post: %v", err)
	}

	if _, err := store.CreatePost(t.Context(), db.CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "draft with media",
			Status:      domain.PostStatusDraft,
			MaxAttempts: 3,
		},
		MediaIDs: []string{draftOnly.ID},
	}); err != nil {
		t.Fatalf("create draft post: %v", err)
	}
	if _, err := store.CreatePost(t.Context(), db.CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "canceled with media",
			Status:      domain.PostStatusCanceled,
			MaxAttempts: 3,
		},
		MediaIDs: []string{canceledOnly.ID},
	}); err != nil {
		t.Fatalf("create canceled post: %v", err)
	}
	if _, err := store.CreatePost(t.Context(), db.CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "failed with media",
			Status:      domain.PostStatusFailed,
			MaxAttempts: 3,
		},
		MediaIDs: []string{failedOnly.ID},
	}); err != nil {
		t.Fatalf("create failed post: %v", err)
	}

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	h := srv.Handler()

	form := bytes.NewBufferString("return_to=%2F%3Fview%3Dsettings")
	req := httptest.NewRequest(http.MethodPost, "/media/purge", form)
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected purge redirect, got %d: %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "media_success=") {
		t.Fatalf("expected purge success redirect, got %q", loc)
	}
	if !strings.Contains(loc, "3+media+files+purged") {
		t.Fatalf("expected purge to report 3 deleted files, got %q", loc)
	}

	items, err := store.ListMedia(t.Context(), 20)
	if err != nil {
		t.Fatalf("list media after purge: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 media items to remain after purge, got %+v", items)
	}
	remaining := map[string]struct{}{}
	for _, item := range items {
		remaining[item.Media.ID] = struct{}{}
	}
	if _, ok := remaining[draftOnly.ID]; !ok {
		t.Fatalf("expected draft media to remain after purge")
	}
	if _, ok := remaining[failedOnly.ID]; !ok {
		t.Fatalf("expected failed media to remain after purge")
	}

	for _, path := range []string{orphan.StoragePath, publishedOnly.StoragePath, canceledOnly.StoragePath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected purged file %s to be deleted, got err=%v", path, err)
		}
	}
	if _, err := os.Stat(draftOnly.StoragePath); err != nil {
		t.Fatalf("expected draft media file to remain, stat err=%v", err)
	}
	if _, err := os.Stat(failedOnly.StoragePath); err != nil {
		t.Fatalf("expected failed media file to remain, stat err=%v", err)
	}

	publishedPost, err := store.GetPost(t.Context(), publishedResult.Post.ID)
	if err != nil {
		t.Fatalf("get published post after purge: %v", err)
	}
	if len(publishedPost.Media) != 0 {
		t.Fatalf("expected published post media references to be cleared by purge")
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

func TestUploadMediaFallsBackToFileExtensionWhenMultipartUsesGenericMime(t *testing.T) {
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
	filePart, err := writer.CreateFormFile("file", "cover.jpg")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	payload := []byte("not-a-real-jpeg-but-extension-matters")
	if _, err := filePart.Write(payload); err != nil {
		t.Fatalf("write file payload: %v", err)
	}
	if err := writer.WriteField("kind", "image"); err != nil {
		t.Fatalf("write kind field: %v", err)
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
		MimeType string `json:"mime_type"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.MimeType != "image/jpeg" {
		t.Fatalf("expected mime_type image/jpeg, got %q", resp.MimeType)
	}
}

func TestUploadMediaSniffsMimeWhenMultipartUsesGenericMimeWithoutExtension(t *testing.T) {
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
	filePart, err := writer.CreateFormFile("file", "cover")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	payload := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}
	if _, err := filePart.Write(payload); err != nil {
		t.Fatalf("write file payload: %v", err)
	}
	if err := writer.WriteField("kind", "image"); err != nil {
		t.Fatalf("write kind field: %v", err)
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
		MimeType string `json:"mime_type"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.MimeType != "image/png" {
		t.Fatalf("expected mime_type image/png, got %q", resp.MimeType)
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

func TestUploadMediaRejectsOversizedFile(t *testing.T) {
	oldMax := maxMediaUploadBytes
	maxMediaUploadBytes = 4
	t.Cleanup(func() { maxMediaUploadBytes = oldMax })

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
	filePart, err := writer.CreateFormFile("file", "large.txt")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := filePart.Write([]byte("too large")); err != nil {
		t.Fatalf("write file payload: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/media", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d: %s", w.Code, w.Body.String())
	}

	entries, err := os.ReadDir(filepath.Join(tempDir, "media"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read media dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected oversized upload file to be removed, got %d entries", len(entries))
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
