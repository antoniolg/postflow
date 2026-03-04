package parity_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRequiredCapabilitiesFailureParity(t *testing.T) {
	env := newParityEnv(t)

	t.Run("posts.create invalid account", func(t *testing.T) {
		raw, status := env.apiJSON(http.MethodPost, "/posts", map[string]any{
			"account_id": "acc_missing",
			"text":       "fail me",
		}, "application/json")
		if status != http.StatusBadRequest {
			t.Fatalf("api expected 400, got %d body=%s", status, string(raw))
		}
		assertContainsAny(t, "api", apiErrorMessage(raw), "account not found")

		code, _, stderr := env.runCLIResult("posts", "create", "--account-id", "acc_missing", "--text", "fail me")
		if code == 0 {
			t.Fatalf("cli expected non-zero exit for invalid account")
		}
		assertContainsAny(t, "cli", stderr, "account not found")

		msg := env.mcpCallToolError("postflow_create_post", map[string]any{"account_id": "acc_missing", "text": "fail me"})
		assertContainsAny(t, "mcp", msg, "account not found")
	})

	t.Run("posts.validate invalid account", func(t *testing.T) {
		raw, status := env.apiJSON(http.MethodPost, "/posts/validate", map[string]any{
			"account_id": "acc_missing",
			"text":       "fail me",
		}, "application/json")
		if status != http.StatusBadRequest {
			t.Fatalf("api expected 400, got %d body=%s", status, string(raw))
		}
		assertContainsAny(t, "api", apiErrorMessage(raw), "account not found")

		code, _, stderr := env.runCLIResult("posts", "validate", "--account-id", "acc_missing", "--text", "fail me")
		if code == 0 {
			t.Fatalf("cli expected non-zero exit for invalid account")
		}
		assertContainsAny(t, "cli", stderr, "account not found")

		msg := env.mcpCallToolError("postflow_validate_post", map[string]any{"account_id": "acc_missing", "text": "fail me"})
		assertContainsAny(t, "mcp", msg, "account not found")
	})

	t.Run("posts.schedule missing scheduled_at", func(t *testing.T) {
		postID := env.apiCreatePost("schedule missing date", time.Time{}, nil)

		raw, status := env.apiJSON(http.MethodPost, "/posts/"+postID+"/schedule", map[string]any{}, "application/json")
		if status != http.StatusBadRequest {
			t.Fatalf("api expected 400, got %d body=%s", status, string(raw))
		}
		assertContainsAny(t, "api", apiErrorMessage(raw), "scheduled_at is required")

		code, _, stderr := env.runCLIResult("posts", "schedule", "--id", postID)
		if code == 0 {
			t.Fatalf("cli expected non-zero exit for missing scheduled-at")
		}
		assertContainsAny(t, "cli", stderr, "--scheduled-at is required")

		msg := env.mcpCallToolError("postflow_schedule_post", map[string]any{"post_id": postID})
		assertContainsAny(t, "mcp", msg, "scheduled_at is required", "scheduled_at")
	})

	t.Run("posts.delete published not deletable", func(t *testing.T) {
		apiID := env.apiCreatePost("published delete api", time.Now().UTC().Add(15*time.Minute), nil)
		cliID := env.apiCreatePost("published delete cli", time.Now().UTC().Add(16*time.Minute), nil)
		mcpID := env.apiCreatePost("published delete mcp", time.Now().UTC().Add(17*time.Minute), nil)

		if err := env.store.MarkPublished(t.Context(), apiID, "x-api"); err != nil {
			t.Fatalf("mark published api: %v", err)
		}
		if err := env.store.MarkPublished(t.Context(), cliID, "x-cli"); err != nil {
			t.Fatalf("mark published cli: %v", err)
		}
		if err := env.store.MarkPublished(t.Context(), mcpID, "x-mcp"); err != nil {
			t.Fatalf("mark published mcp: %v", err)
		}

		raw, status := env.apiJSON(http.MethodPost, "/posts/"+apiID+"/delete", map[string]any{}, "application/json")
		if status != http.StatusConflict {
			t.Fatalf("api expected 409, got %d body=%s", status, string(raw))
		}
		assertContainsAny(t, "api", apiErrorMessage(raw), "post not deletable")

		code, _, stderr := env.runCLIResult("posts", "delete", "--id", cliID)
		if code == 0 {
			t.Fatalf("cli expected non-zero exit for published delete")
		}
		assertContainsAny(t, "cli", stderr, "post not deletable")

		msg := env.mcpCallToolError("postflow_delete_post", map[string]any{"post_id": mcpID})
		assertContainsAny(t, "mcp", msg, "post not deletable")
	})

	t.Run("schedule.list invalid from", func(t *testing.T) {
		raw, status := env.apiJSON(http.MethodGet, "/schedule?from=invalid-time&to="+time.Now().UTC().Format(time.RFC3339), nil, "")
		if status != http.StatusBadRequest {
			t.Fatalf("api expected 400, got %d body=%s", status, string(raw))
		}
		assertContainsAny(t, "api", apiErrorMessage(raw), "from must be RFC3339", "RFC3339", "invalid from", "cannot parse")

		code, _, stderr := env.runCLIResult("schedule", "list", "--from", "invalid-time", "--to", time.Now().UTC().Format(time.RFC3339))
		if code == 0 {
			t.Fatalf("cli expected non-zero exit for invalid from")
		}
		assertContainsAny(t, "cli", stderr, "RFC3339", "from must", "invalid from", "cannot parse")

		msg := env.mcpCallToolError("postflow_list_schedule", map[string]any{"from": "invalid-time", "to": time.Now().UTC().Format(time.RFC3339)})
		assertContainsAny(t, "mcp", msg, "RFC3339", "from must", "invalid from", "cannot parse")
	})

	t.Run("dlq.requeue unknown id", func(t *testing.T) {
		raw, status := env.apiJSON(http.MethodPost, "/dlq/dlq_missing/requeue", nil, "application/json")
		if status != http.StatusNotFound {
			t.Fatalf("api expected 404, got %d body=%s", status, string(raw))
		}
		assertContainsAny(t, "api", apiErrorMessage(raw), "not found")

		code, _, stderr := env.runCLIResult("dlq", "requeue", "--id", "dlq_missing")
		if code == 0 {
			t.Fatalf("cli expected non-zero exit for missing DLQ id")
		}
		assertContainsAny(t, "cli", stderr, "not found", "no rows")

		msg := env.mcpCallToolError("postflow_requeue_failed", map[string]any{"dead_letter_id": "dlq_missing"})
		assertContainsAny(t, "mcp", msg, "not found", "no rows")
	})

	t.Run("media.delete unknown id", func(t *testing.T) {
		raw, status := env.apiJSON(http.MethodDelete, "/media/med_missing", nil, "")
		if status != http.StatusNotFound {
			t.Fatalf("api expected 404, got %d body=%s", status, string(raw))
		}
		assertContainsAny(t, "api", apiErrorMessage(raw), "media not found")

		code, _, stderr := env.runCLIResult("media", "delete", "--id", "med_missing")
		if code == 0 {
			t.Fatalf("cli expected non-zero exit for missing media id")
		}
		assertContainsAny(t, "cli", stderr, "media not found")

		msg := env.mcpCallToolError("postflow_delete_media", map[string]any{"media_id": "med_missing"})
		assertContainsAny(t, "mcp", msg, "media not found")
	})
}

func apiErrorMessage(raw []byte) string {
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &payload); err == nil && strings.TrimSpace(payload.Error) != "" {
		return strings.TrimSpace(payload.Error)
	}
	return strings.TrimSpace(string(raw))
}

func assertContainsAny(t *testing.T, surface string, got string, expected ...string) {
	t.Helper()
	value := strings.ToLower(strings.TrimSpace(got))
	if value == "" {
		t.Fatalf("%s expected error message, got empty", surface)
	}
	for _, candidate := range expected {
		if strings.Contains(value, strings.ToLower(strings.TrimSpace(candidate))) {
			return
		}
	}
	t.Fatalf("%s error %q does not contain any of %v", surface, got, expected)
}
