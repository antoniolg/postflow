package api

import (
	"context"
	"database/sql"
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
	RateLimitRPM      int
	APIToken          string
	UIBasicUser       string
	UIBasicPass       string
}

func (s Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("POST /media", s.handleUploadMedia)
	mux.HandleFunc("POST /posts", s.handleCreatePost)
	mux.HandleFunc("POST /posts/", s.handlePostActions)
	mux.HandleFunc("POST /posts/validate", s.handleValidatePost)
	mux.HandleFunc("GET /schedule", s.handleScheduleJSON)
	mux.HandleFunc("GET /dlq", s.handleListDLQ)
	mux.HandleFunc("POST /dlq/", s.handleRequeueDLQ)
	mux.HandleFunc("GET /", s.handleScheduleHTML)
	return s.withMiddlewares(mux)
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
	var scheduledAt time.Time
	if strings.TrimSpace(req.ScheduledAt) != "" {
		parsed, err := time.Parse(time.RFC3339, req.ScheduledAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("scheduled_at must be RFC3339: %w", err))
			return
		}
		scheduledAt = parsed.UTC()
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
			Status:      defaultStatusForScheduledAt(scheduledAt),
			ScheduledAt: scheduledAt,
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

	scheduledAtRaw := strings.TrimSpace(r.FormValue("scheduled_at"))
	if scheduledAtRaw == "" {
		localRaw := strings.TrimSpace(r.FormValue("scheduled_at_local"))
		if localRaw != "" {
			localTime, err := time.ParseInLocation("2006-01-02T15:04", localRaw, time.Local)
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
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("invalid dead letter id"))
		return
	}

	post, err := s.Store.RequeueDeadLetter(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, errors.New("dead letter not found"))
			return
		}
		if strings.Contains(err.Error(), "not requeueable") {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"dead_letter_id": id,
		"post":           post,
	})
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
	drafts, err := s.Store.ListDrafts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	scheduledCount := 0
	publishedCount := 0
	failedCount := 0
	var nextRun *time.Time
	for _, item := range items {
		switch item.Status {
		case domain.PostStatusScheduled:
			scheduledCount++
			if !item.ScheduledAt.IsZero() && (nextRun == nil || item.ScheduledAt.Before(*nextRun)) {
				t := item.ScheduledAt
				nextRun = &t
			}
		case domain.PostStatusPublished:
			publishedCount++
		case domain.PostStatusFailed:
			failedCount++
		}
	}
	nextRunLabel := "Sin próxima ejecución"
	if nextRun != nil {
		nextRunLabel = nextRun.Local().Format("2006-01-02 15:04 MST")
	}
	const tpl = `<!doctype html>
<html lang="es">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>publisher · schedule</title>
  <link rel="preconnect" href="https://fonts.googleapis.com">
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
  <link href="https://fonts.googleapis.com/css2?family=Instrument+Sans:wght@400;500;600;700&family=Sora:wght@500;600;700&display=swap" rel="stylesheet">
  <style>
    :root {
      --bg: #f3f5fb;
      --text: #10142a;
      --muted: #59617c;
      --card: #ffffff;
      --border: #dde3f2;
      --accent: #356dff;
      --accent-soft: #e9efff;
      --ok: #0b9f74;
      --warn: #d97706;
      --danger: #d64045;
      --shadow: 0 18px 40px rgba(28, 44, 108, 0.08);
    }
    * { box-sizing: border-box; }
    body {
      font-family: "Instrument Sans", ui-sans-serif, -apple-system, sans-serif;
      margin: 0;
      color: var(--text);
      background:
        radial-gradient(1200px 600px at 0% -20%, #e1ebff 0%, transparent 55%),
        radial-gradient(900px 500px at 100% -10%, #efe7ff 0%, transparent 45%),
        var(--bg);
      min-height: 100vh;
    }
    .shell {
      max-width: 1200px;
      margin: 0 auto;
      padding: 28px 20px 36px;
    }
    .hero {
      background: linear-gradient(135deg, #101c49 0%, #192f77 60%, #3357d6 100%);
      color: #eef2ff;
      border-radius: 24px;
      padding: 24px;
      box-shadow: var(--shadow);
      margin-bottom: 16px;
      position: relative;
      overflow: hidden;
    }
    .hero::after {
      content: "";
      position: absolute;
      width: 320px;
      height: 320px;
      right: -110px;
      top: -120px;
      background: radial-gradient(circle, rgba(255,255,255,0.18) 0%, transparent 70%);
      pointer-events: none;
    }
    .eyebrow {
      display: inline-flex;
      align-items: center;
      gap: 6px;
      font-size: 12px;
      background: rgba(255,255,255,0.14);
      border: 1px solid rgba(255,255,255,0.2);
      border-radius: 999px;
      padding: 4px 10px;
      margin-bottom: 10px;
    }
    h1 {
      font-family: "Sora", "Instrument Sans", sans-serif;
      font-size: 28px;
      letter-spacing: -0.03em;
      margin: 0 0 8px 0;
    }
    p.lead {
      margin: 0;
      font-size: 15px;
      color: #d9e3ff;
      max-width: 760px;
    }
    .stats {
      margin-top: 16px;
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 10px;
    }
    @media(min-width: 860px) {
      .stats { grid-template-columns: repeat(4, minmax(0, 1fr)); }
    }
    .stat {
      border-radius: 14px;
      background: rgba(255,255,255,0.12);
      border: 1px solid rgba(255,255,255,0.22);
      padding: 10px 12px;
    }
    .stat .label { font-size: 12px; color: #cfdbff; margin-bottom: 3px; }
    .stat .value { font-size: 18px; font-weight: 700; }
    .grid {
      display: grid;
      grid-template-columns: 1fr;
      gap: 16px;
    }
    @media(min-width: 1040px){
      .grid { grid-template-columns: minmax(0, 2fr) minmax(340px, 1fr); }
    }
    .card {
      background: var(--card);
      border: 1px solid var(--border);
      border-radius: 18px;
      box-shadow: var(--shadow);
      overflow: hidden;
    }
    .card-head {
      padding: 16px 18px 12px;
      border-bottom: 1px solid #edf1fb;
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 10px;
    }
    .card h2 {
      margin: 0;
      font-family: "Sora", "Instrument Sans", sans-serif;
      font-size: 16px;
      letter-spacing: -0.01em;
    }
    .hint { font-size: 12px; color: var(--muted); }
    .table-wrap { overflow: auto; }
    table {
      width: 100%;
      border-collapse: collapse;
      background: #fff;
    }
    th, td {
      padding: 11px 12px;
      font-size: 13px;
      text-align: left;
      vertical-align: top;
      border-bottom: 1px solid #edf1fb;
    }
    th {
      position: sticky;
      top: 0;
      background: #f8faff;
      color: #3f4a69;
      font-size: 12px;
      letter-spacing: 0.02em;
      text-transform: uppercase;
      z-index: 1;
    }
    tr:hover td { background: #fbfcff; }
    .badge {
      padding: 3px 9px;
      border-radius: 999px;
      display: inline-block;
      font-size: 11px;
      font-weight: 600;
      border: 1px solid transparent;
      text-transform: capitalize;
    }
    .status-scheduled { background: #eef4ff; color: #2255d8; border-color: #d9e5ff; }
    .status-published { background: #e8fbf3; color: var(--ok); border-color: #c9f3e2; }
    .status-failed { background: #ffeef0; color: var(--danger); border-color: #ffd6db; }
    .status-canceled { background: #f3f4f6; color: #5d6375; border-color: #e4e7ee; }
    .status-publishing { background: #fff6e7; color: var(--warn); border-color: #ffe4ba; }
    .status-draft { background: #f0ecff; color: #6a4cd8; border-color: #dfd5ff; }
    .text-cell { max-width: 520px; line-height: 1.4; }
    .draft-list {
      padding: 14px;
      max-height: 640px;
      overflow: auto;
    }
    .draft-item {
      border: 1px solid var(--border);
      border-radius: 14px;
      padding: 12px;
      margin-bottom: 10px;
      background: linear-gradient(180deg, #ffffff 0%, #fdfdff 100%);
    }
    .draft-title {
      font-size: 14px;
      font-weight: 600;
      line-height: 1.35;
      margin-bottom: 6px;
      color: #141a33;
    }
    .meta {
      color: var(--muted);
      font-size: 12px;
      margin-top: 2px;
    }
    .row {
      display: flex;
      gap: 8px;
      align-items: center;
      margin-top: 10px;
      flex-wrap: wrap;
    }
    input[type=datetime-local]{
      min-width: 210px;
      padding: 8px 10px;
      border: 1px solid #cfd7ea;
      border-radius: 9px;
      background: #fff;
      color: #11162f;
      font: inherit;
      font-size: 13px;
    }
    input[type=datetime-local]:focus {
      outline: none;
      border-color: #7ca2ff;
      box-shadow: 0 0 0 3px rgba(75, 127, 255, 0.14);
    }
    button {
      border: 1px solid #2d54c7;
      background: linear-gradient(180deg, #4b7fff 0%, #356dff 100%);
      color: #fff;
      border-radius: 9px;
      padding: 8px 12px;
      font-weight: 600;
      cursor: pointer;
      transition: transform .12s ease, box-shadow .12s ease, filter .12s ease;
    }
    button:hover {
      transform: translateY(-1px);
      box-shadow: 0 10px 24px rgba(53,109,255,0.22);
      filter: brightness(1.02);
    }
    .empty {
      border: 1px dashed var(--border);
      border-radius: 12px;
      padding: 16px;
      text-align: center;
      color: var(--muted);
      background: #fbfcff;
      font-size: 13px;
    }
  </style>
</head>
<body>
  <div class="shell">
    <section class="hero">
      <div class="eyebrow">Publisher Mock Studio</div>
      <h1>Calendario editorial + banco de ideas</h1>
      <p class="lead">Guarda ideas como borradores, asígnales fecha cuando estén listas y mantén el calendario controlado en un solo panel.</p>
      <div class="stats">
        <div class="stat">
          <div class="label">Programadas</div>
          <div class="value">{{.ScheduledCount}}</div>
        </div>
        <div class="stat">
          <div class="label">Borradores</div>
          <div class="value">{{.DraftCount}}</div>
        </div>
        <div class="stat">
          <div class="label">Publicadas</div>
          <div class="value">{{.PublishedCount}}</div>
        </div>
        <div class="stat">
          <div class="label">Siguiente ejecución</div>
          <div class="value" style="font-size:14px;line-height:1.2;">{{.NextRunLabel}}</div>
        </div>
      </div>
    </section>
    <div class="grid">
      <section class="card">
        <div class="card-head">
          <h2>Programadas</h2>
          <span class="hint">{{len .Items}} en ventana actual</span>
        </div>
        <div class="table-wrap">
          <table>
            <thead>
              <tr><th>Fecha</th><th>Plataforma</th><th>Estado</th><th>Texto</th><th>Media</th></tr>
            </thead>
            <tbody>
              {{range .Items}}
              <tr>
                <td>{{if .ScheduledAt.IsZero}}-{{else}}{{.ScheduledAt.Format "2006-01-02 15:04:05Z07:00"}}{{end}}</td>
                <td>{{.Platform}}</td>
                <td><span class="badge status-{{.Status}}">{{.Status}}</span></td>
                <td class="text-cell">{{.Text}}</td>
                <td>{{len .Media}}</td>
              </tr>
              {{else}}
              <tr><td colspan="5"><div class="empty">No hay publicaciones en este rango todavía.</div></td></tr>
              {{end}}
            </tbody>
          </table>
        </div>
      </section>
      <section class="card">
        <div class="card-head">
          <h2>Borradores</h2>
          <span class="hint">Ideas sin fecha</span>
        </div>
        <div class="draft-list">
          {{range .Drafts}}
          <div class="draft-item">
            <div class="draft-title">{{.Text}}</div>
            <div class="meta">ID: {{.ID}} · {{len .Media}} media</div>
            <form method="post" action="/posts/{{.ID}}/schedule">
              <div class="row">
                <input type="datetime-local" name="scheduled_at_local" required />
                <button type="submit">Programar</button>
              </div>
            </form>
          </div>
          {{else}}
          <div class="empty">No hay borradores aún. Crea ideas desde API y aparecerán aquí.</div>
          {{end}}
        </div>
      </section>
    </div>
  </div>
</body>
</html>`
	t, err := template.New("schedule").Parse(tpl)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	type pageData struct {
		Items          []domain.Post
		Drafts         []domain.Post
		ScheduledCount int
		DraftCount     int
		PublishedCount int
		FailedCount    int
		NextRunLabel   string
	}
	_ = t.Execute(w, pageData{
		Items:          items,
		Drafts:         drafts,
		ScheduledCount: scheduledCount,
		DraftCount:     len(drafts),
		PublishedCount: publishedCount,
		FailedCount:    failedCount,
		NextRunLabel:   nextRunLabel,
	})
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
