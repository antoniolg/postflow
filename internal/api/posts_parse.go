package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	postsapp "github.com/antoniolg/publisher/internal/application/posts"
)

func parseCreatePostRequest(r *http.Request) (createPostRequest, bool, error) {
	rawBody, readErr := io.ReadAll(r.Body)
	if readErr != nil {
		return createPostRequest{}, false, fmt.Errorf("read body: %w", readErr)
	}
	r.Body = io.NopCloser(bytes.NewReader(rawBody))

	ct := strings.ToLower(r.Header.Get("content-type"))
	trimmed := bytes.TrimSpace(rawBody)
	looksLikeJSON := len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[')

	if strings.Contains(ct, "application/json") || looksLikeJSON {
		var req createPostRequest
		if err := json.Unmarshal(rawBody, &req); err != nil {
			return createPostRequest{}, false, fmt.Errorf("invalid json body: %w", err)
		}
		normalizedIDs := make([]string, 0, len(req.AccountIDs))
		for _, rawID := range req.AccountIDs {
			id := strings.TrimSpace(rawID)
			if id == "" {
				continue
			}
			normalizedIDs = append(normalizedIDs, id)
		}
		req.AccountIDs = postsapp.NormalizeAccountIDs("", normalizedIDs)
		req.AccountID = strings.TrimSpace(req.AccountID)
		if len(req.AccountIDs) == 0 && req.AccountID != "" {
			req.AccountIDs = []string{req.AccountID}
		}
		if req.AccountID == "" && len(req.AccountIDs) > 0 {
			req.AccountID = req.AccountIDs[0]
		}
		return req, false, nil
	}

	if err := r.ParseForm(); err != nil {
		return createPostRequest{}, true, fmt.Errorf("invalid form body: %w", err)
	}
	req := createPostRequest{
		AccountID: strings.TrimSpace(r.FormValue("account_id")),
		Text:      strings.TrimSpace(r.FormValue("text")),
		Intent:    strings.ToLower(strings.TrimSpace(r.FormValue("intent"))),
		ReturnTo:  strings.TrimSpace(r.FormValue("return_to")),
	}
	for _, rawID := range r.Form["account_ids"] {
		id := strings.TrimSpace(rawID)
		if id == "" {
			continue
		}
		req.AccountIDs = append(req.AccountIDs, id)
	}
	req.AccountIDs = postsapp.NormalizeAccountIDs("", req.AccountIDs)
	if len(req.AccountIDs) == 0 && req.AccountID != "" {
		req.AccountIDs = []string{req.AccountID}
	}
	if req.AccountID == "" && len(req.AccountIDs) > 0 {
		req.AccountID = req.AccountIDs[0]
	}
	if raw := strings.TrimSpace(r.FormValue("scheduled_at_local")); raw != "" {
		req.ScheduledAt = raw
	} else {
		req.ScheduledAt = strings.TrimSpace(r.FormValue("scheduled_at"))
	}
	for _, rawID := range r.Form["media_ids"] {
		id := strings.TrimSpace(rawID)
		if id == "" {
			continue
		}
		req.MediaIDs = append(req.MediaIDs, id)
	}
	return req, true, nil
}

func parseScheduledAtInput(raw string) (time.Time, error) {
	return parseScheduledAtInputInLocation(raw, time.Local)
}

func parseScheduledAtInputInLocation(raw string, loc *time.Location) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	if loc == nil {
		loc = time.UTC
	}

	if localParsed, err := time.ParseInLocation("2006-01-02T15:04", raw, loc); err == nil {
		return localParsed.UTC(), nil
	}

	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("scheduled_at must be RFC3339 or datetime-local: %w", err)
	}
	return parsed.UTC(), nil
}

func createViewURL(editID, text, scheduledAtLocal, returnTo, errorMsg, successMsg string) string {
	q := url.Values{}
	q.Set("view", "create")
	if strings.TrimSpace(editID) != "" {
		q.Set("edit_id", strings.TrimSpace(editID))
	}
	if strings.TrimSpace(returnTo) != "" {
		q.Set("return_to", strings.TrimSpace(returnTo))
	}
	if strings.TrimSpace(text) != "" {
		q.Set("text", strings.TrimSpace(text))
	}
	if strings.TrimSpace(scheduledAtLocal) != "" {
		q.Set("scheduled_at_local", strings.TrimSpace(scheduledAtLocal))
	}
	if strings.TrimSpace(errorMsg) != "" {
		q.Set("error", strings.TrimSpace(errorMsg))
	}
	if strings.TrimSpace(successMsg) != "" {
		q.Set("success", strings.TrimSpace(successMsg))
	}
	return "/?" + q.Encode()
}
