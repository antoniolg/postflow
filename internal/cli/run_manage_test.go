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

func TestRunHealth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/healthz" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"--base-url", server.URL, "health"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "status: ok") {
		t.Fatalf("expected health output, got %s", stdout.String())
	}
}

func TestRunDraftsList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/drafts" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := strings.TrimSpace(r.URL.Query().Get("limit")); got != "10" {
			t.Fatalf("expected limit=10, got %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"count": 1,
			"drafts": []map[string]any{
				{"id": "pst_1", "platform": "x", "text": "hello draft"},
			},
		})
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"--base-url", server.URL, "drafts", "list", "--limit", "10"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "count: 1") {
		t.Fatalf("expected drafts output, got %s", stdout.String())
	}
}

func TestRunPostsCancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/posts/pst_1/cancel" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "pst_1", "status": "canceled"})
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"--base-url", server.URL, "posts", "cancel", "--id", "pst_1"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "canceled post: pst_1") {
		t.Fatalf("expected cancel output, got %s", stdout.String())
	}
}

func TestRunPostsSchedule(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/posts/pst_1/schedule" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if got := strings.TrimSpace(anyString(payload["scheduled_at"])); got != "2026-03-01T10:00:00Z" {
			t.Fatalf("expected scheduled_at payload, got %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "pst_1", "status": "scheduled"})
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"--base-url", server.URL,
		"posts", "schedule",
		"--id", "pst_1",
		"--scheduled-at", "2026-03-01T10:00:00Z",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "scheduled post: pst_1") {
		t.Fatalf("expected schedule output, got %s", stdout.String())
	}
}

func TestRunPostsEdit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/posts/pst_1/edit" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if got := strings.TrimSpace(anyString(payload["text"])); got != "updated text" {
			t.Fatalf("expected text payload, got %q", got)
		}
		if got := strings.TrimSpace(anyString(payload["intent"])); got != "schedule" {
			t.Fatalf("expected intent payload, got %q", got)
		}
		if got := strings.TrimSpace(anyString(payload["scheduled_at"])); got != "2026-03-01T10:15:00Z" {
			t.Fatalf("expected scheduled_at payload, got %q", got)
		}
		rawPostIDs, ok := payload["post_ids"].([]any)
		if !ok {
			t.Fatalf("expected post_ids array in payload, got %#v", payload["post_ids"])
		}
		if len(rawPostIDs) != 1 || strings.TrimSpace(anyString(rawPostIDs[0])) != "pst_2" {
			t.Fatalf("expected post_ids [pst_2], got %#v", rawPostIDs)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "pst_1", "status": "scheduled"})
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"--base-url", server.URL,
		"posts", "edit",
		"--id", "pst_1",
		"--text", "updated text",
		"--intent", "schedule",
		"--scheduled-at", "2026-03-01T10:15:00Z",
		"--post-id", "pst_2",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "edited post: pst_1") {
		t.Fatalf("expected edit output, got %s", stdout.String())
	}
}

func TestRunPostsEditWithMediaReplacement(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/posts/pst_1/edit" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		rawMedia, ok := payload["media_ids"].([]any)
		if !ok {
			t.Fatalf("expected media_ids array in payload, got %#v", payload["media_ids"])
		}
		if len(rawMedia) != 2 || strings.TrimSpace(anyString(rawMedia[0])) != "med_1" || strings.TrimSpace(anyString(rawMedia[1])) != "med_2" {
			t.Fatalf("expected media_ids [med_1 med_2], got %#v", rawMedia)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "pst_1", "status": "draft"})
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"--base-url", server.URL,
		"posts", "edit",
		"--id", "pst_1",
		"--text", "updated text",
		"--replace-media",
		"--media-id", "med_1",
		"--media-id", "med_2",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d, stderr=%s", code, stderr.String())
	}
}

func TestRunPostsEditWithReplaceMediaWithoutIDsClearsMedia(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/posts/pst_1/edit" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		rawMedia, ok := payload["media_ids"].([]any)
		if !ok {
			t.Fatalf("expected media_ids array in payload, got %#v", payload["media_ids"])
		}
		if len(rawMedia) != 0 {
			t.Fatalf("expected empty media_ids to clear media, got %#v", rawMedia)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "pst_1", "status": "draft"})
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"--base-url", server.URL,
		"posts", "edit",
		"--id", "pst_1",
		"--text", "updated text",
		"--replace-media",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d, stderr=%s", code, stderr.String())
	}
}

func TestRunPostsDelete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/posts/pst_1/delete" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "pst_1", "deleted": true})
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"--base-url", server.URL,
		"posts", "delete",
		"--id", "pst_1",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "deleted post: pst_1") {
		t.Fatalf("expected delete output, got %s", stdout.String())
	}
}

func TestRunAccountsCreateStaticRejectsX(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"--base-url", "http://example.invalid",
		"accounts", "create-static",
		"--platform", "x",
		"--external-account-id", "x-test",
		"--credential", "access_token=tok_1",
	}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "static x accounts are not supported; connect via oauth") {
		t.Fatalf("expected x static rejection, got stderr=%s", stderr.String())
	}
}

func TestRunSettingsSetTimezone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/settings/timezone" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if got := strings.TrimSpace(anyString(payload["timezone"])); got != "Europe/Madrid" {
			t.Fatalf("expected timezone Europe/Madrid, got %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"timezone": "Europe/Madrid"})
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"--base-url", server.URL, "settings", "set-timezone", "--timezone", "Europe/Madrid"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "timezone updated: Europe/Madrid") {
		t.Fatalf("expected timezone output, got %s", stdout.String())
	}
}

func anyString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	default:
		return ""
	}
}
