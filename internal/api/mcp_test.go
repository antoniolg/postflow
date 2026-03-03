package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/publisher/internal/db"
	"github.com/antoniolg/publisher/internal/domain"
)

func TestMCPStreamableHTTPExposesToolsAndCreatesPost(t *testing.T) {
	tempDir := t.TempDir()
	store, err := db.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	account := createTestAccount(t, store)

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
		"publisher_health",
		"publisher_list_schedule",
		"publisher_list_drafts",
		"publisher_list_accounts",
		"publisher_create_static_account",
		"publisher_connect_account",
		"publisher_disconnect_account",
		"publisher_set_x_premium",
		"publisher_delete_account",
		"publisher_list_failed",
		"publisher_create_post",
		"publisher_cancel_post",
		"publisher_schedule_post",
		"publisher_edit_post",
		"publisher_delete_post",
		"publisher_validate_post",
		"publisher_upload_media",
		"publisher_list_media",
		"publisher_delete_media",
		"publisher_requeue_failed",
		"publisher_delete_failed",
		"publisher_set_timezone",
	} {
		if !strings.Contains(string(listToolsRaw), expected) {
			t.Fatalf("expected tools/list to include %q", expected)
		}
	}

	uploadBody := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"publisher_upload_media","arguments":{"kind":"image","original_name":"hello.txt","content_base64":"aGVsbG8gd29ybGQ="}}}`
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

	createPostBody := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"publisher_create_post","arguments":{"account_id":"` + account.ID + `","text":"draft from mcp tool","media_ids":["` + mediaID + `"]}}}`
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
	draftID := strings.TrimSpace(drafts[0].ID)
	if draftID == "" {
		t.Fatalf("expected draft id from created post")
	}

	scheduledAt := time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339)
	schedulePostBody := `{"jsonrpc":"2.0","id":4.6,"method":"tools/call","params":{"name":"publisher_schedule_post","arguments":{"post_id":"` + draftID + `","scheduled_at":"` + scheduledAt + `"}}}`
	schedulePostResp, schedulePostRaw := postMCPRequest(t, mcpURL, sessionID, schedulePostBody)
	if schedulePostResp.StatusCode != http.StatusOK {
		t.Fatalf("expected schedule post tools/call status 200, got %d: %s", schedulePostResp.StatusCode, string(schedulePostRaw))
	}
	if strings.Contains(string(schedulePostRaw), `"isError":true`) {
		t.Fatalf("expected schedule post tool call without isError=true, got: %s", string(schedulePostRaw))
	}
	afterSchedule, err := store.GetPost(t.Context(), draftID)
	if err != nil {
		t.Fatalf("get scheduled post: %v", err)
	}
	if afterSchedule.Status != domain.PostStatusScheduled {
		t.Fatalf("expected scheduled status after scheduling tool, got %s", afterSchedule.Status)
	}

	rescheduledAt := time.Now().UTC().Add(15 * time.Minute).Format(time.RFC3339)
	editPostBody := `{"jsonrpc":"2.0","id":4.7,"method":"tools/call","params":{"name":"publisher_edit_post","arguments":{"post_id":"` + draftID + `","text":"edited from mcp tool","intent":"schedule","scheduled_at":"` + rescheduledAt + `"}}}`
	editPostResp, editPostRaw := postMCPRequest(t, mcpURL, sessionID, editPostBody)
	if editPostResp.StatusCode != http.StatusOK {
		t.Fatalf("expected edit post tools/call status 200, got %d: %s", editPostResp.StatusCode, string(editPostRaw))
	}
	if strings.Contains(string(editPostRaw), `"isError":true`) {
		t.Fatalf("expected edit post tool call without isError=true, got: %s", string(editPostRaw))
	}
	afterEdit, err := store.GetPost(t.Context(), draftID)
	if err != nil {
		t.Fatalf("get edited post: %v", err)
	}
	if strings.TrimSpace(afterEdit.Text) != "edited from mcp tool" {
		t.Fatalf("expected edited text, got %q", afterEdit.Text)
	}

	createDeletablePostBody := `{"jsonrpc":"2.0","id":4.8,"method":"tools/call","params":{"name":"publisher_create_post","arguments":{"account_id":"` + account.ID + `","text":"delete from mcp tool"}}}`
	createDeletablePostResp, createDeletablePostRaw := postMCPRequest(t, mcpURL, sessionID, createDeletablePostBody)
	if createDeletablePostResp.StatusCode != http.StatusOK {
		t.Fatalf("expected deletable create tools/call status 200, got %d: %s", createDeletablePostResp.StatusCode, string(createDeletablePostRaw))
	}
	if strings.Contains(string(createDeletablePostRaw), `"isError":true`) {
		t.Fatalf("expected deletable create tool call without isError=true, got: %s", string(createDeletablePostRaw))
	}
	drafts, err = store.ListDrafts(t.Context())
	if err != nil {
		t.Fatalf("list drafts after creating deletable: %v", err)
	}
	deleteDraftID := ""
	for _, draft := range drafts {
		if strings.TrimSpace(draft.Text) == "delete from mcp tool" {
			deleteDraftID = strings.TrimSpace(draft.ID)
			break
		}
	}
	if deleteDraftID == "" {
		t.Fatalf("expected to find deletable draft id")
	}
	deletePostBody := `{"jsonrpc":"2.0","id":4.9,"method":"tools/call","params":{"name":"publisher_delete_post","arguments":{"post_id":"` + deleteDraftID + `"}}}`
	deletePostResp, deletePostRaw := postMCPRequest(t, mcpURL, sessionID, deletePostBody)
	if deletePostResp.StatusCode != http.StatusOK {
		t.Fatalf("expected delete post tools/call status 200, got %d: %s", deletePostResp.StatusCode, string(deletePostRaw))
	}
	if strings.Contains(string(deletePostRaw), `"isError":true`) {
		t.Fatalf("expected delete post tool call without isError=true, got: %s", string(deletePostRaw))
	}
	if !strings.Contains(string(deletePostRaw), `"deleted":true`) {
		t.Fatalf("expected delete post response with deleted=true, got: %s", string(deletePostRaw))
	}
	if _, err := store.GetPost(t.Context(), deleteDraftID); err == nil {
		t.Fatalf("expected deleted post %q to be absent", deleteDraftID)
	}

	validateBody := `{"jsonrpc":"2.0","id":4.5,"method":"tools/call","params":{"name":"publisher_validate_post","arguments":{"account_id":"` + account.ID + `","text":"validate from mcp tool"}}}`
	validateResp, validateRaw := postMCPRequest(t, mcpURL, sessionID, validateBody)
	if validateResp.StatusCode != http.StatusOK {
		t.Fatalf("expected validate tools/call status 200, got %d: %s", validateResp.StatusCode, string(validateRaw))
	}
	if strings.Contains(string(validateRaw), `"isError":true`) {
		t.Fatalf("expected validate tool call without isError=true, got: %s", string(validateRaw))
	}
	if !strings.Contains(string(validateRaw), `"valid":true`) {
		t.Fatalf("expected validate tool response to confirm valid payload: %s", string(validateRaw))
	}

	listMediaBody := `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"publisher_list_media","arguments":{"limit":50}}}`
	listMediaResp, listMediaRaw := postMCPRequest(t, mcpURL, sessionID, listMediaBody)
	if listMediaResp.StatusCode != http.StatusOK {
		t.Fatalf("expected list media status 200, got %d: %s", listMediaResp.StatusCode, string(listMediaRaw))
	}
	if strings.Contains(string(listMediaRaw), `"isError":true`) {
		t.Fatalf("expected list media tool call without isError=true, got: %s", string(listMediaRaw))
	}
	if !strings.Contains(string(listMediaRaw), mediaID) {
		t.Fatalf("expected media list to include uploaded media id %q", mediaID)
	}

	uploadDeletableBody := `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"publisher_upload_media","arguments":{"kind":"image","original_name":"delete-me.txt","content_base64":"ZGVsZXRlLW1l"}}}`
	uploadDeletableResp, uploadDeletableRaw := postMCPRequest(t, mcpURL, sessionID, uploadDeletableBody)
	if uploadDeletableResp.StatusCode != http.StatusOK {
		t.Fatalf("expected second upload tools/call status 200, got %d: %s", uploadDeletableResp.StatusCode, string(uploadDeletableRaw))
	}
	if strings.Contains(string(uploadDeletableRaw), `"isError":true`) {
		t.Fatalf("expected second upload tool call without isError=true, got: %s", string(uploadDeletableRaw))
	}
	deletableMediaID := extractToolStructuredString(uploadDeletableRaw, "media_id")
	if deletableMediaID == "" {
		t.Fatalf("expected media_id in second upload tool response: %s", string(uploadDeletableRaw))
	}

	deleteBody := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"publisher_delete_media","arguments":{"media_id":"` + deletableMediaID + `"}}}`
	deleteResp, deleteRaw := postMCPRequest(t, mcpURL, sessionID, deleteBody)
	if deleteResp.StatusCode != http.StatusOK {
		t.Fatalf("expected delete media status 200, got %d: %s", deleteResp.StatusCode, string(deleteRaw))
	}
	if strings.Contains(string(deleteRaw), `"isError":true`) {
		t.Fatalf("expected delete media tool call without isError=true, got: %s", string(deleteRaw))
	}
	if !strings.Contains(string(deleteRaw), `"deleted":true`) {
		t.Fatalf("expected delete tool response to confirm deletion: %s", string(deleteRaw))
	}

	toFail, err := store.CreatePost(t.Context(), db.CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Platform:    account.Platform,
			Text:        "should fail once",
			Status:      domain.PostStatusScheduled,
			ScheduledAt: time.Now().UTC(),
			MaxAttempts: 1,
		},
	})
	if err != nil {
		t.Fatalf("create failing post: %v", err)
	}
	if err := store.RecordPublishFailure(t.Context(), toFail.Post.ID, errors.New("synthetic failure"), time.Second); err != nil {
		t.Fatalf("record publish failure: %v", err)
	}
	dlqItems, err := store.ListDeadLetters(t.Context(), 10)
	if err != nil {
		t.Fatalf("list dead letters: %v", err)
	}
	if len(dlqItems) == 0 {
		t.Fatalf("expected at least one dead letter after failure")
	}
	failedID := dlqItems[0].ID

	requeueBody := `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"publisher_requeue_failed","arguments":{"dead_letter_id":"` + failedID + `"}}}`
	requeueResp, requeueRaw := postMCPRequest(t, mcpURL, sessionID, requeueBody)
	if requeueResp.StatusCode != http.StatusOK {
		t.Fatalf("expected requeue failed status 200, got %d: %s", requeueResp.StatusCode, string(requeueRaw))
	}
	if strings.Contains(string(requeueRaw), `"isError":true`) {
		t.Fatalf("expected requeue failed tool call without isError=true, got: %s", string(requeueRaw))
	}

	toDelete, err := store.CreatePost(t.Context(), db.CreatePostParams{
		Post: domain.Post{
			AccountID:   account.ID,
			Platform:    account.Platform,
			Text:        "should fail and delete",
			Status:      domain.PostStatusScheduled,
			ScheduledAt: time.Now().UTC(),
			MaxAttempts: 1,
		},
	})
	if err != nil {
		t.Fatalf("create second failing post: %v", err)
	}
	if err := store.RecordPublishFailure(t.Context(), toDelete.Post.ID, errors.New("synthetic failure 2"), time.Second); err != nil {
		t.Fatalf("record second publish failure: %v", err)
	}
	dlqItems, err = store.ListDeadLetters(t.Context(), 10)
	if err != nil {
		t.Fatalf("list dead letters second time: %v", err)
	}
	if len(dlqItems) == 0 {
		t.Fatalf("expected dead letter to delete")
	}
	deleteFailedID := dlqItems[0].ID
	deleteFailedBody := `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"publisher_delete_failed","arguments":{"dead_letter_id":"` + deleteFailedID + `"}}}`
	deleteFailedResp, deleteFailedRaw := postMCPRequest(t, mcpURL, sessionID, deleteFailedBody)
	if deleteFailedResp.StatusCode != http.StatusOK {
		t.Fatalf("expected delete failed status 200, got %d: %s", deleteFailedResp.StatusCode, string(deleteFailedRaw))
	}
	if strings.Contains(string(deleteFailedRaw), `"isError":true`) {
		t.Fatalf("expected delete failed tool call without isError=true, got: %s", string(deleteFailedRaw))
	}
	if !strings.Contains(string(deleteFailedRaw), `"deleted":true`) {
		t.Fatalf("expected delete failed response with deleted=true, got: %s", string(deleteFailedRaw))
	}
}

func TestMCPInitializeAcceptJSONOnly(t *testing.T) {
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
	initializeResp, initializeRaw := postMCPRequestWithAccept(t, mcpURL, "", initializeBody, "application/json")
	if initializeResp.StatusCode != http.StatusOK {
		t.Fatalf("expected initialize status 200, got %d: %s", initializeResp.StatusCode, string(initializeRaw))
	}
	if strings.TrimSpace(initializeResp.Header.Get("Mcp-Session-Id")) == "" {
		t.Fatalf("expected initialize response to include MCP session id")
	}
	if !strings.Contains(string(initializeRaw), "publisher-mcp") {
		t.Fatalf("expected initialize response to include publisher server info")
	}
}

func TestMCPUploadMediaRejectsFilePath(t *testing.T) {
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
	if sessionID == "" {
		t.Fatalf("expected initialize response to include MCP session id")
	}

	uploadBody := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"publisher_upload_media","arguments":{"kind":"image","file_path":"/tmp/media.png"}}}`
	uploadResp, uploadRaw := postMCPRequest(t, mcpURL, sessionID, uploadBody)
	if uploadResp.StatusCode != http.StatusOK {
		t.Fatalf("expected tools/call status 200, got %d: %s", uploadResp.StatusCode, string(uploadRaw))
	}
	if !strings.Contains(string(uploadRaw), `"isError":true`) {
		t.Fatalf("expected upload media tool call to return isError=true, got: %s", string(uploadRaw))
	}
	if !strings.Contains(string(uploadRaw), "file_path is not supported in MCP; use content_base64") {
		t.Fatalf("expected upload error message to explain file_path is unsupported, got: %s", string(uploadRaw))
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
		AppVersion:        "v9.9.9-test",
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
	if !strings.Contains(body, "version: v9.9.9-test") {
		t.Fatalf("expected app version in settings")
	}
}

func postMCPRequest(t *testing.T, endpoint, sessionID, payload string) (*http.Response, []byte) {
	return postMCPRequestWithAccept(t, endpoint, sessionID, payload, "application/json, text/event-stream")
}

func postMCPRequestWithAccept(t *testing.T, endpoint, sessionID, payload, accept string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewBufferString(payload))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", accept)
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
