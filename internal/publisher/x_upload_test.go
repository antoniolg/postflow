package publisher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/antoniolg/publisher/internal/domain"
)

func TestUploadChunkedUsesGETForStatusCommand(t *testing.T) {
	var statusMethod string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/1.1/media/upload.json" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		command := r.URL.Query().Get("command")
		switch command {
		case "INIT":
			if r.Method != http.MethodPost {
				t.Fatalf("INIT method = %s, want POST", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"media_id_string":"mid_123"}`))
		case "APPEND":
			if r.Method != http.MethodPost {
				t.Fatalf("APPEND method = %s, want POST", r.Method)
			}
			w.WriteHeader(http.StatusNoContent)
		case "FINALIZE":
			if r.Method != http.MethodPost {
				t.Fatalf("FINALIZE method = %s, want POST", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"media_id_string":"mid_123","processing_info":{"state":"pending","check_after_secs":1}}`))
		case "STATUS":
			statusMethod = r.Method
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"media_id_string":"mid_123","processing_info":{"state":"succeeded"}}`))
		default:
			t.Fatalf("unexpected upload command %q", command)
		}
	}))
	defer srv.Close()

	client, err := NewXClient(XConfig{
		APIBaseURL:        srv.URL,
		UploadBaseURL:     srv.URL,
		APIKey:            "key",
		APIKeySecret:      "secret",
		AccessToken:       "token",
		AccessTokenSecret: "token_secret",
	})
	if err != nil {
		t.Fatalf("NewXClient() error = %v", err)
	}

	dir := t.TempDir()
	videoPath := filepath.Join(dir, "clip.mp4")
	content := []byte("video-bytes-for-test")
	if err := os.WriteFile(videoPath, content, 0o644); err != nil {
		t.Fatalf("write temp video: %v", err)
	}

	mediaID, err := client.uploadChunked(context.Background(), domain.Media{
		ID:          "med_test",
		StoragePath: videoPath,
		MimeType:    "video/mp4",
		SizeBytes:   int64(len(content)),
	})
	if err != nil {
		t.Fatalf("uploadChunked() error = %v", err)
	}
	if mediaID != "mid_123" {
		t.Fatalf("mediaID = %q, want %q", mediaID, "mid_123")
	}
	if statusMethod != http.MethodGet {
		t.Fatalf("STATUS method = %q, want %q", statusMethod, http.MethodGet)
	}
}
