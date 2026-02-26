package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antoniolg/publisher/internal/db"
)

func TestMCPStreamableHTTPExposesToolsAndCreatesPost(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{Store: store, DataDir: tempDir, DefaultMaxRetries: 3}
	httpServer := httptest.NewServer(srv.Handler())
	defer httpServer.Close()

	mcpURL := httpServer.URL + "/mcp"
	initializeBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test-client","version":"1.0.0"}}}`
	initializeResp, initializeRaw := postMCPRequest(t, mcpURL, "", initializeBody)
	if initializeResp.StatusCode != http.StatusOK {
		t.Fatalf("expected initialize status 200, got %d: %s", initializeResp.StatusCode, string(initializeRaw))
	}
	sessionID := strings.TrimSpace(initializeResp.Header.Get("Mcp-Session-Id"))
	if !strings.Contains(string(initializeRaw), "publisher-mcp") {
		t.Fatalf("expected initialize response to include publisher server info")
	}

	listToolsBody := `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`
	listToolsResp, listToolsRaw := postMCPRequest(t, mcpURL, sessionID, listToolsBody)
	if listToolsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected tools/list status 200, got %d: %s", listToolsResp.StatusCode, string(listToolsRaw))
	}
	for _, expected := range []string{
		"publisher_list_schedule",
		"publisher_list_drafts",
		"publisher_list_failed",
		"publisher_create_post",
		"publisher_upload_media",
	} {
		if !strings.Contains(string(listToolsRaw), expected) {
			t.Fatalf("expected tools/list to include %q", expected)
		}
	}

	uploadBody := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"publisher_upload_media","arguments":{"platform":"x","kind":"image","original_name":"hello.txt","content_base64":"aGVsbG8gd29ybGQ="}}}`
	uploadResp, uploadRaw := postMCPRequest(t, mcpURL, sessionID, uploadBody)
	if uploadResp.StatusCode != http.StatusOK {
		t.Fatalf("expected upload tools/call status 200, got %d: %s", uploadResp.StatusCode, string(uploadRaw))
	}
	if strings.Contains(string(uploadRaw), `"isError":true`) {
		t.Fatalf("expected upload media tool call without isError=true, got: %s", string(uploadRaw))
	}
	mediaID := extractToolStructuredString(uploadRaw, "media_id")
	if mediaID == "" {
		t.Fatalf("expected media_id in upload tool response: %s", string(uploadRaw))
	}

	createPostBody := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"publisher_create_post","arguments":{"platform":"x","text":"draft from mcp tool","media_ids":["` + mediaID + `"]}}}`
	createPostResp, createPostRaw := postMCPRequest(t, mcpURL, sessionID, createPostBody)
	if createPostResp.StatusCode != http.StatusOK {
		t.Fatalf("expected tools/call status 200, got %d: %s", createPostResp.StatusCode, string(createPostRaw))
	}
	if strings.Contains(string(createPostRaw), `"isError":true`) {
		t.Fatalf("expected create post tool call without isError=true, got: %s", string(createPostRaw))
	}

	drafts, err := store.ListDrafts(t.Context())
	if err != nil {
		t.Fatalf("list drafts: %v", err)
	}
	if len(drafts) != 1 {
		t.Fatalf("expected 1 draft created via mcp, got %d", len(drafts))
	}
	if strings.TrimSpace(drafts[0].Text) != "draft from mcp tool" {
		t.Fatalf("unexpected draft text %q", drafts[0].Text)
	}
	if len(drafts[0].Media) != 1 {
		t.Fatalf("expected draft with one media attachment, got %d", len(drafts[0].Media))
	}
	if drafts[0].Media[0].ID != mediaID {
		t.Fatalf("expected attached media id %q, got %q", mediaID, drafts[0].Media[0].ID)
	}
}

func TestSettingsViewIncludesMCPURLAndConfig(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	srv := Server{
		Store:             store,
		DataDir:           tempDir,
		DefaultMaxRetries: 3,
		APIToken:          "super-secret",
	}
	h := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/?view=settings", nil)
	req.Host = "publisher.example.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Authorization", "Bearer super-secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for settings view, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `id="mcp-url"`) {
		t.Fatalf("expected mcp url field in settings")
	}
	if !strings.Contains(body, "https://publisher.example.com/mcp") {
		t.Fatalf("expected absolute mcp url in settings")
	}
	if !strings.Contains(body, "claude mcp add -t http publisher") {
		t.Fatalf("expected claude mcp command in settings")
	}
	if !strings.Contains(body, "codex mcp add publisher --url") {
		t.Fatalf("expected codex mcp command in settings")
	}
	if !strings.Contains(body, "[mcp_servers.publisher]") {
		t.Fatalf("expected codex toml config in settings")
	}
	if !strings.Contains(body, "bearer_token_env_var = &#34;PUBLISHER_API_TOKEN&#34;") {
		t.Fatalf("expected codex bearer token env var hint in settings")
	}
	if !strings.Contains(body, "streamable_http") {
		t.Fatalf("expected streamable http config in settings")
	}
	if !strings.Contains(body, "Authorization: Bearer") {
		t.Fatalf("expected bearer auth hint in settings")
	}
}

func postMCPRequest(t *testing.T, endpoint, sessionID, payload string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewBufferString(payload))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if strings.TrimSpace(sessionID) != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post mcp request: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return resp, normalizeMCPResponseBody(body)
}

func extractToolStructuredString(raw []byte, key string) string {
	var response struct {
		Result struct {
			StructuredContent map[string]any `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return ""
	}
	value, ok := response.Result.StructuredContent[key]
	if !ok {
		return ""
	}
	out, _ := value.(string)
	return strings.TrimSpace(out)
}

func normalizeMCPResponseBody(raw []byte) []byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.HasPrefix(trimmed, []byte("{")) {
		return trimmed
	}

	lines := strings.Split(string(trimmed), "\n")
	dataLines := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		dataLines = append(dataLines, data)
	}
	if len(dataLines) == 0 {
		return trimmed
	}
	return []byte(strings.Join(dataLines, "\n"))
}
