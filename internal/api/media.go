package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/antoniolg/publisher/internal/db"
	"github.com/antoniolg/publisher/internal/domain"
)

const (
	defaultMediaListLimit = 200
	maxMediaListLimit     = 500
)

type mediaListItem struct {
	ID           string `json:"id"`
	Platform     string `json:"platform"`
	Kind         string `json:"kind"`
	OriginalName string `json:"original_name"`
	MimeType     string `json:"mime_type"`
	SizeBytes    int64  `json:"size_bytes"`
	SizeLabel    string `json:"size_label"`
	CreatedAt    string `json:"created_at"`
	CreatedLabel string `json:"created_label"`
	UsageCount   int    `json:"usage_count"`
	InUse        bool   `json:"in_use"`
	IsImage      bool   `json:"is_image"`
	IsVideo      bool   `json:"is_video"`
	PreviewURL   string `json:"preview_url"`
}

func clampMediaListLimit(limit int) int {
	if limit <= 0 {
		return defaultMediaListLimit
	}
	if limit > maxMediaListLimit {
		return maxMediaListLimit
	}
	return limit
}

func mediaContentURL(id string) string {
	return "/media/" + url.PathEscape(strings.TrimSpace(id)) + "/content"
}

func formatByteSize(n int64) string {
	if n <= 0 {
		return "0 B"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	v := float64(n)
	idx := 0
	for v >= 1024 && idx < len(units)-1 {
		v /= 1024
		idx++
	}
	if idx == 0 || v >= 10 {
		return fmt.Sprintf("%.0f %s", v, units[idx])
	}
	return fmt.Sprintf("%.1f %s", v, units[idx])
}

func toMediaListItemWithUsage(media domain.Media, usageCount int, loc *time.Location) mediaListItem {
	if loc == nil {
		loc = time.UTC
	}
	mimeType := strings.TrimSpace(media.MimeType)
	mimeLower := strings.ToLower(mimeType)
	isImage := strings.HasPrefix(mimeLower, "image/")
	isVideo := strings.HasPrefix(mimeLower, "video/")
	createdAt := ""
	createdLabel := ""
	if !media.CreatedAt.IsZero() {
		createdAt = media.CreatedAt.UTC().Format(time.RFC3339)
		createdLabel = media.CreatedAt.In(loc).Format("2006-01-02 15:04 MST")
	}
	return mediaListItem{
		ID:           media.ID,
		Platform:     string(media.Platform),
		Kind:         media.Kind,
		OriginalName: media.OriginalName,
		MimeType:     mimeType,
		SizeBytes:    media.SizeBytes,
		SizeLabel:    formatByteSize(media.SizeBytes),
		CreatedAt:    createdAt,
		CreatedLabel: createdLabel,
		UsageCount:   usageCount,
		InUse:        usageCount > 0,
		IsImage:      isImage,
		IsVideo:      isVideo,
		PreviewURL:   mediaContentURL(media.ID),
	}
}

func (s Server) listMediaItems(ctx context.Context, limit int, loc *time.Location) ([]mediaListItem, error) {
	raw, err := s.Store.ListMedia(ctx, clampMediaListLimit(limit))
	if err != nil {
		return nil, err
	}
	items := make([]mediaListItem, 0, len(raw))
	for _, row := range raw {
		items = append(items, toMediaListItemWithUsage(row.Media, row.UsageCount, loc))
	}
	return items, nil
}

func (s Server) deleteMediaByID(ctx context.Context, mediaID string, loc *time.Location) (mediaListItem, error) {
	deleted, err := s.Store.DeleteMediaIfUnused(ctx, strings.TrimSpace(mediaID))
	if err != nil {
		return mediaListItem{}, err
	}
	if path := strings.TrimSpace(deleted.StoragePath); path != "" {
		removeErr := os.Remove(path)
		if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			// Keep the DB delete as source of truth; file cleanup can be retried later.
		}
	}
	return toMediaListItemWithUsage(deleted, 0, loc), nil
}

func (s Server) handleListMedia(w http.ResponseWriter, r *http.Request) {
	limit := defaultMediaListLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		var parsed int
		if _, err := fmt.Sscanf(raw, "%d", &parsed); err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, errors.New("limit must be a positive integer"))
			return
		}
		limit = parsed
	}

	uiLoc, _, _, err := s.resolveUILocation(r.Context())
	if err != nil {
		uiLoc = time.UTC
	}
	items, err := s.listMediaItems(r.Context(), limit, uiLoc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"count": len(items),
		"items": items,
	})
}

func (s Server) handleMediaContent(w http.ResponseWriter, r *http.Request) {
	mediaID := strings.TrimSpace(r.PathValue("id"))
	if mediaID == "" {
		writeError(w, http.StatusBadRequest, errors.New("media id is required"))
		return
	}

	media, err := s.Store.GetMediaByIDs(r.Context(), []string{mediaID})
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			writeError(w, http.StatusNotFound, errors.New("media not found"))
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if len(media) == 0 {
		writeError(w, http.StatusNotFound, errors.New("media not found"))
		return
	}
	item := media[0]
	if item.MimeType != "" {
		w.Header().Set("Content-Type", item.MimeType)
	}
	http.ServeFile(w, r, item.StoragePath)
}

func mediaDeleteErrorStatus(err error) int {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return http.StatusNotFound
	case errors.Is(err, db.ErrMediaInUse):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func mediaDeleteErrorMessage(err error) string {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "media not found"
	case errors.Is(err, db.ErrMediaInUse):
		return "media is in use by existing posts"
	default:
		return strings.TrimSpace(err.Error())
	}
}

func (s Server) handleDeleteMedia(w http.ResponseWriter, r *http.Request) {
	mediaID := strings.TrimSpace(r.PathValue("id"))
	if mediaID == "" {
		writeError(w, http.StatusBadRequest, errors.New("media id is required"))
		return
	}

	uiLoc, _, _, err := s.resolveUILocation(r.Context())
	if err != nil {
		uiLoc = time.UTC
	}
	deleted, err := s.deleteMediaByID(r.Context(), mediaID, uiLoc)
	if err != nil {
		writeError(w, mediaDeleteErrorStatus(err), errors.New(mediaDeleteErrorMessage(err)))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deleted": true,
		"media":   deleted,
	})
}

func (s Server) handleDeleteMediaForm(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/?view=settings&media_error=invalid+form", http.StatusSeeOther)
		return
	}
	mediaID := strings.TrimSpace(r.PathValue("id"))
	returnTo := sanitizeReturnTo(strings.TrimSpace(r.FormValue("return_to")))
	if returnTo == "" {
		returnTo = "/?view=settings"
	}
	uiLoc, _, _, err := s.resolveUILocation(r.Context())
	if err != nil {
		uiLoc = time.UTC
	}
	if _, err := s.deleteMediaByID(r.Context(), mediaID, uiLoc); err != nil {
		http.Redirect(w, r, withQueryValue(returnTo, "media_error", mediaDeleteErrorMessage(err)), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, withQueryValue(returnTo, "media_success", "media deleted"), http.StatusSeeOther)
}
