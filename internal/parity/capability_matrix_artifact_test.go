package parity_test

import (
	"bytes"
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

	"github.com/antoniolg/publisher/internal/capabilities"
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
				if msg := env.mcpCallToolError("publisher_list_schedule", map[string]any{"from": from, "to": to, "limit": 10}); msg != "" {
					return errors.New(msg)
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
				if msg := env.mcpCallToolError("publisher_create_post", map[string]any{"account_id": env.account.ID, "text": "matrix create mcp"}); msg != "" {
					return errors.New(msg)
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
				if msg := env.mcpCallToolError("publisher_validate_post", map[string]any{"account_id": env.account.ID, "text": "matrix validate mcp"}); msg != "" {
					return errors.New(msg)
				}
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
				if msg := env.mcpCallToolError("publisher_list_failed", map[string]any{"limit": 10}); msg != "" {
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
				if msg := env.mcpCallToolError("publisher_requeue_failed", map[string]any{"dead_letter_id": dlqID}); msg != "" {
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
				if msg := env.mcpCallToolError("publisher_delete_failed", map[string]any{"dead_letter_id": dlqID}); msg != "" {
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
				if msg := env.mcpCallToolError("publisher_upload_media", map[string]any{
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
				if msg := env.mcpCallToolError("publisher_list_media", map[string]any{"limit": 10}); msg != "" {
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
				if msg := env.mcpCallToolError("publisher_delete_media", map[string]any{"media_id": mediaID}); msg != "" {
					return errors.New(msg)
				}
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
