package parity_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (e *parityEnv) apiCreatePost(text string, scheduledAt time.Time, mediaIDs []string) string {
	e.t.Helper()
	body := map[string]any{"account_id": e.account.ID, "text": text}
	if !scheduledAt.IsZero() {
		body["scheduled_at"] = scheduledAt.UTC().Format(time.RFC3339)
	}
	if len(mediaIDs) > 0 {
		body["media_ids"] = mediaIDs
	}
	raw, status := e.apiJSON(http.MethodPost, "/posts", body, "application/json")
	if status != http.StatusCreated && status != http.StatusOK {
		e.t.Fatalf("create post status=%d body=%s", status, string(raw))
	}
	var out struct {
		ID string `json:"id"`
	}
	mustJSON(e.t, raw, &out)
	if strings.TrimSpace(out.ID) == "" {
		e.t.Fatalf("expected post id in create response")
	}
	return out.ID
}

func (e *parityEnv) apiValidatePost(text string) bool {
	e.t.Helper()
	raw, status := e.apiJSON(http.MethodPost, "/posts/validate", map[string]any{"account_id": e.account.ID, "text": text}, "application/json")
	if status != http.StatusOK {
		e.t.Fatalf("validate post status=%d body=%s", status, string(raw))
	}
	var out struct {
		Valid bool `json:"valid"`
	}
	mustJSON(e.t, raw, &out)
	return out.Valid
}

func (e *parityEnv) apiScheduleListIDs(from, to string) []string {
	e.t.Helper()
	path := "/schedule?from=" + url.QueryEscape(from) + "&to=" + url.QueryEscape(to)
	raw, status := e.apiJSON(http.MethodGet, path, nil, "")
	if status != http.StatusOK {
		e.t.Fatalf("schedule list status=%d body=%s", status, string(raw))
	}
	var out struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	mustJSON(e.t, raw, &out)
	ids := make([]string, 0, len(out.Items))
	for _, item := range out.Items {
		ids = append(ids, strings.TrimSpace(item.ID))
	}
	return ids
}

func (e *parityEnv) apiDLQListIDs() []string {
	e.t.Helper()
	raw, status := e.apiJSON(http.MethodGet, "/dlq?limit=200", nil, "")
	if status != http.StatusOK {
		e.t.Fatalf("dlq list status=%d body=%s", status, string(raw))
	}
	var out struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	mustJSON(e.t, raw, &out)
	ids := make([]string, 0, len(out.Items))
	for _, item := range out.Items {
		ids = append(ids, strings.TrimSpace(item.ID))
	}
	return ids
}

func (e *parityEnv) apiRequeueDLQ(id string) {
	e.t.Helper()
	raw, status := e.apiJSON(http.MethodPost, "/dlq/"+strings.TrimSpace(id)+"/requeue", nil, "application/json")
	if status != http.StatusOK {
		e.t.Fatalf("requeue dlq status=%d body=%s", status, string(raw))
	}
}

func (e *parityEnv) apiDeleteDLQ(id string) {
	e.t.Helper()
	raw, status := e.apiJSON(http.MethodPost, "/dlq/"+strings.TrimSpace(id)+"/delete", nil, "application/json")
	if status != http.StatusOK {
		e.t.Fatalf("delete dlq status=%d body=%s", status, string(raw))
	}
}

