package parity_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/capabilities"
	"github.com/antoniolg/postflow/internal/domain"
)

type matrixSurfaceResult struct {
	Supported bool   `json:"supported"`
	Error     string `json:"error,omitempty"`
}

type matrixCapability struct {
	ID       string                                       `json:"id"`
	Surfaces map[capabilities.Surface]matrixSurfaceResult `json:"surfaces"`
}

type capabilityMatrixArtifact struct {
	GeneratedAt  string             `json:"generated_at"`
	Capabilities []matrixCapability `json:"capabilities"`
}

type parityCheck func(*parityEnv) error

func TestCapabilityMatrixArtifact(t *testing.T) {
	env := newParityEnv(t)
	checks := capabilityChecks()

	artifact := capabilityMatrixArtifact{
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
		Capabilities: make([]matrixCapability, 0, len(capabilities.RequiredParityCapabilities())),
	}

	index := make(map[string]matrixCapability, len(checks))
	for _, required := range capabilities.RequiredParityCapabilities() {
		entry := matrixCapability{
			ID:       required.ID,
			Surfaces: map[capabilities.Surface]matrixSurfaceResult{},
		}
		surfaceChecks, ok := checks[required.ID]
		if !ok {
			t.Fatalf("missing parity checks for capability %q", required.ID)
		}
		for _, surface := range []capabilities.Surface{capabilities.SurfaceAPI, capabilities.SurfaceCLI, capabilities.SurfaceMCP} {
			check, found := surfaceChecks[surface]
			if !found {
				entry.Surfaces[surface] = matrixSurfaceResult{Supported: false, Error: "missing check"}
				continue
			}
			err := check(env)
			entry.Surfaces[surface] = matrixSurfaceResult{Supported: err == nil}
			if err != nil {
				entry.Surfaces[surface] = matrixSurfaceResult{Supported: false, Error: strings.TrimSpace(err.Error())}
			}
		}
		artifact.Capabilities = append(artifact.Capabilities, entry)
		index[entry.ID] = entry
	}

	raw, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatalf("marshal artifact: %v", err)
	}
	artifactPath := filepath.Join(t.TempDir(), "capability-matrix.json")
	if err := os.WriteFile(artifactPath, raw, 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	t.Logf("capability matrix artifact: %s", artifactPath)

	for _, required := range capabilities.RequiredParityCapabilities() {
		entry, ok := index[required.ID]
		if !ok {
			t.Fatalf("missing matrix entry for capability %q", required.ID)
		}
		for _, surface := range required.RequiredSurfaces {
			result, ok := entry.Surfaces[surface]
			if !ok {
				t.Fatalf("missing matrix surface %q for capability %q", surface, required.ID)
			}
			if !result.Supported {
				t.Fatalf("capability %q is not supported on %q: %s", required.ID, surface, result.Error)
			}
		}
	}
}

