package parity_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type parityThreadPost struct {
	ID             string `json:"id"`
	Text           string `json:"text"`
	ThreadGroupID  string `json:"thread_group_id,omitempty"`
	ThreadPosition int    `json:"thread_position,omitempty"`
	ParentPostID   string `json:"parent_post_id,omitempty"`
	RootPostID     string `json:"root_post_id,omitempty"`
}

type parityThreadCreateOutput struct {
	RootID     string             `json:"root_id,omitempty"`
	TotalSteps int                `json:"total_steps,omitempty"`
	Post       parityThreadPost   `json:"post"`
	Items      []parityThreadPost `json:"items"`
}

func (e *parityEnv) apiCreateThreadDetailed(segments []map[string]any) parityThreadCreateOutput {
	e.t.Helper()
	body := map[string]any{
		"account_id": e.account.ID,
		"segments":   segments,
	}
	raw, status := e.apiJSON(http.MethodPost, "/posts", body, "application/json")
	if status != http.StatusCreated && status != http.StatusOK {
		e.t.Fatalf("create thread status=%d body=%s", status, string(raw))
	}
	var out parityThreadCreateOutput
	mustJSON(e.t, raw, &out)
	return out
}

func (e *parityEnv) apiCreateScheduledThreadDetailed(segments []map[string]any, scheduledAt time.Time) parityThreadCreateOutput {
	e.t.Helper()
	body := map[string]any{
		"account_id":   e.account.ID,
		"segments":     segments,
		"scheduled_at": scheduledAt.UTC().Format(time.RFC3339),
	}
	raw, status := e.apiJSON(http.MethodPost, "/posts", body, "application/json")
	if status != http.StatusCreated && status != http.StatusOK {
		e.t.Fatalf("create scheduled thread status=%d body=%s", status, string(raw))
	}
	var out parityThreadCreateOutput
	mustJSON(e.t, raw, &out)
	return out
}

func (e *parityEnv) apiEditThread(id string, segments []map[string]any) {
	e.t.Helper()
	raw, status := e.apiJSON(http.MethodPost, "/posts/"+strings.TrimSpace(id)+"/edit", map[string]any{
		"segments": segments,
	}, "application/json")
	if status != http.StatusOK {
		e.t.Fatalf("edit thread status=%d body=%s", status, string(raw))
	}
}

func (e *parityEnv) apiScheduleListPostsDetailed(from, to string) []parityThreadPost {
	e.t.Helper()
	path := "/schedule?from=" + encodeQueryValue(from) + "&to=" + encodeQueryValue(to)
	raw, status := e.apiJSON(http.MethodGet, path, nil, "")
	if status != http.StatusOK {
		e.t.Fatalf("schedule list status=%d body=%s", status, string(raw))
	}
	var out struct {
		Items []parityThreadPost `json:"items"`
	}
	mustJSON(e.t, raw, &out)
	return out.Items
}

func (e *parityEnv) cliCreateThreadDetailed(segmentsJSON string) parityThreadCreateOutput {
	e.t.Helper()
	raw := e.runCLI("posts", "create", "--account-id", e.account.ID, "--segments-json", strings.TrimSpace(segmentsJSON))
	var out parityThreadCreateOutput
	mustJSON(e.t, raw, &out)
	return out
}

func (e *parityEnv) cliEditThread(id, segmentsJSON string) {
	e.t.Helper()
	_ = e.runCLI("posts", "edit", "--id", strings.TrimSpace(id), "--segments-json", strings.TrimSpace(segmentsJSON))
}

func (e *parityEnv) cliScheduleListPostsDetailed(from, to string) []parityThreadPost {
	e.t.Helper()
	raw := e.runCLI("schedule", "list", "--from", from, "--to", to)
	var out struct {
		Items []parityThreadPost `json:"items"`
	}
	mustJSON(e.t, raw, &out)
	return out.Items
}

func (e *parityEnv) mcpCreateThreadDetailed(segments []map[string]any) parityThreadCreateOutput {
	e.t.Helper()
	rootText := ""
	if len(segments) > 0 {
		rootText = strings.TrimSpace(stringValue(segments[0], "text"))
	}
	out := e.mcpCallTool("postflow_create_post", map[string]any{
		"account_id": e.account.ID,
		"text":       rootText,
		"segments":   segments,
	})
	return decodeMCPThreadCreateOutput(e, out)
}

func (e *parityEnv) mcpEditThread(id string, segments []map[string]any) {
	e.t.Helper()
	args := map[string]any{
		"post_id":  strings.TrimSpace(id),
		"segments": segments,
	}
	if len(segments) > 0 {
		args["text"] = strings.TrimSpace(stringValue(segments[0], "text"))
	}
	_ = e.mcpCallTool("postflow_edit_post", args)
}

func (e *parityEnv) mcpScheduleListPostsDetailed(from, to string) []parityThreadPost {
	e.t.Helper()
	out := e.mcpCallTool("postflow_list_schedule", map[string]any{"from": from, "to": to, "limit": 200})
	return decodeMCPThreadPosts(out["posts"])
}

func decodeMCPThreadCreateOutput(e *parityEnv, out map[string]any) parityThreadCreateOutput {
	e.t.Helper()
	result := parityThreadCreateOutput{
		RootID:     strings.TrimSpace(stringValue(out, "root_id")),
		TotalSteps: intValue(out, "total_steps"),
	}
	if post, ok := out["post"].(map[string]any); ok {
		result.Post = decodeMCPThreadPost(post)
		if result.RootID == "" {
			result.RootID = strings.TrimSpace(result.Post.ID)
		}
	}
	result.Items = decodeMCPThreadPosts(out["items"])
	if len(result.Items) == 0 && strings.TrimSpace(result.Post.ID) != "" {
		result.Items = []parityThreadPost{result.Post}
	}
	if result.TotalSteps == 0 {
		result.TotalSteps = len(result.Items)
	}
	return result
}

func decodeMCPThreadPosts(raw any) []parityThreadPost {
	items, _ := raw.([]any)
	out := make([]parityThreadPost, 0, len(items))
	for _, entry := range items {
		obj, _ := entry.(map[string]any)
		if obj == nil {
			continue
		}
		out = append(out, decodeMCPThreadPost(obj))
	}
	return out
}

func decodeMCPThreadPost(obj map[string]any) parityThreadPost {
	return parityThreadPost{
		ID:             strings.TrimSpace(stringValue(obj, "id")),
		Text:           strings.TrimSpace(stringValue(obj, "text")),
		ThreadGroupID:  strings.TrimSpace(stringValue(obj, "thread_group_id")),
		ThreadPosition: intValue(obj, "thread_position"),
		ParentPostID:   strings.TrimSpace(stringValue(obj, "parent_post_id")),
		RootPostID:     strings.TrimSpace(stringValue(obj, "root_post_id")),
	}
}

func intValue(obj map[string]any, key string) int {
	raw, ok := obj[key]
	if !ok {
		return 0
	}
	switch value := raw.(type) {
	case float64:
		return int(value)
	case float32:
		return int(value)
	case int:
		return value
	case int64:
		return int(value)
	case json.Number:
		parsed, err := value.Int64()
		if err == nil {
			return int(parsed)
		}
	}
	return 0
}

func encodeQueryValue(raw string) string {
	return url.QueryEscape(strings.TrimSpace(raw))
}
