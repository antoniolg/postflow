package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/publisher/internal/db"
	"github.com/antoniolg/publisher/internal/domain"
	"github.com/antoniolg/publisher/internal/publisher"
)

type mcpToolCallResponse struct {
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
	Result struct {
		IsError           bool           `json:"isError"`
		StructuredContent map[string]any `json:"structuredContent"`
		Content           []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"result"`
}

func TestMCPEditPostEditsTextAndMedia(t *testing.T) {
	store, mcpURL, sessionID := setupMCPMediaEditTest(t)
	account := createConnectedAccountForPlatform(t, store, domain.PlatformLinkedIn, "li-edit-media")

	initialMedia := createMCPTestMedia(t, store, "initial.png")
	replacementMedia := createMCPTestMedia(t, store, "replacement.png")
	created, err := store.CreatePost(t.Context(), db.CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "original text",
			Status:      domain.PostStatusDraft,
			MaxAttempts: 3,
		},
		MediaIDs: []string{initialMedia.ID},
	})
	if err != nil {
		t.Fatalf("create post: %v", err)
	}

	call := mcpCallTool(t, mcpURL, sessionID, "publisher_edit_post", map[string]any{
		"post_id":      created.Post.ID,
		"text":         "edited with replacement media",
		"media_ids":    []string{replacementMedia.ID},
		"intent":       "draft",
		"scheduled_at": "",
	})
	if call.Error != nil || call.Result.IsError {
		t.Fatalf("expected successful edit, got error=%s raw_error=%s", mcpToolErrorMessage(call), callRawError(call))
	}

	updated, err := store.GetPost(t.Context(), created.Post.ID)
	if err != nil {
		t.Fatalf("get post: %v", err)
	}
	if strings.TrimSpace(updated.Text) != "edited with replacement media" {
		t.Fatalf("expected updated text, got %q", updated.Text)
	}
	if len(updated.Media) != 1 || updated.Media[0].ID != replacementMedia.ID {
		t.Fatalf("expected one replacement media %q, got %#v", replacementMedia.ID, updated.Media)
	}
}

func TestMCPEditPostPreservesScheduledStateWithoutIntentOrScheduledAt(t *testing.T) {
	store, mcpURL, sessionID := setupMCPMediaEditTest(t)
	account := createConnectedAccountForPlatform(t, store, domain.PlatformLinkedIn, "li-preserve-schedule")
	initialMedia := createMCPTestMedia(t, store, "scheduled-initial.png")
	replacementMedia := createMCPTestMedia(t, store, "scheduled-replacement.png")
	scheduledAt := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)

	created, err := store.CreatePost(t.Context(), db.CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "scheduled post",
			Status:      domain.PostStatusScheduled,
			ScheduledAt: scheduledAt,
			MaxAttempts: 3,
		},
		MediaIDs: []string{initialMedia.ID},
	})
	if err != nil {
		t.Fatalf("create post: %v", err)
	}

	call := mcpCallTool(t, mcpURL, sessionID, "publisher_edit_post", map[string]any{
		"post_id":   created.Post.ID,
		"text":      "scheduled post edited",
		"media_ids": []string{replacementMedia.ID},
	})
	if call.Error != nil || call.Result.IsError {
		t.Fatalf("expected successful edit, got error=%s raw_error=%s", mcpToolErrorMessage(call), callRawError(call))
	}

	updated, err := store.GetPost(t.Context(), created.Post.ID)
	if err != nil {
		t.Fatalf("get post: %v", err)
	}
	if updated.Status != domain.PostStatusScheduled {
		t.Fatalf("expected scheduled status to be preserved, got %s", updated.Status)
	}
	if !updated.ScheduledAt.Equal(scheduledAt) {
		t.Fatalf("expected scheduled_at %s, got %s", scheduledAt, updated.ScheduledAt)
	}
}

func TestMCPEditPostAddsMediaWhenPostHasNone(t *testing.T) {
	store, mcpURL, sessionID := setupMCPMediaEditTest(t)
	account := createConnectedAccountForPlatform(t, store, domain.PlatformLinkedIn, "li-add-media")
	media := createMCPTestMedia(t, store, "add.png")

	created, err := store.CreatePost(t.Context(), db.CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "text without media",
			Status:      domain.PostStatusDraft,
			MaxAttempts: 3,
		},
	})
	if err != nil {
		t.Fatalf("create post: %v", err)
	}

	before, err := store.GetPost(t.Context(), created.Post.ID)
	if err != nil {
		t.Fatalf("get initial post: %v", err)
	}
	if len(before.Media) != 0 {
		t.Fatalf("expected post without media before edit, got %d", len(before.Media))
	}

	call := mcpCallTool(t, mcpURL, sessionID, "publisher_edit_post", map[string]any{
		"post_id":   created.Post.ID,
		"text":      "text with first media",
		"media_ids": []string{media.ID},
	})
	if call.Error != nil || call.Result.IsError {
		t.Fatalf("expected successful edit, got error=%s raw_error=%s", mcpToolErrorMessage(call), callRawError(call))
	}

	updated, err := store.GetPost(t.Context(), created.Post.ID)
	if err != nil {
		t.Fatalf("get updated post: %v", err)
	}
	if len(updated.Media) != 1 || updated.Media[0].ID != media.ID {
		t.Fatalf("expected one attached media %q, got %#v", media.ID, updated.Media)
	}
}

func TestMCPEditPostRemovesMediaWhenPlatformAllowsIt(t *testing.T) {
	store, mcpURL, sessionID := setupMCPMediaEditTest(t)
	account := createConnectedAccountForPlatform(t, store, domain.PlatformLinkedIn, "li-remove-media")
	media := createMCPTestMedia(t, store, "remove.png")

	created, err := store.CreatePost(t.Context(), db.CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "text with media",
			Status:      domain.PostStatusDraft,
			MaxAttempts: 3,
		},
		MediaIDs: []string{media.ID},
	})
	if err != nil {
		t.Fatalf("create post: %v", err)
	}

	call := mcpCallTool(t, mcpURL, sessionID, "publisher_edit_post", map[string]any{
		"post_id":   created.Post.ID,
		"text":      "text without media",
		"media_ids": []string{},
	})
	if call.Error != nil || call.Result.IsError {
		t.Fatalf("expected successful edit, got error=%s raw_error=%s", mcpToolErrorMessage(call), callRawError(call))
	}

	updated, err := store.GetPost(t.Context(), created.Post.ID)
	if err != nil {
		t.Fatalf("get updated post: %v", err)
	}
	if len(updated.Media) != 0 {
		t.Fatalf("expected media to be removed, got %#v", updated.Media)
	}
}

func TestMCPEditPostRejectsInstagramWithoutMedia(t *testing.T) {
	store, mcpURL, sessionID := setupMCPMediaEditTest(t)
	account := createConnectedAccountForPlatform(t, store, domain.PlatformInstagram, "ig-no-media")
	media := createMCPTestMedia(t, store, "instagram-required.png")

	created, err := store.CreatePost(t.Context(), db.CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Text:        "caption",
			Status:      domain.PostStatusScheduled,
			MaxAttempts: 3,
		},
		MediaIDs: []string{media.ID},
	})
	if err != nil {
		t.Fatalf("create post: %v", err)
	}

	call := mcpCallTool(t, mcpURL, sessionID, "publisher_edit_post", map[string]any{
		"post_id":   created.Post.ID,
		"text":      "caption updated",
		"media_ids": []string{},
	})
	if call.Error == nil && !call.Result.IsError {
		t.Fatalf("expected edit to fail for instagram without media")
	}
	msg := strings.ToLower(mcpToolErrorMessage(call))
	if !strings.Contains(msg, "instagram") || !strings.Contains(msg, "media") {
		t.Fatalf("expected instagram media validation error, got %q", msg)
	}

	unchanged, err := store.GetPost(t.Context(), created.Post.ID)
	if err != nil {
		t.Fatalf("get post after failed edit: %v", err)
	}
	if len(unchanged.Media) != 1 || unchanged.Media[0].ID != media.ID {
		t.Fatalf("expected original media to remain after failed edit, got %#v", unchanged.Media)
	}
}

func setupMCPMediaEditTest(t *testing.T) (*db.Store, string, string) {
	t.Helper()

	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	registry := publisher.NewProviderRegistry(
		publisher.NewMockProvider(domain.PlatformX),
		publisher.NewLinkedInProvider(publisher.LinkedInProviderConfig{}),
		publisher.NewFacebookProvider(publisher.MetaProviderConfig{}),
		publisher.NewInstagramProvider(publisher.MetaProviderConfig{}),
	)

	srv := Server{
		Store:             store,
		DataDir:           tempDir,
		DefaultMaxRetries: 3,
		Registry:          registry,
	}
	httpServer := httptest.NewServer(srv.Handler())
	t.Cleanup(httpServer.Close)

	mcpURL := httpServer.URL + "/mcp"
	initializeBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test-client","version":"1.0.0"}}}`
	initializeResp, initializeRaw := postMCPRequest(t, mcpURL, "", initializeBody)
	if initializeResp.StatusCode != http.StatusOK {
		t.Fatalf("expected initialize status 200, got %d: %s", initializeResp.StatusCode, string(initializeRaw))
	}
	sessionID := strings.TrimSpace(initializeResp.Header.Get("Mcp-Session-Id"))
	if sessionID == "" {
		t.Fatalf("expected initialize response to include MCP session id")
	}
	return store, mcpURL, sessionID
}

func createConnectedAccountForPlatform(t *testing.T, store *db.Store, platform domain.Platform, externalID string) domain.SocialAccount {
	t.Helper()
	account, err := store.UpsertAccount(t.Context(), db.UpsertAccountParams{
		Platform:          platform,
		DisplayName:       string(platform) + " test",
		ExternalAccountID: externalID,
		AuthMethod:        domain.AuthMethodStatic,
		Status:            domain.AccountStatusConnected,
	})
	if err != nil {
		t.Fatalf("create test account: %v", err)
	}
	return account
}

func createMCPTestMedia(t *testing.T, store *db.Store, originalName string) domain.Media {
	t.Helper()
	media, err := store.CreateMedia(t.Context(), domain.Media{
		Kind:         "image",
		OriginalName: originalName,
		StoragePath:  "/tmp/" + originalName,
		MimeType:     "image/png",
		SizeBytes:    64,
	})
	if err != nil {
		t.Fatalf("create media: %v", err)
	}
	return media
}

func mcpCallTool(t *testing.T, endpoint, sessionID, toolName string, arguments map[string]any) mcpToolCallResponse {
	t.Helper()

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      10,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": arguments,
		},
	})
	if err != nil {
		t.Fatalf("encode mcp tool payload: %v", err)
	}

	resp, raw := postMCPRequest(t, endpoint, sessionID, string(body))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected tools/call status 200, got %d: %s", resp.StatusCode, string(raw))
	}

	var out mcpToolCallResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode mcp tool response: %v (raw=%s)", err, string(raw))
	}
	return out
}

func mcpToolErrorMessage(call mcpToolCallResponse) string {
	if call.Error != nil && strings.TrimSpace(call.Error.Message) != "" {
		return strings.TrimSpace(call.Error.Message)
	}
	if len(call.Result.Content) > 0 && strings.TrimSpace(call.Result.Content[0].Text) != "" {
		return strings.TrimSpace(call.Result.Content[0].Text)
	}
	if call.Result.StructuredContent != nil {
		if raw, ok := call.Result.StructuredContent["error"]; ok {
			if msg, ok := raw.(string); ok {
				return strings.TrimSpace(msg)
			}
		}
	}
	return ""
}

func callRawError(call mcpToolCallResponse) string {
	if call.Error == nil {
		return ""
	}
	return strings.TrimSpace(call.Error.Message)
}