func capabilityChecks() map[string]map[capabilities.Surface]parityCheck {
	return map[string]map[capabilities.Surface]parityCheck{
		capabilities.CapabilityHealthCheck: {
			capabilities.SurfaceAPI: func(env *parityEnv) error {
				if got := env.apiHealthStatus(); got != "ok" {
					return fmt.Errorf("expected status ok, got %q", got)
				}
				return nil
			},
			capabilities.SurfaceCLI: func(env *parityEnv) error {
				if got := env.cliHealthStatus(); got != "ok" {
					return fmt.Errorf("expected status ok, got %q", got)
				}
				return nil
			},
			capabilities.SurfaceMCP: func(env *parityEnv) error {
				if got := env.mcpHealthStatus(); got != "ok" {
					return fmt.Errorf("expected status ok, got %q", got)
				}
				return nil
			},
		},
		capabilities.CapabilityScheduleList: {
			capabilities.SurfaceAPI: func(env *parityEnv) error {
				from := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
				to := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
				_, status := env.apiJSON(http.MethodGet, "/schedule?from="+from+"&to="+to, nil, "")
				if status != http.StatusOK {
					return fmt.Errorf("expected status 200, got %d", status)
				}
				return nil
			},
			capabilities.SurfaceCLI: func(env *parityEnv) error {
				from := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
				to := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
				code, _, stderr := env.runCLIResult("schedule", "list", "--from", from, "--to", to)
				if code != 0 {
					return fmt.Errorf("exit %d: %s", code, strings.TrimSpace(stderr))
				}
				return nil
			},
			capabilities.SurfaceMCP: func(env *parityEnv) error {
				from := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
				to := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
				if msg := env.mcpCallToolError("postflow_list_schedule", map[string]any{"from": from, "to": to, "limit": 10}); msg != "" {
					return errors.New(msg)
				}
				return nil
			},
		},
		capabilities.CapabilityDraftsList: {
			capabilities.SurfaceAPI: func(env *parityEnv) error {
				postID := env.apiCreatePost("matrix draft api", time.Time{}, nil)
				ids := env.apiDraftListIDs(200)
				if !containsID(ids, postID) {
					return fmt.Errorf("expected draft id %s in api response", postID)
				}
				return nil
			},
			capabilities.SurfaceCLI: func(env *parityEnv) error {
				postID := env.apiCreatePost("matrix draft cli", time.Time{}, nil)
				ids := env.cliDraftListIDs(200)
				if !containsID(ids, postID) {
					return fmt.Errorf("expected draft id %s in cli response", postID)
				}
				return nil
			},
			capabilities.SurfaceMCP: func(env *parityEnv) error {
				postID := env.apiCreatePost("matrix draft mcp", time.Time{}, nil)
				ids := env.mcpDraftListIDs(200)
				if !containsID(ids, postID) {
					return fmt.Errorf("expected draft id %s in mcp response", postID)
				}
				return nil
			},
		},
		capabilities.CapabilityPostsCreate: {
			capabilities.SurfaceAPI: func(env *parityEnv) error {
				_, status := env.apiJSON(http.MethodPost, "/posts", map[string]any{"account_id": env.account.ID, "text": "matrix create api"}, "application/json")
				if status != http.StatusCreated && status != http.StatusOK {
					return fmt.Errorf("expected 201/200, got %d", status)
				}
				return nil
			},
			capabilities.SurfaceCLI: func(env *parityEnv) error {
				code, _, stderr := env.runCLIResult("posts", "create", "--account-id", env.account.ID, "--text", "matrix create cli")
				if code != 0 {
					return fmt.Errorf("exit %d: %s", code, strings.TrimSpace(stderr))
				}
				return nil
			},
			capabilities.SurfaceMCP: func(env *parityEnv) error {
				if msg := env.mcpCallToolError("postflow_create_post", map[string]any{"account_id": env.account.ID, "text": "matrix create mcp"}); msg != "" {
					return errors.New(msg)
				}
				return nil
			},
		},
		capabilities.CapabilityPostsSchedule: {
			capabilities.SurfaceAPI: func(env *parityEnv) error {
				postID := env.apiCreatePost("matrix schedule api", time.Time{}, nil)
				env.apiSchedulePost(postID, time.Now().UTC().Add(45*time.Minute))
				post, err := env.store.GetPost(env.t.Context(), postID)
				if err != nil {
					return err
				}
				if post.Status != domain.PostStatusScheduled {
					return fmt.Errorf("expected scheduled status, got %s", post.Status)
				}
				return nil
			},
			capabilities.SurfaceCLI: func(env *parityEnv) error {
				postID := env.apiCreatePost("matrix schedule cli", time.Time{}, nil)
				env.cliSchedulePost(postID, time.Now().UTC().Add(45*time.Minute))
				post, err := env.store.GetPost(env.t.Context(), postID)
				if err != nil {
					return err
				}
				if post.Status != domain.PostStatusScheduled {
					return fmt.Errorf("expected scheduled status, got %s", post.Status)
				}
				return nil
			},
			capabilities.SurfaceMCP: func(env *parityEnv) error {
				postID := env.apiCreatePost("matrix schedule mcp", time.Time{}, nil)
				env.mcpSchedulePost(postID, time.Now().UTC().Add(45*time.Minute))
				post, err := env.store.GetPost(env.t.Context(), postID)
				if err != nil {
					return err
				}
				if post.Status != domain.PostStatusScheduled {
					return fmt.Errorf("expected scheduled status, got %s", post.Status)
				}
				return nil
			},
		},
		capabilities.CapabilityPostsEdit: {
			capabilities.SurfaceAPI: func(env *parityEnv) error {
				postID := env.apiCreatePost("matrix edit start api", time.Time{}, nil)
				env.apiEditPost(postID, "matrix edit api", "schedule", time.Now().UTC().Add(70*time.Minute))
				post, err := env.store.GetPost(env.t.Context(), postID)
				if err != nil {
					return err
				}
				if post.Status != domain.PostStatusScheduled {
					return fmt.Errorf("expected scheduled status, got %s", post.Status)
				}
				if strings.TrimSpace(post.Text) != "matrix edit api" {
					return fmt.Errorf("expected updated text, got %q", post.Text)
				}
				return nil
			},
			capabilities.SurfaceCLI: func(env *parityEnv) error {
				postID := env.apiCreatePost("matrix edit start cli", time.Time{}, nil)
				env.cliEditPost(postID, "matrix edit cli", "schedule", time.Now().UTC().Add(70*time.Minute))
				post, err := env.store.GetPost(env.t.Context(), postID)
				if err != nil {
					return err
				}
				if post.Status != domain.PostStatusScheduled {
					return fmt.Errorf("expected scheduled status, got %s", post.Status)
				}
				if strings.TrimSpace(post.Text) != "matrix edit cli" {
					return fmt.Errorf("expected updated text, got %q", post.Text)
				}
				return nil
			},
			capabilities.SurfaceMCP: func(env *parityEnv) error {
				postID := env.apiCreatePost("matrix edit start mcp", time.Time{}, nil)
				env.mcpEditPost(postID, "matrix edit mcp", "schedule", time.Now().UTC().Add(70*time.Minute))
				post, err := env.store.GetPost(env.t.Context(), postID)
				if err != nil {
					return err
				}
				if post.Status != domain.PostStatusScheduled {
					return fmt.Errorf("expected scheduled status, got %s", post.Status)
				}
				if strings.TrimSpace(post.Text) != "matrix edit mcp" {
					return fmt.Errorf("expected updated text, got %q", post.Text)
				}
				return nil
			},
		},
		capabilities.CapabilityPostsDelete: {
			capabilities.SurfaceAPI: func(env *parityEnv) error {
				postID := env.apiCreatePost("matrix delete api", time.Now().UTC().Add(35*time.Minute), nil)
				env.apiDeletePost(postID)
				if _, err := env.store.GetPost(env.t.Context(), postID); !errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("expected deleted post, err=%v", err)
				}
				return nil
			},
			capabilities.SurfaceCLI: func(env *parityEnv) error {
				postID := env.apiCreatePost("matrix delete cli", time.Now().UTC().Add(35*time.Minute), nil)
				env.cliDeletePost(postID)
				if _, err := env.store.GetPost(env.t.Context(), postID); !errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("expected deleted post, err=%v", err)
				}
				return nil
			},
			capabilities.SurfaceMCP: func(env *parityEnv) error {
				postID := env.apiCreatePost("matrix delete mcp", time.Now().UTC().Add(35*time.Minute), nil)
				env.mcpDeletePost(postID)
				if _, err := env.store.GetPost(env.t.Context(), postID); !errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("expected deleted post, err=%v", err)
				}
				return nil
			},
		},
		capabilities.CapabilityPostsCancel: {
			capabilities.SurfaceAPI: func(env *parityEnv) error {
				postID := env.apiCreatePost("matrix cancel api", time.Now().UTC().Add(30*time.Minute), nil)
				env.apiCancelPost(postID)
				post, err := env.store.GetPost(env.t.Context(), postID)
				if err != nil {
					return err
				}
				if post.Status != domain.PostStatusCanceled {
					return fmt.Errorf("expected canceled status, got %s", post.Status)
				}
				return nil
			},
			capabilities.SurfaceCLI: func(env *parityEnv) error {
				postID := env.apiCreatePost("matrix cancel cli", time.Now().UTC().Add(30*time.Minute), nil)
				env.cliCancelPost(postID)
				post, err := env.store.GetPost(env.t.Context(), postID)
				if err != nil {
					return err
				}
				if post.Status != domain.PostStatusCanceled {
					return fmt.Errorf("expected canceled status, got %s", post.Status)
				}
				return nil
			},
			capabilities.SurfaceMCP: func(env *parityEnv) error {
				postID := env.apiCreatePost("matrix cancel mcp", time.Now().UTC().Add(30*time.Minute), nil)
				env.mcpCancelPost(postID)
				post, err := env.store.GetPost(env.t.Context(), postID)
				if err != nil {
					return err
				}
				if post.Status != domain.PostStatusCanceled {
					return fmt.Errorf("expected canceled status, got %s", post.Status)
				}
				return nil
			},
		},
		capabilities.CapabilityPostsValidate: {
			capabilities.SurfaceAPI: func(env *parityEnv) error {
				_, status := env.apiJSON(http.MethodPost, "/posts/validate", map[string]any{"account_id": env.account.ID, "text": "matrix validate api"}, "application/json")
				if status != http.StatusOK {
					return fmt.Errorf("expected 200, got %d", status)
				}
				return nil
			},
			capabilities.SurfaceCLI: func(env *parityEnv) error {
				code, _, stderr := env.runCLIResult("posts", "validate", "--account-id", env.account.ID, "--text", "matrix validate cli")
				if code != 0 {
					return fmt.Errorf("exit %d: %s", code, strings.TrimSpace(stderr))
				}
				return nil
			},
			capabilities.SurfaceMCP: func(env *parityEnv) error {
				if msg := env.mcpCallToolError("postflow_validate_post", map[string]any{"account_id": env.account.ID, "text": "matrix validate mcp"}); msg != "" {
					return errors.New(msg)
				}
				return nil
			},
		},
		capabilities.CapabilityAccountsList: {
			capabilities.SurfaceAPI: func(env *parityEnv) error {
				_, status := env.apiJSON(http.MethodGet, "/accounts", nil, "")
				if status != http.StatusOK {
					return fmt.Errorf("expected 200, got %d", status)
				}
				return nil
			},
			capabilities.SurfaceCLI: func(env *parityEnv) error {
				code, _, stderr := env.runCLIResult("accounts", "list")
				if code != 0 {
					return fmt.Errorf("exit %d: %s", code, strings.TrimSpace(stderr))
				}
				return nil
			},
			capabilities.SurfaceMCP: func(env *parityEnv) error {
				ids := env.mcpListAccountIDs()
				if len(ids) == 0 {
					return errors.New("expected at least one account")
				}
				return nil
			},
		},
		capabilities.CapabilityAccountsCreateStatic: {
			capabilities.SurfaceAPI: func(env *parityEnv) error {
				id := env.apiCreateStaticAccount("linkedin", "matrix_static_api", map[string]any{"access_token": "tok_matrix_api"})
				if strings.TrimSpace(id) == "" {
					return errors.New("empty account id")
				}
				return nil
			},
			capabilities.SurfaceCLI: func(env *parityEnv) error {
				id := env.cliCreateStaticAccount("linkedin", "matrix_static_cli", map[string]string{"access_token": "tok_matrix_cli"})
				if strings.TrimSpace(id) == "" {
					return errors.New("empty account id")
				}
				return nil
			},
			capabilities.SurfaceMCP: func(env *parityEnv) error {
				id := env.mcpCreateStaticAccount("linkedin", "matrix_static_mcp", map[string]any{"access_token": "tok_matrix_mcp"})
				if strings.TrimSpace(id) == "" {
					return errors.New("empty account id")
				}
				return nil
			},
		},
		capabilities.CapabilityAccountsConnect: {
			capabilities.SurfaceAPI: func(env *parityEnv) error {
				id := env.apiCreateStaticAccount("linkedin", "matrix_connect_api", map[string]any{"access_token": "tok_connect_api"})
				env.apiDisconnectAccount(id)
				env.apiConnectAccount(id)
				return nil
			},
			capabilities.SurfaceCLI: func(env *parityEnv) error {
				id := env.apiCreateStaticAccount("linkedin", "matrix_connect_cli", map[string]any{"access_token": "tok_connect_cli"})
				env.apiDisconnectAccount(id)
				env.cliConnectAccount(id)
				return nil
			},
			capabilities.SurfaceMCP: func(env *parityEnv) error {
				id := env.apiCreateStaticAccount("linkedin", "matrix_connect_mcp", map[string]any{"access_token": "tok_connect_mcp"})
				env.apiDisconnectAccount(id)
				env.mcpConnectAccount(id)
				return nil
			},
		},
		capabilities.CapabilityAccountsDisconnect: {
			capabilities.SurfaceAPI: func(env *parityEnv) error {
				id := env.apiCreateStaticAccount("linkedin", "matrix_disconnect_api", map[string]any{"access_token": "tok_disconnect_api"})
				env.apiDisconnectAccount(id)
				return nil
			},
			capabilities.SurfaceCLI: func(env *parityEnv) error {
				id := env.apiCreateStaticAccount("linkedin", "matrix_disconnect_cli", map[string]any{"access_token": "tok_disconnect_cli"})
				env.cliDisconnectAccount(id)
				return nil
			},
			capabilities.SurfaceMCP: func(env *parityEnv) error {
				id := env.apiCreateStaticAccount("linkedin", "matrix_disconnect_mcp", map[string]any{"access_token": "tok_disconnect_mcp"})
				env.mcpDisconnectAccount(id)
				return nil
			},
		},
		capabilities.CapabilityAccountsDelete: {
			capabilities.SurfaceAPI: func(env *parityEnv) error {
				id := env.apiCreateStaticAccount("linkedin", "matrix_delete_api", map[string]any{"access_token": "tok_delete_api"})
				env.apiDisconnectAccount(id)
				env.apiDeleteAccount(id)
				return nil
			},
			capabilities.SurfaceCLI: func(env *parityEnv) error {
				id := env.apiCreateStaticAccount("linkedin", "matrix_delete_cli", map[string]any{"access_token": "tok_delete_cli"})
				env.apiDisconnectAccount(id)
				env.cliDeleteAccount(id)
				return nil
			},
			capabilities.SurfaceMCP: func(env *parityEnv) error {
				id := env.apiCreateStaticAccount("linkedin", "matrix_delete_mcp", map[string]any{"access_token": "tok_delete_mcp"})
				env.apiDisconnectAccount(id)
				env.mcpDeleteAccount(id)
				return nil
			},
		},
		capabilities.CapabilityAccountsSetXPremium: {
			capabilities.SurfaceAPI: func(env *parityEnv) error {
				id := env.apiCreateStaticAccount("x", "matrix_xpremium_api", map[string]any{"access_token": "tok_xpremium_api", "access_token_secret": "sec_xpremium_api"})
				env.apiSetXPremium(id, true)
				return nil
			},
			capabilities.SurfaceCLI: func(env *parityEnv) error {
				id := env.apiCreateStaticAccount("x", "matrix_xpremium_cli", map[string]any{"access_token": "tok_xpremium_cli", "access_token_secret": "sec_xpremium_cli"})
				env.cliSetXPremium(id, true)
				return nil
			},
			capabilities.SurfaceMCP: func(env *parityEnv) error {
				id := env.apiCreateStaticAccount("x", "matrix_xpremium_mcp", map[string]any{"access_token": "tok_xpremium_mcp", "access_token_secret": "sec_xpremium_mcp"})
				env.mcpSetXPremium(id, true)
				return nil
			},
		},
		capabilities.CapabilityFailedList: {
			capabilities.SurfaceAPI: func(env *parityEnv) error {
				_, status := env.apiJSON(http.MethodGet, "/dlq?limit=10", nil, "")
				if status != http.StatusOK {
					return fmt.Errorf("expected 200, got %d", status)
				}
				return nil
			},
			capabilities.SurfaceCLI: func(env *parityEnv) error {
				code, _, stderr := env.runCLIResult("dlq", "list", "--limit", "10")
				if code != 0 {
					return fmt.Errorf("exit %d: %s", code, strings.TrimSpace(stderr))
				}
				return nil
			},
			capabilities.SurfaceMCP: func(env *parityEnv) error {
				if msg := env.mcpCallToolError("postflow_list_failed", map[string]any{"limit": 10}); msg != "" {
					return errors.New(msg)
				}
				return nil
			},
		},
		capabilities.CapabilityDLQRequeue: {
			capabilities.SurfaceAPI: func(env *parityEnv) error {
				dlqID := env.seedFailedDeadLetter("matrix requeue api")
				_, status := env.apiJSON(http.MethodPost, "/dlq/"+dlqID+"/requeue", nil, "application/json")
				if status != http.StatusOK {
					return fmt.Errorf("expected 200, got %d", status)
				}
				return nil
			},
			capabilities.SurfaceCLI: func(env *parityEnv) error {
				dlqID := env.seedFailedDeadLetter("matrix requeue cli")
				code, _, stderr := env.runCLIResult("dlq", "requeue", "--id", dlqID)
				if code != 0 {
					return fmt.Errorf("exit %d: %s", code, strings.TrimSpace(stderr))
				}
				return nil
			},
			capabilities.SurfaceMCP: func(env *parityEnv) error {
				dlqID := env.seedFailedDeadLetter("matrix requeue mcp")
				if msg := env.mcpCallToolError("postflow_requeue_failed", map[string]any{"dead_letter_id": dlqID}); msg != "" {
					return errors.New(msg)
				}
				return nil
			},
		},
		capabilities.CapabilityDLQDelete: {
			capabilities.SurfaceAPI: func(env *parityEnv) error {
				dlqID := env.seedFailedDeadLetter("matrix delete api")
				_, status := env.apiJSON(http.MethodPost, "/dlq/"+dlqID+"/delete", nil, "application/json")
				if status != http.StatusOK {
					return fmt.Errorf("expected 200, got %d", status)
				}
				return nil
			},
			capabilities.SurfaceCLI: func(env *parityEnv) error {
				dlqID := env.seedFailedDeadLetter("matrix delete cli")
				code, _, stderr := env.runCLIResult("dlq", "delete", "--id", dlqID)
				if code != 0 {
					return fmt.Errorf("exit %d: %s", code, strings.TrimSpace(stderr))
				}
				return nil
			},
			capabilities.SurfaceMCP: func(env *parityEnv) error {
				dlqID := env.seedFailedDeadLetter("matrix delete mcp")
				if msg := env.mcpCallToolError("postflow_delete_failed", map[string]any{"dead_letter_id": dlqID}); msg != "" {
					return errors.New(msg)
				}
				return nil
			},
		},
		capabilities.CapabilityMediaUpload: {
			capabilities.SurfaceAPI: func(env *parityEnv) error {
				if _, err := uploadMediaViaAPI(env, env.tempFile); err != nil {
					return err
				}
				return nil
			},
			capabilities.SurfaceCLI: func(env *parityEnv) error {
				code, _, stderr := env.runCLIResult("media", "upload", "--file", env.tempFile, "--kind", "image")
				if code != 0 {
					return fmt.Errorf("exit %d: %s", code, strings.TrimSpace(stderr))
				}
				return nil
			},
			capabilities.SurfaceMCP: func(env *parityEnv) error {
				if msg := env.mcpCallToolError("postflow_upload_media", map[string]any{
					"kind":           "image",
					"original_name":  "matrix-upload.bin",
					"content_base64": "bWF0cml4LXVwbG9hZA==",
				}); msg != "" {
					return errors.New(msg)
				}
				return nil
			},
		},
		capabilities.CapabilityMediaList: {
			capabilities.SurfaceAPI: func(env *parityEnv) error {
				_, status := env.apiJSON(http.MethodGet, "/media?limit=10", nil, "")
				if status != http.StatusOK {
					return fmt.Errorf("expected 200, got %d", status)
				}
				return nil
			},
			capabilities.SurfaceCLI: func(env *parityEnv) error {
				code, _, stderr := env.runCLIResult("media", "list", "--limit", "10")
				if code != 0 {
					return fmt.Errorf("exit %d: %s", code, strings.TrimSpace(stderr))
				}
				return nil
			},
			capabilities.SurfaceMCP: func(env *parityEnv) error {
				if msg := env.mcpCallToolError("postflow_list_media", map[string]any{"limit": 10}); msg != "" {
					return errors.New(msg)
				}
				return nil
			},
		},
		capabilities.CapabilityMediaDelete: {
			capabilities.SurfaceAPI: func(env *parityEnv) error {
				mediaID, err := uploadMediaViaAPI(env, env.tempFile)
				if err != nil {
					return err
				}
				_, status := env.apiJSON(http.MethodDelete, "/media/"+mediaID, nil, "")
				if status != http.StatusOK {
					return fmt.Errorf("expected 200, got %d", status)
				}
				return nil
			},
			capabilities.SurfaceCLI: func(env *parityEnv) error {
				mediaID, err := uploadMediaViaAPI(env, env.tempFile)
				if err != nil {
					return err
				}
				code, _, stderr := env.runCLIResult("media", "delete", "--id", mediaID)
				if code != 0 {
					return fmt.Errorf("exit %d: %s", code, strings.TrimSpace(stderr))
				}
				return nil
			},
			capabilities.SurfaceMCP: func(env *parityEnv) error {
				mediaID, err := uploadMediaViaAPI(env, env.tempFile)
				if err != nil {
					return err
				}
				if msg := env.mcpCallToolError("postflow_delete_media", map[string]any{"media_id": mediaID}); msg != "" {
					return errors.New(msg)
				}
				return nil
			},
		},
		capabilities.CapabilitySettingsTimezone: {
			capabilities.SurfaceAPI: func(env *parityEnv) error {
				env.apiSetTimezone("Europe/Madrid")
				return nil
			},
			capabilities.SurfaceCLI: func(env *parityEnv) error {
				env.cliSetTimezone("Europe/Madrid")
				return nil
			},
			capabilities.SurfaceMCP: func(env *parityEnv) error {
				env.mcpSetTimezone("Europe/Madrid")
				return nil
			},
		},
	}
}

func uploadMediaViaAPI(env *parityEnv, filePath string) (string, error) {
	fileContent, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read media file: %w", err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("kind", "image"); err != nil {
		return "", fmt.Errorf("write kind field: %w", err)
	}
	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return "", fmt.Errorf("create file part: %w", err)
	}
	if _, err := io.Copy(part, bytes.NewReader(fileContent)); err != nil {
		return "", fmt.Errorf("copy media content: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("close multipart writer: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, env.baseURL+"/media", bytes.NewReader(body.Bytes()))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+env.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("api upload request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("api upload status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var payload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", fmt.Errorf("decode upload response: %w", err)
	}
	id := strings.TrimSpace(payload.ID)
	if id == "" {
		return "", fmt.Errorf("upload response missing id")
	}
	return id, nil
}

func containsID(ids []string, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	for _, id := range ids {
		if strings.TrimSpace(id) == target {
			return true
		}
	}
	return false
}
