package postflow

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/antoniolg/postflow/internal/domain"
)

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
			if raw := strings.TrimSpace(r.URL.RawQuery); raw != "" {
				t.Fatalf("expected append request without query params, got %q", raw)
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

func TestUploadAppendOAuth2SendsSegmentIndexInMultipartBody(t *testing.T) {
	var gotSegmentIndex string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/2/media/upload/mid_v2/append" {
			http.NotFound(w, r)
			return
		}
		if raw := strings.TrimSpace(r.URL.RawQuery); raw != "" {
			t.Fatalf("expected append request without query params, got %q", raw)
		}
		reader, err := r.MultipartReader()
		if err != nil {
			t.Fatalf("multipart reader: %v", err)
		}
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("next part: %v", err)
			}
			body, _ := io.ReadAll(part)
			if part.FormName() == "segment_index" {
				gotSegmentIndex = strings.TrimSpace(string(body))
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := mustNewXBearerTestClient(t, srv.URL)
	media := mustWriteTempMedia(t, "clip.mp4", "video/mp4", []byte("video-bytes-for-test"))

	if err := client.uploadAppendOAuth2(context.Background(), "mid_v2", media); err != nil {
		t.Fatalf("uploadAppendOAuth2() error = %v", err)
	}
	if gotSegmentIndex != "0" {
		t.Fatalf("segment_index = %q, want %q", gotSegmentIndex, "0")
	}
}

func TestUploadAppendOAuth2SplitsFileIntoChunks(t *testing.T) {
	type appendCall struct {
		segment string
		size    int
	}
	var calls []appendCall
	var received []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/2/media/upload/mid_v2/append" {
			http.NotFound(w, r)
			return
		}
		reader, err := r.MultipartReader()
		if err != nil {
			t.Errorf("multipart reader: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var call appendCall
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Errorf("next part: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			body, _ := io.ReadAll(part)
			switch part.FormName() {
			case "segment_index":
				call.segment = strings.TrimSpace(string(body))
			case "media":
				call.size = len(body)
				received = append(received, body...)
			}
		}
		calls = append(calls, call)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	content := make([]byte, 12*1024*1024)
	for i := range content {
		content[i] = byte(i % 251)
	}
	client := mustNewXBearerTestClient(t, srv.URL)
	media := mustWriteTempMedia(t, "teaser.mp4", "video/mp4", content)

	if err := client.uploadAppendOAuth2(context.Background(), "mid_v2", media); err != nil {
		t.Fatalf("uploadAppendOAuth2() error = %v", err)
	}
	if len(calls) != 3 {
		t.Fatalf("append calls = %d, want 3 (%+v)", len(calls), calls)
	}
	for i, call := range calls {
		if want := strconv.Itoa(i); call.segment != want {
			t.Fatalf("call %d segment_index = %q, want %q", i, call.segment, want)
		}
		if call.size == 0 || call.size > uploadChunkSize {
			t.Fatalf("call %d chunk size = %d, want 1..%d", i, call.size, uploadChunkSize)
		}
	}
	if !bytes.Equal(received, content) {
		t.Fatalf("reassembled chunks differ from original file (got %d bytes, want %d)", len(received), len(content))
	}
}

func TestUploadAppendOAuth2ReportsFailingSegment(t *testing.T) {
	var count int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if count == 2 {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := mustNewXBearerTestClient(t, srv.URL)
	media := mustWriteTempMedia(t, "clip.mp4", "video/mp4", make([]byte, uploadChunkSize+10))

	err := client.uploadAppendOAuth2(context.Background(), "mid_v2", media)
	if err == nil || !strings.Contains(err.Error(), "segment=1") || !strings.Contains(err.Error(), "status=413") {
		t.Fatalf("uploadAppendOAuth2() error = %v, want segment=1 status=413", err)
	}
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
	file, err := os.CreateTemp(t.TempDir(), filename+"-*")
	if err != nil {
		t.Fatalf("create temp media: %v", err)
	}
	if _, err := file.Write(content); err != nil {
		t.Fatalf("write temp media: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close temp media: %v", err)
	}
	return domain.Media{
		ID:           "med_test",
		OriginalName: filename,
		MimeType:     mimeType,
		StoragePath:  file.Name(),
		SizeBytes:    int64(len(content)),
	}
}
