package parity_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (e *parityEnv) mcpInitialize() string {
	e.t.Helper()
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name":    "parity-test",
				"version": "1.0.0",
			},
		},
	}
	raw, status, headers := e.mcpPost(payload)
	if status != http.StatusOK {
		e.t.Fatalf("mcp initialize status=%d body=%s", status, string(raw))
	}
	session := strings.TrimSpace(headers.Get("Mcp-Session-Id"))
	if session == "" {
		e.t.Fatalf("missing Mcp-Session-Id in initialize response")
	}
	return session
}

func (e *parityEnv) mcpCallTool(name string, args map[string]any) map[string]any {
	e.t.Helper()
	e.nextReq++
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      e.nextReq,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	}
	raw, status, _ := e.mcpPost(payload)
	if status != http.StatusOK {
		e.t.Fatalf("mcp call %s status=%d body=%s", name, status, string(raw))
	}
	var out struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Result struct {
			IsError           bool           `json:"isError"`
			StructuredContent map[string]any `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		e.t.Fatalf("decode mcp response for %s: %v (raw=%s)", name, err, string(raw))
	}
	if out.Error != nil {
		e.t.Fatalf("mcp call %s returned error: %s", name, strings.TrimSpace(out.Error.Message))
	}
	if out.Result.IsError {
		e.t.Fatalf("mcp call %s returned isError=true: %s", name, string(raw))
	}
	if out.Result.StructuredContent == nil {
		e.t.Fatalf("mcp call %s missing structuredContent", name)
	}
	return out.Result.StructuredContent
}

func (e *parityEnv) mcpCallToolError(name string, args map[string]any) string {
	e.t.Helper()
	e.nextReq++
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      e.nextReq,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	}
	raw, status, _ := e.mcpPost(payload)
	if status != http.StatusOK {
		return strings.TrimSpace(fmt.Sprintf("http status %d: %s", status, string(raw)))
	}
	var out struct {
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
	if err := json.Unmarshal(raw, &out); err != nil {
		return strings.TrimSpace(err.Error())
	}
	if out.Error != nil {
		return strings.TrimSpace(out.Error.Message)
	}
	if out.Result.IsError {
		if len(out.Result.Content) > 0 && strings.TrimSpace(out.Result.Content[0].Text) != "" {
			return strings.TrimSpace(out.Result.Content[0].Text)
		}
		if msg := stringValue(out.Result.StructuredContent, "error"); strings.TrimSpace(msg) != "" {
			return strings.TrimSpace(msg)
		}
		return "tool returned isError=true"
	}
	return ""
}

func (e *parityEnv) mcpScheduleListIDs(from, to string) []string {
	e.t.Helper()
	out := e.mcpCallTool("publisher_list_schedule", map[string]any{"from": from, "to": to, "limit": 200})
	posts, _ := out["posts"].([]any)
	ids := make([]string, 0, len(posts))
	for _, item := range posts {
		obj, _ := item.(map[string]any)
		ids = append(ids, strings.TrimSpace(stringValue(obj, "id")))
	}
	return ids
}

func (e *parityEnv) mcpHealthStatus() string {
	e.t.Helper()
	out := e.mcpCallTool("publisher_health", map[string]any{})
	return strings.TrimSpace(stringValue(out, "status"))
}

func (e *parityEnv) mcpDraftListIDs(limit int) []string {
	e.t.Helper()
	out := e.mcpCallTool("publisher_list_drafts", map[string]any{"limit": limit})
	drafts, _ := out["drafts"].([]any)
	ids := make([]string, 0, len(drafts))
	for _, item := range drafts {
		obj, _ := item.(map[string]any)
		ids = append(ids, strings.TrimSpace(stringValue(obj, "id")))
	}
	return ids
}

func (e *parityEnv) mcpCreatePost(text string) string {
	e.t.Helper()
	return e.mcpCreatePostForAccount(e.account.ID, text, nil)
}

func (e *parityEnv) mcpCreatePostForAccount(accountID, text string, mediaIDs []string) string {
	e.t.Helper()
	args := map[string]any{
		"account_id": strings.TrimSpace(accountID),
		"text":       text,
	}
	if len(mediaIDs) > 0 {
		args["media_ids"] = mediaIDs
	}
	out := e.mcpCallTool("publisher_create_post", args)
	post, _ := out["post"].(map[string]any)
	return strings.TrimSpace(stringValue(post, "id"))
}

func (e *parityEnv) mcpCreateThread(segments []map[string]any) []string {
	e.t.Helper()
	rootText := ""
	if len(segments) > 0 {
		rootText = strings.TrimSpace(stringValue(segments[0], "text"))
	}
	out := e.mcpCallTool("publisher_create_post", map[string]any{
		"account_id": e.account.ID,
		"text":       rootText,
		"segments":   segments,
	})
	if post, ok := out["post"].(map[string]any); ok {
		postID := strings.TrimSpace(stringValue(post, "id"))
		if postID != "" {
			return []string{postID}
		}
	}
	items, _ := out["items"].([]any)
	ids := make([]string, 0, len(items))
	for _, rawItem := range items {
		item, _ := rawItem.(map[string]any)
		postID := strings.TrimSpace(stringValue(item, "id"))
		if postID == "" {
			continue
		}
		ids = append(ids, postID)
	}
	if len(ids) == 0 {
		e.t.Fatalf("expected thread post ids in mcp response")
	}
	return ids
}

func (e *parityEnv) mcpValidatePost(text string) bool {
	e.t.Helper()
	out := e.mcpCallTool("publisher_validate_post", map[string]any{"account_id": e.account.ID, "text": text})
	valid, _ := out["valid"].(bool)
	return valid
}

func (e *parityEnv) mcpValidateThread(segments []map[string]any) bool {
	e.t.Helper()
	rootText := ""
	if len(segments) > 0 {
		rootText = strings.TrimSpace(stringValue(segments[0], "text"))
	}
	out := e.mcpCallTool("publisher_validate_post", map[string]any{
		"account_id": e.account.ID,
		"text":       rootText,
		"segments":   segments,
	})
	valid, _ := out["valid"].(bool)
	return valid
}

func (e *parityEnv) mcpCancelPost(id string) {
	e.t.Helper()
	_ = e.mcpCallTool("publisher_cancel_post", map[string]any{"post_id": strings.TrimSpace(id)})
}

func (e *parityEnv) mcpSchedulePost(id string, scheduledAt time.Time) {
	e.t.Helper()
	_ = e.mcpCallTool("publisher_schedule_post", map[string]any{
		"post_id":      strings.TrimSpace(id),
		"scheduled_at": scheduledAt.UTC().Format(time.RFC3339),
	})
}

func (e *parityEnv) mcpEditPost(id, text, intent string, scheduledAt time.Time) {
	e.t.Helper()
	e.mcpEditPostWithMedia(id, text, intent, scheduledAt, nil, false)
}

func (e *parityEnv) mcpEditPostWithMedia(id, text, intent string, scheduledAt time.Time, mediaIDs []string, includeMediaIDs bool) {
	e.t.Helper()
	args := map[string]any{
		"post_id": strings.TrimSpace(id),
		"text":    strings.TrimSpace(text),
	}
	if strings.TrimSpace(intent) != "" {
		args["intent"] = strings.TrimSpace(intent)
	}
	if !scheduledAt.IsZero() {
		args["scheduled_at"] = scheduledAt.UTC().Format(time.RFC3339)
	}
	if includeMediaIDs {
		args["media_ids"] = append([]string{}, mediaIDs...)
	}
	_ = e.mcpCallTool("publisher_edit_post", args)
}

func (e *parityEnv) mcpDeletePost(id string) {
	e.t.Helper()
	_ = e.mcpCallTool("publisher_delete_post", map[string]any{"post_id": strings.TrimSpace(id)})
}

func (e *parityEnv) mcpListAccountIDs() []string {
	e.t.Helper()
	out := e.mcpCallTool("publisher_list_accounts", map[string]any{})
	items, _ := out["items"].([]any)
	ids := make([]string, 0, len(items))
	for _, item := range items {
		obj, _ := item.(map[string]any)
		ids = append(ids, strings.TrimSpace(stringValue(obj, "id")))
	}
	return ids
}

func (e *parityEnv) mcpCreateStaticAccount(platform, externalID string, credentials map[string]any) string {
	e.t.Helper()
	out := e.mcpCallTool("publisher_create_static_account", map[string]any{
		"platform":            strings.TrimSpace(platform),
		"display_name":        "MCP " + strings.TrimSpace(platform),
		"external_account_id": strings.TrimSpace(externalID),
		"credentials":         credentials,
	})
	account, _ := out["account"].(map[string]any)
	return strings.TrimSpace(stringValue(account, "id"))
}

func (e *parityEnv) mcpConnectAccount(id string) {
	e.t.Helper()
	_ = e.mcpCallTool("publisher_connect_account", map[string]any{"account_id": strings.TrimSpace(id)})
}

func (e *parityEnv) mcpDisconnectAccount(id string) {
	e.t.Helper()
	_ = e.mcpCallTool("publisher_disconnect_account", map[string]any{"account_id": strings.TrimSpace(id)})
}

func (e *parityEnv) mcpSetXPremium(id string, enabled bool) {
	e.t.Helper()
	_ = e.mcpCallTool("publisher_set_x_premium", map[string]any{"account_id": strings.TrimSpace(id), "x_premium": enabled})
}

func (e *parityEnv) mcpDeleteAccount(id string) {
	e.t.Helper()
	_ = e.mcpCallTool("publisher_delete_account", map[string]any{"account_id": strings.TrimSpace(id)})
}

func (e *parityEnv) mcpSetTimezone(timezone string) {
	e.t.Helper()
	_ = e.mcpCallTool("publisher_set_timezone", map[string]any{"timezone": strings.TrimSpace(timezone)})
}

func (e *parityEnv) mcpFailedListIDs() []string {
	e.t.Helper()
	out := e.mcpCallTool("publisher_list_failed", map[string]any{"limit": 200})
	items, _ := out["items"].([]any)
	ids := make([]string, 0, len(items))
	for _, item := range items {
		obj, _ := item.(map[string]any)
		ids = append(ids, strings.TrimSpace(stringValue(obj, "dead_letter_id")))
	}
	return ids
}

func (e *parityEnv) mcpRequeueDLQ(id string) {
	e.t.Helper()
	_ = e.mcpCallTool("publisher_requeue_failed", map[string]any{"dead_letter_id": id})
}
func (e *parityEnv) mcpDeleteDLQ(id string) {
	e.t.Helper()
	_ = e.mcpCallTool("publisher_delete_failed", map[string]any{"dead_letter_id": id})
}

func (e *parityEnv) mcpUploadMedia(content string) string {
	e.t.Helper()
	out := e.mcpCallTool("publisher_upload_media", map[string]any{
		"kind":           "image",
		"original_name":  "mcp.bin",
		"content_base64": base64.StdEncoding.EncodeToString([]byte(content)),
	})
	return strings.TrimSpace(stringValue(out, "media_id"))
}

func (e *parityEnv) mcpListMediaIDs() []string {
	e.t.Helper()
	out := e.mcpCallTool("publisher_list_media", map[string]any{"limit": 200})
	items, _ := out["items"].([]any)
	ids := make([]string, 0, len(items))
	for _, item := range items {
		obj, _ := item.(map[string]any)
		ids = append(ids, strings.TrimSpace(stringValue(obj, "media_id")))
	}
	return ids
}

func (e *parityEnv) mcpDeleteMedia(id string) {
	e.t.Helper()
	_ = e.mcpCallTool("publisher_delete_media", map[string]any{"media_id": id})
}

func (e *parityEnv) mcpPost(payload map[string]any) ([]byte, int, http.Header) {
	e.t.Helper()
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		e.t.Fatalf("marshal mcp payload: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, e.baseURL+"/mcp", bytes.NewReader(rawPayload))
	if err != nil {
		e.t.Fatalf("build mcp request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+e.token)
	if strings.TrimSpace(e.session) != "" {
		req.Header.Set("Mcp-Session-Id", e.session)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("mcp request failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return normalizeMCPResponseBody(body), resp.StatusCode, resp.Header
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

func stringValue(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	default:
		return ""
	}
}
