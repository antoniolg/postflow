package postflow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antoniolg/postflow/internal/domain"
)

func TestUploadChunkedUsesGETForStatusCommand(t *testing.T) {
	var statusMethod string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/1.1/media/upload.json" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		switch r.URL.Query().Get("command") {
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
			t.Fatalf("unexpected upload command %q", r.URL.Query().Get("command"))
		}
	}))
	defer srv.Close()

	client := mustNewXTestClient(t, srv.URL)
	media := mustWriteTempMedia(t, "clip.mp4", "video/mp4", []byte("video-bytes-for-test"))

	mediaID, err := client.uploadChunked(context.Background(), media)
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

func TestUploadChunkedReturnsErrorWhenInitMissingMediaID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/1.1/media/upload.json" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.URL.Query().Get("command") != "INIT" {
			t.Fatalf("expected only INIT request, got %q", r.URL.Query().Get("command"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"media_id_string":""}`))
	}))
	defer srv.Close()

	client := mustNewXTestClient(t, srv.URL)
	media := mustWriteTempMedia(t, "clip.mp4", "video/mp4", []byte("video-bytes-for-test"))

	_, err := client.uploadChunked(context.Background(), media)
	if err == nil {
		t.Fatalf("expected init without media id to fail")
	}
	if !strings.Contains(err.Error(), "empty media_id_string") {
		t.Fatalf("expected empty media id error, got %v", err)
	}
}

func TestUploadChunkedReturnsErrorOnAppendFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/1.1/media/upload.json" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		switch r.URL.Query().Get("command") {
		case "INIT":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"media_id_string":"mid_123"}`))
		case "APPEND":
			http.Error(w, `{"error":"bad chunk"}`, http.StatusBadRequest)
		default:
			t.Fatalf("unexpected upload command %q", r.URL.Query().Get("command"))
		}
	}))
	defer srv.Close()

	client := mustNewXTestClient(t, srv.URL)
	media := mustWriteTempMedia(t, "clip.mp4", "video/mp4", []byte("video-bytes-for-test"))

	_, err := client.uploadChunked(context.Background(), media)
	if err == nil {
		t.Fatalf("expected append error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "x upload APPEND failed") || !strings.Contains(msg, "bad chunk") {
		t.Fatalf("expected append failure details, got %v", err)
	}
}

func TestUploadChunkedReturnsErrorOnProcessingFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/1.1/media/upload.json" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		switch r.URL.Query().Get("command") {
		case "INIT":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"media_id_string":"mid_123"}`))
		case "APPEND":
			w.WriteHeader(http.StatusNoContent)
		case "FINALIZE":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"media_id_string":"mid_123","processing_info":{"state":"pending","check_after_secs":1}}`))
		case "STATUS":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"media_id_string":"mid_123","processing_info":{"state":"failed","error":{"name":"InvalidMedia","message":"unsupported codec"}}}`))
		default:
			t.Fatalf("unexpected upload command %q", r.URL.Query().Get("command"))
		}
	}))
	defer srv.Close()

	client := mustNewXTestClient(t, srv.URL)
	media := mustWriteTempMedia(t, "clip.mp4", "video/mp4", []byte("video-bytes-for-test"))

	_, err := client.uploadChunked(context.Background(), media)
	if err == nil {
		t.Fatalf("expected processing failure")
	}
	if !strings.Contains(err.Error(), "x processing failed: unsupported codec (InvalidMedia)") {
		t.Fatalf("expected processing failure details, got %v", err)
	}
}

func TestUploadChunkedReturnsErrorOnStatusHTTPFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/1.1/media/upload.json" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		switch r.URL.Query().Get("command") {
		case "INIT":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"media_id_string":"mid_123"}`))
		case "APPEND":
			w.WriteHeader(http.StatusNoContent)
		case "FINALIZE":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"media_id_string":"mid_123","processing_info":{"state":"pending","check_after_secs":1}}`))
		case "STATUS":
			http.Error(w, `{"errors":[{"message":"upstream outage"}]}`, http.StatusBadGateway)
		default:
			t.Fatalf("unexpected upload command %q", r.URL.Query().Get("command"))
		}
	}))
	defer srv.Close()

	client := mustNewXTestClient(t, srv.URL)
	media := mustWriteTempMedia(t, "clip.mp4", "video/mp4", []byte("video-bytes-for-test"))

	_, err := client.uploadChunked(context.Background(), media)
	if err == nil {
		t.Fatalf("expected status http failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, "x upload command STATUS failed") || !strings.Contains(msg, "status=502") {
		t.Fatalf("expected status command failure details, got %v", err)
	}
}

func TestUploadChunkedReturnsErrorOnMalformedFinalizeResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/1.1/media/upload.json" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		switch r.URL.Query().Get("command") {
		case "INIT":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"media_id_string":"mid_123"}`))
		case "APPEND":
			w.WriteHeader(http.StatusNoContent)
		case "FINALIZE":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"media_id_string":`))
		default:
			t.Fatalf("unexpected upload command %q", r.URL.Query().Get("command"))
		}
	}))
	defer srv.Close()

	client := mustNewXTestClient(t, srv.URL)
	media := mustWriteTempMedia(t, "clip.mp4", "video/mp4", []byte("video-bytes-for-test"))

	_, err := client.uploadChunked(context.Background(), media)
	if err == nil {
		t.Fatalf("expected malformed finalize json error")
	}
	if !strings.Contains(err.Error(), "invalid character") && !strings.Contains(err.Error(), "unexpected end of JSON input") {
		t.Fatalf("expected json decode error, got %v", err)
	}
}

func mustNewXTestClient(t *testing.T, baseURL string) *XClient {
	t.Helper()
	client, err := NewXClient(XConfig{
		APIBaseURL:        baseURL,
		UploadBaseURL:     baseURL,
		APIKey:            "key",
		APIKeySecret:      "secret",
		AccessToken:       "token",
		AccessTokenSecret: "token_secret",
	})
	if err != nil {
		t.Fatalf("NewXClient() error = %v", err)
	}
	return client
}

func mustNewXBearerTestClient(t *testing.T, baseURL string) *XClient {
	t.Helper()
	client, err := NewXClient(XConfig{
		APIBaseURL:  baseURL,
		AccessToken: "bearer-token",
	})
	if err != nil {
		t.Fatalf("NewXClient() error = %v", err)
	}
	return client
}

func mustWriteTempMedia(t *testing.T, filename, mimeType string, content []byte) domain.Media {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write temp media: %v", err)
	}
	return domain.Media{
		ID:          "med_test_" + strings.ReplaceAll(filename, ".", "_"),
		StoragePath: path,
		MimeType:    mimeType,
		SizeBytes:   int64(len(content)),
	}
}

func TestUploadChunkedCompletesLifecycleWithStatusPolling(t *testing.T) {
	commands := make([]string, 0, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/1.1/media/upload.json" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		command := r.URL.Query().Get("command")
		commands = append(commands, fmt.Sprintf("%s:%s", command, r.Method))
		switch command {
		case "INIT":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"media_id_string":"mid_789"}`))
		case "APPEND":
			w.WriteHeader(http.StatusNoContent)
		case "FINALIZE":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"media_id_string":"mid_789","processing_info":{"state":"pending","check_after_secs":1}}`))
		case "STATUS":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"media_id_string":"mid_789","processing_info":{"state":"succeeded"}}`))
		default:
			t.Fatalf("unexpected upload command %q", command)
		}
	}))
	defer srv.Close()

	client := mustNewXTestClient(t, srv.URL)
	media := mustWriteTempMedia(t, "clip.mp4", "video/mp4", []byte("video-bytes-for-test"))

	mediaID, err := client.uploadChunked(context.Background(), media)
	if err != nil {
		t.Fatalf("uploadChunked() error = %v", err)
	}
	if mediaID != "mid_789" {
		t.Fatalf("mediaID = %q, want %q", mediaID, "mid_789")
	}
	if len(commands) < 4 {
		t.Fatalf("expected lifecycle commands INIT/APPEND/FINALIZE/STATUS, got %v", commands)
	}
	if commands[0] != "INIT:POST" {
		t.Fatalf("first command = %q, want INIT:POST", commands[0])
	}
	if commands[1] != "APPEND:POST" {
		t.Fatalf("second command = %q, want APPEND:POST", commands[1])
	}
	if commands[2] != "FINALIZE:POST" {
		t.Fatalf("third command = %q, want FINALIZE:POST", commands[2])
	}
	if commands[3] != "STATUS:GET" {
		t.Fatalf("fourth command = %q, want STATUS:GET", commands[3])
	}
}

