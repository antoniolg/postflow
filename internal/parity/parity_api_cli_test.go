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
	"strconv"
	"strings"
	"time"

	"github.com/antoniolg/postflow/internal/db"
	"github.com/antoniolg/postflow/internal/domain"
)

func (e *parityEnv) apiCreatePost(text string, scheduledAt time.Time, mediaIDs []string) string {
	e.t.Helper()
	return e.apiCreatePostForAccount(e.account.ID, text, scheduledAt, mediaIDs)
}

func (e *parityEnv) apiCreatePostForAccount(accountID, text string, scheduledAt time.Time, mediaIDs []string) string {
	e.t.Helper()
	body := map[string]any{"account_id": strings.TrimSpace(accountID), "text": text}
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

func (e *parityEnv) apiCreateThread(segments []map[string]any) []string {
	e.t.Helper()
	body := map[string]any{
		"account_id": e.account.ID,
		"segments":   segments,
	}
	raw, status := e.apiJSON(http.MethodPost, "/posts", body, "application/json")
	if status != http.StatusCreated && status != http.StatusOK {
		e.t.Fatalf("create thread status=%d body=%s", status, string(raw))
	}
	var out struct {
		ID    string `json:"id"`
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	mustJSON(e.t, raw, &out)
	if strings.TrimSpace(out.ID) != "" {
		return []string{strings.TrimSpace(out.ID)}
	}
	ids := make([]string, 0, len(out.Items))
	for _, item := range out.Items {
		ids = append(ids, strings.TrimSpace(item.ID))
	}
	if len(ids) == 0 {
		e.t.Fatalf("expected thread post ids in create response")
	}
	return ids
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

func (e *parityEnv) apiValidateThread(segments []map[string]any) bool {
	e.t.Helper()
	raw, status := e.apiJSON(http.MethodPost, "/posts/validate", map[string]any{
		"account_id": e.account.ID,
		"segments":   segments,
	}, "application/json")
	if status != http.StatusOK {
		e.t.Fatalf("validate thread status=%d body=%s", status, string(raw))
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
			PublicationID string `json:"publication_id"`
			RootPostID    string `json:"root_post_id"`
		} `json:"items"`
	}
	mustJSON(e.t, raw, &out)
	ids := make([]string, 0, len(out.Items))
	for _, item := range out.Items {
		id := strings.TrimSpace(item.PublicationID)
		if id == "" {
			id = strings.TrimSpace(item.RootPostID)
		}
		ids = append(ids, id)
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

func (e *parityEnv) apiHealthStatus() string {
	e.t.Helper()
	raw, status := e.apiJSON(http.MethodGet, "/healthz", nil, "")
	if status != http.StatusOK {
		e.t.Fatalf("health status=%d body=%s", status, string(raw))
	}
	var out struct {
		Status string `json:"status"`
	}
	mustJSON(e.t, raw, &out)
	return strings.TrimSpace(out.Status)
}

func (e *parityEnv) apiDraftListIDs(limit int) []string {
	e.t.Helper()
	path := "/drafts"
	if limit > 0 {
		path += "?limit=" + url.QueryEscape(strconv.Itoa(limit))
	}
	raw, status := e.apiJSON(http.MethodGet, path, nil, "")
	if status != http.StatusOK {
		e.t.Fatalf("draft list status=%d body=%s", status, string(raw))
	}
	var out struct {
		Drafts []struct {
			ID string `json:"id"`
		} `json:"drafts"`
	}
	mustJSON(e.t, raw, &out)
	ids := make([]string, 0, len(out.Drafts))
	for _, item := range out.Drafts {
		ids = append(ids, strings.TrimSpace(item.ID))
	}
	return ids
}

func (e *parityEnv) apiCancelPost(id string) {
	e.t.Helper()
	raw, status := e.apiJSON(http.MethodPost, "/posts/"+strings.TrimSpace(id)+"/cancel", nil, "application/json")
	if status != http.StatusOK {
		e.t.Fatalf("cancel post status=%d body=%s", status, string(raw))
	}
}

func (e *parityEnv) apiSchedulePost(id string, scheduledAt time.Time) {
	e.t.Helper()
	raw, status := e.apiJSON(http.MethodPost, "/posts/"+strings.TrimSpace(id)+"/schedule", map[string]any{
		"scheduled_at": scheduledAt.UTC().Format(time.RFC3339),
	}, "application/json")
	if status != http.StatusOK {
		e.t.Fatalf("schedule post status=%d body=%s", status, string(raw))
	}
}

func (e *parityEnv) apiEditPost(id, text, intent string, scheduledAt time.Time) {
	e.t.Helper()
	payload := map[string]any{
		"text": strings.TrimSpace(text),
	}
	if strings.TrimSpace(intent) != "" {
		payload["intent"] = strings.TrimSpace(intent)
	}
	if !scheduledAt.IsZero() {
		payload["scheduled_at"] = scheduledAt.UTC().Format(time.RFC3339)
	}
	raw, status := e.apiJSON(http.MethodPost, "/posts/"+strings.TrimSpace(id)+"/edit", payload, "application/json")
	if status != http.StatusOK {
		e.t.Fatalf("edit post status=%d body=%s", status, string(raw))
	}
}

func (e *parityEnv) apiDeletePost(id string) {
	e.t.Helper()
	raw, status := e.apiJSON(http.MethodPost, "/posts/"+strings.TrimSpace(id)+"/delete", map[string]any{}, "application/json")
	if status != http.StatusOK {
		e.t.Fatalf("delete post status=%d body=%s", status, string(raw))
	}
}

func (e *parityEnv) apiCreateStaticAccount(platform, externalID string, credentials map[string]any) string {
	e.t.Helper()
	if credentials == nil {
		credentials = map[string]any{"access_token": "tok_" + externalID}
	}
	raw, status := e.apiJSON(http.MethodPost, "/accounts/static", map[string]any{
		"platform":            platform,
		"display_name":        "Parity " + platform,
		"external_account_id": externalID,
		"credentials":         credentials,
	}, "application/json")
	if status != http.StatusCreated && status != http.StatusOK {
		e.t.Fatalf("create static account status=%d body=%s", status, string(raw))
	}
	var out struct {
		ID string `json:"id"`
	}
	mustJSON(e.t, raw, &out)
	return strings.TrimSpace(out.ID)
}

func (e *parityEnv) apiConnectAccount(id string) {
	e.t.Helper()
	raw, status := e.apiJSON(http.MethodPost, "/accounts/"+strings.TrimSpace(id)+"/connect", nil, "application/json")
	if status != http.StatusOK {
		e.t.Fatalf("connect account status=%d body=%s", status, string(raw))
	}
}

func (e *parityEnv) apiDisconnectAccount(id string) {
	e.t.Helper()
	raw, status := e.apiJSON(http.MethodPost, "/accounts/"+strings.TrimSpace(id)+"/disconnect", nil, "application/json")
	if status != http.StatusOK {
		e.t.Fatalf("disconnect account status=%d body=%s", status, string(raw))
	}
}

func (e *parityEnv) apiDeleteAccount(id string) {
	e.t.Helper()
	raw, status := e.apiJSON(http.MethodDelete, "/accounts/"+strings.TrimSpace(id), nil, "")
	if status != http.StatusOK {
		e.t.Fatalf("delete account status=%d body=%s", status, string(raw))
	}
}

func (e *parityEnv) apiSetXPremium(id string, enabled bool) {
	e.t.Helper()
	raw, status := e.apiJSON(http.MethodPost, "/accounts/"+strings.TrimSpace(id)+"/x-premium", map[string]any{"x_premium": enabled}, "application/json")
	if status != http.StatusOK {
		e.t.Fatalf("set x premium status=%d body=%s", status, string(raw))
	}
}

func (e *parityEnv) seedXOAuthAccount(externalID string) string {
	e.t.Helper()
	account, err := e.store.UpsertAccount(e.t.Context(), db.UpsertAccountParams{
		Platform:          domain.PlatformX,
		DisplayName:       "Parity X " + strings.TrimSpace(externalID),
		ExternalAccountID: strings.TrimSpace(externalID),
		AuthMethod:        domain.AuthMethodOAuth,
		Status:            domain.AccountStatusConnected,
	})
	if err != nil {
		e.t.Fatalf("seed x oauth account: %v", err)
	}
	return account.ID
}

func (e *parityEnv) apiSetTimezone(timezone string) {
	e.t.Helper()
	raw, status := e.apiJSON(http.MethodPost, "/settings/timezone", map[string]any{"timezone": strings.TrimSpace(timezone)}, "application/json")
	if status != http.StatusOK {
		e.t.Fatalf("set timezone status=%d body=%s", status, string(raw))
	}
}

func (e *parityEnv) cliScheduleListIDs(from, to string) []string {
	e.t.Helper()
	raw := e.runCLI("schedule", "list", "--from", from, "--to", to)
	var out struct {
		Items []struct {
			PublicationID string `json:"publication_id"`
			RootPostID    string `json:"root_post_id"`
		} `json:"items"`
	}
	mustJSON(e.t, raw, &out)
	ids := make([]string, 0, len(out.Items))
	for _, item := range out.Items {
		id := strings.TrimSpace(item.PublicationID)
		if id == "" {
			id = strings.TrimSpace(item.RootPostID)
		}
		ids = append(ids, id)
	}
	return ids
}

func (e *parityEnv) cliCreatePost(text string) string {
	e.t.Helper()
	return e.cliCreatePostForAccount(e.account.ID, text, nil)
}

func (e *parityEnv) cliCreatePostForAccount(accountID, text string, mediaIDs []string) string {
	e.t.Helper()
	args := []string{"posts", "create", "--account-id", strings.TrimSpace(accountID), "--text", text}
	for _, mediaID := range mediaIDs {
		args = append(args, "--media-id", strings.TrimSpace(mediaID))
	}
	raw := e.runCLI(args...)
	var out struct {
		ID string `json:"id"`
	}
	mustJSON(e.t, raw, &out)
	return strings.TrimSpace(out.ID)
}

func (e *parityEnv) cliCreateThread(segmentsJSON string) []string {
	e.t.Helper()
	raw := e.runCLI("posts", "create", "--account-id", e.account.ID, "--segments-json", strings.TrimSpace(segmentsJSON))
	var out struct {
		ID    string `json:"id"`
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	mustJSON(e.t, raw, &out)
	if strings.TrimSpace(out.ID) != "" {
		return []string{strings.TrimSpace(out.ID)}
	}
	ids := make([]string, 0, len(out.Items))
	for _, item := range out.Items {
		ids = append(ids, strings.TrimSpace(item.ID))
	}
	if len(ids) == 0 {
		e.t.Fatalf("expected thread post ids in cli response")
	}
	return ids
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

func (e *parityEnv) cliValidateThread(segmentsJSON string) bool {
	e.t.Helper()
	raw := e.runCLI("posts", "validate", "--account-id", e.account.ID, "--segments-json", strings.TrimSpace(segmentsJSON))
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

func (e *parityEnv) cliHealthStatus() string {
	e.t.Helper()
	raw := e.runCLI("health")
	var out struct {
		Status string `json:"status"`
	}
	mustJSON(e.t, raw, &out)
	return strings.TrimSpace(out.Status)
}

func (e *parityEnv) cliDraftListIDs(limit int) []string {
	e.t.Helper()
	args := []string{"drafts", "list"}
	if limit > 0 {
		args = append(args, "--limit", strconv.Itoa(limit))
	}
	raw := e.runCLI(args...)
	var out struct {
		Drafts []struct {
			ID string `json:"id"`
		} `json:"drafts"`
	}
	mustJSON(e.t, raw, &out)
	ids := make([]string, 0, len(out.Drafts))
	for _, item := range out.Drafts {
		ids = append(ids, strings.TrimSpace(item.ID))
	}
	return ids
}

func (e *parityEnv) cliCancelPost(id string) {
	e.t.Helper()
	_ = e.runCLI("posts", "cancel", "--id", strings.TrimSpace(id))
}

func (e *parityEnv) cliSchedulePost(id string, scheduledAt time.Time) {
	e.t.Helper()
	_ = e.runCLI("posts", "schedule", "--id", strings.TrimSpace(id), "--scheduled-at", scheduledAt.UTC().Format(time.RFC3339))
}

func (e *parityEnv) cliEditPost(id, text, intent string, scheduledAt time.Time) {
	e.t.Helper()
	e.cliEditPostWithMedia(id, text, intent, scheduledAt, false, nil)
}

func (e *parityEnv) cliEditPostWithMedia(id, text, intent string, scheduledAt time.Time, replaceMedia bool, mediaIDs []string) {
	e.t.Helper()
	args := []string{"posts", "edit", "--id", strings.TrimSpace(id), "--text", strings.TrimSpace(text)}
	if strings.TrimSpace(intent) != "" {
		args = append(args, "--intent", strings.TrimSpace(intent))
	}
	if !scheduledAt.IsZero() {
		args = append(args, "--scheduled-at", scheduledAt.UTC().Format(time.RFC3339))
	}
	if replaceMedia {
		args = append(args, "--replace-media")
		for _, mediaID := range mediaIDs {
			args = append(args, "--media-id", strings.TrimSpace(mediaID))
		}
	}
	_ = e.runCLI(args...)
}

func (e *parityEnv) cliDeletePost(id string) {
	e.t.Helper()
	_ = e.runCLI("posts", "delete", "--id", strings.TrimSpace(id))
}

func (e *parityEnv) cliCreateStaticAccount(platform, externalID string, credentials map[string]string) string {
	e.t.Helper()
	args := []string{"accounts", "create-static", "--platform", platform, "--external-account-id", externalID}
	for key, value := range credentials {
		args = append(args, "--credential", key+"="+value)
	}
	raw := e.runCLI(args...)
	var out struct {
		ID string `json:"id"`
	}
	mustJSON(e.t, raw, &out)
	return strings.TrimSpace(out.ID)
}

func (e *parityEnv) cliConnectAccount(id string) {
	e.t.Helper()
	_ = e.runCLI("accounts", "connect", "--id", strings.TrimSpace(id))
}

func (e *parityEnv) cliDisconnectAccount(id string) {
	e.t.Helper()
	_ = e.runCLI("accounts", "disconnect", "--id", strings.TrimSpace(id))
}

func (e *parityEnv) cliDeleteAccount(id string) {
	e.t.Helper()
	_ = e.runCLI("accounts", "delete", "--id", strings.TrimSpace(id))
}

func (e *parityEnv) cliSetXPremium(id string, enabled bool) {
	e.t.Helper()
	_ = e.runCLI("accounts", "x-premium", "--id", strings.TrimSpace(id), "--enabled", strconv.FormatBool(enabled))
}

func (e *parityEnv) cliSetTimezone(timezone string) {
	e.t.Helper()
	_ = e.runCLI("settings", "set-timezone", "--timezone", strings.TrimSpace(timezone))
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
