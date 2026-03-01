package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
			"from":  "2026-03-01T00:00:00Z",
			"to":    "2026-03-31T23:59:59Z",
			"items": []map[string]any{},
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
	if !strings.Contains(stdout.String(), `"items": []`) {
		t.Fatalf("expected json output, got %s", stdout.String())
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
