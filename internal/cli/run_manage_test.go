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
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "edited post: pst_1") {
		t.Fatalf("expected edit output, got %s", stdout.String())
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

func TestRunAccountsCreateStaticWithXPremium(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/accounts/static":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode payload: %v", err)
			}
			credentials, ok := payload["credentials"].(map[string]any)
			if !ok || strings.TrimSpace(anyString(credentials["access_token"])) == "" {
				t.Fatalf("expected credentials.access_token in payload")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "acc_1", "platform": "x"})
		case r.Method == http.MethodPost && r.URL.Path == "/accounts/acc_1/x-premium":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode x-premium payload: %v", err)
			}
			enabled, _ := payload["x_premium"].(bool)
			if !enabled {
				t.Fatalf("expected x_premium=true")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "acc_1", "x_premium": true})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"--base-url", server.URL,
		"accounts", "create-static",
		"--platform", "x",
		"--external-account-id", "x-default",
		"--credential", "access_token=tok_1",
		"--credential", "access_token_secret=sec_1",
		"--x-premium", "true",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "account upserted: acc_1") {
		t.Fatalf("expected create-static output, got %s", stdout.String())
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
