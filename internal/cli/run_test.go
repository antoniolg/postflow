package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunScheduleListJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/schedule" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"from": "2026-03-01T00:00:00Z",
			"to":   "2026-03-31T23:59:59Z",
			"items": []map[string]any{{
				"id":              "pst_thread_root",
				"text":            "thread root",
				"thread_group_id": "thg_123",
				"thread_position": 1,
				"root_post_id":    "pst_thread_root",
			}},
		})
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"--base-url", server.URL,
		"--json",
		"schedule", "list",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"thread_group_id": "thg_123"`) || !strings.Contains(stdout.String(), `"thread_position": 1`) {
		t.Fatalf("expected json output, got %s", stdout.String())
	}
}

func TestRunPostsEditSupportsSegmentsJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/posts/pst_thread_root/edit" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if _, ok := payload["text"]; ok {
			t.Fatalf("did not expect legacy text payload when using segments_json")
		}
		rawSegments, ok := payload["segments"].([]any)
		if !ok || len(rawSegments) != 2 {
			t.Fatalf("expected 2 segments, got %#v", payload["segments"])
		}
		first, _ := rawSegments[0].(map[string]any)
		second, _ := rawSegments[1].(map[string]any)
		if first["text"] != "root updated" || second["text"] != "reply updated" {
			t.Fatalf("unexpected segments payload %#v", rawSegments)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "pst_thread_root",
			"status": "draft",
			"post": map[string]any{
				"id":              "pst_thread_root",
				"text":            "root updated",
				"thread_group_id": "thg_123",
				"thread_position": 1,
			},
		})
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"--base-url", server.URL,
		"posts", "edit",
		"--id", "pst_thread_root",
		"--segments-json", `[{"text":"root updated"},{"text":"reply updated"}]`,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected edit exit 0, got %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "edited post: pst_thread_root") {
		t.Fatalf("expected edit output, got %s", stdout.String())
	}
}

func TestRunPostsCreateIncludesAuthAndIdempotencyHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/posts" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := strings.TrimSpace(r.Header.Get("Authorization")); got != "Bearer tok_123" {
			t.Fatalf("expected bearer auth header, got %q", got)
		}
		if got := strings.TrimSpace(r.Header.Get("Idempotency-Key")); got != "idem_001" {
			t.Fatalf("expected idempotency header, got %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload["text"] != "hello world" {
			t.Fatalf("unexpected text payload %v", payload["text"])
		}
		if payload["account_id"] != "acc_1" {
			t.Fatalf("unexpected account_id payload %v", payload["account_id"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "pst_1"})
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"--base-url", server.URL,
		"--api-token", "tok_123",
		"posts", "create",
		"--account-id", "acc_1",
		"--text", "hello world",
		"--idempotency-key", "idem_001",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "post created/updated: pst_1") {
		t.Fatalf("expected create output, got %s", stdout.String())
	}
}

func TestRunVersion(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"--version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if strings.TrimSpace(stdout.String()) != Version {
		t.Fatalf("expected version output %q, got %q", Version, stdout.String())
	}
}

func TestRunMediaListAndDelete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/media":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"count": 1,
				"items": []map[string]any{{"id": "med_1", "kind": "image", "size_bytes": 12, "in_use": false}},
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/media/med_1":
			_ = json.NewEncoder(w).Encode(map[string]any{"deleted": true})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"--base-url", server.URL,
		"media", "list",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected media list exit 0, got %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "count: 1") {
		t.Fatalf("expected media list output, got %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), []string{
		"--base-url", server.URL,
		"media", "delete", "--id", "med_1",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected media delete exit 0, got %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "deleted media: med_1") {
		t.Fatalf("expected media delete output, got %s", stdout.String())
	}
}

func TestRunDLQDelete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/dlq/dlq_1/delete" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"dead_letter_id": "dlq_1", "deleted": true})
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"--base-url", server.URL,
		"dlq", "delete", "--id", "dlq_1",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected dlq delete exit 0, got %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "deleted: dlq_1") {
		t.Fatalf("expected dlq delete output, got %s", stdout.String())
	}
}

func TestRunInvalidCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"unknown"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
}

func TestRunPostsValidateSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/posts/validate" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload["account_id"] != "acc_val_1" {
			t.Fatalf("unexpected account_id payload %v", payload["account_id"])
		}
		if payload["text"] != "validate me" {
			t.Fatalf("unexpected text payload %v", payload["text"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"valid":    true,
			"warnings": []string{},
		})
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"--base-url", server.URL,
		"posts", "validate",
		"--account-id", "acc_val_1",
		"--text", "validate me",
		"--scheduled-at", "2026-03-01T10:00:00Z",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected validate exit 0, got %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "valid: true") {
		t.Fatalf("expected validate success output, got %s", stdout.String())
	}
}

func TestRunPostsValidateFailureShowsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/posts/validate" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "account not found"})
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"--base-url", server.URL,
		"posts", "validate",
		"--account-id", "acc_missing",
		"--text", "validate me",
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected validate failure exit 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "POST /posts/validate: status 400: account not found") {
		t.Fatalf("expected validate failure message, got %s", stderr.String())
	}
}

func TestRunMediaUploadMissingFile(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"--base-url", "http://127.0.0.1:1",
		"media", "upload",
		"--file", "/tmp/this-file-should-not-exist.postflow",
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected upload missing-file exit 1, got %d", code)
	}
	if !strings.Contains(strings.ToLower(stderr.String()), "open file:") {
		t.Fatalf("expected missing-file error message, got %s", stderr.String())
	}
}

func TestRunMediaUploadHTTPErrorJSON(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(filePath, []byte("fake-bytes"), 0o644); err != nil {
		t.Fatalf("write media file: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/media" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "file too large"})
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"--base-url", server.URL,
		"media", "upload",
		"--file", filePath,
		"--kind", "video",
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected upload json-error exit 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "POST /media: status 413: file too large") {
		t.Fatalf("expected upload json-error message, got %s", stderr.String())
	}
}

func TestRunMediaUploadSendsDetectedFileMimeType(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "cover.jpg")
	payload := []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43, 0x00}
	if err := os.WriteFile(filePath, payload, 0o644); err != nil {
		t.Fatalf("write media file: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/media" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		mr, err := r.MultipartReader()
		if err != nil {
			t.Fatalf("multipart reader: %v", err)
		}
		seenFile := false
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("next part: %v", err)
			}
			if part.FormName() != "file" {
				_, _ = io.Copy(io.Discard, part)
				_ = part.Close()
				continue
			}
			seenFile = true
			if got := strings.TrimSpace(part.Header.Get("Content-Type")); got != "image/jpeg" {
				t.Fatalf("expected file part content-type image/jpeg, got %q", got)
			}
			_, _ = io.Copy(io.Discard, part)
			_ = part.Close()
		}
		if !seenFile {
			t.Fatalf("expected file part in multipart body")
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "med_1", "mime_type": "image/jpeg"})
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"--base-url", server.URL,
		"media", "upload",
		"--file", filePath,
		"--kind", "image",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected upload exit 0, got %d, stderr=%s", code, stderr.String())
	}
}

func TestRunMediaUploadHTTPErrorFallbacks(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(filePath, []byte("fake-bytes"), 0o644); err != nil {
		t.Fatalf("write media file: %v", err)
	}

	tests := []struct {
		name         string
		status       int
		body         string
		expectedText string
	}{
		{
			name:         "plain text body",
			status:       http.StatusBadGateway,
			body:         "upstream media service failed",
			expectedText: "POST /media: status 502: upstream media service failed",
		},
		{
			name:         "empty body",
			status:       http.StatusServiceUnavailable,
			body:         "",
			expectedText: "POST /media: status 503: Service Unavailable",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/media" {
					t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(tc.status)
				if tc.body != "" {
					_, _ = w.Write([]byte(tc.body))
				}
			}))
			defer server.Close()

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := Run(context.Background(), []string{
				"--base-url", server.URL,
				"media", "upload",
				"--file", filePath,
				"--kind", "video",
			}, &stdout, &stderr)
			if code != 1 {
				t.Fatalf("expected upload fallback exit 1, got %d", code)
			}
			if !strings.Contains(stderr.String(), tc.expectedText) {
				t.Fatalf("expected upload fallback message %q, got %s", tc.expectedText, stderr.String())
			}
		})
	}
}

func TestRunMediaUploadInvalidJSONResponse(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(filePath, []byte("fake-bytes"), 0o644); err != nil {
		t.Fatalf("write media file: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/media" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("{not-json"))
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"--base-url", server.URL,
		"media", "upload",
		"--file", filePath,
		"--kind", "video",
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected upload invalid-json exit 1, got %d", code)
	}
	if !strings.Contains(strings.ToLower(stderr.String()), "decode response body") {
		t.Fatalf("expected invalid-json decode error, got %s", stderr.String())
	}
}
