package api

import (
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/db"
)

// A slow upload must outlive the server-wide timeouts, which stay short for every other route.
func TestUploadMediaOutlivesServerTimeouts(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	ts := httptest.NewUnstartedServer(srv.Handler())
	// Same shape as cmd/postflow-server, scaled down so the test stays fast.
	ts.Config.ReadTimeout = 100 * time.Millisecond
	ts.Config.WriteTimeout = 200 * time.Millisecond
	ts.Start()
	defer ts.Close()

	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)
	go func() {
		part, err := writer.CreateFormFile("file", "clip.mp4")
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		_, _ = part.Write([]byte("first half "))
		time.Sleep(400 * time.Millisecond) // longer than both server timeouts
		_, _ = part.Write([]byte("second half"))
		_ = writer.Close()
		_ = pw.Close()
	}()

	resp, err := http.Post(ts.URL+"/media", writer.FormDataContentType(), pr)
	if err != nil {
		t.Fatalf("upload was cut off by the server timeouts: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 201, got %d: %s", resp.StatusCode, body)
	}

	entries, err := os.ReadDir(filepath.Join(tempDir, "media"))
	if err != nil {
		t.Fatalf("read media dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected the uploaded file on disk, found %d entries", len(entries))
	}
	got, err := os.ReadFile(filepath.Join(tempDir, "media", entries[0].Name()))
	if err != nil {
		t.Fatalf("read uploaded file: %v", err)
	}
	if string(got) != "first half second half" {
		t.Fatalf("uploaded file is incomplete: %q", got)
	}
}
