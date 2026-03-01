package parity_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
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

func (e *parityEnv) mcpCreatePost(text string) string {
	e.t.Helper()
	out := e.mcpCallTool("publisher_create_post", map[string]any{"account_id": e.account.ID, "text": text})
	post, _ := out["post"].(map[string]any)
	return strings.TrimSpace(stringValue(post, "id"))
}

func (e *parityEnv) mcpValidatePost(text string) bool {
	e.t.Helper()
	out := e.mcpCallTool("publisher_validate_post", map[string]any{"account_id": e.account.ID, "text": text})
	valid, _ := out["valid"].(bool)
	return valid
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