func (e *parityEnv) apiUploadMedia(filePath string) string {
	e.t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("kind", "image")
	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		e.t.Fatalf("create file part: %v", err)
	}
	content, err := os.ReadFile(filePath)
	if err != nil {
		e.t.Fatalf("read media file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		e.t.Fatalf("write media part: %v", err)
	}
	if err := writer.Close(); err != nil {
		e.t.Fatalf("close multipart writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, e.baseURL+"/media", &body)
	if err != nil {
		e.t.Fatalf("build upload request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("upload media request failed: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		e.t.Fatalf("upload media status=%d body=%s", resp.StatusCode, string(raw))
	}
	var out struct {
		ID string `json:"id"`
	}
	mustJSON(e.t, raw, &out)
	return strings.TrimSpace(out.ID)
}

func (e *parityEnv) apiListMediaIDs() []string {
	e.t.Helper()
	raw, status := e.apiJSON(http.MethodGet, "/media?limit=200", nil, "")
	if status != http.StatusOK {
		e.t.Fatalf("list media status=%d body=%s", status, string(raw))
	}
	var out struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	mustJSON(e.t, raw, &out)
	ids := make([]string, 0, len(out.Items))
	for _, item := range out.Items {
		ids = append(ids, strings.TrimSpace(item.ID))
	}
	return ids
}

func (e *parityEnv) apiDeleteMedia(id string) {
	e.t.Helper()
	raw, status := e.apiJSON(http.MethodDelete, "/media/"+strings.TrimSpace(id), nil, "")
	if status != http.StatusOK {
		e.t.Fatalf("delete media status=%d body=%s", status, string(raw))
	}
}

func (e *parityEnv) cliScheduleListIDs(from, to string) []string {
	e.t.Helper()
	raw := e.runCLI("schedule", "list", "--from", from, "--to", to)
	var out struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	mustJSON(e.t, raw, &out)
	ids := make([]string, 0, len(out.Items))
	for _, item := range out.Items {
		ids = append(ids, strings.TrimSpace(item.ID))
	}
	return ids
}

func (e *parityEnv) cliCreatePost(text string) string {
	e.t.Helper()
	raw := e.runCLI("posts", "create", "--account-id", e.account.ID, "--text", text)
	var out struct {
		ID string `json:"id"`
	}
	mustJSON(e.t, raw, &out)
	return strings.TrimSpace(out.ID)
}

func (e *parityEnv) cliValidatePost(text string) bool {
	e.t.Helper()
	raw := e.runCLI("posts", "validate", "--account-id", e.account.ID, "--text", text)
	var out struct {
		Valid bool `json:"valid"`
	}
	mustJSON(e.t, raw, &out)
	return out.Valid
}

func (e *parityEnv) cliDLQListIDs() []string {
	e.t.Helper()
	raw := e.runCLI("dlq", "list", "--limit", "200")
	var out struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	mustJSON(e.t, raw, &out)
	ids := make([]string, 0, len(out.Items))
	for _, item := range out.Items {
		ids = append(ids, strings.TrimSpace(item.ID))
	}
	return ids
}

func (e *parityEnv) cliRequeueDLQ(id string) {
	e.t.Helper()
	_ = e.runCLI("dlq", "requeue", "--id", id)
}
func (e *parityEnv) cliDeleteDLQ(id string) { e.t.Helper(); _ = e.runCLI("dlq", "delete", "--id", id) }

func (e *parityEnv) cliUploadMedia(filePath string) string {
	e.t.Helper()
	raw := e.runCLI("media", "upload", "--file", filePath, "--kind", "image")
	var out struct {
		ID string `json:"id"`
	}
	mustJSON(e.t, raw, &out)
	return strings.TrimSpace(out.ID)
}

func (e *parityEnv) cliListMediaIDs() []string {
	e.t.Helper()
	raw := e.runCLI("media", "list", "--limit", "200")
	var out struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	mustJSON(e.t, raw, &out)
	ids := make([]string, 0, len(out.Items))
	for _, item := range out.Items {
		ids = append(ids, strings.TrimSpace(item.ID))
	}
	return ids
}

func (e *parityEnv) cliDeleteMedia(id string) {
	e.t.Helper()
	_ = e.runCLI("media", "delete", "--id", id)
}

func (e *parityEnv) apiJSON(method, path string, body any, contentType string) ([]byte, int) {
	e.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			e.t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, e.baseURL+path, reader)
	if err != nil {
		e.t.Fatalf("build request: %v", err)
	}
	if strings.TrimSpace(contentType) != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("http request failed: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return raw, resp.StatusCode
}
