package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/antoniolg/publisher/internal/db"
	"github.com/antoniolg/publisher/internal/domain"
)

type Server struct {
	Store             *db.Store
	DataDir           string
	DefaultMaxRetries int
	RateLimitRPM      int
	APIToken          string
	UIBasicUser       string
	UIBasicPass       string
}

func (s Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mcpHandler := s.newMCPHandler()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.Handle("GET /mcp", mcpHandler)
	mux.Handle("POST /mcp", mcpHandler)
	mux.Handle("DELETE /mcp", mcpHandler)
	mux.HandleFunc("GET /media", s.handleListMedia)
	mux.HandleFunc("GET /media/{id}/content", s.handleMediaContent)
	mux.HandleFunc("DELETE /media/{id}", s.handleDeleteMedia)
	mux.HandleFunc("POST /media/{id}/delete", s.handleDeleteMediaForm)
	mux.HandleFunc("POST /media", s.handleUploadMedia)
	mux.HandleFunc("POST /posts", s.handleCreatePost)
	mux.HandleFunc("POST /posts/", s.handlePostActions)
	mux.HandleFunc("POST /posts/validate", s.handleValidatePost)
	mux.HandleFunc("GET /schedule", s.handleScheduleJSON)
	mux.HandleFunc("GET /dlq", s.handleListDLQ)
	mux.HandleFunc("POST /dlq/requeue", s.handleBulkRequeueDLQ)
	mux.HandleFunc("POST /dlq/", s.handleRequeueDLQ)
	mux.HandleFunc("POST /settings/timezone", s.handleSetTimezone)
	mux.HandleFunc("GET /", s.handleScheduleHTML)
	return s.withMiddlewares(mux)
}

func (s Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s Server) handleUploadMedia(w http.ResponseWriter, r *http.Request) {
	upload, status, err := s.saveUploadToDisk(r)
	if err != nil {
		writeError(w, status, err)
		return
	}
	cleanupFile := true
	defer func() {
		if cleanupFile && upload.StoragePath != "" {
			_ = removeFileQuiet(upload.StoragePath)
		}
	}()

	platform := domain.Platform(strings.ToLower(upload.Platform))
	if platform == "" {
		platform = domain.PlatformX
	}
	if platform != domain.PlatformX {
		writeError(w, http.StatusBadRequest, errors.New("only platform 'x' is supported in this MVP"))
		return
	}
	kind := strings.ToLower(upload.Kind)
	if kind == "" {
		kind = "video"
	}

	created, err := s.Store.CreateMedia(r.Context(), domain.Media{
		ID:           upload.MediaID,
		Platform:     platform,
		Kind:         kind,
		OriginalName: upload.OriginalName,
		StoragePath:  upload.StoragePath,
		MimeType:     upload.MimeType,
		SizeBytes:    upload.SizeBytes,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	cleanupFile = false
	mimeLower := strings.ToLower(strings.TrimSpace(created.MimeType))
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":            created.ID,
		"platform":      created.Platform,
		"kind":          created.Kind,
		"original_name": created.OriginalName,
		"storage_path":  created.StoragePath,
		"mime_type":     created.MimeType,
		"size_bytes":    created.SizeBytes,
		"created_at":    created.CreatedAt.UTC().Format(time.RFC3339),
		"usage_count":   0,
		"in_use":        false,
		"is_image":      strings.HasPrefix(mimeLower, "image/"),
		"is_video":      strings.HasPrefix(mimeLower, "video/"),
		"preview_url":   mediaContentURL(created.ID),
	})
}

type createPostRequest struct {
	Platform    string   `json:"platform"`
	Text        string   `json:"text"`
	ScheduledAt string   `json:"scheduled_at"`
	MediaIDs    []string `json:"media_ids"`
	MaxAttempts int      `json:"max_attempts"`
	Intent      string   `json:"intent"`
	ReturnTo    string   `json:"return_to"`
}

