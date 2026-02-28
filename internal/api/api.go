package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	dlqapp "github.com/antoniolg/publisher/internal/application/dlq"
	"github.com/antoniolg/publisher/internal/db"
	"github.com/antoniolg/publisher/internal/domain"
	"github.com/antoniolg/publisher/internal/publisher"
	"github.com/antoniolg/publisher/internal/secure"
)

type Server struct {
	Store             *db.Store
	DataDir           string
	DefaultMaxRetries int
	RateLimitRPM      int
	APIToken          string
	UIBasicUser       string
	UIBasicPass       string
	Registry          *publisher.ProviderRegistry
	Cipher            *secure.Cipher
	PublicBaseURL     string
}

func (s Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mcpHandler := s.newMCPHandler()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", uiAssetsHandler()))
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
	mux.HandleFunc("GET /accounts", s.handleListAccounts)
	mux.HandleFunc("POST /accounts/static", s.handleCreateStaticAccount)
	mux.HandleFunc("POST /accounts/", s.handleAccountActions)
	mux.HandleFunc("DELETE /accounts/", s.handleDeleteAccount)
	mux.HandleFunc("POST /oauth/", s.handleOAuthStart)
	mux.HandleFunc("GET /oauth/", s.handleOAuthCallback)
	mux.HandleFunc("GET /schedule", s.handleScheduleJSON)
	mux.HandleFunc("GET /dlq", s.handleListDLQ)
	mux.HandleFunc("POST /dlq/requeue", s.handleBulkRequeueDLQ)
	mux.HandleFunc("POST /dlq/delete", s.handleBulkDeleteDLQ)
	mux.HandleFunc("POST /dlq/", s.handleDLQAction)
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

	kind := strings.ToLower(upload.Kind)
	if kind == "" {
		kind = "video"
	}

	created, err := s.Store.CreateMedia(r.Context(), domain.Media{
		ID:           upload.MediaID,
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

func (s Server) handleListDLQ(w http.ResponseWriter, r *http.Request) {
	limit := dlqapp.DefaultListLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		var parsed int
		_, err := fmt.Sscanf(raw, "%d", &parsed)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, errors.New("limit must be a positive integer"))
			return
		}
		limit = dlqapp.ClampListLimit(parsed)
	}

	svc := dlqapp.Service{Store: s.Store}
	items, err := svc.List(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"count": len(items),
	})
}

func (s Server) handleDLQAction(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/requeue"):
		s.handleRequeueDLQ(w, r)
	case strings.HasSuffix(r.URL.Path, "/delete"):
		s.handleDeleteDLQ(w, r)
	default:
		writeError(w, http.StatusNotFound, errors.New("not found"))
	}
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
	svc := dlqapp.Service{Store: s.Store}
	post, err := svc.Requeue(r.Context(), id)
	if errors.Is(err, dlqapp.ErrDeadLetterIDRequired) {
		if fromForm {
			http.Redirect(w, r, "/?view=failed&failed_error=invalid+dead+letter+id", http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, errors.New("invalid dead letter id"))
		return
	}
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

func (s Server) handleDeleteDLQ(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/dlq/") || !strings.HasSuffix(r.URL.Path, "/delete") {
		writeError(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	trimmed := strings.TrimPrefix(r.URL.Path, "/dlq/")
	id := strings.TrimSuffix(trimmed, "/delete")
	id = strings.TrimSuffix(id, "/")
	contentType := strings.ToLower(r.Header.Get("content-type"))
	fromForm := strings.Contains(contentType, "application/x-www-form-urlencoded") || strings.Contains(contentType, "multipart/form-data")
	svc := dlqapp.Service{Store: s.Store}
	err := svc.Delete(r.Context(), id)
	if errors.Is(err, dlqapp.ErrDeadLetterIDRequired) {
		if fromForm {
			http.Redirect(w, r, "/?view=failed&failed_error=invalid+dead+letter+id", http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, errors.New("invalid dead letter id"))
		return
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if fromForm {
				http.Redirect(w, r, "/?view=failed&failed_error=dead+letter+not+found", http.StatusSeeOther)
				return
			}
			writeError(w, http.StatusNotFound, errors.New("dead letter not found"))
			return
		}
		if strings.Contains(err.Error(), "not deletable") {
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
		http.Redirect(w, r, "/?view=failed&failed_success=deleted", http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"dead_letter_id": id,
		"deleted":        true,
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

	svc := dlqapp.Service{Store: s.Store}
	result, err := svc.BulkRequeue(r.Context(), ids)
	if errors.Is(err, dlqapp.ErrIDsRequired) {
		if fromForm {
			http.Redirect(w, r, "/?view=failed&failed_error=no+items+selected", http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, errors.New("ids are required"))
		return
	}
	if err != nil {
		if fromForm {
			http.Redirect(w, r, "/?view=failed&failed_error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	if fromForm {
		q := url.Values{}
		q.Set("view", "failed")
		q.Set("failed_success", fmt.Sprintf("requeued %d", result.Success))
		if result.Failed > 0 {
			q.Set("failed_error", fmt.Sprintf("failed %d", result.Failed))
		}
		http.Redirect(w, r, "/?"+q.Encode(), http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"selected": result.Selected,
		"success":  result.Success,
		"failed":   result.Failed,
	})
}

func (s Server) handleBulkDeleteDLQ(w http.ResponseWriter, r *http.Request) {
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

	svc := dlqapp.Service{Store: s.Store}
	result, err := svc.BulkDelete(r.Context(), ids)
	if errors.Is(err, dlqapp.ErrIDsRequired) {
		if fromForm {
			http.Redirect(w, r, "/?view=failed&failed_error=no+items+selected", http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, errors.New("ids are required"))
		return
	}
	if err != nil {
		if fromForm {
			http.Redirect(w, r, "/?view=failed&failed_error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	if fromForm {
		q := url.Values{}
		q.Set("view", "failed")
		q.Set("failed_success", fmt.Sprintf("deleted %d", result.Success))
		if result.Failed > 0 {
			q.Set("failed_error", fmt.Sprintf("failed %d", result.Failed))
		}
		http.Redirect(w, r, "/?"+q.Encode(), http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"selected": result.Selected,
		"success":  result.Success,
		"failed":   result.Failed,
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