func TestUploadChunkedOAuth2UsesV2Endpoints(t *testing.T) {
	commands := make([]string, 0, 4)
	var initPayload map[string]any
	var appendAuth string
	var statusQuery string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/2/media/upload/initialize":
			commands = append(commands, "INIT:"+r.Method)
			if got := strings.TrimSpace(r.Header.Get("Authorization")); got != "Bearer bearer-token" {
				t.Fatalf("initialize auth = %q, want bearer auth", got)
			}
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &initPayload)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"id":"mid_v2"}}`))
		case r.URL.Path == "/2/media/upload/mid_v2/append":
			commands = append(commands, "APPEND:"+r.Method)
			appendAuth = strings.TrimSpace(r.Header.Get("Authorization"))
			if got := r.URL.Query().Get("segment_index"); got != "0" {
				t.Fatalf("segment_index = %q, want 0", got)
			}
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/2/media/upload/mid_v2/finalize":
			commands = append(commands, "FINALIZE:"+r.Method)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"id":"mid_v2","processing_info":{"state":"pending","check_after_secs":1}}}`))
		case r.URL.Path == "/2/media/upload":
			commands = append(commands, "STATUS:"+r.Method)
			statusQuery = r.URL.RawQuery
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"id":"mid_v2","processing_info":{"state":"succeeded"}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := mustNewXBearerTestClient(t, srv.URL)
	media := mustWriteTempMedia(t, "clip.mp4", "video/mp4", []byte("video-bytes-for-test"))

	mediaID, err := client.uploadChunked(context.Background(), media)
	if err != nil {
		t.Fatalf("uploadChunked() error = %v", err)
	}
	if mediaID != "mid_v2" {
		t.Fatalf("mediaID = %q, want %q", mediaID, "mid_v2")
	}
	if len(commands) != 4 {
		t.Fatalf("expected INIT/APPEND/FINALIZE/STATUS commands, got %v", commands)
	}
	if initPayload["media_category"] != "tweet_video" {
		t.Fatalf("media_category = %v, want tweet_video", initPayload["media_category"])
	}
	if appendAuth != "Bearer bearer-token" {
		t.Fatalf("append auth = %q, want bearer auth", appendAuth)
	}
	if !strings.Contains(statusQuery, "command=STATUS") || !strings.Contains(statusQuery, "media_id=mid_v2") {
		t.Fatalf("unexpected status query %q", statusQuery)
	}
}