func (s Server) handleCreatePost(w http.ResponseWriter, r *http.Request) {
	req, fromForm, err := parseCreatePostRequest(r)
	if err != nil {
		if fromForm {
			http.Redirect(w, r, createViewURL("", req.Text, req.ScheduledAt, req.ReturnTo, err.Error(), ""), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}
	platform := domain.Platform(strings.ToLower(req.Platform))
	if platform == "" {
		platform = domain.PlatformX
	}
	if platform != domain.PlatformX {
		if fromForm {
			http.Redirect(w, r, createViewURL("", req.Text, req.ScheduledAt, req.ReturnTo, "only platform 'x' is supported in this MVP", ""), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, errors.New("only platform 'x' is supported in this MVP"))
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		if fromForm {
			http.Redirect(w, r, createViewURL("", req.Text, req.ScheduledAt, req.ReturnTo, "text is required", ""), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, errors.New("text is required"))
		return
	}
	uiLoc, _, _, err := s.resolveUILocation(r.Context())
	if err != nil {
		if fromForm {
			http.Redirect(w, r, createViewURL("", req.Text, req.ScheduledAt, req.ReturnTo, err.Error(), ""), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	scheduledAt, err := parseScheduledAtInputInLocation(req.ScheduledAt, uiLoc)
	if err != nil {
		if fromForm {
			http.Redirect(w, r, createViewURL("", req.Text, req.ScheduledAt, req.ReturnTo, err.Error(), ""), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if fromForm {
		intent := strings.ToLower(strings.TrimSpace(req.Intent))
		switch intent {
		case "draft":
			scheduledAt = time.Time{}
		case "schedule":
			if scheduledAt.IsZero() {
				http.Redirect(w, r, createViewURL("", req.Text, req.ScheduledAt, req.ReturnTo, "scheduled_at is required to schedule", ""), http.StatusSeeOther)
				return
			}
		case "publish_now":
			if scheduledAt.IsZero() {
				scheduledAt = time.Now().UTC()
			}
		}
	}
	if _, err := s.Store.GetMediaByIDs(r.Context(), req.MediaIDs); err != nil {
		if fromForm {
			http.Redirect(w, r, createViewURL("", req.Text, req.ScheduledAt, req.ReturnTo, err.Error(), ""), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}
	maxAttempts := req.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = s.DefaultMaxRetries
		if maxAttempts <= 0 {
			maxAttempts = 3
		}
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(idempotencyKey) > 128 {
		if fromForm {
			http.Redirect(w, r, createViewURL("", req.Text, req.ScheduledAt, req.ReturnTo, "idempotency key too long (max 128 chars)", ""), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, errors.New("Idempotency-Key too long (max 128 chars)"))
		return
	}
	result, err := s.Store.CreatePost(r.Context(), db.CreatePostParams{
		Post: domain.Post{
			Platform:    platform,
			Text:        req.Text,
			Status:      defaultStatusForScheduledAt(scheduledAt),
			ScheduledAt: scheduledAt,
			MaxAttempts: maxAttempts,
		},
		MediaIDs:       req.MediaIDs,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		if fromForm {
			http.Redirect(w, r, createViewURL("", req.Text, req.ScheduledAt, req.ReturnTo, err.Error(), ""), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if result.Created {
		if fromForm {
			http.Redirect(w, r, createViewURL("", "", "", req.ReturnTo, "", "post created"), http.StatusSeeOther)
			return
		}
		writeJSON(w, http.StatusCreated, result.Post)
		return
	}
	if fromForm {
		http.Redirect(w, r, createViewURL("", "", "", req.ReturnTo, "", "post updated"), http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, result.Post)
}

func (s Server) handleScheduleJSON(w http.ResponseWriter, r *http.Request) {
	from, to, err := parseRange(r.Context(), r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	items, err := s.Store.ListSchedule(r.Context(), from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"from":  from.Format(time.RFC3339),
		"to":    to.Format(time.RFC3339),
		"items": items,
	})
}

func (s Server) handlePostActions(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/posts/") {
		writeError(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	switch {
	case strings.HasSuffix(r.URL.Path, "/cancel"):
		s.handleCancelPost(w, r)
	case strings.HasSuffix(r.URL.Path, "/schedule"):
		s.handleScheduleDraftPost(w, r)
	case strings.HasSuffix(r.URL.Path, "/edit"):
		s.handleEditPost(w, r)
	default:
		writeError(w, http.StatusNotFound, errors.New("not found"))
	}
}

func (s Server) handleCancelPost(w http.ResponseWriter, r *http.Request) {
	postID, err := extractPostIDFromPath(r.URL.Path, "cancel")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Store.CancelPost(r.Context(), postID); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": postID, "status": string(domain.PostStatusCanceled)})
}

func (s Server) handleScheduleDraftPost(w http.ResponseWriter, r *http.Request) {
	postID, err := extractPostIDFromPath(r.URL.Path, "schedule")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	uiLoc, _, _, err := s.resolveUILocation(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	scheduledAtRaw := strings.TrimSpace(r.FormValue("scheduled_at"))
	if scheduledAtRaw == "" {
		localRaw := strings.TrimSpace(r.FormValue("scheduled_at_local"))
		if localRaw != "" {
			localTime, err := time.ParseInLocation("2006-01-02T15:04", localRaw, uiLoc)
			if err == nil {
				scheduledAtRaw = localTime.UTC().Format(time.RFC3339)
			}
		}
	}
	if scheduledAtRaw == "" {
		var body struct {
			ScheduledAt string `json:"scheduled_at"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			scheduledAtRaw = strings.TrimSpace(body.ScheduledAt)
		}
	}
	if scheduledAtRaw == "" {
		writeError(w, http.StatusBadRequest, errors.New("scheduled_at is required"))
		return
	}
	scheduledAt, err := time.Parse(time.RFC3339, scheduledAtRaw)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("scheduled_at must be RFC3339: %w", err))
		return
	}
	if err := s.Store.ScheduleDraftPost(r.Context(), postID, scheduledAt.UTC()); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	post, err := s.Store.GetPost(r.Context(), postID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": postID, "status": string(post.Status), "post": post})
}

func (s Server) handleEditPost(w http.ResponseWriter, r *http.Request) {
	postID, err := extractPostIDFromPath(r.URL.Path, "edit")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	fromForm := !strings.Contains(strings.ToLower(r.Header.Get("content-type")), "application/json")
	returnTo := strings.TrimSpace(r.FormValue("return_to"))
	uiLoc, _, _, err := s.resolveUILocation(r.Context())
	if err != nil {
		if fromForm {
			http.Redirect(w, r, createViewURL(postID, "", strings.TrimSpace(r.FormValue("scheduled_at_local")), returnTo, err.Error(), ""), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	text := strings.TrimSpace(r.FormValue("text"))
	intent := strings.ToLower(strings.TrimSpace(r.FormValue("intent")))
	if text == "" {
		var body struct {
			Text   string `json:"text"`
			Intent string `json:"intent"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			text = strings.TrimSpace(body.Text)
			intent = strings.ToLower(strings.TrimSpace(body.Intent))
		}
	}
	if text == "" {
		if fromForm {
			http.Redirect(w, r, createViewURL(postID, text, strings.TrimSpace(r.FormValue("scheduled_at_local")), returnTo, "text is required", ""), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, errors.New("text is required"))
		return
	}
	scheduledAtRaw := strings.TrimSpace(r.FormValue("scheduled_at"))
	if scheduledAtRaw == "" {
		scheduledAtRaw = strings.TrimSpace(r.FormValue("scheduled_at_local"))
	}
	var scheduledAt time.Time
	if scheduledAtRaw != "" {
		parsed, err := parseScheduledAtInputInLocation(scheduledAtRaw, uiLoc)
		if err != nil {
			if fromForm {
				http.Redirect(w, r, createViewURL(postID, text, scheduledAtRaw, returnTo, err.Error(), ""), http.StatusSeeOther)
				return
			}
			writeError(w, http.StatusBadRequest, err)
			return
		}
		scheduledAt = parsed
	}
	if intent == "draft" {
		scheduledAt = time.Time{}
	}
	if intent == "publish_now" && scheduledAt.IsZero() {
		scheduledAt = time.Now().UTC()
	}
	if intent == "schedule" && scheduledAt.IsZero() {
		if fromForm {
			http.Redirect(w, r, createViewURL(postID, text, scheduledAtRaw, returnTo, "scheduled_at is required to schedule", ""), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, errors.New("scheduled_at is required"))
		return
	}
	if err := s.Store.UpdatePostEditable(r.Context(), postID, text, scheduledAt); err != nil {
		if fromForm {
			http.Redirect(w, r, createViewURL(postID, text, scheduledAtRaw, returnTo, err.Error(), ""), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusConflict, err)
		return
	}
	post, err := s.Store.GetPost(r.Context(), postID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !fromForm {
		writeJSON(w, http.StatusOK, map[string]any{"id": post.ID, "status": string(post.Status), "post": post})
		return
	}
	scheduledLocal := ""
	if !post.ScheduledAt.IsZero() {
		scheduledLocal = post.ScheduledAt.In(uiLoc).Format("2006-01-02T15:04")
	}
	http.Redirect(w, r, createViewURL(post.ID, post.Text, scheduledLocal, returnTo, "", "changes saved"), http.StatusSeeOther)
}

func (s Server) handleListDLQ(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		var parsed int
		_, err := fmt.Sscanf(raw, "%d", &parsed)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, errors.New("limit must be a positive integer"))
			return
		}
		if parsed > 500 {
			parsed = 500
		}
		limit = parsed
	}

	items, err := s.Store.ListDeadLetters(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"count": len(items),
	})
}

func (s Server) handleRequeueDLQ(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/dlq/") || !strings.HasSuffix(r.URL.Path, "/requeue") {
		writeError(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	trimmed := strings.TrimPrefix(r.URL.Path, "/dlq/")
	id := strings.TrimSuffix(trimmed, "/requeue")
	id = strings.TrimSuffix(id, "/")
	contentType := strings.ToLower(r.Header.Get("content-type"))
	fromForm := strings.Contains(contentType, "application/x-www-form-urlencoded") || strings.Contains(contentType, "multipart/form-data")
	if id == "" {
		if fromForm {
			http.Redirect(w, r, "/?view=failed&failed_error=invalid+dead+letter+id", http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, errors.New("invalid dead letter id"))
		return
	}

	post, err := s.Store.RequeueDeadLetter(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if fromForm {
				http.Redirect(w, r, "/?view=failed&failed_error=dead+letter+not+found", http.StatusSeeOther)
				return
			}
			writeError(w, http.StatusNotFound, errors.New("dead letter not found"))
			return
		}
		if strings.Contains(err.Error(), "not requeueable") {
			if fromForm {
				http.Redirect(w, r, "/?view=failed&failed_error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
				return
			}
			writeError(w, http.StatusConflict, err)
			return
		}
		if fromForm {
			http.Redirect(w, r, "/?view=failed&failed_error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	if fromForm {
		http.Redirect(w, r, "/?view=failed&failed_success=requeued", http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"dead_letter_id": id,
		"post":           post,
	})
}

func (s Server) handleBulkRequeueDLQ(w http.ResponseWriter, r *http.Request) {
	contentType := strings.ToLower(r.Header.Get("content-type"))
	fromForm := strings.Contains(contentType, "application/x-www-form-urlencoded") || strings.Contains(contentType, "multipart/form-data")

	var ids []string
	if fromForm {
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/?view=failed&failed_error=invalid+form", http.StatusSeeOther)
			return
		}
		ids = r.Form["ids"]
	} else {
		var body struct {
			IDs []string `json:"ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid json body: %w", err))
			return
		}
		ids = body.IDs
	}

	cleaned := make([]string, 0, len(ids))
	seen := map[string]struct{}{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		cleaned = append(cleaned, id)
	}
	if len(cleaned) == 0 {
		if fromForm {
			http.Redirect(w, r, "/?view=failed&failed_error=no+items+selected", http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, errors.New("ids are required"))
		return
	}

	success := 0
	failed := 0
	for _, id := range cleaned {
		if _, err := s.Store.RequeueDeadLetter(r.Context(), id); err != nil {
			failed++
			continue
		}
		success++
	}

	if fromForm {
		q := url.Values{}
		q.Set("view", "failed")
		q.Set("failed_success", fmt.Sprintf("requeued %d", success))
		if failed > 0 {
			q.Set("failed_error", fmt.Sprintf("failed %d", failed))
		}
		http.Redirect(w, r, "/?"+q.Encode(), http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"selected": len(cleaned),
		"success":  success,
		"failed":   failed,
	})
}

func (s Server) handleSetTimezone(w http.ResponseWriter, r *http.Request) {
	contentType := strings.ToLower(r.Header.Get("content-type"))
	fromForm := strings.Contains(contentType, "application/x-www-form-urlencoded") || strings.Contains(contentType, "multipart/form-data")

	timezone := ""
	returnTo := "/?view=settings"
	if fromForm {
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/?view=settings&tz_error=invalid+form", http.StatusSeeOther)
			return
		}
		timezone = strings.TrimSpace(r.FormValue("timezone"))
		returnTo = sanitizeReturnTo(strings.TrimSpace(r.FormValue("return_to")))
		if returnTo == "" {
			returnTo = "/?view=settings"
		}
	} else {
		var body struct {
			Timezone string `json:"timezone"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid json body: %w", err))
			return
		}
		timezone = strings.TrimSpace(body.Timezone)
	}

	if timezone == "" {
		if fromForm {
			http.Redirect(w, r, withQueryValue(returnTo, "tz_error", "timezone is required"), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, errors.New("timezone is required"))
		return
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		if fromForm {
			http.Redirect(w, r, withQueryValue(returnTo, "tz_error", "invalid timezone"), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid timezone: %w", err))
		return
	}
	if err := s.Store.SetUITimezone(r.Context(), timezone); err != nil {
		if fromForm {
			http.Redirect(w, r, withQueryValue(returnTo, "tz_error", err.Error()), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	if fromForm {
		http.Redirect(w, r, withQueryValue(returnTo, "tz_success", "timezone saved"), http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"timezone": timezone})
}

func sanitizeReturnTo(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.IsAbs() || parsed.Host != "" {
		return ""
	}
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed.RequestURI()
}

func withQueryValue(rawURL, key, value string) string {
	rawURL = sanitizeReturnTo(rawURL)
	if rawURL == "" {
		rawURL = "/"
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "/"
	}
	q := parsed.Query()
	q.Set(key, value)
	parsed.RawQuery = q.Encode()
	return parsed.RequestURI()
}

func (s Server) resolveUILocation(ctx context.Context) (*time.Location, string, bool, error) {
	tz, err := s.Store.GetUITimezone(ctx)
	if err != nil {
		return nil, "", false, err
	}
	tz = strings.TrimSpace(tz)
	if tz == "" {
		return time.UTC, "UTC", false, nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, "", false, fmt.Errorf("invalid configured timezone %q: %w", tz, err)
	}
	return loc, tz, true, nil
}

func (s Server) handleScheduleHTML(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		writeError(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	uiLoc, uiTimezone, timezoneConfigured, err := s.resolveUILocation(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	nowLocal := time.Now().In(uiLoc)
	view := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("view")))
	if view == "" {
		view = "calendar"
	}
	if view != "calendar" && view != "publications" && view != "drafts" && view != "create" && view != "failed" && view != "settings" {
		view = "calendar"
	}
	editID := strings.TrimSpace(r.URL.Query().Get("edit_id"))
	returnTo := strings.TrimSpace(r.URL.Query().Get("return_to"))
	createError := strings.TrimSpace(r.URL.Query().Get("error"))
	createSuccess := strings.TrimSpace(r.URL.Query().Get("success"))
	failedError := strings.TrimSpace(r.URL.Query().Get("failed_error"))
	failedSuccess := strings.TrimSpace(r.URL.Query().Get("failed_success"))
	settingsError := strings.TrimSpace(r.URL.Query().Get("tz_error"))
	settingsSuccess := strings.TrimSpace(r.URL.Query().Get("tz_success"))
	mediaError := strings.TrimSpace(r.URL.Query().Get("media_error"))
	mediaSuccess := strings.TrimSpace(r.URL.Query().Get("media_success"))
	displayMonth := time.Date(nowLocal.Year(), nowLocal.Month(), 1, 0, 0, 0, 0, uiLoc)
	if monthRaw := strings.TrimSpace(r.URL.Query().Get("month")); monthRaw != "" {
		if parsedMonth, err := time.ParseInLocation("2006-01", monthRaw, uiLoc); err == nil {
			displayMonth = time.Date(parsedMonth.Year(), parsedMonth.Month(), 1, 0, 0, 0, 0, uiLoc)
		}
	}
	monthStartLocal := displayMonth
	monthEndLocal := monthStartLocal.AddDate(0, 1, 0).Add(-time.Second)
	from := monthStartLocal.UTC()
	to := monthEndLocal.UTC()
	items, err := s.Store.ListSchedule(r.Context(), from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	for i := range items {
		if !items[i].ScheduledAt.IsZero() {
			items[i].ScheduledAt = items[i].ScheduledAt.In(uiLoc)
		}
	}
	publicationsWindowDays := 14
	publicationsFrom := nowLocal
	publicationsTo := nowLocal.AddDate(0, 0, publicationsWindowDays)
	publicationsRaw, err := s.Store.ListSchedule(r.Context(), publicationsFrom.UTC(), publicationsTo.UTC())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	publicationsItems := make([]domain.Post, 0, len(publicationsRaw))
	for _, item := range publicationsRaw {
		if item.Status != domain.PostStatusScheduled {
			continue
		}
		if !item.ScheduledAt.IsZero() {
			item.ScheduledAt = item.ScheduledAt.In(uiLoc)
		}
		publicationsItems = append(publicationsItems, item)
	}
	drafts, err := s.Store.ListDrafts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	deadLetters, err := s.Store.ListDeadLetters(r.Context(), 200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	scheduledCount := len(publicationsItems)
	failedCount := len(deadLetters)
	var nextRun *time.Time
	for _, item := range publicationsItems {
		if !item.ScheduledAt.IsZero() && (nextRun == nil || item.ScheduledAt.Before(*nextRun)) {
			t := item.ScheduledAt
			nextRun = &t
		}
	}
	nextRunLabel := "Sin próxima ejecución"
	if nextRun != nil {
		nextRunLabel = nextRun.In(uiLoc).Format("2006-01-02 15:04 MST")
	}

	type calendarEvent struct {
		TimeLabel   string
		StatusClass string
		StatusLabel string
		StatusKey   string
		TextPreview string
	}
	type dayDetailItem struct {
		PostID      string
		Editable    bool
		TimeLabel   string
		StatusClass string
		StatusLabel string
		StatusKey   string
		Text        string
		Platform    domain.Platform
		MediaCount  int
	}
	type failedQueueItem struct {
		DeadLetterID     string
		PostID           string
		Text             string
		Platform         domain.Platform
		MediaCount       int
		Attempts         int
		MaxAttempts      int
		LastError        string
		FailedAtLabel    string
		ScheduledAtLabel string
	}
	type calendarDay struct {
		DateKey        string
		DayNumber      int
		InCurrentMonth bool
		IsToday        bool
		IsSelected     bool
		Events         []calendarEvent
	}

	firstWeekday := int(monthStartLocal.Weekday())
	// Convert Go weekday (Sunday=0) to Monday-first index.
	firstWeekday = (firstWeekday + 6) % 7
	gridStart := monthStartLocal.AddDate(0, 0, -firstWeekday)

	lastDayLocal := monthEndLocal
	lastWeekday := int(lastDayLocal.Weekday())
	lastWeekday = (lastWeekday + 6) % 7
	gridEnd := lastDayLocal.AddDate(0, 0, 6-lastWeekday)

	eventsByDate := make(map[string][]calendarEvent)
	detailsByDate := make(map[string][]dayDetailItem)
	for _, item := range items {
		if item.ScheduledAt.IsZero() {
			continue
		}
		localTime := item.ScheduledAt.In(uiLoc)
		key := localTime.Format("2006-01-02")
		statusClass := "drft"
		statusLabel := "DRFT"
		statusKey := "draft"
		switch item.Status {
		case domain.PostStatusPublished:
			statusClass = "live"
			statusLabel = "LIVE"
			statusKey = "published"
		case domain.PostStatusScheduled:
			statusClass = "schd"
			statusLabel = "SCHD"
			statusKey = "scheduled"
		}
		text := strings.TrimSpace(item.Text)
		if len(text) > 56 {
			text = text[:53] + "..."
		}
		eventsByDate[key] = append(eventsByDate[key], calendarEvent{
			TimeLabel:   localTime.Format("15:04"),
			StatusClass: statusClass,
			StatusLabel: statusLabel,
			StatusKey:   statusKey,
			TextPreview: text,
		})
		detailsByDate[key] = append(detailsByDate[key], dayDetailItem{
			PostID:      item.ID,
			Editable:    item.Status != domain.PostStatusPublished,
			TimeLabel:   localTime.Format("15:04"),
			StatusClass: statusClass,
			StatusLabel: statusLabel,
			StatusKey:   statusKey,
			Text:        strings.TrimSpace(item.Text),
			Platform:    item.Platform,
			MediaCount:  len(item.Media),
		})
	}

	selectedDayLocal := nowLocal
	if selectedDayLocal.Month() != monthStartLocal.Month() || selectedDayLocal.Year() != monthStartLocal.Year() {
		selectedDayLocal = monthStartLocal
	}
	if dayRaw := strings.TrimSpace(r.URL.Query().Get("day")); dayRaw != "" {
		if parsedDay, err := time.ParseInLocation("2006-01-02", dayRaw, uiLoc); err == nil {
			selectedDayLocal = parsedDay
		}
	}

	var calendarDays []calendarDay
	for d := gridStart; !d.After(gridEnd); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		dayEvents := eventsByDate[key]
		calendarDays = append(calendarDays, calendarDay{
			DateKey:        key,
			DayNumber:      d.Day(),
			InCurrentMonth: d.Month() == monthStartLocal.Month(),
			IsToday:        d.Year() == nowLocal.Year() && d.Month() == nowLocal.Month() && d.Day() == nowLocal.Day(),
			IsSelected:     d.Year() == selectedDayLocal.Year() && d.Month() == selectedDayLocal.Month() && d.Day() == selectedDayLocal.Day(),
			Events:         dayEvents,
		})
	}

	var calendarWeeks [][]calendarDay
	for i := 0; i < len(calendarDays); i += 7 {
		end := i + 7
		if end > len(calendarDays) {
			end = len(calendarDays)
		}
		calendarWeeks = append(calendarWeeks, calendarDays[i:end])
	}

	calendarMonthLabel := strings.ToUpper(monthStartLocal.Format("January 2006"))
	prevMonthParam := monthStartLocal.AddDate(0, -1, 0).Format("2006-01")
	nextMonthParam := monthStartLocal.AddDate(0, 1, 0).Format("2006-01")
	currentMonthParam := monthStartLocal.Format("2006-01")
	selectedDayKey := selectedDayLocal.Format("2006-01-02")
	selectedDayLabel := strings.ToUpper(selectedDayLocal.Format("Mon 02 Jan 2006"))
	selectedDayItems := detailsByDate[selectedDayKey]
	selectedDayPendingItems := make([]dayDetailItem, 0, len(selectedDayItems))
	selectedDayPublishedItems := make([]dayDetailItem, 0, len(selectedDayItems))
	for _, item := range selectedDayItems {
		if item.StatusKey == "published" {
			selectedDayPublishedItems = append(selectedDayPublishedItems, item)
			continue
		}
		selectedDayPendingItems = append(selectedDayPendingItems, item)
	}
	todayMonthParam := nowLocal.Format("2006-01")
	todayDayKey := nowLocal.Format("2006-01-02")
	currentViewURL := "/?view=calendar&month=" + currentMonthParam + "&day=" + selectedDayKey
	switch view {
	case "publications":
		currentViewURL = "/?view=publications"
	case "calendar":
		currentViewURL = "/?view=calendar&month=" + currentMonthParam + "&day=" + selectedDayKey
	case "drafts":
		currentViewURL = "/?view=drafts"
	case "failed":
		currentViewURL = "/?view=failed"
	case "settings":
		currentViewURL = "/?view=settings"
	case "create":
		if returnTo != "" {
			currentViewURL = returnTo
		}
	}
	createViewURL := "/?view=create&return_to=" + url.QueryEscape(currentViewURL)
	backURL := "/?view=calendar&month=" + currentMonthParam + "&day=" + selectedDayKey
	if returnTo != "" {
		backURL = returnTo
	}
	activeNavView := view
	if activeNavView == "create" {
		activeNavView = "calendar"
		if returnTo != "" {
			if parsed, err := url.Parse(returnTo); err == nil {
				sourceView := strings.ToLower(strings.TrimSpace(parsed.Query().Get("view")))
				switch sourceView {
				case "publications", "calendar", "drafts", "failed", "settings":
					activeNavView = sourceView
				}
			}
		}
	}
	failedItems := make([]failedQueueItem, 0, len(deadLetters))
	for _, dead := range deadLetters {
		post, err := s.Store.GetPost(r.Context(), dead.PostID)
		if err != nil {
			continue
		}
		scheduledAtLabel := "no date"
		if !post.ScheduledAt.IsZero() {
			scheduledAtLabel = post.ScheduledAt.In(uiLoc).Format("2006-01-02 15:04 MST")
		}
		failedAtLabel := dead.AttemptedAt.In(uiLoc).Format("2006-01-02 15:04 MST")
		failedItems = append(failedItems, failedQueueItem{
			DeadLetterID:     dead.ID,
			PostID:           post.ID,
			Text:             strings.TrimSpace(post.Text),
			Platform:         post.Platform,
			MediaCount:       len(post.Media),
			Attempts:         post.Attempts,
			MaxAttempts:      post.MaxAttempts,
			LastError:        strings.TrimSpace(dead.LastError),
			FailedAtLabel:    failedAtLabel,
			ScheduledAtLabel: scheduledAtLabel,
		})
	}
	mediaLibrary, err := s.listMediaItems(r.Context(), 200, uiLoc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	createRecentMedia := mediaLibrary
	if len(createRecentMedia) > 18 {
		createRecentMedia = createRecentMedia[:18]
	}
	var mediaInUseCount int
	var mediaTotalBytes int64
	for _, item := range mediaLibrary {
		mediaTotalBytes += item.SizeBytes
		if item.InUse {
			mediaInUseCount++
		}
	}
	mediaTotalSizeLabel := formatByteSize(mediaTotalBytes)
	var editingPost *domain.Post
	var createText string
	var createScheduledLocal string
	if editID != "" {
		p, err := s.Store.GetPost(r.Context(), editID)
		if err == nil {
			editingPost = &p
			createText = p.Text
			if !p.ScheduledAt.IsZero() {
				createScheduledLocal = p.ScheduledAt.In(uiLoc).Format("2006-01-02T15:04")
			}
		}
	}
	if qText := strings.TrimSpace(r.URL.Query().Get("text")); qText != "" {
		createText = qText
	}
	if qScheduled := strings.TrimSpace(r.URL.Query().Get("scheduled_at_local")); qScheduled != "" {
		createScheduledLocal = qScheduled
	}
	mcpURL, mcpAuthHint, mcpConfigJSON, mcpClaudeCommand, mcpCodexCommand, mcpCodexConfigTOML := s.mcpSettingsInfo(r)
	const tpl = `<!doctype html>
<html lang="es">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>publisher · schedule</title>
  <link rel="preconnect" href="https://fonts.googleapis.com">
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
  <link href="https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@400;500;600;700&family=Oswald:wght@500;600;700&display=swap" rel="stylesheet">
  <style>
    :root {
      --bg-page: #1a1a1a;
      --bg-panel: #1a1a1a;
      --bg-card: #212121;
      --bg-elevated: #2d2d2d;
      --bg-muted: #343434;
      --text-primary: #ffffff;
      --text-secondary: #a8a8a8;
      --accent-orange: #ff6b35;
      --accent-teal: #00d4aa;
      --border: #2a2a2a;
      --radius: 12px;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      min-height: 100vh;
      color: var(--text-primary);
      background:
        radial-gradient(1200px 540px at 90% -12%, rgba(255, 107, 53, 0.07), transparent 62%),
        radial-gradient(950px 480px at -10% -18%, rgba(255, 255, 255, 0.03), transparent 65%),
        var(--bg-page);
      font-family: "JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, monospace;
    }
    .app {
      width: 100%;
      min-height: 100vh;
      display: flex;
    }
    .sidebar {
      width: 220px;
      border-right: 1px solid #242424;
      padding: 24px 16px;
      background: #1a1a1a;
      display: flex;
      flex-direction: column;
    }
    .logo {
      display: flex;
      align-items: center;
      gap: 8px;
      margin-bottom: 28px;
      padding: 0 6px;
    }
    .logo-dot {
      width: 10px;
      height: 10px;
      border-radius: 999px;
      background: var(--accent-orange);
      box-shadow: 0 0 18px rgba(255,122,48,0.6);
    }
    .logo span {
      font-family: "Oswald", sans-serif;
      font-size: 20px;
      letter-spacing: 0.02em;
    }
    .nav {
      display: flex;
      flex-direction: column;
      gap: 6px;
      flex: 1;
      min-height: 0;
    }
    .nav-item {
      border-radius: 16px;
      padding: 10px 16px;
      font-size: 13px;
      color: var(--text-secondary);
      border: 0;
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 8px;
      text-decoration: none;
    }
    .nav-main {
      display: inline-flex;
      align-items: center;
      gap: 8px;
      min-width: 0;
    }
    .nav-icon {
      width: 16px;
      height: 16px;
      flex: 0 0 auto;
      color: currentColor;
    }
    .nav-badge {
      min-width: 18px;
      height: 18px;
      padding: 0 6px;
      border-radius: 999px;
      background: #2d2d2d;
      color: var(--text-secondary);
      display: inline-flex;
      align-items: center;
      justify-content: center;
      font-size: 11px;
      font-weight: 700;
      line-height: 1;
      border: 0;
    }
    .nav-badge-danger {
      background: #c93d4f;
      color: #fff;
      border-color: rgba(255, 255, 255, 0.15);
    }
    .nav-item.active {
      color: var(--text-primary);
      background: var(--bg-elevated);
      border: 0;
    }
    .nav-item.active .nav-badge {
      background: #3a3a3a;
      color: #f0f0f0;
      border: 0;
    }
    .nav-item-settings {
      margin-top: auto;
    }
    .main {
      flex: 1;
      padding: 34px 44px 22px;
      width: 100%;
      max-width: 1180px;
    }
    body[data-view="calendar"] .main,
    body[data-view="create"] .main {
      max-width: none;
    }
    .header {
      display: flex;
      justify-content: space-between;
      gap: 16px;
      align-items: center;
    }
    .title-row {
      display: inline-flex;
      align-items: center;
      gap: 12px;
    }
    body[data-view="calendar"] .header {
      width: min(100%, 1540px);
      margin-left: auto;
      margin-right: auto;
      display: grid;
      grid-template-columns: minmax(0, 1fr) clamp(300px, 28vw, 390px);
      gap: 12px;
      align-items: center;
    }
    body[data-view="calendar"] .title-row {
      width: 100%;
      display: flex;
    }
    body[data-view="calendar"] .calendar-controls {
      margin-left: auto;
    }
    body[data-view="calendar"] .header-right {
      justify-content: flex-end;
    }
    .title-copy {
      display: inline-flex;
      flex-direction: column;
      gap: 3px;
    }
    .title-sub {
      font-size: 12px;
      letter-spacing: 0.06em;
      color: var(--text-secondary);
      text-transform: lowercase;
    }
    .title-back {
      color: #c8c8c8;
      text-decoration: none;
      border: 0;
      border-radius: 16px;
      min-width: 70px;
      height: 34px;
      padding: 0 12px;
      background: #2d2d2d;
      display: inline-flex;
      align-items: center;
      justify-content: flex-start;
      gap: 6px;
      font-size: 18px;
      line-height: 1;
    }
    .title-back::after {
      content: "back";
      font-size: 12px;
      font-weight: 600;
      text-transform: lowercase;
      line-height: 1;
    }
    .title-back:hover {
      color: var(--text-primary);
      background: #383838;
    }
    h1 {
      margin: 0;
      font-family: "Oswald", sans-serif;
      font-weight: 700;
      letter-spacing: 0.02em;
      font-size: 50px;
      line-height: 1;
    }
    .create-pill {
      display: inline-flex;
      align-items: center;
      border-radius: 16px;
      background: var(--accent-orange);
      color: #0d0d0d;
      border: 0;
      padding: 10px 18px;
      font-size: 12px;
      font-weight: 600;
      text-transform: lowercase;
      letter-spacing: 0.03em;
      text-decoration: none;
    }
    body[data-view="create"] .create-pill {
      display: none;
    }
    .create-header-actions {
      display: inline-flex;
      align-items: center;
      gap: 12px;
      flex-wrap: wrap;
      justify-content: flex-end;
    }
    .create-header-actions button {
      min-height: 36px;
      padding: 0 16px;
      font-family: 'JetBrains Mono', monospace;
      font-size: 12px;
      font-weight: 600;
      border-radius: 12px;
      display: inline-flex;
      align-items: center;
      gap: 6px;
    }
    .create-header-actions button svg {
      flex: 0 0 auto;
    }
    .create-header-actions .btn-secondary {
      color: var(--text-secondary);
      background: var(--bg-elevated, #2d2d2d);
    }
    .create-header-actions .btn-schedule {
      color: var(--accent-orange);
    }
    .tabs {
      margin-top: 18px;
      display: flex;
      gap: 8px;
      flex-wrap: wrap;
    }
    .tab {
      border-radius: 16px;
      padding: 8px 14px;
      font-size: 12px;
      color: var(--text-secondary);
      background: #242424;
      border: 0;
      display: inline-flex;
      align-items: center;
      gap: 8px;
    }
    .tab.active {
      background: #323232;
      color: var(--text-primary);
    }
    .tab-link {
      color: inherit;
      text-decoration: none;
    }
    .filter-chip {
      cursor: pointer;
      transition: opacity .12s ease, transform .12s ease;
    }
    .filter-chip.inactive {
      opacity: .45;
      transform: scale(.98);
    }
    .is-hidden {
      display: none !important;
    }
    .dot {
      width: 6px;
      height: 6px;
      border-radius: 999px;
      display: inline-block;
    }
    .dot.live { background: var(--accent-teal); }
    .dot.scheduled { background: var(--accent-orange); }
    .dot.draft { background: #666666; }
    .dot.fail { background: #ff5f70; }
    .stats {
      margin-top: 14px;
      display: grid;
      grid-template-columns: repeat(4, minmax(0, 1fr));
      gap: 10px;
    }
    .stat {
      background: #212121;
      border: 0;
      border-radius: 16px;
      padding: 10px 12px;
    }
    .stat .k { color: var(--text-secondary); font-size: 12px; margin-bottom: 4px; }
    .stat .v { font-size: 16px; }
    .calendar-wrap {
      border: 0;
      background: transparent;
      border-radius: 4px;
      overflow: hidden;
    }
    .calendar-grid-scroll {
      overflow-x: auto;
      overflow-y: hidden;
    }
    .calendar-layout {
      margin-top: 12px;
      display: grid;
      grid-template-columns: minmax(0, 2.3fr) minmax(280px, 1fr);
      gap: 12px;
      align-items: start;
    }
    body[data-view="calendar"] .calendar-layout {
      width: min(100%, 1540px);
      margin-left: auto;
      margin-right: auto;
      padding-top: 6px;
      grid-template-columns: minmax(0, 1fr) clamp(300px, 28vw, 390px);
      align-items: stretch;
    }
    body[data-view="calendar"] .calendar-wrap {
      display: flex;
      flex-direction: column;
    }
    body[data-view="calendar"] .calendar-grid-scroll {
      display: flex;
      flex-direction: column;
      gap: 1px;
      flex: 1;
      min-height: 0;
      overflow: auto;
    }
    body[data-view="calendar"] .weekday-row {
      flex: 0 0 auto;
    }
    body[data-view="calendar"] .week-row {
      flex: 1 1 0;
      min-height: 0;
      overflow: hidden;
    }
    body[data-view="calendar"] .day-cell {
      min-height: 0;
      height: 100%;
      overflow: hidden;
    }
    body[data-view="calendar"] .day-link {
      min-height: 0;
      height: 100%;
      display: flex;
      flex-direction: column;
    }
    body[data-view="calendar"] .day-events {
      flex: 1;
      min-height: 0;
      overflow: hidden;
    }
    body[data-view="calendar"] .day-panel {
      display: flex;
      flex-direction: column;
      align-self: stretch;
    }
    body[data-view="calendar"] .day-panel-body {
      flex: 1;
      min-height: 0;
      max-height: none;
    }
    .day-panel {
      border: 0;
      background: var(--bg-card, #212121);
      border-radius: 8px;
      overflow: hidden;
      position: sticky;
      top: 16px;
    }
    .day-panel-head {
      padding: 14px 14px 12px;
      border-bottom: 1px solid #2a2a2a;
      background: var(--bg-card, #212121);
    }
    .day-panel-title {
      font-family: 'Oswald', sans-serif;
      font-size: 20px;
      font-weight: 700;
      letter-spacing: 0.04em;
      text-transform: uppercase;
      color: var(--text-primary);
      margin-bottom: 2px;
    }
    .day-panel-sub {
      font-family: 'JetBrains Mono', monospace;
      font-size: 12px;
      color: var(--accent-orange);
    }
    .day-panel-body {
      padding: 10px;
      display: flex;
      flex-direction: column;
      gap: 8px;
      max-height: 560px;
      overflow: auto;
    }
    .day-group-title {
      font-size: 11px;
      letter-spacing: 0.08em;
      text-transform: uppercase;
      color: #a8a8a8;
      font-weight: 700;
      padding: 2px 2px 0;
    }
    .day-separator {
      display: flex;
      align-items: center;
      gap: 8px;
      font-size: 11px;
      letter-spacing: 0.08em;
      text-transform: uppercase;
      color: #a8a8a8;
      margin: 2px 0;
    }
    .day-separator::before,
    .day-separator::after {
      content: "";
      flex: 1;
      height: 1px;
      background: #343434;
    }
    .day-item {
      border: 0;
      border-radius: 12px;
      background: #2a2a2a;
      padding: 8px;
    }
    .day-item.live { box-shadow: inset 0 0 0 1px rgba(0,212,170,0.32); }
    .day-item.schd { box-shadow: inset 0 0 0 1px rgba(255,107,53,0.32); }
    .day-item-head {
      display: flex;
      align-items: center;
      justify-content: space-between;
      margin-bottom: 4px;
      gap: 8px;
    }
    .day-item-time {
      color: #e0e0e0;
      font-size: 12px;
      font-weight: 700;
      letter-spacing: 0.06em;
    }
    .day-item-text {
      font-size: 13px;
      line-height: 1.35;
      color: #ffffff;
      margin-bottom: 5px;
      word-break: break-word;
    }
    .day-item-platform {
      font-size: 11px;
      color: #a8a8a8;
      text-transform: uppercase;
      letter-spacing: 0.04em;
    }
    .day-item-actions {
      display: flex;
      gap: 6px;
      justify-content: flex-end;
      margin-top: 4px;
    }
    .day-item-btn {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      width: 26px;
      height: 26px;
      border-radius: 6px;
      border: 0;
      background: #343434;
      color: #b8b8b8;
      font-size: 13px;
      cursor: pointer;
      text-decoration: none;
      line-height: 1;
    }
    .day-item-btn:hover { background: #404040; color: #e0e0e0; }
    .day-item-btn-del { color: #a87272; }
    .day-item-btn-del:hover { background: #4a2a2a; color: #e88; }
    .day-item-btn:disabled { opacity: 0.35; cursor: not-allowed; }
    .day-item-meta {
      display: flex;
      gap: 8px;
      flex-wrap: wrap;
      font-size: 11px;
      color: #a8a8a8;
    }
    .header-right {
      display: flex;
      align-items: center;
      gap: 16px;
    }
    .calendar-controls {
      display: flex;
      align-items: center;
      gap: 8px;
    }
    .month-link {
      color: var(--text-primary);
      text-decoration: none;
      border: 0;
      border-radius: 8px;
      width: 32px;
      height: 32px;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      font-size: 14px;
      line-height: 1;
      background: var(--bg-elevated, #2d2d2d);
    }
    .month-link:hover {
      background: #3a3a3a;
    }
    .month-link svg {
      width: 16px;
      height: 16px;
    }
    .month-label {
      font-family: 'Oswald', sans-serif;
      font-size: 20px;
      font-weight: 700;
      letter-spacing: 0.04em;
      color: var(--text-primary);
      min-width: 180px;
      text-align: center;
    }
    .month-go {
      display: inline-flex;
      align-items: center;
      text-decoration: none;
      border: 0;
      background: var(--accent-orange);
      color: #0d0d0d;
      border-radius: 999px;
      min-height: 30px;
      padding: 0 14px;
      font-size: 12px;
      font-weight: 600;
      text-transform: lowercase;
      line-height: 1;
      margin-left: 4px;
    }
    .weekday-row {
      display: grid;
      grid-template-columns: repeat(7, minmax(0, 1fr));
      gap: 1px;
      min-width: 700px;
    }
    .weekday {
      padding: 8px 8px;
      font-size: 11px;
      letter-spacing: 0.08em;
      text-transform: uppercase;
      color: #a8a8a8;
      border-radius: 4px;
      background: var(--bg-card, #212121);
    }
    .week-row {
      display: grid;
      grid-template-columns: repeat(7, minmax(0, 1fr));
      gap: 1px;
      min-width: 700px;
    }
    .day-cell {
      min-height: 106px;
      min-width: 100px;
      border-radius: 4px;
      padding: 8px;
      background: var(--bg-card, #212121);
    }
    .day-cell.outside { background: var(--bg-card, #212121); }
    .day-cell.outside .day-num { color: #585858; }
    .day-cell.today-cell {
      background: var(--bg-elevated, #2d2d2d);
      box-shadow: inset 0 0 0 2px var(--accent-orange);
    }
    .day-cell.selected {
      background: var(--bg-elevated, #2d2d2d);
      box-shadow: inset 0 0 0 1px rgba(255,107,53,0.55);
    }
    .day-cell.today-cell.selected {
      box-shadow: inset 0 0 0 2px var(--accent-orange);
    }
    .day-link {
      display: block;
      text-decoration: none;
      color: inherit;
      min-height: 90px;
      border-radius: 4px;
      transition: background-color .12s ease;
    }
    .day-link:hover {
      background: rgba(255,255,255,0.03);
    }
    .day-head {
      display: flex;
      align-items: center;
      gap: 4px;
      margin-bottom: 7px;
    }
    .day-count { margin-left: auto; }
    .day-num {
      font-size: 12px;
      color: #c4c4c4;
    }
    .day-num.today {
      color: var(--accent-orange);
      font-weight: 700;
    }
    .today-badge {
      font-family: 'JetBrains Mono', monospace;
      font-size: 10px;
      color: var(--accent-orange);
    }
    .day-count {
      border-radius: 999px;
      background: #343434;
      color: #b8b8b8;
      padding: 1px 6px;
      font-size: 10px;
      line-height: 1.4;
      min-width: 16px;
      text-align: center;
    }
    .day-cell.selected .day-count {
      background: #4a3127;
      color: #ffc6af;
    }
    .day-events {
      display: flex;
      flex-direction: column;
      gap: 3px;
    }
    .day-event {
      flex: 0 0 auto;
      border-radius: 4px;
      padding: 3px 6px;
      background: #3a3a3a;
      border: 0;
      font-size: 10px;
      font-weight: 600;
      color: #b8b8b8;
      line-height: 1.3;
      display: flex;
      align-items: center;
      gap: 4px;
      min-width: 0;
    }
    .day-event.live {
      background: var(--accent-teal);
      color: #0d0d0d;
    }
    .day-event.schd {
      background: var(--accent-orange);
      color: #0d0d0d;
    }
    .event-dot { display: none; }
    .event-time {
      flex: 0 0 auto;
    }
    .event-title {
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
      min-width: 0;
      flex: 1 1 auto;
    }
    .more {
      font-size: 11px;
      color: #9a9a9a;
      margin-top: 1px;
      padding-left: 2px;
    }
    .list {
      margin-top: 14px;
      display: flex;
      flex-direction: column;
      gap: 10px;
      padding-bottom: 20px;
    }
    .card {
      border: 0;
      border-radius: 16px;
      background: #212121;
      padding: 12px 14px;
      display: flex;
      justify-content: space-between;
      gap: 12px;
      align-items: center;
    }
    .card-editable {
      cursor: pointer;
    }
    .day-item[data-edit-url] {
      cursor: pointer;
    }
    .card.scheduled { box-shadow: inset 0 0 0 1px rgba(255, 107, 53, 0.24); }
    .card.published { box-shadow: inset 0 0 0 1px rgba(0, 212, 170, 0.22); }
    .card.draft { box-shadow: inset 0 0 0 1px rgba(255, 255, 255, 0.08); }
    .card.failed { box-shadow: inset 0 0 0 1px rgba(255, 68, 68, 0.24); }
    .card-left {
      display: flex;
      gap: 10px;
      align-items: flex-start;
      min-width: 0;
      flex: 1;
    }
    .failed-select {
      flex: 0 0 auto;
      padding-top: 3px;
    }
    .content {
      min-width: 0;
    }
    .content .text {
      font-size: 13px;
      line-height: 1.4;
      color: var(--text-primary);
      word-break: break-word;
    }
    .meta {
      margin-top: 5px;
      font-size: 12px;
      color: var(--text-secondary);
      display: flex;
      gap: 14px;
      flex-wrap: wrap;
    }
    .card-actions {
      display: flex;
      gap: 6px;
      align-items: center;
      flex-wrap: wrap;
      justify-content: flex-end;
    }
    .pill {
      border: 0;
      background: #2d2d2d;
      color: #b7b7b7;
      border-radius: 16px;
      padding: 6px 10px;
      font-size: 12px;
      font-weight: 600;
      text-transform: lowercase;
    }
    .pill-link {
      color: inherit;
      text-decoration: none;
      display: inline-flex;
      align-items: center;
    }
    .editor {
      margin-top: 14px;
      border: 0;
      border-radius: 18px;
      background: #212121;
      overflow: hidden;
      max-width: 760px;
    }
    .editor.editor-wide {
      max-width: 920px;
    }
    .editor-head {
      padding: 10px 12px;
      border-bottom: 1px solid #2a2a2a;
      background: #212121;
      font-size: 13px;
      letter-spacing: 0.08em;
      text-transform: uppercase;
      color: #b8b8b8;
      font-weight: 700;
    }
    .editor-body {
      padding: 12px;
      display: flex;
      flex-direction: column;
      gap: 10px;
      align-items: stretch;
      justify-content: flex-start;
    }
    .field {
      display: flex;
      flex-direction: column;
      gap: 6px;
    }
    .field label {
      font-size: 12px;
      color: #a8a8a8;
      text-transform: uppercase;
      letter-spacing: 0.08em;
    }
    .field textarea {
      width: 100%;
      min-height: 150px;
      resize: vertical;
      box-sizing: border-box;
      border-radius: 16px;
      border: 0;
      background: #212121;
      color: var(--text-primary);
      font: inherit;
      font-size: 14px;
      line-height: 1.45;
      padding: 10px;
    }
    .field input[type=datetime-local] {
      min-width: 0;
      width: 100%;
      max-width: none;
    }
    .field input[type=text] {
      min-width: 0;
      width: 100%;
      max-width: none;
      padding: 9px 10px;
      border-radius: 12px;
      border: 0;
      background-color: #2d2d2d;
      color: var(--text-primary);
      font: inherit;
      font-size: 14px;
      box-sizing: border-box;
    }
    .field .date-input {
      width: min(100%, 320px);
    }
    .field select {
      min-width: 0;
      width: 100%;
      max-width: 320px;
      padding: 8px 34px 8px 10px;
      border-radius: 12px;
      border: 0;
      background-color: #2d2d2d;
      color: var(--text-primary);
      font: inherit;
      font-size: 14px;
      appearance: none;
      -webkit-appearance: none;
      -moz-appearance: none;
      background-image:
        linear-gradient(45deg, transparent 50%, #a8a8a8 50%),
        linear-gradient(135deg, #a8a8a8 50%, transparent 50%);
      background-position:
        calc(100% - 16px) calc(50% - 1px),
        calc(100% - 11px) calc(50% - 1px);
      background-size: 5px 5px, 5px 5px;
      background-repeat: no-repeat;
    }
    .field select:focus {
      outline: none;
      box-shadow: 0 0 0 2px rgba(255, 107, 53, 0.2);
    }
    .field input[type=text]:focus {
      outline: none;
      box-shadow: 0 0 0 2px rgba(255, 107, 53, 0.2);
    }
    .field input[readonly] {
      color: #d7d7d7;
      cursor: text;
    }
    .code-block {
      margin: 0;
      background: #2d2d2d;
      color: #d7d7d7;
      border-radius: 12px;
      padding: 10px;
      font-size: 12px;
      line-height: 1.45;
      white-space: pre-wrap;
      word-break: break-word;
      overflow-wrap: anywhere;
    }
    .editor-actions {
      display: flex;
      gap: 8px;
      flex-wrap: wrap;
      align-items: center;
    }
    .btn-primary {
      border: 0;
      background: #ff6b35;
      color: #0d0d0d;
      box-shadow: none;
    }
    .btn-primary:hover:not(:disabled) {
      filter: brightness(1.05);
    }
    .btn-secondary {
      border: 0;
      background: #2d2d2d;
      color: #d0d0d0;
      box-shadow: none;
    }
    .alert {
      border-radius: 10px;
      padding: 8px 10px;
      font-size: 13px;
      border: 1px solid transparent;
    }
    .alert.error {
      border-color: rgba(255,95,112,0.45);
      color: #ffd5da;
      background: rgba(87, 25, 33, 0.42);
    }
    .alert.success {
      border-color: rgba(54,211,191,0.45);
      color: #c2fff5;
      background: rgba(22, 67, 62, 0.42);
    }
    .ghost-btn {
      border: 0;
      color: #d0d0d0;
      background: #2d2d2d;
      border-radius: 16px;
      padding: 6px 10px;
      font-size: 12px;
      text-transform: lowercase;
      text-decoration: none;
      font-weight: 600;
    }
    .ghost-toggle {
      width: 34px;
      height: 18px;
      border-radius: 999px;
      background: #2d2d2d;
      border: 0;
      position: relative;
    }
    .ghost-toggle::after {
      content: "";
      width: 12px;
      height: 12px;
      border-radius: 999px;
      background: #a8a8a8;
      position: absolute;
      top: 2px;
      left: 2px;
    }
    .status-live { color: var(--accent-teal); }
    .status-schd { color: var(--accent-orange); }
    .status-drft { color: #a8a8a8; }
    .status-fail { color: #ff4444; }
    .bulk-actions {
      display: flex;
      align-items: center;
      gap: 8px;
      flex-wrap: wrap;
      margin-bottom: 4px;
    }
    .bulk-actions .pill {
      cursor: pointer;
    }
    .failed-checkbox {
      margin-top: 2px;
      width: 16px;
      height: 16px;
      appearance: none;
      -webkit-appearance: none;
      border-radius: 5px;
      border: 1px solid #4a4a4a;
      background: #2d2d2d;
      display: inline-grid;
      place-content: center;
      box-shadow: inset 0 1px 0 rgba(255,255,255,0.04);
      transition: border-color .12s ease, background .12s ease, box-shadow .12s ease;
      cursor: pointer;
    }
    .failed-checkbox::before {
      content: "";
      width: 8px;
      height: 8px;
      border-radius: 2px;
      background: #ff6b35;
      transform: scale(0);
      transition: transform .12s ease;
      box-shadow: 0 0 10px rgba(255,122,48,0.45);
    }
    .failed-checkbox:hover {
      border-color: #666666;
      background: #343434;
    }
    .failed-checkbox:checked {
      border-color: #ff6b35;
      background: #3a2a24;
    }
    .failed-checkbox:checked::before {
      transform: scale(1);
    }
    .failed-checkbox:focus-visible {
      outline: none;
      box-shadow: 0 0 0 2px rgba(255,122,48,0.25);
    }
    .meta-accent { color: var(--accent-orange); }
    .meta-soft { color: #a8a8a8; }
    form {
      display: flex;
      gap: 6px;
      align-items: center;
      flex-wrap: wrap;
      justify-content: flex-end;
    }
    input[type=datetime-local] {
      min-width: 170px;
      padding: 6px 8px;
      border-radius: 12px;
      border: 0;
      background: #2d2d2d;
      color: var(--text-primary);
      font: inherit;
      font-size: 13px;
      box-shadow: none;
    }
    .date-input {
      position: relative;
      display: inline-flex;
      align-items: stretch;
      width: min(100%, 320px);
      min-height: 36px;
    }
    .date-native-value {
      position: absolute !important;
      inset: 0 !important;
      opacity: 0 !important;
      pointer-events: none !important;
      width: 100% !important;
      height: 100% !important;
      margin: 0 !important;
      padding: 0 !important;
      border: 0 !important;
    }
    .date-display {
      width: 100%;
      min-height: 36px;
      border-radius: 12px;
      border: 0;
      background: #2d2d2d;
      color: #f2f2f2;
      font: inherit;
      font-size: 13px;
      text-align: left;
      padding: 0 38px 0 12px;
      line-height: 1;
      display: inline-flex;
      align-items: center;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
    .date-display[data-empty="1"] {
      color: #a8a8a8;
    }
    .date-input.is-focus .date-display,
    .date-input.is-open .date-display {
      box-shadow: 0 0 0 2px rgba(255, 107, 53, 0.22);
    }
    .date-input.is-invalid .date-display {
      box-shadow: 0 0 0 2px rgba(255, 107, 53, 0.35);
    }
    .date-trigger {
      position: absolute;
      top: 4px;
      right: 4px;
      width: 28px;
      height: calc(100% - 8px);
      border-radius: 8px;
      border: 0;
      background: #3a3a3a;
      color: #d0d0d0;
      font-size: 12px;
      line-height: 1;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      padding: 0;
    }
    .date-picker-popover {
      position: fixed;
      z-index: 80;
      width: min(360px, calc(100vw - 24px));
      background: #212121;
      border-radius: 16px;
      box-shadow: 0 16px 48px rgba(0, 0, 0, 0.55), inset 0 0 0 1px #2f2f2f;
      padding: 12px;
      color: #f2f2f2;
    }
    .date-picker-popover[hidden] {
      display: none;
    }
    .date-picker-head {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 8px;
      margin-bottom: 8px;
    }
    .date-picker-month {
      font-size: 13px;
      font-weight: 600;
      color: #f2f2f2;
      text-transform: lowercase;
      letter-spacing: 0.02em;
    }
    .date-picker-nav {
      width: 30px;
      height: 30px;
      border-radius: 10px;
      padding: 0;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      font-size: 14px;
      line-height: 1;
      background: #2d2d2d;
      color: #d0d0d0;
    }
    .date-picker-weekdays {
      display: grid;
      grid-template-columns: repeat(7, minmax(0, 1fr));
      gap: 4px;
      margin-bottom: 4px;
    }
    .date-picker-weekday {
      font-size: 10px;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      color: #a8a8a8;
      text-align: center;
      padding: 4px 0;
    }
    .date-picker-days {
      display: grid;
      grid-template-columns: repeat(7, minmax(0, 1fr));
      gap: 4px;
    }
    .date-picker-day {
      height: 34px;
      border-radius: 10px;
      background: #2d2d2d;
      color: #e8e8e8;
      padding: 0;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      font-size: 12px;
      font-weight: 500;
      text-transform: none;
    }
    .date-picker-day.outside {
      color: #7d7d7d;
      background: #252525;
    }
    .date-picker-day.today {
      box-shadow: inset 0 0 0 1px rgba(255, 107, 53, 0.4);
    }
    .date-picker-day.selected {
      background: #ff6b35;
      color: #101010;
      font-weight: 700;
    }
    .date-picker-time {
      margin-top: 10px;
      display: flex;
      align-items: center;
      gap: 8px;
      color: #d0d0d0;
    }
    .date-picker-time select {
      min-width: 0;
      border: 0;
      border-radius: 10px;
      background: #2d2d2d;
      color: #f2f2f2;
      font: inherit;
      font-size: 12px;
      padding: 6px 10px;
      appearance: none;
      -webkit-appearance: none;
      -moz-appearance: none;
      box-shadow: none;
    }
    .date-picker-time-divider {
      color: #a8a8a8;
      font-size: 13px;
      font-weight: 600;
      line-height: 1;
    }
    .date-picker-actions {
      margin-top: 10px;
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 8px;
    }
    .date-picker-actions-left,
    .date-picker-actions-right {
      display: inline-flex;
      gap: 8px;
    }
    .date-picker-actions button {
      min-height: 30px;
      padding: 0 10px;
      border-radius: 10px;
    }
    .date-picker-popover[data-mode="date"] .date-picker-time {
      display: none;
    }
    button {
      border: 0;
      background: #2d2d2d;
      color: #d0d0d0;
      border-radius: 16px;
      padding: 7px 12px;
      font-size: 12px;
      font-weight: 600;
      text-transform: lowercase;
      cursor: pointer;
      box-shadow: none;
      transition: transform .08s ease, filter .12s ease;
    }
    button:hover:not(:disabled) {
      filter: brightness(1.05);
    }
    button:active:not(:disabled) {
      transform: translateY(1px);
    }
    button:focus-visible {
      outline: none;
      box-shadow: 0 0 0 2px rgba(255, 107, 53, 0.25);
    }
    button:disabled {
      opacity: 0.52;
      cursor: not-allowed;
      filter: none;
    }
    .empty {
      border: 1px dashed #343434;
      background: #202020;
      border-radius: 16px;
      padding: 18px;
      font-size: 12px;
      color: var(--text-secondary);
      text-align: center;
    }
    .line {
      margin-top: 14px;
      margin-bottom: 8px;
      color: var(--text-secondary);
      font-size: 11px;
      text-transform: uppercase;
      letter-spacing: 0.06em;
    }
    .composer-layout {
      margin-top: 12px;
      display: grid;
      grid-template-columns: minmax(0, 1fr) 360px;
      gap: 24px;
      align-items: stretch;
    }
    .composer-main {
      min-width: 0;
    }
    .composer-main .editor {
      margin-top: 0;
      max-width: none;
      background: transparent;
      border: 0;
      border-radius: 0;
      overflow: visible;
    }
    body[data-view="create"] .composer-main .editor-head {
      display: none;
    }
    body[data-view="create"] .composer-main .editor-body {
      padding: 0;
      gap: 18px;
    }
    .composer-label {
      font-family: 'JetBrains Mono', monospace;
      font-size: 11px;
      color: var(--text-secondary);
      text-transform: lowercase;
      letter-spacing: 0.08em;
      font-weight: 600;
      margin-bottom: 2px;
    }
    .create-field {
      display: flex;
      flex-direction: column;
      gap: 8px;
    }
    .create-field-content .composer-text-wrap,
    .create-field-media .media-block,
    .create-field-schedule .schedule-block {
      background: var(--bg-card, #212121);
      border-radius: 12px;
      padding: 20px;
    }
    .create-field-content .composer-text-wrap {
      gap: 12px;
      display: flex;
      flex-direction: column;
      min-height: clamp(320px, 46vh, 520px);
    }
    .create-field-content .composer-text-wrap .composer-label {
      margin-bottom: 0;
    }
    .create-field-schedule .schedule-block {
      padding: 12px 14px;
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 10px;
      flex-wrap: wrap;
    }
    .schedule-label {
      font-size: 11px;
      color: #a8a8a8;
      text-transform: uppercase;
      letter-spacing: 0.08em;
    }
    .network-picker {
      display: flex;
      gap: 8px;
      flex-wrap: wrap;
    }
    .network-chip {
      border: 0;
      background: var(--bg-card, #212121);
      color: var(--text-secondary);
      border-radius: 12px;
      padding: 10px 14px;
      font-family: 'JetBrains Mono', monospace;
      font-size: 12px;
      font-weight: 600;
      display: inline-flex;
      align-items: center;
      gap: 8px;
      cursor: pointer;
    }
    .network-chip svg {
      flex: 0 0 auto;
    }
    .network-chip.active {
      background: var(--bg-elevated, #2d2d2d);
      color: var(--accent-orange);
      box-shadow: inset 0 0 0 2px var(--accent-orange);
    }
    .network-chip.active::after {
      content: "✓";
      font-size: 11px;
      line-height: 1;
    }
    .network-chip.disabled {
      background: var(--bg-card, #212121);
      opacity: 1;
      cursor: not-allowed;
    }
    .composer-text-wrap {
      border: 0;
      border-radius: 12px;
      background: transparent;
      overflow: hidden;
    }
    .composer-text-wrap textarea {
      border: 0;
      background: transparent;
      width: 100%;
      min-height: clamp(250px, 34vh, 420px);
      resize: vertical;
      display: block;
      color: #ffffff;
      padding: 0;
    }
    .composer-text-meta {
      border-top: 1px solid #2f2f2f;
      padding: 10px 0 0;
      display: flex;
      justify-content: space-between;
      gap: 8px;
      align-items: center;
      color: #a8a8a8;
      font-size: 11px;
    }
    .char-over {
      color: var(--accent-orange);
    }
    .composer-format-btns {
      display: flex;
      gap: 8px;
      font-size: 16px;
      color: var(--text-secondary);
    }
    .media-upload-actions {
      display: flex;
      align-items: center;
      gap: 8px;
      flex-wrap: wrap;
    }
    .media-block {
      display: flex;
      flex-direction: column;
      gap: 12px;
    }
    .upload-notice {
      font-size: 12px;
      color: #a8a8a8;
    }
    .upload-notice[data-state="error"] {
      color: #ffb0b9;
    }
    .upload-notice[data-state="success"] {
      color: #8be7d7;
    }
    .media-list {
      display: flex;
      flex-direction: column;
      gap: 10px;
    }
    .media-item {
      border: 0;
      border-radius: 12px;
      background: #2a2a2a;
      padding: 8px 10px;
      display: grid;
      grid-template-columns: 44px minmax(0, 1fr) auto;
      gap: 10px;
      align-items: center;
    }
    .media-thumb {
      width: 44px;
      height: 44px;
      border-radius: 8px;
      background: #2d2d2d;
      border: 0;
      background-size: cover;
      background-position: center;
      display: grid;
      place-items: center;
      color: #a8a8a8;
      font-size: 11px;
      text-transform: uppercase;
    }
    .media-info {
      min-width: 0;
    }
    .media-name {
      font-size: 12px;
      color: #ffffff;
      white-space: nowrap;
      text-overflow: ellipsis;
      overflow: hidden;
    }
    .media-meta {
      margin-top: 2px;
      font-size: 11px;
      color: #a8a8a8;
    }
    .media-item-actions {
      display: inline-flex;
      align-items: center;
      gap: 6px;
    }
    .media-item-actions button {
      padding: 5px 9px;
      font-size: 11px;
      min-height: 26px;
    }
    .media-item-actions .btn-secondary {
      border: 0;
      background: #2d2d2d;
      color: #a8a8a8;
    }
    .media-item-actions .btn-danger {
      border: 0;
      background: #2d2d2d;
      color: #ff4444;
    }
    .media-library-wrap {
      display: flex;
      flex-direction: column;
      gap: 8px;
    }
    .media-library {
      display: flex;
      flex-direction: column;
      gap: 8px;
      max-height: 260px;
      overflow: auto;
      padding-right: 2px;
    }
    .settings-media-library {
      max-height: 440px;
    }
    .media-library-item {
      border: 0;
      border-radius: 12px;
      background: #2a2a2a;
      padding: 8px 10px;
      display: grid;
      grid-template-columns: 44px minmax(0, 1fr) auto;
      gap: 10px;
      align-items: center;
    }
    .media-library-item.in-use {
      opacity: 0.9;
    }
    .media-library-thumb {
      width: 44px;
      height: 44px;
      border-radius: 8px;
      background: #2d2d2d;
      overflow: hidden;
      display: grid;
      place-items: center;
      color: #a8a8a8;
      font-size: 10px;
      text-transform: uppercase;
    }
    .media-library-thumb img {
      width: 100%;
      height: 100%;
      object-fit: cover;
      display: block;
    }
    .media-library-info {
      min-width: 0;
    }
    .media-library-name {
      font-size: 12px;
      color: #ffffff;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
    .media-library-meta {
      margin-top: 2px;
      font-size: 11px;
      color: #a8a8a8;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
    .media-library-actions {
      display: inline-flex;
      align-items: center;
      gap: 6px;
      flex-wrap: wrap;
      justify-content: flex-end;
    }
    .media-library-actions form {
      margin: 0;
    }
    .media-library-actions .btn-secondary,
    .media-library-actions .btn-danger {
      min-height: 26px;
      font-size: 11px;
      padding: 5px 9px;
      border-radius: 12px;
      text-decoration: none;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      cursor: pointer;
    }
    .media-library-actions [data-media-attach].attached {
      background: rgba(0, 212, 170, 0.2);
      color: #8be7d7;
    }
    .media-pill-used {
      background: #2d2d2d;
      color: #a8a8a8;
      font-size: 10px;
      padding: 5px 8px;
    }
    .preview-panel {
      border: 0;
      background: transparent;
      overflow: visible;
      padding: 0;
      display: flex;
      flex-direction: column;
      gap: 16px;
    }
    .preview-head {
      padding: 0;
      border-bottom: 0;
      background: transparent;
    }
    .preview-title {
      font-size: 11px;
      letter-spacing: 0.08em;
      text-transform: lowercase;
      color: var(--text-secondary);
      font-weight: 600;
      margin-bottom: 6px;
    }
    .preview-platforms {
      display: flex;
      gap: 4px;
      flex-wrap: wrap;
      font-family: 'JetBrains Mono', monospace;
      font-size: 11px;
      font-weight: 600;
      color: var(--text-secondary);
    }
    .preview-platforms span {
      border-radius: 8px;
      background: var(--bg-elevated, #2d2d2d);
      padding: 6px 12px;
      min-width: 24px;
      text-align: center;
      text-transform: uppercase;
    }
    .preview-platforms .active {
      color: var(--text-primary);
      background: var(--bg-elevated, #2d2d2d);
    }
    .preview-body {
      padding: 0;
      flex: 1;
      min-height: 0;
    }
    .preview-card {
      border: 0;
      border-radius: 12px;
      background: var(--bg-card, #212121);
      padding: 16px;
      flex: 1;
      display: flex;
      flex-direction: column;
    }
    .preview-author {
      display: flex;
      align-items: center;
      gap: 8px;
      margin-bottom: 10px;
    }
    .preview-avatar {
      width: 30px;
      height: 30px;
      border-radius: 999px;
      background: #2d2d2d;
      color: #cfcfcf;
      display: grid;
      place-items: center;
      font-weight: 700;
      font-size: 12px;
    }
    .preview-name {
      font-size: 12px;
      color: #ffffff;
      font-weight: 700;
    }
    .preview-handle {
      font-size: 11px;
      color: #a8a8a8;
    }
    .preview-text {
      font-size: 13px;
      line-height: 1.45;
      color: #ffffff;
      white-space: pre-wrap;
      word-break: break-word;
      min-height: 68px;
    }
    .preview-media {
      margin-top: 10px;
      border: 0;
      border-radius: 8px;
      background: #2d2d2d;
      min-height: 120px;
      display: grid;
      place-items: center;
      overflow: hidden;
    }
    .preview-media img {
      width: 100%;
      height: 100%;
      object-fit: cover;
      display: block;
    }
    .preview-media[hidden],
    .preview-media img[hidden] {
      display: none;
    }
    .preview-media-empty {
      font-size: 12px;
      color: #a8a8a8;
      padding: 10px;
      text-align: center;
    }
    .preview-footer {
      margin-top: 10px;
      font-size: 11px;
      color: #a8a8a8;
    }
    .composer-submit-actions {
      justify-content: flex-end;
      display: none;
    }
    @media (max-width: 980px) {
      .app { flex-direction: column; }
      .sidebar {
        display: block;
        width: 100%;
        border-right: 0;
        border-bottom: 1px solid #191e29;
        padding: 12px 12px 10px;
        position: sticky;
        top: 0;
        z-index: 20;
        backdrop-filter: blur(6px);
      }
      .logo {
        margin-bottom: 10px;
        padding: 0 2px;
      }
      .nav {
        flex: initial;
        flex-direction: row;
        overflow-x: auto;
        padding-bottom: 2px;
      }
      .nav-item-settings {
        margin-top: 0;
      }
      .nav-item {
        white-space: nowrap;
        flex: 0 0 auto;
      }
      .main {
        padding: 16px 12px 18px;
      }
      .header {
        flex-direction: row;
        align-items: flex-start;
        gap: 10px;
      }
      body[data-view="calendar"] .header {
        display: flex;
        flex-direction: row;
        align-items: flex-start;
        gap: 10px;
      }
      .title-row {
        align-items: flex-start;
      }
      body[data-view="calendar"] .title-row {
        align-items: flex-start;
        flex-wrap: wrap;
      }
      .title-copy {
        gap: 2px;
      }
      h1 { font-size: 34px; }
      .title-sub {
        font-size: 11px;
      }
      .create-pill {
        padding: 8px 12px;
        font-size: 11px;
      }
      .create-header-actions {
        width: 100%;
        justify-content: flex-start;
      }
      .create-header-actions button {
        min-height: 32px;
        padding: 0 12px;
      }
      .tabs {
        flex-wrap: nowrap;
        overflow-x: auto;
        padding-bottom: 2px;
      }
      .tab {
        white-space: nowrap;
        flex: 0 0 auto;
      }
      .stats { grid-template-columns: repeat(2, minmax(0, 1fr)); }
      .calendar-layout {
        grid-template-columns: 1fr;
        gap: 10px;
      }
      .header-right {
        flex-wrap: nowrap;
        gap: 8px;
      }
      body[data-view="calendar"] .header-right {
        margin-left: auto;
      }
      .calendar-controls {
        gap: 4px;
      }
      body[data-view="calendar"] .calendar-controls {
        margin-left: 0;
        flex-wrap: wrap;
        justify-content: flex-start;
      }
      .month-label {
        font-size: 16px;
        min-width: auto;
      }
      .month-link {
        width: 28px;
        height: 28px;
      }
      .month-go {
        min-height: 28px;
        padding: 0 10px;
        font-size: 11px;
      }
      body[data-view="calendar"] .calendar-grid-scroll {
        display: block;
      }
      body[data-view="calendar"] .week-row {
        flex: initial;
        min-height: initial;
      }
      .day-cell { min-height: 82px; height: auto; }
      body[data-view="calendar"] .day-link {
        min-height: 0;
        height: auto;
        display: block;
      }
      body[data-view="calendar"] .day-events {
        flex: initial;
        min-height: initial;
      }
      .calendar-layout { grid-template-columns: 1fr; }
      .day-panel {
        position: static;
        max-height: none;
      }
      .day-panel-body {
        max-height: none;
      }
      .card {
        flex-direction: column;
        align-items: stretch;
        padding: 10px 10px;
      }
      .card-left {
        gap: 8px;
      }
      .card-actions, form {
        justify-content: flex-start;
      }
      .field textarea {
        min-height: 140px;
      }
      .field input[type=datetime-local] {
        max-width: none;
      }
      .field .date-input {
        width: 100%;
        max-width: none;
      }
      .editor-actions {
        width: 100%;
      }
      .editor-actions button, .editor-actions .ghost-btn {
        width: 100%;
        justify-content: center;
        text-align: center;
      }
      .composer-layout {
        grid-template-columns: 1fr;
      }
      .preview-panel {
        padding: 0;
      }
      .composer-submit-actions {
        display: flex;
        justify-content: stretch;
      }
      .composer-submit-actions button {
        width: 100%;
      }
      .create-field-content .composer-text-wrap,
      .create-field-media .media-block {
        padding: 14px;
      }
      .create-field-schedule .schedule-block {
        padding: 10px 12px;
      }
    }
    @media (max-width: 520px) {
      h1 { font-size: 30px; }
      .stats { grid-template-columns: 1fr; }
      .content .text { font-size: 12px; }
      .meta { font-size: 11px; gap: 8px; }
    }
  </style>
</head>
<body data-view="{{.View}}" data-ui-timezone="{{.UITimezone}}">
  <div class="app">
    <aside class="sidebar" aria-label="Primary navigation">
      <div class="logo">
        <span class="logo-dot"></span>
        <span>post_flow</span>
      </div>
      <nav class="nav">
        <a class="nav-item {{if eq .ActiveNavView "calendar"}}active{{end}}" href="/?view=calendar&month={{.CurrentMonthParam}}&day={{.SelectedDayKey}}">
          <span class="nav-main">
            <svg class="nav-icon nav-icon-calendar" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="4" width="18" height="18" rx="2" ry="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/></svg>
            <span>calendar</span>
          </span>
        </a>
        <a class="nav-item {{if eq .ActiveNavView "publications"}}active{{end}}" href="/?view=publications">
          <span class="nav-main">
            <svg class="nav-icon nav-icon-scheduled" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>
            <span>scheduled</span>
          </span>
          {{if gt .ScheduledCount 0}}<span class="nav-badge">{{.ScheduledCount}}</span>{{end}}
        </a>
        <a class="nav-item {{if eq .ActiveNavView "drafts"}}active{{end}}" href="/?view=drafts">
          <span class="nav-main">
            <svg class="nav-icon nav-icon-drafts" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M14.5 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V7.5L14.5 2z"/><polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/><line x1="16" y1="17" x2="8" y2="17"/><line x1="10" y1="9" x2="8" y2="9"/></svg>
            <span>drafts</span>
          </span>
          {{if gt .DraftCount 0}}<span class="nav-badge">{{.DraftCount}}</span>{{end}}
        </a>
        <a class="nav-item {{if eq .ActiveNavView "failed"}}active{{end}}" href="/?view=failed">
          <span class="nav-main">
            <svg class="nav-icon nav-icon-failed" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M10.29 3.86L1.82 18a2 2 0 001.71 3h16.94a2 2 0 001.71-3L13.71 3.86a2 2 0 00-3.42 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/></svg>
            <span>failed</span>
          </span>
          {{if gt .FailedCount 0}}<span class="nav-badge nav-badge-danger">{{.FailedCount}}</span>{{end}}
        </a>
        <a class="nav-item nav-item-settings {{if eq .ActiveNavView "settings"}}active{{end}}" href="/?view=settings">
          <span class="nav-main">
            <svg class="nav-icon nav-icon-settings" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 00.33 1.82l.06.06a2 2 0 010 2.83 2 2 0 01-2.83 0l-.06-.06a1.65 1.65 0 00-1.82-.33 1.65 1.65 0 00-1 1.51V21a2 2 0 01-4 0v-.09A1.65 1.65 0 009 19.4a1.65 1.65 0 00-1.82.33l-.06.06a2 2 0 01-2.83 0 2 2 0 010-2.83l.06-.06A1.65 1.65 0 004.68 15a1.65 1.65 0 00-1.51-1H3a2 2 0 010-4h.09A1.65 1.65 0 004.6 9a1.65 1.65 0 00-.33-1.82l-.06-.06a2 2 0 012.83-2.83l.06.06A1.65 1.65 0 009 4.68a1.65 1.65 0 001-1.51V3a2 2 0 014 0v.09a1.65 1.65 0 001 1.51 1.65 1.65 0 001.82-.33l.06-.06a2 2 0 012.83 2.83l-.06.06A1.65 1.65 0 0019.4 9a1.65 1.65 0 001.51 1H21a2 2 0 010 4h-.09a1.65 1.65 0 00-1.51 1z"/></svg>
            <span>settings</span>
          </span>
        </a>
      </nav>
    </aside>
    <main class="main">
      <header class="header">
        <div class="title-row">
          {{if and (eq .View "create") .BackURL}}<a class="title-back" href="{{.BackURL}}" aria-label="back">←</a>{{end}}
          <div class="title-copy">
            <h1>{{if eq .View "calendar"}}CALENDAR{{else if eq .View "drafts"}}DRAFTS{{else if eq .View "failed"}}FAILED{{else if eq .View "create"}}NEW POST{{else if eq .View "settings"}}SETTINGS{{else}}SCHEDULED{{end}}</h1>
            {{if eq .View "calendar"}}<div class="title-sub">// scheduled content overview</div>{{end}}
            {{if eq .View "create"}}<div class="title-sub">// compose and schedule your content</div>{{end}}
          </div>
          {{if eq .View "calendar"}}
          <div class="calendar-controls">
            <a class="month-link" href="/?view=calendar&month={{.PrevMonthParam}}&day={{.SelectedDayKey}}" aria-label="Previous month"><svg width="16" height="16" viewBox="0 0 16 16" fill="none"><path d="M10 12L6 8l4-4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg></a>
            <span class="month-label">{{.CalendarMonthLabel}}</span>
            <a class="month-link" href="/?view=calendar&month={{.NextMonthParam}}&day={{.SelectedDayKey}}" aria-label="Next month"><svg width="16" height="16" viewBox="0 0 16 16" fill="none"><path d="M6 4l4 4-4 4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg></a>
            <a class="month-go" href="/?view=calendar&month={{.TodayMonthParam}}&day={{.TodayDayKey}}">today</a>
          </div>
          {{end}}
        </div>
        {{if eq .View "calendar"}}
        <div class="header-right">
          <a class="create-pill" href="{{.CreateViewURL}}">create_post</a>
        </div>
        {{else if eq .View "create"}}
        <div class="create-header-actions">
          <button class="btn-secondary" type="submit" form="create-post-form" name="intent" value="draft"><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M19 21H5a2 2 0 01-2-2V5a2 2 0 012-2h11l5 5v11a2 2 0 01-2 2z"/><polyline points="17 21 17 13 7 13 7 21"/><polyline points="7 3 7 8 15 8"/></svg> save_draft</button>
          <button class="btn-secondary btn-schedule" type="submit" form="create-post-form" name="intent" value="schedule"><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="18" rx="2" ry="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/></svg> {{if .EditingPost}}update_schedule{{else}}schedule{{end}}</button>
          <button class="btn-primary" type="submit" form="create-post-form" name="intent" value="publish_now"><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="22" y1="2" x2="11" y2="13"/><polygon points="22 2 15 22 11 13 2 9 22 2"/></svg> publish_now</button>
        </div>
        {{else}}
        <a class="create-pill" href="{{.CreateViewURL}}">create_post</a>
        {{end}}
      </header>
      {{if eq .View "calendar"}}
      <div class="calendar-layout">
      <div class="calendar-wrap">
        <div class="calendar-grid-scroll">
          <div class="weekday-row">
            <div class="weekday">Mon</div>
            <div class="weekday">Tue</div>
            <div class="weekday">Wed</div>
            <div class="weekday">Thu</div>
            <div class="weekday">Fri</div>
            <div class="weekday">Sat</div>
            <div class="weekday">Sun</div>
          </div>
          {{range .CalendarWeeks}}
          <div class="week-row">
            {{range .}}
            <div class="day-cell {{if not .InCurrentMonth}}outside{{end}} {{if .IsToday}}today-cell{{end}} {{if .IsSelected}}selected{{end}}">
              <a class="day-link" href="/?view=calendar&month={{$.CurrentMonthParam}}&day={{.DateKey}}">
                <div class="day-head">
                  <span class="day-num {{if .IsToday}}today{{end}}">{{.DayNumber}}</span>
                  {{if .IsToday}}<span class="today-badge">// today</span>{{end}}
                  {{if gt (len .Events) 0}}<span class="day-count">{{len .Events}}</span>{{end}}
                </div>
                <div class="day-events" data-day-events>
                  {{range .Events}}
                  <div class="day-event {{.StatusClass}}" data-day-event data-status="{{.StatusKey}}">
                    <span class="event-dot" aria-hidden="true"></span>
                    <span class="event-time">{{.TimeLabel}}</span>
                    <span class="event-title">{{.TextPreview}}</span>
                  </div>
                  {{end}}
                  {{if gt (len .Events) 0}}
                  <div class="more" data-day-overflow hidden></div>
                  {{end}}
                </div>
              </a>
            </div>
            {{end}}
          </div>
          {{end}}
        </div>
      </div>
      <aside class="day-panel" aria-label="Day detail">
        <div class="day-panel-head">
          <div class="day-panel-title">{{.SelectedDayLabel}}</div>
          <div class="day-panel-sub">// {{len .SelectedDayPendingItems}} scheduled posts</div>
        </div>
        <div class="day-panel-body">
          {{if and (eq (len .SelectedDayPendingItems) 0) (eq (len .SelectedDayPublishedItems) 0)}}
          <div class="empty">No hay publicaciones para este día.</div>
          {{else}}
          {{if gt (len .SelectedDayPendingItems) 0}}
          <div class="day-group-title">to publish ({{len .SelectedDayPendingItems}})</div>
          {{range .SelectedDayPendingItems}}
          <article class="day-item {{.StatusClass}}" data-status="{{.StatusKey}}" {{if .Editable}}data-edit-url="/?view=create&edit_id={{.PostID}}&return_to={{urlquery $.CurrentViewURL}}"{{end}}>
            <div class="day-item-head">
              <span class="day-item-time">{{.TimeLabel}}</span>
              <span class="day-item-platform">{{.Platform}}</span>
            </div>
            <div class="day-item-text">{{.Text}}</div>
            <div class="day-item-actions">
              {{if .Editable}}<a class="day-item-btn" href="/?view=create&edit_id={{.PostID}}&return_to={{urlquery $.CurrentViewURL}}" title="Edit">&#9998;</a>{{end}}
              <button class="day-item-btn day-item-btn-del" title="Delete" disabled>&#10005;</button>
            </div>
          </article>
          {{end}}
          {{end}}
          {{if and (gt (len .SelectedDayPendingItems) 0) (gt (len .SelectedDayPublishedItems) 0)}}
          <div class="day-separator">published</div>
          {{end}}
          {{if gt (len .SelectedDayPublishedItems) 0}}
          <div class="day-group-title">published ({{len .SelectedDayPublishedItems}})</div>
          {{range .SelectedDayPublishedItems}}
          <article class="day-item {{.StatusClass}}" data-status="{{.StatusKey}}">
            <div class="day-item-head">
              <span class="day-item-time">{{.TimeLabel}}</span>
              <span class="day-item-platform">{{.Platform}}</span>
            </div>
            <div class="day-item-text">{{.Text}}</div>
            <div class="day-item-actions">
              <button class="day-item-btn day-item-btn-del" title="Delete" disabled>&#10005;</button>
            </div>
          </article>
          {{end}}
          {{end}}
          {{end}}
        </div>
      </aside>
      </div>
      {{end}}

      {{if eq .View "publications"}}
      <div class="line">upcoming queue</div>
      <section class="list">
        {{range .Publications}}
        <article class="card scheduled card-editable" data-edit-url="/?view=create&edit_id={{.ID}}&return_to={{urlquery $.CurrentViewURL}}">
          <div class="card-left">
            <div class="content">
              <div class="text">{{.Text}}</div>
              <div class="meta">
                <span class="meta-accent">{{if .ScheduledAt.IsZero}}no date{{else}}{{.ScheduledAt.Format "2006-01-02 15:04 MST"}}{{end}}</span>
                <span>{{.Platform}}</span>
                <span>{{len .Media}} media</span>
              </div>
            </div>
          </div>
        </article>
        {{else}}
        <div class="empty">No hay publicaciones programadas para los próximos {{.PublicationsWindowDays}} días.</div>
        {{end}}
      </section>
      {{end}}

      {{if eq .View "drafts"}}
      <div class="line">draft queue</div>
      <section class="list">
        {{range .Drafts}}
        <article class="card draft card-editable" data-status="draft" data-edit-url="/?view=create&edit_id={{.ID}}&return_to={{urlquery $.CurrentViewURL}}">
          <div class="card-left">
            <div class="content">
              <div class="text">{{.Text}}</div>
              <div class="meta">
                <span class="meta-soft">no date assigned</span>
                <span>{{.Platform}}</span>
                <span>{{len .Media}} media</span>
              </div>
            </div>
          </div>
          <div class="card-actions">
            <form method="post" action="/posts/{{.ID}}/schedule">
              <input type="datetime-local" name="scheduled_at_local" required data-date-picker aria-label="scheduled at for draft {{.ID}}" />
              <button type="submit" class="btn-primary">schedule</button>
            </form>
          </div>
        </article>
        {{else}}
        <div class="empty">No hay borradores aún. Crea ideas por API y aparecerán aquí.</div>
        {{end}}
      </section>
      {{end}}

      {{if eq .View "failed"}}
      <div class="line">failed queue</div>
      <section class="list">
        {{if .FailedError}}<div class="alert error">{{.FailedError}}</div>{{end}}
        {{if .FailedSuccess}}<div class="alert success">{{.FailedSuccess}}</div>{{end}}
        <div class="bulk-actions">
          <button type="button" class="pill" id="failed-select-all">mark all</button>
          <button type="button" class="pill" id="failed-clear-all">clear all</button>
          <form method="post" action="/dlq/requeue" id="failed-bulk-form">
            <button type="submit" id="failed-requeue-selected" class="btn-primary" disabled>requeue selected</button>
          </form>
        </div>
        {{range .FailedItems}}
        <article class="card failed card-editable" data-edit-url="/?view=create&edit_id={{.PostID}}&return_to={{urlquery $.CurrentViewURL}}">
          <div class="card-left">
            <div class="failed-select">
              <input class="failed-checkbox" type="checkbox" value="{{.DeadLetterID}}" data-failed-checkbox aria-label="select failed publication {{.PostID}}" />
            </div>
            <div class="content">
              <div class="text">{{.Text}}</div>
              <div class="meta">
                <span class="meta-soft">{{.ScheduledAtLabel}}</span>
                <span>{{.Platform}}</span>
                <span>{{.MediaCount}} media</span>
                <span>attempts {{.Attempts}}/{{.MaxAttempts}}</span>
                <span>failed {{.FailedAtLabel}}</span>
                <span>{{.LastError}}</span>
              </div>
            </div>
          </div>
          <div class="card-actions">
            <form method="post" action="/dlq/{{.DeadLetterID}}/requeue">
              <button type="submit" class="btn-secondary">requeue</button>
            </form>
          </div>
        </article>
        {{else}}
        <div class="empty">No hay publicaciones fallidas en cola.</div>
        {{end}}
      </section>
      {{end}}

      {{if eq .View "create"}}
      <div class="composer-layout">
        <section class="composer-main">
          <section class="editor">
            <div class="editor-head">{{if .EditingPost}}edit publication{{else}}new post{{end}}</div>
            <form class="editor-body" id="create-post-form" method="post" action="{{if .EditingPost}}/posts/{{.EditingPost.ID}}/edit{{else}}/posts{{end}}">
              <input id="create-platform" type="hidden" name="platform" value="x" />
              <input type="hidden" name="return_to" value="{{.ReturnTo}}" />
              <div id="create-media-hidden"></div>
              {{if .CreateError}}<div class="alert error">{{.CreateError}}</div>{{end}}
              {{if .CreateSuccess}}<div class="alert success">{{.CreateSuccess}}</div>{{end}}
              {{if .MediaError}}<div class="alert error">{{.MediaError}}</div>{{end}}
              {{if .MediaSuccess}}<div class="alert success">{{.MediaSuccess}}</div>{{end}}

              <div class="field create-field create-field-networks">
                <div class="composer-label">// select networks</div>
                <div class="network-picker" id="create-network-picker">
                  <button type="button" class="network-chip active" data-network-chip data-platform="x" aria-pressed="true"><svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 4l11.733 16h4.267l-11.733-16zM4 20l6.768-6.768M20 4l-6.768 6.768"/></svg> X</button>
                  <button type="button" class="network-chip disabled" disabled aria-disabled="true"><svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M16 8a6 6 0 016 6v7h-4v-7a2 2 0 00-4 0v7h-4v-7a6 6 0 016-6z"/><rect x="2" y="9" width="4" height="12"/><circle cx="4" cy="4" r="2"/></svg> LinkedIn</button>
                  <button type="button" class="network-chip disabled" disabled aria-disabled="true"><svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="2" width="20" height="20" rx="5" ry="5"/><path d="M16 11.37A4 4 0 1112.63 8 4 4 0 0116 11.37z"/><line x1="17.5" y1="6.5" x2="17.51" y2="6.5"/></svg> Instagram</button>
                  <button type="button" class="network-chip disabled" disabled aria-disabled="true"><svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 2h-3a5 5 0 00-5 5v3H7v4h3v8h4v-8h3l1-4h-4V7a1 1 0 011-1h3z"/></svg> Facebook</button>
                </div>
              </div>

              <div class="field create-field create-field-content">
                <div class="composer-text-wrap">
                  <div class="composer-label">// post content</div>
                  <textarea id="create-text" name="text" required placeholder="Write your post...">{{.CreateText}}</textarea>
                  <div class="composer-text-meta">
                    <span id="create-char-count">// 0/280 chars (X limit)</span>
                    <span class="composer-format-btns">☺ # @</span>
                  </div>
                </div>
              </div>

              <div class="field create-field create-field-schedule">
                <div class="schedule-block">
                  <label class="schedule-label" for="create-scheduled-at">scheduled at ({{.UITimezone}})</label>
                  <input id="create-scheduled-at" type="datetime-local" name="scheduled_at_local" data-date-picker value="{{.CreateScheduledLocal}}" />
                </div>
              </div>

              <div class="field create-field create-field-media">
                <div class="composer-label">// media attachment (4 max)</div>
                <div class="media-block">
                  <div class="media-upload-actions">
                    <input id="create-media-input" type="file" accept="image/*,video/*" multiple hidden />
                    <button type="button" class="pill" id="create-media-trigger">upload files</button>
                    <span class="upload-notice" id="create-upload-notice">no media uploaded</span>
                  </div>
                  <div class="media-list" id="create-media-list"></div>
                  <div class="media-library-wrap">
                    <div class="composer-label">// recent library</div>
                    <div class="media-library" id="create-media-library">
                      {{range .CreateRecentMedia}}
                      <article class="media-library-item {{if .InUse}}in-use{{end}}" data-media-library-item data-media-id="{{.ID}}" data-media-name="{{.OriginalName}}" data-media-size="{{.SizeBytes}}" data-media-mime="{{.MimeType}}" data-media-preview="{{.PreviewURL}}">
                        <div class="media-library-thumb">
                          {{if .IsImage}}<img src="{{.PreviewURL}}" alt="{{.OriginalName}}" loading="lazy" />{{else if .IsVideo}}<span>video</span>{{else}}<span>file</span>{{end}}
                        </div>
                        <div class="media-library-info">
                          <div class="media-library-name">{{.OriginalName}}</div>
                          <div class="media-library-meta">{{.SizeLabel}} · {{.CreatedLabel}}{{if .InUse}} · used {{.UsageCount}}{{end}}</div>
                        </div>
                        <div class="media-library-actions">
                          <button type="button" class="btn-secondary" data-media-attach="{{.ID}}">attach</button>
                          {{if .InUse}}
                          <span class="pill media-pill-used">in_use</span>
                          {{else}}
                          <button type="button" class="btn-danger" data-media-delete="{{.ID}}">delete</button>
                          {{end}}
                        </div>
                      </article>
                      {{else}}
                      <div class="empty">No media uploaded yet.</div>
                      {{end}}
                    </div>
                  </div>
                </div>
              </div>

              <div class="editor-actions composer-submit-actions">
                <button class="btn-secondary" type="submit" name="intent" value="draft">save_draft</button>
                <button class="btn-secondary" type="submit" name="intent" value="schedule">{{if .EditingPost}}update_schedule{{else}}schedule{{end}}</button>
                <button class="btn-primary" type="submit" name="intent" value="publish_now">publish_now</button>
              </div>
            </form>
          </section>
        </section>

        <aside class="preview-panel" aria-label="Live preview">
          <div class="preview-head">
            <div class="preview-title">// live preview</div>
            <div class="preview-platforms">
              <span class="active" id="preview-network">X</span>
              <span>LI</span>
              <span>IG</span>
              <span>FB</span>
            </div>
          </div>
          <div class="preview-body">
            <article class="preview-card">
              <div class="preview-author">
                <div class="preview-avatar">pf</div>
                <div>
                  <div class="preview-name">post_flow</div>
                  <div class="preview-handle">@postflow_app</div>
                </div>
              </div>
              <div class="preview-text" id="preview-text">{{if .CreateText}}{{.CreateText}}{{else}}Start typing to preview your post...{{end}}</div>
              <div class="preview-media" id="preview-media" hidden>
                <img id="preview-media-image" alt="media preview" hidden />
                <div class="preview-media-empty" id="preview-media-empty">No media selected yet.</div>
              </div>
              <div class="preview-footer">just now</div>
            </article>
          </div>
        </aside>
      </div>
      {{end}}

      {{if eq .View "settings"}}
      <div class="line">preferences</div>
      <section class="editor">
        <div class="editor-head">timezone</div>
        <form class="editor-body" method="post" action="/settings/timezone">
          <input type="hidden" name="return_to" value="{{.CurrentViewURL}}" />
          {{if .SettingsError}}<div class="alert error">{{.SettingsError}}</div>{{end}}
          {{if .SettingsSuccess}}<div class="alert success">{{.SettingsSuccess}}</div>{{end}}
          <div class="field">
            <label for="timezone-select">Timezone (IANA)</label>
            <select name="timezone" id="timezone-select" data-current-timezone="{{.UITimezone}}" required>
              <option value="UTC">UTC</option>
              <option value="Europe/Madrid">Europe/Madrid</option>
              <option value="Europe/London">Europe/London</option>
              <option value="America/New_York">America/New_York</option>
              <option value="America/Chicago">America/Chicago</option>
              <option value="America/Los_Angeles">America/Los_Angeles</option>
              <option value="America/Mexico_City">America/Mexico_City</option>
              <option value="America/Bogota">America/Bogota</option>
              <option value="America/Buenos_Aires">America/Buenos_Aires</option>
            </select>
          </div>
          <div class="editor-actions">
            <button type="submit" class="btn-primary">save timezone</button>
            <button type="button" class="btn-secondary" id="tz-detect">use browser timezone</button>
          </div>
          <div class="meta"><span class="meta-soft">current timezone: {{.UITimezone}}</span></div>
        </form>
      </section>
      <section class="editor editor-wide">
        <div class="editor-head">media library</div>
        <div class="editor-body">
          {{if .MediaError}}<div class="alert error">{{.MediaError}}</div>{{end}}
          {{if .MediaSuccess}}<div class="alert success">{{.MediaSuccess}}</div>{{end}}
          <div class="meta">
            <span class="meta-soft">{{len .MediaLibrary}} files · {{.MediaInUseCount}} in use · {{.MediaTotalSizeLabel}}</span>
          </div>
          <div class="media-library settings-media-library">
            {{range .MediaLibrary}}
            <article class="media-library-item {{if .InUse}}in-use{{end}}">
              <div class="media-library-thumb">
                {{if .IsImage}}<img src="{{.PreviewURL}}" alt="{{.OriginalName}}" loading="lazy" />{{else if .IsVideo}}<span>video</span>{{else}}<span>file</span>{{end}}
              </div>
              <div class="media-library-info">
                <div class="media-library-name">{{.OriginalName}}</div>
                <div class="media-library-meta">{{.SizeLabel}} · {{.MimeType}} · {{.CreatedLabel}}</div>
              </div>
              <div class="media-library-actions">
                <a class="btn-secondary" href="{{.PreviewURL}}" target="_blank" rel="noreferrer">view</a>
                {{if .InUse}}
                <span class="pill media-pill-used">used {{.UsageCount}}</span>
                {{else}}
                <form method="post" action="/media/{{.ID}}/delete">
                  <input type="hidden" name="return_to" value="/?view=settings" />
                  <button type="submit" class="btn-danger">delete</button>
                </form>
                {{end}}
              </div>
            </article>
            {{else}}
            <div class="empty">No media uploaded yet.</div>
            {{end}}
          </div>
        </div>
      </section>
      <section class="editor editor-wide">
        <div class="editor-head">mcp · streamable http</div>
        <div class="editor-body">
          <div class="field">
            <label for="mcp-url">MCP URL</label>
            <input id="mcp-url" type="text" value="{{.MCPURL}}" readonly />
          </div>
          <div class="meta">
            <span class="meta-soft">{{.MCPAuthHint}}</span>
          </div>
          <div class="field">
            <label>Claude Code</label>
            <pre class="code-block">{{.MCPClaudeCommand}}</pre>
          </div>
          <div class="field">
            <label>Codex CLI</label>
            <pre class="code-block">{{.MCPCodexCommand}}</pre>
          </div>
          <div class="field">
            <label>Codex config.toml</label>
            <pre class="code-block">{{.MCPCodexConfigTOML}}</pre>
          </div>
          <div class="field">
            <label>JSON config</label>
            <pre class="code-block">{{.MCPConfigJSON}}</pre>
          </div>
        </div>
      </section>
      {{end}}
    </main>
  </div>
<script>
(() => {
  const isInteractive = (node) => !!node.closest("a,button,input,select,textarea,form,label");
  document.querySelectorAll("[data-edit-url]").forEach((el) => {
    el.addEventListener("click", (e) => {
      const target = e.target;
      if (!(target instanceof Element)) {
        return;
      }
      if (isInteractive(target)) {
        return;
      }
      const url = el.getAttribute("data-edit-url");
      if (url) {
        window.location.href = url;
      }
    });
  });
})();

(() => {
  const monthNames = ["enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"];
  const weekdayNames = ["L", "M", "X", "J", "V", "S", "D"];
  const pad2 = (value) => String(value).padStart(2, "0");

  const parseInputValue = (raw, mode) => {
    const value = String(raw || "").trim();
    if (value === "") {
      return null;
    }
    if (mode === "date") {
      const match = value.match(/^(\d{4})-(\d{2})-(\d{2})$/);
      if (!match) {
        return null;
      }
      const d = new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]), 12, 0, 0, 0);
      if (Number.isNaN(d.getTime())) {
        return null;
      }
      return d;
    }
    const match = value.match(/^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})/);
    if (!match) {
      return null;
    }
    const d = new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]), Number(match[4]), Number(match[5]), 0, 0);
    if (Number.isNaN(d.getTime())) {
      return null;
    }
    return d;
  };

  const toInputValue = (date, mode) => {
    if (!(date instanceof Date) || Number.isNaN(date.getTime())) {
      return "";
    }
    const y = String(date.getFullYear());
    const m = pad2(date.getMonth() + 1);
    const d = pad2(date.getDate());
    if (mode === "date") {
      return y + "-" + m + "-" + d;
    }
    const h = pad2(date.getHours());
    const min = pad2(date.getMinutes());
    return y + "-" + m + "-" + d + "T" + h + ":" + min;
  };

  const formatDisplay = (raw, mode) => {
    const parsed = parseInputValue(raw, mode);
    if (!parsed) {
      return mode === "date" ? "dd/mm/aaaa" : "dd/mm/aaaa, --:--";
    }
    const dd = pad2(parsed.getDate());
    const mm = pad2(parsed.getMonth() + 1);
    const yyyy = parsed.getFullYear();
    if (mode === "date") {
      return dd + "/" + mm + "/" + yyyy;
    }
    return dd + "/" + mm + "/" + yyyy + ", " + pad2(parsed.getHours()) + ":" + pad2(parsed.getMinutes());
  };

  const isSameDay = (a, b) => (
    a.getFullYear() === b.getFullYear() &&
    a.getMonth() === b.getMonth() &&
    a.getDate() === b.getDate()
  );

  const isToday = (date) => {
    const now = new Date();
    return isSameDay(date, now);
  };

  const popover = document.createElement("div");
  popover.className = "date-picker-popover";
  popover.hidden = true;
  popover.innerHTML =
    '<div class="date-picker-head">' +
      '<button type="button" class="date-picker-nav" data-date-nav="-1" aria-label="previous month">‹</button>' +
      '<div class="date-picker-month" data-date-month></div>' +
      '<button type="button" class="date-picker-nav" data-date-nav="1" aria-label="next month">›</button>' +
    "</div>" +
    '<div class="date-picker-weekdays" data-date-weekdays></div>' +
    '<div class="date-picker-days" data-date-days></div>' +
    '<div class="date-picker-time">' +
      '<select data-date-hour></select>' +
      '<span class="date-picker-time-divider">:</span>' +
      '<select data-date-minute></select>' +
    "</div>" +
    '<div class="date-picker-actions">' +
      '<div class="date-picker-actions-left">' +
        '<button type="button" class="btn-secondary" data-date-clear>clear</button>' +
        '<button type="button" class="btn-secondary" data-date-now>today</button>' +
      "</div>" +
      '<div class="date-picker-actions-right">' +
        '<button type="button" class="btn-primary" data-date-apply>apply</button>' +
      "</div>" +
    "</div>";
  document.body.appendChild(popover);

  const monthLabel = popover.querySelector("[data-date-month]");
  const weekdaysRoot = popover.querySelector("[data-date-weekdays]");
  const daysRoot = popover.querySelector("[data-date-days]");
  const hourSelect = popover.querySelector("[data-date-hour]");
  const minuteSelect = popover.querySelector("[data-date-minute]");
  const prevBtn = popover.querySelector('[data-date-nav="-1"]');
  const nextBtn = popover.querySelector('[data-date-nav="1"]');
  const clearBtn = popover.querySelector("[data-date-clear]");
  const nowBtn = popover.querySelector("[data-date-now]");
  const applyBtn = popover.querySelector("[data-date-apply]");

  if (!(monthLabel instanceof HTMLElement) ||
      !(weekdaysRoot instanceof HTMLElement) ||
      !(daysRoot instanceof HTMLElement) ||
      !(hourSelect instanceof HTMLSelectElement) ||
      !(minuteSelect instanceof HTMLSelectElement) ||
      !(prevBtn instanceof HTMLButtonElement) ||
      !(nextBtn instanceof HTMLButtonElement) ||
      !(clearBtn instanceof HTMLButtonElement) ||
      !(nowBtn instanceof HTMLButtonElement) ||
      !(applyBtn instanceof HTMLButtonElement)) {
    return;
  }

  const state = {
    input: null,
    wrapper: null,
    mode: "datetime-local",
    selected: null,
    viewYear: 0,
    viewMonth: 0
  };

  const positionPopover = () => {
    if (popover.hidden || !(state.wrapper instanceof HTMLElement)) {
      return;
    }
    const rect = state.wrapper.getBoundingClientRect();
    const margin = 8;
    const width = popover.offsetWidth || 320;
    const height = popover.offsetHeight || 360;

    let left = rect.left;
    if (left + width > window.innerWidth - margin) {
      left = window.innerWidth - width - margin;
    }
    if (left < margin) {
      left = margin;
    }

    let top = rect.bottom + margin;
    if (top + height > window.innerHeight - margin) {
      top = rect.top - height - margin;
    }
    if (top < margin) {
      top = margin;
    }

    popover.style.left = Math.round(left) + "px";
    popover.style.top = Math.round(top) + "px";
  };

  const closePicker = () => {
    if (state.wrapper instanceof HTMLElement) {
      state.wrapper.classList.remove("is-open");
    }
    popover.hidden = true;
    state.input = null;
    state.wrapper = null;
    state.selected = null;
  };

  const updateDisplayForInput = (input) => {
    if (!(input instanceof HTMLInputElement)) {
      return;
    }
    const mode = input.dataset.datePickerMode || "datetime-local";
    const wrapper = input.closest(".date-input");
    if (!(wrapper instanceof HTMLElement)) {
      return;
    }
    const display = wrapper.querySelector("[data-date-display]");
    if (!(display instanceof HTMLButtonElement)) {
      return;
    }
    const raw = String(input.value || "").trim();
    display.textContent = formatDisplay(raw, mode);
    display.setAttribute("data-empty", raw === "" ? "1" : "0");
    wrapper.classList.toggle("is-empty", raw === "");
  };

  const applyCurrentSelection = () => {
    if (!(state.input instanceof HTMLInputElement)) {
      return;
    }
    const nextValue = state.selected ? toInputValue(state.selected, state.mode) : "";
    state.input.value = nextValue;
    state.input.dispatchEvent(new Event("input", { bubbles: true }));
    state.input.dispatchEvent(new Event("change", { bubbles: true }));
    closePicker();
  };

  const renderPicker = () => {
    const displayMonth = monthNames[state.viewMonth] || "";
    monthLabel.textContent = displayMonth + " de " + String(state.viewYear);
    popover.setAttribute("data-mode", state.mode === "date" ? "date" : "datetime-local");

    weekdaysRoot.innerHTML = "";
    weekdayNames.forEach((name) => {
      const day = document.createElement("div");
      day.className = "date-picker-weekday";
      day.textContent = name;
      weekdaysRoot.appendChild(day);
    });

    daysRoot.innerHTML = "";
    const firstOfMonth = new Date(state.viewYear, state.viewMonth, 1);
    const firstOffset = (firstOfMonth.getDay() + 6) % 7;
    const gridStart = new Date(state.viewYear, state.viewMonth, 1 - firstOffset);

    for (let i = 0; i < 42; i += 1) {
      const dayDate = new Date(gridStart.getFullYear(), gridStart.getMonth(), gridStart.getDate() + i);
      const btn = document.createElement("button");
      btn.type = "button";
      btn.className = "date-picker-day";
      if (dayDate.getMonth() !== state.viewMonth) {
        btn.classList.add("outside");
      }
      if (isToday(dayDate)) {
        btn.classList.add("today");
      }
      if (state.selected && isSameDay(dayDate, state.selected)) {
        btn.classList.add("selected");
      }
      btn.textContent = String(dayDate.getDate());
      btn.addEventListener("click", () => {
        const next = state.selected instanceof Date ? new Date(state.selected.getTime()) : new Date();
        next.setFullYear(dayDate.getFullYear(), dayDate.getMonth(), dayDate.getDate());
        if (!(state.selected instanceof Date)) {
          if (state.mode === "date") {
            next.setHours(12, 0, 0, 0);
          } else {
            next.setHours(9, 0, 0, 0);
          }
        }
        state.selected = next;
        if (state.mode === "date") {
          applyCurrentSelection();
          return;
        }
        renderPicker();
      });
      daysRoot.appendChild(btn);
    }

    if (!(state.selected instanceof Date)) {
      const now = new Date();
      state.selected = new Date(state.viewYear, state.viewMonth, now.getDate(), now.getHours(), now.getMinutes(), 0, 0);
    }
    hourSelect.value = pad2(state.selected.getHours());
    minuteSelect.value = pad2(state.selected.getMinutes());
  };

  for (let hour = 0; hour < 24; hour += 1) {
    const option = document.createElement("option");
    option.value = pad2(hour);
    option.textContent = pad2(hour);
    hourSelect.appendChild(option);
  }
  for (let minute = 0; minute < 60; minute += 1) {
    const option = document.createElement("option");
    option.value = pad2(minute);
    option.textContent = pad2(minute);
    minuteSelect.appendChild(option);
  }

  const openPicker = (input, wrapper) => {
    if (!(input instanceof HTMLInputElement) || !(wrapper instanceof HTMLElement)) {
      return;
    }
    wrapper.classList.remove("is-invalid");
    state.input = input;
    state.wrapper = wrapper;
    state.mode = input.dataset.datePickerMode || "datetime-local";
    state.selected = parseInputValue(input.value, state.mode);
    const baseDate = state.selected instanceof Date ? state.selected : new Date();
    state.viewYear = baseDate.getFullYear();
    state.viewMonth = baseDate.getMonth();

    renderPicker();
    popover.hidden = false;
    wrapper.classList.add("is-open");
    positionPopover();
  };

  prevBtn.addEventListener("click", () => {
    state.viewMonth -= 1;
    if (state.viewMonth < 0) {
      state.viewMonth = 11;
      state.viewYear -= 1;
    }
    renderPicker();
  });

  nextBtn.addEventListener("click", () => {
    state.viewMonth += 1;
    if (state.viewMonth > 11) {
      state.viewMonth = 0;
      state.viewYear += 1;
    }
    renderPicker();
  });

  hourSelect.addEventListener("change", () => {
    if (!(state.selected instanceof Date)) {
      state.selected = new Date();
    }
    state.selected.setHours(Number(hourSelect.value), state.selected.getMinutes(), 0, 0);
  });

  minuteSelect.addEventListener("change", () => {
    if (!(state.selected instanceof Date)) {
      state.selected = new Date();
    }
    state.selected.setHours(state.selected.getHours(), Number(minuteSelect.value), 0, 0);
  });

  clearBtn.addEventListener("click", () => {
    state.selected = null;
    applyCurrentSelection();
  });

  nowBtn.addEventListener("click", () => {
    const now = new Date();
    state.selected = now;
    state.viewYear = now.getFullYear();
    state.viewMonth = now.getMonth();
    if (state.mode === "date") {
      applyCurrentSelection();
      return;
    }
    renderPicker();
  });

  applyBtn.addEventListener("click", () => {
    applyCurrentSelection();
  });

  document.addEventListener("mousedown", (event) => {
    if (popover.hidden) {
      return;
    }
    const target = event.target;
    if (!(target instanceof Node)) {
      return;
    }
    if (popover.contains(target)) {
      return;
    }
    if (state.wrapper instanceof HTMLElement && state.wrapper.contains(target)) {
      return;
    }
    closePicker();
  });

  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape" || popover.hidden) {
      return;
    }
    event.preventDefault();
    closePicker();
  });

  window.addEventListener("resize", positionPopover);
  window.addEventListener("scroll", positionPopover, true);

  document.addEventListener("submit", (event) => {
    const form = event.target;
    if (!(form instanceof HTMLFormElement)) {
      return;
    }
    const requiredDateInput = Array.from(form.querySelectorAll("input[data-date-picker][required]"))
      .find((node) => node instanceof HTMLInputElement && !node.disabled && String(node.value || "").trim() === "");
    if (!(requiredDateInput instanceof HTMLInputElement)) {
      return;
    }
    event.preventDefault();
    const wrapper = requiredDateInput.closest(".date-input");
    if (wrapper instanceof HTMLElement) {
      wrapper.classList.add("is-invalid");
      openPicker(requiredDateInput, wrapper);
    }
  });

  const setupDatePickerInput = (input) => {
    if (!(input instanceof HTMLInputElement)) {
      return;
    }
    if (input.dataset.datePickerReady === "1") {
      return;
    }
    input.dataset.datePickerReady = "1";
    input.dataset.datePickerMode = input.type === "date" ? "date" : "datetime-local";

    let wrapper = input.parentElement;
    if (!(wrapper instanceof HTMLElement) || !wrapper.classList.contains("date-input")) {
      wrapper = document.createElement("div");
      wrapper.className = "date-input";
      input.replaceWith(wrapper);
      wrapper.appendChild(input);
    }

    input.classList.add("date-native-value");
    input.setAttribute("tabindex", "-1");
    input.setAttribute("aria-hidden", "true");

    let display = wrapper.querySelector("[data-date-display]");
    if (!(display instanceof HTMLButtonElement)) {
      display = document.createElement("button");
      display.type = "button";
      display.className = "date-display";
      display.setAttribute("data-date-display", "1");
      wrapper.appendChild(display);
    }

    let trigger = wrapper.querySelector("[data-date-trigger]");
    if (!(trigger instanceof HTMLButtonElement)) {
      trigger = document.createElement("button");
      trigger.type = "button";
      trigger.className = "date-trigger";
      trigger.setAttribute("data-date-trigger", "1");
      trigger.setAttribute("aria-label", "open date picker");
      trigger.textContent = "▾";
      wrapper.appendChild(trigger);
    }

    const open = (event) => {
      event.preventDefault();
      if (input.disabled) {
        return;
      }
      openPicker(input, wrapper);
    };

    display.addEventListener("click", open);
    display.addEventListener("focus", () => wrapper.classList.add("is-focus"));
    display.addEventListener("blur", () => wrapper.classList.remove("is-focus"));
    trigger.addEventListener("click", open);

    input.addEventListener("input", () => updateDisplayForInput(input));
    input.addEventListener("change", () => updateDisplayForInput(input));
    updateDisplayForInput(input);
  };

  document.querySelectorAll('input[type="date"], input[type="datetime-local"], input[data-date-picker]').forEach((node) => {
    if (node instanceof HTMLInputElement) {
      if (node.disabled) {
        return;
      }
      setupDatePickerInput(node);
    }
  });
})();

(() => {
  const view = document.body.dataset.view || "";
  if (view !== "calendar") {
    return;
  }

  const storageKey = "publisher.ui.calendar.scroll.v1";
  const main = document.querySelector(".main");
  const layout = document.querySelector(".calendar-layout");
  const calendarWrap = document.querySelector(".calendar-wrap");
  const dayPanel = document.querySelector(".day-panel");
  const grid = document.querySelector(".calendar-grid-scroll");
  const panelBody = document.querySelector(".day-panel-body");
  const mobileQuery = window.matchMedia("(max-width: 980px)");

  const syncCalendarCellOverflow = () => {
    if (!layout) {
      return;
    }

    const dayEventLists = Array.from(layout.querySelectorAll(".day-events[data-day-events]"));
    dayEventLists.forEach((list) => {
      const events = Array.from(list.querySelectorAll(".day-event[data-day-event]"));
      const more = list.querySelector(".more[data-day-overflow]");

      if (events.length === 0) {
        if (more) {
          more.hidden = true;
          more.textContent = "";
        }
        return;
      }

      events.forEach((eventEl) => {
        eventEl.hidden = false;
      });

      if (!more) {
        return;
      }

      more.hidden = true;
      more.textContent = "";

      const availableHeight = list.clientHeight;
      if (availableHeight <= 0) {
        return;
      }

      if (list.scrollHeight <= availableHeight + 0.5) {
        return;
      }

      more.hidden = false;
      more.textContent = "+0 more";

      const styles = window.getComputedStyle(list);
      const gapValue = parseFloat(styles.rowGap || styles.gap || "0");
      const gap = Number.isFinite(gapValue) ? gapValue : 0;
      const moreHeight = more.offsetHeight;
      let usedHeight = 0;
      let visible = 0;

      for (let i = 0; i < events.length; i += 1) {
        const eventEl = events[i];
        const eventHeight = eventEl.offsetHeight;
        const gapBeforeEvent = visible > 0 ? gap : 0;
        const remainingAfterThis = events.length - (visible + 1);
        const reserveForMore = remainingAfterThis > 0 ? gap + moreHeight : 0;
        const nextUsedHeight = usedHeight + gapBeforeEvent + eventHeight + reserveForMore;
        if (nextUsedHeight > availableHeight + 0.5) {
          break;
        }
        usedHeight += gapBeforeEvent + eventHeight;
        visible += 1;
      }

      events.forEach((eventEl, index) => {
        eventEl.hidden = index >= visible;
      });

      const hiddenCount = events.length - visible;
      if (hiddenCount > 0) {
        more.textContent = "+" + hiddenCount + " more";
        more.hidden = false;
        return;
      }

      more.textContent = "";
      more.hidden = true;
    });
  };

  const syncDayPanelHeightToCalendar = () => {
    if (!layout || !calendarWrap || !dayPanel) {
      return;
    }
    if (mobileQuery.matches) {
      calendarWrap.style.height = "";
      calendarWrap.style.minHeight = "";
      dayPanel.style.height = "";
      return;
    }

    const viewportHeight = window.innerHeight || document.documentElement.clientHeight || 0;
    const top = layout.getBoundingClientRect().top;
    const mainStyles = main ? window.getComputedStyle(main) : null;
    const bottomPadding = mainStyles ? parseFloat(mainStyles.paddingBottom || "0") : 0;
    const availableHeight = Math.max(0, Math.floor(viewportHeight - top - bottomPadding - 4));
    if (availableHeight > 0) {
      calendarWrap.style.height = availableHeight + "px";
      calendarWrap.style.minHeight = "";
      dayPanel.style.height = calendarWrap.offsetHeight + "px";
      return;
    }
    calendarWrap.style.height = "";
    calendarWrap.style.minHeight = "";
    dayPanel.style.height = "";
  };

  let syncFrame = 0;
  const scheduleHeightSync = () => {
    if (syncFrame) {
      cancelAnimationFrame(syncFrame);
    }
    syncFrame = requestAnimationFrame(() => {
      syncDayPanelHeightToCalendar();
      syncCalendarCellOverflow();
      syncFrame = 0;
    });
  };

  const saveScrollState = () => {
    const payload = {
      y: window.scrollY || window.pageYOffset || 0,
      x: grid ? grid.scrollLeft : 0,
      panelY: panelBody ? panelBody.scrollTop : 0
    };
    try {
      sessionStorage.setItem(storageKey, JSON.stringify(payload));
    } catch (_) {}
  };

  const restoreScrollState = () => {
    let payload = null;
    try {
      const raw = sessionStorage.getItem(storageKey);
      if (raw) {
        payload = JSON.parse(raw);
      }
    } catch (_) {}
    if (!payload) {
      return;
    }

    const y = Number(payload.y || 0);
    const x = Number(payload.x || 0);
    const panelY = Number(payload.panelY || 0);

    requestAnimationFrame(() => {
      syncDayPanelHeightToCalendar();
      syncCalendarCellOverflow();
      if (grid) {
        grid.scrollLeft = x;
      }
      if (panelBody) {
        panelBody.scrollTop = panelY;
      }
      window.scrollTo(0, y);
      setTimeout(() => {
        syncDayPanelHeightToCalendar();
        syncCalendarCellOverflow();
        if (grid) {
          grid.scrollLeft = x;
        }
        if (panelBody) {
          panelBody.scrollTop = panelY;
        }
        window.scrollTo(0, y);
      }, 0);
    });
  };

  document.querySelectorAll(".day-link").forEach((link) => {
    link.addEventListener("click", saveScrollState);
  });
  window.addEventListener("beforeunload", saveScrollState);
  window.addEventListener("resize", scheduleHeightSync);

  scheduleHeightSync();
  restoreScrollState();
})();

(() => {
  const view = document.body.dataset.view || "";
  if (view !== "failed") {
    return;
  }

  const checkboxes = Array.from(document.querySelectorAll("[data-failed-checkbox]"));
  const selectAll = document.getElementById("failed-select-all");
  const clearAll = document.getElementById("failed-clear-all");
  const form = document.getElementById("failed-bulk-form");
  const submit = document.getElementById("failed-requeue-selected");

  const updateSubmit = () => {
    const count = checkboxes.filter((cb) => cb.checked).length;
    submit.disabled = count === 0;
    submit.textContent = count > 0 ? "requeue selected (" + count + ")" : "requeue selected";
  };

  selectAll?.addEventListener("click", () => {
    checkboxes.forEach((cb) => { cb.checked = true; });
    updateSubmit();
  });

  clearAll?.addEventListener("click", () => {
    checkboxes.forEach((cb) => { cb.checked = false; });
    updateSubmit();
  });

  checkboxes.forEach((cb) => cb.addEventListener("change", updateSubmit));

  form?.addEventListener("submit", (e) => {
    const selected = checkboxes.filter((cb) => cb.checked).map((cb) => cb.value);
    if (selected.length === 0) {
      e.preventDefault();
      updateSubmit();
      return;
    }
    form.querySelectorAll('input[name="ids"]').forEach((el) => el.remove());
    selected.forEach((id) => {
      const input = document.createElement("input");
      input.type = "hidden";
      input.name = "ids";
      input.value = id;
      form.appendChild(input);
    });
  });

  updateSubmit();
})();

(() => {
  const view = document.body.dataset.view || "";
  if (view !== "create") {
    return;
  }

  const form = document.getElementById("create-post-form");
  if (!(form instanceof HTMLFormElement)) {
    return;
  }

  const platformInput = document.getElementById("create-platform");
  const networkChips = Array.from(document.querySelectorAll("[data-network-chip]"));
  const previewNetwork = document.getElementById("preview-network");
  const textInput = document.getElementById("create-text");
  const charCount = document.getElementById("create-char-count");
  const previewText = document.getElementById("preview-text");
  const scheduleInput = document.getElementById("create-scheduled-at");
  const mediaInput = document.getElementById("create-media-input");
  const mediaTrigger = document.getElementById("create-media-trigger");
  const mediaList = document.getElementById("create-media-list");
  const mediaLibrary = document.getElementById("create-media-library");
  const mediaHidden = document.getElementById("create-media-hidden");
  const uploadNotice = document.getElementById("create-upload-notice");
  const previewMedia = document.getElementById("preview-media");
  const previewImage = document.getElementById("preview-media-image");
  const previewEmpty = document.getElementById("preview-media-empty");

  if (!(textInput instanceof HTMLTextAreaElement) ||
      !(scheduleInput instanceof HTMLInputElement) ||
      !(mediaInput instanceof HTMLInputElement) ||
      !(mediaTrigger instanceof HTMLButtonElement) ||
      !(mediaList instanceof HTMLElement) ||
      !(mediaHidden instanceof HTMLElement) ||
      !(uploadNotice instanceof HTMLElement) ||
      !(previewMedia instanceof HTMLElement) ||
      !(previewImage instanceof HTMLImageElement) ||
      !(previewEmpty instanceof HTMLElement)) {
    return;
  }

  const limit = 280;
  const maxMedia = 4;
  let uploadInFlight = 0;
  let replaceIndex = -1;
  const attachments = [];

  const formatBytes = (size) => {
    if (!Number.isFinite(size) || size <= 0) {
      return "0 B";
    }
    const units = ["B", "KB", "MB", "GB"];
    let value = size;
    let unitIndex = 0;
    while (value >= 1024 && unitIndex < units.length - 1) {
      value /= 1024;
      unitIndex += 1;
    }
    return (value >= 10 || unitIndex === 0 ? value.toFixed(0) : value.toFixed(1)) + " " + units[unitIndex];
  };

  const toDatetimeLocal = (d) => {
    const year = d.getFullYear();
    const month = String(d.getMonth() + 1).padStart(2, "0");
    const day = String(d.getDate()).padStart(2, "0");
    const hour = String(d.getHours()).padStart(2, "0");
    const minute = String(d.getMinutes()).padStart(2, "0");
    return year + "-" + month + "-" + day + "T" + hour + ":" + minute;
  };

  const setNotice = (message, state) => {
    uploadNotice.textContent = message;
    if (state) {
      uploadNotice.setAttribute("data-state", state);
      return;
    }
    uploadNotice.removeAttribute("data-state");
  };

  const updateCharCount = () => {
    const count = textInput.value.length;
    charCount.textContent = "// " + count + "/" + limit + " chars (X limit)";
    charCount.classList.toggle("char-over", count > limit);
  };

  const updatePreviewText = () => {
    const content = textInput.value.trim();
    previewText.textContent = content === "" ? "Start typing to preview your post..." : content;
  };

  const updatePreviewMedia = () => {
    const firstImage = attachments.find((item) => item.previewUrl);
    if (firstImage && firstImage.previewUrl) {
      previewImage.src = firstImage.previewUrl;
      previewImage.hidden = false;
      previewEmpty.hidden = true;
      previewMedia.hidden = false;
      return;
    }
    previewImage.removeAttribute("src");
    previewImage.hidden = true;
    previewEmpty.hidden = true;
    previewMedia.hidden = true;
  };

  const syncHiddenMediaInputs = () => {
    mediaHidden.innerHTML = "";
    attachments.forEach((item) => {
      const input = document.createElement("input");
      input.type = "hidden";
      input.name = "media_ids";
      input.value = item.id;
      mediaHidden.appendChild(input);
    });
  };

  const setActionsEnabled = (enabled) => {
    document.querySelectorAll('button[type=submit][form="create-post-form"], #create-post-form button[type=submit]').forEach((btn) => {
      if (btn instanceof HTMLButtonElement) {
        btn.disabled = !enabled;
      }
    });
  };

  const isAttachedMedia = (id) => attachments.some((item) => item.id === id);

  const syncLibraryAttachButtons = () => {
    if (!(mediaLibrary instanceof HTMLElement)) {
      return;
    }
    mediaLibrary.querySelectorAll("[data-media-attach]").forEach((node) => {
      if (!(node instanceof HTMLButtonElement)) {
        return;
      }
      const id = (node.getAttribute("data-media-attach") || "").trim();
      if (!id) {
        return;
      }
      const attached = isAttachedMedia(id);
      node.classList.toggle("attached", attached);
      node.textContent = attached ? "attached" : "attach";
    });
  };

  const parseLibraryNode = (node) => {
    if (!(node instanceof HTMLElement)) {
      return null;
    }
    const id = (node.dataset.mediaId || "").trim();
    if (!id) {
      return null;
    }
    const size = Number(node.dataset.mediaSize || "0");
    const mime = (node.dataset.mediaMime || "").trim();
    const previewUrl = (node.dataset.mediaPreview || "").trim();
    return {
      id,
      name: (node.dataset.mediaName || "file").trim() || "file",
      size: Number.isFinite(size) ? size : 0,
      mime,
      previewUrl: previewUrl
    };
  };

  const removeAttachmentByID = (mediaID) => {
    const index = attachments.findIndex((item) => item.id === mediaID);
    if (index < 0) {
      return false;
    }
    destroyPreviewURL(attachments[index]);
    attachments.splice(index, 1);
    return true;
  };

  const upsertLibraryItem = (item, inUse) => {
    if (!(mediaLibrary instanceof HTMLElement) || !item || !item.id) {
      return;
    }

    let row = mediaLibrary.querySelector('[data-media-library-item][data-media-id="' + item.id + '"]');
    if (row instanceof HTMLElement) {
      row.dataset.mediaName = item.name || "file";
      row.dataset.mediaSize = String(Number(item.size || 0));
      row.dataset.mediaMime = item.mime || "";
      row.dataset.mediaPreview = item.previewUrl || "";
      const nameNode = row.querySelector(".media-library-name");
      if (nameNode) {
        nameNode.textContent = item.name || "file";
      }
      const metaNode = row.querySelector(".media-library-meta");
      if (metaNode) {
        metaNode.textContent = formatBytes(Number(item.size || 0)) + " · just now";
      }
      syncLibraryAttachButtons();
      return;
    }

    row = document.createElement("article");
    row.className = "media-library-item";
    row.setAttribute("data-media-library-item", "");
    row.dataset.mediaId = item.id;
    row.dataset.mediaName = item.name || "file";
    row.dataset.mediaSize = String(Number(item.size || 0));
    row.dataset.mediaMime = item.mime || "";
    row.dataset.mediaPreview = item.previewUrl || "";

    const thumb = document.createElement("div");
    thumb.className = "media-library-thumb";
    if (item.previewUrl) {
      const img = document.createElement("img");
      img.src = item.previewUrl;
      img.alt = item.name || "media";
      img.loading = "lazy";
      thumb.appendChild(img);
    } else if (String(item.mime || "").startsWith("video/")) {
      thumb.textContent = "video";
    } else {
      thumb.textContent = "file";
    }

    const info = document.createElement("div");
    info.className = "media-library-info";
    const name = document.createElement("div");
    name.className = "media-library-name";
    name.textContent = item.name || "file";
    const meta = document.createElement("div");
    meta.className = "media-library-meta";
    meta.textContent = formatBytes(Number(item.size || 0)) + " · just now";
    info.appendChild(name);
    info.appendChild(meta);

    const actions = document.createElement("div");
    actions.className = "media-library-actions";
    const attach = document.createElement("button");
    attach.type = "button";
    attach.className = "btn-secondary";
    attach.setAttribute("data-media-attach", item.id);
    attach.textContent = "attach";
    actions.appendChild(attach);

    if (inUse) {
      const used = document.createElement("span");
      used.className = "pill media-pill-used";
      used.textContent = "in_use";
      actions.appendChild(used);
    } else {
      const del = document.createElement("button");
      del.type = "button";
      del.className = "btn-danger";
      del.setAttribute("data-media-delete", item.id);
      del.textContent = "delete";
      actions.appendChild(del);
    }

    row.appendChild(thumb);
    row.appendChild(info);
    row.appendChild(actions);
    mediaLibrary.prepend(row);
    syncLibraryAttachButtons();
  };

  const destroyPreviewURL = (item) => {
    if (!item || !item.previewUrl) {
      return;
    }
    if (item.previewUrl.startsWith("blob:")) {
      URL.revokeObjectURL(item.previewUrl);
    }
  };

  const renderMediaList = () => {
    if (attachments.length === 0) {
      mediaList.innerHTML = '<div class="empty">No media yet. Upload up to ' + maxMedia + " files.</div>";
      setNotice("no media uploaded");
      updatePreviewMedia();
      syncLibraryAttachButtons();
      return;
    }

    mediaList.innerHTML = "";
    attachments.forEach((item, index) => {
      const row = document.createElement("div");
      row.className = "media-item";

      const thumb = document.createElement("div");
      thumb.className = "media-thumb";
      if (!item.previewUrl) {
        thumb.textContent = "file";
      } else {
        thumb.style.backgroundImage = 'url("' + item.previewUrl + '")';
      }

      const info = document.createElement("div");
      info.className = "media-info";
      const name = document.createElement("div");
      name.className = "media-name";
      name.textContent = item.name;
      const meta = document.createElement("div");
      meta.className = "media-meta";
      meta.textContent = formatBytes(item.size) + " · " + (item.mime || "file");
      info.appendChild(name);
      info.appendChild(meta);

      const actions = document.createElement("div");
      actions.className = "media-item-actions";
      const replace = document.createElement("button");
      replace.type = "button";
      replace.className = "btn-secondary";
      replace.setAttribute("data-media-replace", String(index));
      replace.textContent = "replace";
      const remove = document.createElement("button");
      remove.type = "button";
      remove.className = "btn-danger";
      remove.setAttribute("data-media-remove", String(index));
      remove.textContent = "remove";
      actions.appendChild(replace);
      actions.appendChild(remove);

      row.appendChild(thumb);
      row.appendChild(info);
      row.appendChild(actions);
      mediaList.appendChild(row);
    });
    setNotice(attachments.length + "/" + maxMedia + " media uploaded", "success");
    updatePreviewMedia();
    syncLibraryAttachButtons();
  };

  const detectKind = (file) => file.type.startsWith("video/") ? "video" : "image";

  const uploadMediaFile = async (file) => {
    const payload = new FormData();
    payload.append("platform", platformInput instanceof HTMLInputElement ? platformInput.value : "x");
    payload.append("kind", detectKind(file));
    payload.append("file", file);
    const res = await fetch("/media", { method: "POST", body: payload });
    if (!res.ok) {
      let message = "upload failed (" + res.status + ")";
      try {
        const body = await res.json();
        if (body && typeof body.error === "string" && body.error.trim() !== "") {
          message = body.error.trim();
        }
      } catch (_) {}
      throw new Error(message);
    }
    return res.json();
  };

  const addOrReplaceFile = async (file, index) => {
    if (!(file instanceof File)) {
      return;
    }
    if (index < 0 && attachments.length >= maxMedia) {
      setNotice("max " + maxMedia + " files", "error");
      return;
    }

    uploadInFlight += 1;
    setActionsEnabled(false);
    setNotice("uploading " + file.name + "...");

    try {
      const uploaded = await uploadMediaFile(file);
      const uploadedPreview = typeof uploaded.preview_url === "string" ? uploaded.preview_url.trim() : "";
      const item = {
        id: String(uploaded.id || ""),
        name: String(uploaded.original_name || file.name),
        size: Number(uploaded.size_bytes || file.size || 0),
        mime: String(uploaded.mime_type || file.type || ""),
        previewUrl: uploadedPreview || (file.type.startsWith("image/") ? URL.createObjectURL(file) : "")
      };

      if (item.id === "") {
        throw new Error("upload failed: missing media id");
      }

      if (index >= 0 && index < attachments.length) {
        destroyPreviewURL(attachments[index]);
        attachments[index] = item;
      } else {
        attachments.push(item);
      }

      syncHiddenMediaInputs();
      renderMediaList();
      upsertLibraryItem(item, false);
    } catch (err) {
      const message = err instanceof Error ? err.message : "upload failed";
      setNotice(message, "error");
    } finally {
      uploadInFlight -= 1;
      setActionsEnabled(uploadInFlight === 0);
    }
  };

  mediaTrigger.addEventListener("click", () => {
    mediaInput.click();
  });

  mediaInput.addEventListener("change", async () => {
    const files = Array.from(mediaInput.files || []);
    mediaInput.value = "";
    if (files.length === 0) {
      return;
    }

    if (replaceIndex >= 0) {
      const target = replaceIndex;
      replaceIndex = -1;
      await addOrReplaceFile(files[0], target);
      return;
    }

    for (const file of files) {
      if (attachments.length >= maxMedia) {
        break;
      }
      // Keep uploads sequential so UI state stays stable and predictable.
      // eslint-disable-next-line no-await-in-loop
      await addOrReplaceFile(file, -1);
    }
  });

  mediaList.addEventListener("click", (event) => {
    const target = event.target;
    if (!(target instanceof HTMLElement)) {
      return;
    }
    const removeValue = target.getAttribute("data-media-remove");
    if (removeValue !== null) {
      const index = Number(removeValue);
      if (Number.isInteger(index) && index >= 0 && index < attachments.length) {
        destroyPreviewURL(attachments[index]);
        attachments.splice(index, 1);
        syncHiddenMediaInputs();
        renderMediaList();
      }
      return;
    }

    const replaceValue = target.getAttribute("data-media-replace");
    if (replaceValue !== null) {
      const index = Number(replaceValue);
      if (Number.isInteger(index) && index >= 0 && index < attachments.length) {
        replaceIndex = index;
        mediaInput.click();
      }
    }
  });

  mediaLibrary?.addEventListener("click", async (event) => {
    const target = event.target;
    if (!(target instanceof HTMLElement)) {
      return;
    }

    const attachID = (target.getAttribute("data-media-attach") || "").trim();
    if (attachID) {
      if (isAttachedMedia(attachID)) {
        removeAttachmentByID(attachID);
        syncHiddenMediaInputs();
        renderMediaList();
        return;
      }
      if (attachments.length >= maxMedia) {
        setNotice("max " + maxMedia + " files", "error");
        return;
      }
      const itemNode = target.closest("[data-media-library-item]");
      const libraryItem = parseLibraryNode(itemNode);
      if (!libraryItem) {
        return;
      }
      attachments.push(libraryItem);
      syncHiddenMediaInputs();
      renderMediaList();
      return;
    }

    const deleteID = (target.getAttribute("data-media-delete") || "").trim();
    if (!deleteID) {
      return;
    }
    target.setAttribute("disabled", "true");
    try {
      const res = await fetch("/media/" + encodeURIComponent(deleteID), { method: "DELETE" });
      if (!res.ok) {
        let message = "delete failed (" + res.status + ")";
        try {
          const payload = await res.json();
          if (payload && typeof payload.error === "string" && payload.error.trim() !== "") {
            message = payload.error.trim();
          }
        } catch (_) {}
        throw new Error(message);
      }
      const node = target.closest("[data-media-library-item]");
      if (node instanceof HTMLElement) {
        node.remove();
      }
      if (removeAttachmentByID(deleteID)) {
        syncHiddenMediaInputs();
        renderMediaList();
      } else {
        syncLibraryAttachButtons();
      }
      setNotice("media deleted", "success");
    } catch (err) {
      const message = err instanceof Error ? err.message : "delete failed";
      setNotice(message, "error");
    } finally {
      target.removeAttribute("disabled");
    }
  });

  networkChips.forEach((chip) => {
    if (!(chip instanceof HTMLButtonElement)) {
      return;
    }
    chip.addEventListener("click", () => {
      if (chip.disabled) {
        return;
      }
      const platform = (chip.dataset.platform || "x").trim() || "x";
      if (platformInput instanceof HTMLInputElement) {
        platformInput.value = platform;
      }
      networkChips.forEach((item) => {
        if (item instanceof HTMLButtonElement) {
          item.classList.remove("active");
          item.setAttribute("aria-pressed", "false");
        }
      });
      chip.classList.add("active");
      chip.setAttribute("aria-pressed", "true");
      if (previewNetwork instanceof HTMLElement) {
        previewNetwork.textContent = platform;
      }
    });
  });

  textInput.addEventListener("input", () => {
    updateCharCount();
    updatePreviewText();
  });

  form.addEventListener("submit", (event) => {
    if (uploadInFlight > 0) {
      event.preventDefault();
      setNotice("wait for uploads to finish", "error");
      return;
    }
    const submitter = event.submitter;
    if (submitter instanceof HTMLButtonElement && submitter.value === "publish_now" && scheduleInput.value.trim() === "") {
      scheduleInput.value = toDatetimeLocal(new Date());
      scheduleInput.dispatchEvent(new Event("input", { bubbles: true }));
      scheduleInput.dispatchEvent(new Event("change", { bubbles: true }));
    }
  });

  updateCharCount();
  updatePreviewText();
  renderMediaList();
})();

(() => {
  const view = document.body.dataset.view || "";
  if (view !== "settings") {
    return;
  }
  const input = document.getElementById("timezone-select");
  const detect = document.getElementById("tz-detect");
  if (!(input instanceof HTMLSelectElement)) {
    return;
  }
  const currentTimezone = (input.dataset.currentTimezone || "").trim();
  const fallbackZones = Array.from(input.options).map((opt) => opt.value).filter(Boolean);
  const browserTimezone = Intl?.DateTimeFormat?.().resolvedOptions?.().timeZone || "";
  const runtimeZones = typeof Intl?.supportedValuesOf === "function"
    ? Intl.supportedValuesOf("timeZone")
    : [];
  const zoneSet = new Set([...fallbackZones, ...runtimeZones]);
  if (currentTimezone) {
    zoneSet.add(currentTimezone);
  }
  const zones = Array.from(zoneSet).sort((a, b) => a.localeCompare(b));
  input.innerHTML = "";
  zones.forEach((zone) => {
    const option = document.createElement("option");
    option.value = zone;
    option.textContent = zone;
    input.appendChild(option);
  });
  if (currentTimezone && zoneSet.has(currentTimezone)) {
    input.value = currentTimezone;
  } else if (browserTimezone && zoneSet.has(browserTimezone)) {
    input.value = browserTimezone;
  } else {
    input.value = "UTC";
  }
  if (!(detect instanceof HTMLButtonElement)) {
    return;
  }
  detect.addEventListener("click", () => {
    if (!browserTimezone) {
      return;
    }
    if (!zoneSet.has(browserTimezone)) {
      const option = document.createElement("option");
      option.value = browserTimezone;
      option.textContent = browserTimezone;
      input.appendChild(option);
      zoneSet.add(browserTimezone);
    }
    input.value = browserTimezone;
    input.focus();
  });
})();
</script>
</body>
</html>`
	t, err := template.New("schedule").Parse(tpl)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	type pageData struct {
		View                      string
		ActiveNavView             string
		UITimezone                string
		TimezoneConfigured        bool
		MCPURL                    string
		MCPAuthHint               string
		MCPConfigJSON             string
		MCPClaudeCommand          string
		MCPCodexCommand           string
		MCPCodexConfigTOML        string
		Items                     []domain.Post
		Publications              []domain.Post
		Drafts                    []domain.Post
		FailedItems               []failedQueueItem
		CurrentViewURL            string
		CreateViewURL             string
		ReturnTo                  string
		BackURL                   string
		EditingPost               *domain.Post
		CreateText                string
		CreateScheduledLocal      string
		CreateError               string
		CreateSuccess             string
		FailedError               string
		FailedSuccess             string
		SettingsError             string
		SettingsSuccess           string
		MediaError                string
		MediaSuccess              string
		ScheduledCount            int
		PublicationsWindowDays    int
		DraftCount                int
		FailedCount               int
		MediaLibrary              []mediaListItem
		CreateRecentMedia         []mediaListItem
		MediaInUseCount           int
		MediaTotalSizeLabel       string
		NextRunLabel              string
		CalendarMonthLabel        string
		CalendarWeeks             [][]calendarDay
		PrevMonthParam            string
		NextMonthParam            string
		CurrentMonthParam         string
		TodayMonthParam           string
		TodayDayKey               string
		SelectedDayKey            string
		SelectedDayLabel          string
		SelectedDayItems          []dayDetailItem
		SelectedDayPendingItems   []dayDetailItem
		SelectedDayPublishedItems []dayDetailItem
	}
	_ = t.Execute(w, pageData{
		View:                      view,
		ActiveNavView:             activeNavView,
		UITimezone:                uiTimezone,
		TimezoneConfigured:        timezoneConfigured,
		MCPURL:                    mcpURL,
		MCPAuthHint:               mcpAuthHint,
		MCPConfigJSON:             mcpConfigJSON,
		MCPClaudeCommand:          mcpClaudeCommand,
		MCPCodexCommand:           mcpCodexCommand,
		MCPCodexConfigTOML:        mcpCodexConfigTOML,
		Items:                     items,
		Publications:              publicationsItems,
		Drafts:                    drafts,
		FailedItems:               failedItems,
		CurrentViewURL:            currentViewURL,
		CreateViewURL:             createViewURL,
		ReturnTo:                  returnTo,
		BackURL:                   backURL,
		EditingPost:               editingPost,
		CreateText:                createText,
		CreateScheduledLocal:      createScheduledLocal,
		CreateError:               createError,
		CreateSuccess:             createSuccess,
		FailedError:               failedError,
		FailedSuccess:             failedSuccess,
		SettingsError:             settingsError,
		SettingsSuccess:           settingsSuccess,
		MediaError:                mediaError,
		MediaSuccess:              mediaSuccess,
		ScheduledCount:            scheduledCount,
		PublicationsWindowDays:    publicationsWindowDays,
		DraftCount:                len(drafts),
		FailedCount:               failedCount,
		MediaLibrary:              mediaLibrary,
		CreateRecentMedia:         createRecentMedia,
		MediaInUseCount:           mediaInUseCount,
		MediaTotalSizeLabel:       mediaTotalSizeLabel,
		NextRunLabel:              nextRunLabel,
		CalendarMonthLabel:        calendarMonthLabel,
		CalendarWeeks:             calendarWeeks,
		PrevMonthParam:            prevMonthParam,
		NextMonthParam:            nextMonthParam,
		CurrentMonthParam:         currentMonthParam,
		TodayMonthParam:           todayMonthParam,
		TodayDayKey:               todayDayKey,
		SelectedDayKey:            selectedDayKey,
		SelectedDayLabel:          selectedDayLabel,
		SelectedDayItems:          selectedDayItems,
		SelectedDayPendingItems:   selectedDayPendingItems,
		SelectedDayPublishedItems: selectedDayPublishedItems,
	})
}

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
		return req, false, nil
	}

	if err := r.ParseForm(); err != nil {
		return createPostRequest{}, true, fmt.Errorf("invalid form body: %w", err)
	}
	req := createPostRequest{
		Platform: strings.TrimSpace(r.FormValue("platform")),
		Text:     strings.TrimSpace(r.FormValue("text")),
		Intent:   strings.ToLower(strings.TrimSpace(r.FormValue("intent"))),
		ReturnTo: strings.TrimSpace(r.FormValue("return_to")),
	}
	if req.Platform == "" {
		req.Platform = "x"
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

func parseRange(_ context.Context, fromRaw, toRaw string) (time.Time, time.Time, error) {
	now := time.Now().UTC()
	from := now.Add(-24 * time.Hour)
	to := now.Add(30 * 24 * time.Hour)
	if fromRaw != "" {
		parsed, err := time.Parse(time.RFC3339, fromRaw)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid from: %w", err)
		}
		from = parsed.UTC()
	}
	if toRaw != "" {
		parsed, err := time.Parse(time.RFC3339, toRaw)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid to: %w", err)
		}
		to = parsed.UTC()
	}
	if to.Before(from) {
		return time.Time{}, time.Time{}, errors.New("to must be after from")
	}
	return from, to, nil
}

func sanitizeName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '.', r == '-', r == '_':
			return r
		default:
			return -1
		}
	}, s)
	return s
}

func extractPostIDFromPath(path, action string) (string, error) {
	if !strings.HasPrefix(path, "/posts/") || !strings.HasSuffix(path, "/"+action) {
		return "", errors.New("not found")
	}
	trimmed := strings.TrimPrefix(path, "/posts/")
	id := strings.TrimSuffix(trimmed, "/"+action)
	id = strings.TrimSuffix(id, "/")
	if id == "" {
		return "", errors.New("invalid post id")
	}
	return id, nil
}

func defaultStatusForScheduledAt(t time.Time) domain.PostStatus {
	if t.IsZero() {
		return domain.PostStatusDraft
	}
	return domain.PostStatusScheduled
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]any{"error": err.Error()})
}
