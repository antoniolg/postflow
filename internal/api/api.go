package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/antoniolg/publisher/internal/db"
	"github.com/antoniolg/publisher/internal/domain"
)

type Server struct {
	Store             *db.Store
	DataDir           string
	DefaultMaxRetries int
}

func (s Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("POST /media", s.handleUploadMedia)
	mux.HandleFunc("POST /posts", s.handleCreatePost)
	mux.HandleFunc("GET /schedule", s.handleScheduleJSON)
	mux.HandleFunc("POST /posts/", s.handleCancelPost)
	mux.HandleFunc("GET /", s.handleScheduleHTML)
	return mux
}

func (s Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s Server) handleUploadMedia(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(1 << 30); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid multipart form: %w", err))
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing file field: %w", err))
		return
	}
	defer f.Close()

	platform := domain.Platform(strings.ToLower(r.FormValue("platform")))
	if platform == "" {
		platform = domain.PlatformX
	}
	if platform != domain.PlatformX {
		writeError(w, http.StatusBadRequest, errors.New("only platform 'x' is supported in this MVP"))
		return
	}
	kind := strings.ToLower(r.FormValue("kind"))
	if kind == "" {
		kind = "video"
	}

	mediaID, err := db.NewID("med")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	name := sanitizeName(hdr.Filename)
	if name == "" {
		name = "upload.bin"
	}
	storageDir := filepath.Join(s.DataDir, "media")
	if err := os.MkdirAll(storageDir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	storagePath := filepath.Join(storageDir, mediaID+"_"+name)
	out, err := os.Create(storagePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	size, copyErr := io.Copy(out, f)
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		writeError(w, http.StatusInternalServerError, errors.Join(copyErr, closeErr))
		return
	}

	mimeType := hdr.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = mime.TypeByExtension(filepath.Ext(hdr.Filename))
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
	}

	created, err := s.Store.CreateMedia(r.Context(), domain.Media{
		ID:           mediaID,
		Platform:     platform,
		Kind:         kind,
		OriginalName: hdr.Filename,
		StoragePath:  storagePath,
		MimeType:     mimeType,
		SizeBytes:    size,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

type createPostRequest struct {
	Platform    string   `json:"platform"`
	Text        string   `json:"text"`
	ScheduledAt string   `json:"scheduled_at"`
	MediaIDs    []string `json:"media_ids"`
	MaxAttempts int      `json:"max_attempts"`
}

func (s Server) handleCreatePost(w http.ResponseWriter, r *http.Request) {
	var req createPostRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid json body: %w", err))
		return
	}
	platform := domain.Platform(strings.ToLower(req.Platform))
	if platform == "" {
		platform = domain.PlatformX
	}
	if platform != domain.PlatformX {
		writeError(w, http.StatusBadRequest, errors.New("only platform 'x' is supported in this MVP"))
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeError(w, http.StatusBadRequest, errors.New("text is required"))
		return
	}
	scheduledAt, err := time.Parse(time.RFC3339, req.ScheduledAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("scheduled_at must be RFC3339: %w", err))
		return
	}
	if _, err := s.Store.GetMediaByIDs(r.Context(), req.MediaIDs); err != nil {
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
		writeError(w, http.StatusBadRequest, errors.New("Idempotency-Key too long (max 128 chars)"))
		return
	}
	result, err := s.Store.CreatePost(r.Context(), db.CreatePostParams{
		Post: domain.Post{
			Platform:    platform,
			Text:        req.Text,
			Status:      domain.PostStatusScheduled,
			ScheduledAt: scheduledAt.UTC(),
			MaxAttempts: maxAttempts,
		},
		MediaIDs:       req.MediaIDs,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if result.Created {
		writeJSON(w, http.StatusCreated, result.Post)
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

func (s Server) handleCancelPost(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/posts/") || !strings.HasSuffix(r.URL.Path, "/cancel") {
		writeError(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	trimmed := strings.TrimPrefix(r.URL.Path, "/posts/")
	id := strings.TrimSuffix(trimmed, "/cancel")
	id = strings.TrimSuffix(id, "/")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("invalid post id"))
		return
	}
	if err := s.Store.CancelPost(r.Context(), id); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": string(domain.PostStatusCanceled)})
}

func (s Server) handleScheduleHTML(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		writeError(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	from := time.Now().UTC().Add(-24 * time.Hour)
	to := time.Now().UTC().Add(14 * 24 * time.Hour)
	items, err := s.Store.ListSchedule(r.Context(), from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	const tpl = `<!doctype html>
<html lang="es">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>publisher · schedule</title>
  <style>
    body { font-family: ui-sans-serif, -apple-system, sans-serif; margin: 24px; color:#111; }
    h1 { margin:0 0 16px 0; }
    table { width:100%; border-collapse: collapse; }
    th, td { border:1px solid #ddd; padding:8px; font-size:14px; text-align:left; vertical-align: top; }
    th { background:#f5f5f5; }
    .badge { padding:2px 8px; border-radius:999px; background:#f0f0f0; display:inline-block; }
  </style>
</head>
<body>
  <h1>Publisher schedule</h1>
  <p>Vista informativa (solo lectura).</p>
  <table>
    <thead>
      <tr><th>Fecha</th><th>Plataforma</th><th>Estado</th><th>Texto</th><th>Media</th></tr>
    </thead>
    <tbody>
      {{range .}}
      <tr>
        <td>{{.ScheduledAt.Format "2006-01-02 15:04:05Z07:00"}}</td>
        <td>{{.Platform}}</td>
        <td><span class="badge">{{.Status}}</span></td>
        <td>{{.Text}}</td>
        <td>{{len .Media}}</td>
      </tr>
      {{else}}
      <tr><td colspan="5">No hay publicaciones en este rango.</td></tr>
      {{end}}
    </tbody>
  </table>
</body>
</html>`
	t, err := template.New("schedule").Parse(tpl)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = t.Execute(w, items)
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

func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]any{"error": err.Error()})
}
