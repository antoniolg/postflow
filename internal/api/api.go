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
	"mime"
	"net/http"
	"net/url"
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
	Intent      string   `json:"intent"`
}

func (s Server) handleCreatePost(w http.ResponseWriter, r *http.Request) {
	req, fromForm, err := parseCreatePostRequest(r)
	if err != nil {
		if fromForm {
			http.Redirect(w, r, createViewURL("", req.Text, req.ScheduledAt, err.Error(), ""), http.StatusSeeOther)
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
			http.Redirect(w, r, createViewURL("", req.Text, req.ScheduledAt, "only platform 'x' is supported in this MVP", ""), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, errors.New("only platform 'x' is supported in this MVP"))
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		if fromForm {
			http.Redirect(w, r, createViewURL("", req.Text, req.ScheduledAt, "text is required", ""), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, errors.New("text is required"))
		return
	}
	scheduledAt, err := parseScheduledAtInput(req.ScheduledAt)
	if err != nil {
		if fromForm {
			http.Redirect(w, r, createViewURL("", req.Text, req.ScheduledAt, err.Error(), ""), http.StatusSeeOther)
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
				http.Redirect(w, r, createViewURL("", req.Text, req.ScheduledAt, "scheduled_at is required to schedule", ""), http.StatusSeeOther)
				return
			}
		}
	}
	if _, err := s.Store.GetMediaByIDs(r.Context(), req.MediaIDs); err != nil {
		if fromForm {
			http.Redirect(w, r, createViewURL("", req.Text, req.ScheduledAt, err.Error(), ""), http.StatusSeeOther)
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
			http.Redirect(w, r, createViewURL("", req.Text, req.ScheduledAt, "idempotency key too long (max 128 chars)", ""), http.StatusSeeOther)
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
			http.Redirect(w, r, createViewURL("", req.Text, req.ScheduledAt, err.Error(), ""), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if result.Created {
		if fromForm {
			http.Redirect(w, r, createViewURL("", "", "", "", "post created"), http.StatusSeeOther)
			return
		}
		writeJSON(w, http.StatusCreated, result.Post)
		return
	}
	if fromForm {
		http.Redirect(w, r, createViewURL("", "", "", "", "post updated"), http.StatusSeeOther)
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

func (s Server) handleEditPost(w http.ResponseWriter, r *http.Request) {
	postID, err := extractPostIDFromPath(r.URL.Path, "edit")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	fromForm := !strings.Contains(strings.ToLower(r.Header.Get("content-type")), "application/json")
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
			http.Redirect(w, r, createViewURL(postID, text, strings.TrimSpace(r.FormValue("scheduled_at_local")), "text is required", ""), http.StatusSeeOther)
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
		parsed, err := parseScheduledAtInput(scheduledAtRaw)
		if err != nil {
			if fromForm {
				http.Redirect(w, r, createViewURL(postID, text, scheduledAtRaw, err.Error(), ""), http.StatusSeeOther)
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
	if intent == "schedule" && scheduledAt.IsZero() {
		if fromForm {
			http.Redirect(w, r, createViewURL(postID, text, scheduledAtRaw, "scheduled_at is required to schedule", ""), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, errors.New("scheduled_at is required"))
		return
	}
	if err := s.Store.UpdatePostEditable(r.Context(), postID, text, scheduledAt); err != nil {
		if fromForm {
			http.Redirect(w, r, createViewURL(postID, text, scheduledAtRaw, err.Error(), ""), http.StatusSeeOther)
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
		scheduledLocal = post.ScheduledAt.In(time.Local).Format("2006-01-02T15:04")
	}
	http.Redirect(w, r, createViewURL(post.ID, post.Text, scheduledLocal, "", "changes saved"), http.StatusSeeOther)
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
	nowLocal := time.Now().In(time.Local)
	view := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("view")))
	if view == "" {
		view = "publications"
	}
	if view != "calendar" && view != "publications" && view != "drafts" && view != "create" {
		view = "publications"
	}
	editID := strings.TrimSpace(r.URL.Query().Get("edit_id"))
	createError := strings.TrimSpace(r.URL.Query().Get("error"))
	createSuccess := strings.TrimSpace(r.URL.Query().Get("success"))
	displayMonth := time.Date(nowLocal.Year(), nowLocal.Month(), 1, 0, 0, 0, 0, time.Local)
	if monthRaw := strings.TrimSpace(r.URL.Query().Get("month")); monthRaw != "" {
		if parsedMonth, err := time.ParseInLocation("2006-01", monthRaw, time.Local); err == nil {
			displayMonth = time.Date(parsedMonth.Year(), parsedMonth.Month(), 1, 0, 0, 0, 0, time.Local)
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

	type calendarEvent struct {
		TimeLabel   string
		StatusClass string
		StatusLabel string
		StatusKey   string
		TextPreview string
	}
	type dayDetailItem struct {
		TimeLabel   string
		StatusClass string
		StatusLabel string
		StatusKey   string
		Text        string
		Platform    domain.Platform
		MediaCount  int
	}
	type calendarDay struct {
		DateKey        string
		DayNumber      int
		InCurrentMonth bool
		IsToday        bool
		IsSelected     bool
		Events         []calendarEvent
		OverflowCount  int
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
		localTime := item.ScheduledAt.In(time.Local)
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
		if parsedDay, err := time.ParseInLocation("2006-01-02", dayRaw, time.Local); err == nil {
			selectedDayLocal = parsedDay
		}
	}

	var calendarDays []calendarDay
	for d := gridStart; !d.After(gridEnd); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		dayEvents := eventsByDate[key]
		overflow := 0
		if len(dayEvents) > 3 {
			overflow = len(dayEvents) - 3
			dayEvents = dayEvents[:3]
		}
		calendarDays = append(calendarDays, calendarDay{
			DateKey:        key,
			DayNumber:      d.Day(),
			InCurrentMonth: d.Month() == monthStartLocal.Month(),
			IsToday:        d.Year() == nowLocal.Year() && d.Month() == nowLocal.Month() && d.Day() == nowLocal.Day(),
			IsSelected:     d.Year() == selectedDayLocal.Year() && d.Month() == selectedDayLocal.Month() && d.Day() == selectedDayLocal.Day(),
			Events:         dayEvents,
			OverflowCount:  overflow,
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
	todayMonthParam := nowLocal.Format("2006-01")
	todayDayKey := nowLocal.Format("2006-01-02")
	var editingPost *domain.Post
	var createText string
	var createScheduledLocal string
	if editID != "" {
		p, err := s.Store.GetPost(r.Context(), editID)
		if err == nil {
			editingPost = &p
			createText = p.Text
			if !p.ScheduledAt.IsZero() {
				createScheduledLocal = p.ScheduledAt.In(time.Local).Format("2006-01-02T15:04")
			}
		}
	}
	if qText := strings.TrimSpace(r.URL.Query().Get("text")); qText != "" {
		createText = qText
	}
	if qScheduled := strings.TrimSpace(r.URL.Query().Get("scheduled_at_local")); qScheduled != "" {
		createScheduledLocal = qScheduled
	}
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
      --bg-page: #0f1014;
      --bg-panel: #14161d;
      --bg-card: #1a1d25;
      --bg-elevated: #212632;
      --bg-muted: #2b3140;
      --text-primary: #f5f7fb;
      --text-secondary: #97a2bd;
      --accent-orange: #ff7a30;
      --accent-teal: #36d3bf;
      --border: #2a3040;
      --radius: 12px;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      min-height: 100vh;
      color: var(--text-primary);
      background:
        radial-gradient(1200px 500px at 90% -10%, rgba(255,122,48,0.08), transparent 60%),
        radial-gradient(1000px 500px at 5% 0%, rgba(54,211,191,0.07), transparent 65%),
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
      border-right: 1px solid #191e29;
      padding: 24px 16px;
      background: rgba(13, 14, 19, 0.85);
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
    }
    .nav-item {
      border-radius: 10px;
      padding: 10px 12px;
      font-size: 12px;
      color: var(--text-secondary);
      border: 1px solid transparent;
      display: block;
      text-decoration: none;
    }
    .nav-item.active {
      color: var(--text-primary);
      background: var(--bg-elevated);
      border-color: #31394b;
    }
    .main {
      flex: 1;
      padding: 34px 44px 22px;
      max-width: 1180px;
    }
    .header {
      display: flex;
      justify-content: space-between;
      gap: 16px;
      align-items: center;
    }
    h1 {
      margin: 0 0 4px;
      font-family: "Oswald", sans-serif;
      font-weight: 700;
      letter-spacing: 0.02em;
      font-size: 50px;
      line-height: 1;
    }
    .subtitle {
      margin: 0;
      color: var(--text-secondary);
      font-size: 12px;
    }
    .create-pill {
      display: inline-flex;
      align-items: center;
      border-radius: 999px;
      background: var(--accent-orange);
      color: #111;
      border: 0;
      padding: 10px 16px;
      font-size: 11px;
      font-weight: 700;
      text-transform: lowercase;
      letter-spacing: 0.03em;
      text-decoration: none;
    }
    .tabs {
      margin-top: 18px;
      display: flex;
      gap: 8px;
      flex-wrap: wrap;
    }
    .tab {
      border-radius: 999px;
      padding: 7px 12px;
      font-size: 11px;
      color: var(--text-secondary);
      background: transparent;
      border: 1px solid var(--border);
      display: inline-flex;
      align-items: center;
      gap: 8px;
    }
    .tab.active {
      background: var(--bg-elevated);
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
    .dot.draft { background: #646f88; }
    .stats {
      margin-top: 14px;
      display: grid;
      grid-template-columns: repeat(4, minmax(0, 1fr));
      gap: 10px;
    }
    .stat {
      background: rgba(26, 29, 37, 0.55);
      border: 1px solid var(--border);
      border-radius: 10px;
      padding: 8px 10px;
    }
    .stat .k { color: var(--text-secondary); font-size: 10px; margin-bottom: 4px; }
    .stat .v { font-size: 16px; }
    .calendar-wrap {
      margin-top: 12px;
      border: 1px solid var(--border);
      background: #141923;
      border-radius: 14px;
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
    .day-panel {
      border: 1px solid var(--border);
      background: #141923;
      border-radius: 14px;
      overflow: hidden;
      position: sticky;
      top: 16px;
    }
    .day-panel-head {
      padding: 10px 12px;
      border-bottom: 1px solid #242b3b;
      background: #171d28;
    }
    .day-panel-title {
      font-size: 11px;
      letter-spacing: 0.08em;
      text-transform: uppercase;
      color: #c7d1e8;
      font-weight: 700;
      margin-bottom: 3px;
    }
    .day-panel-sub {
      font-size: 10px;
      color: var(--text-secondary);
    }
    .day-panel-body {
      padding: 10px;
      display: flex;
      flex-direction: column;
      gap: 8px;
      max-height: 560px;
      overflow: auto;
    }
    .day-item {
      border: 1px solid #2a3244;
      border-radius: 10px;
      background: #1a2130;
      padding: 8px;
    }
    .day-item.live { border-color: rgba(54,211,191,0.4); }
    .day-item.schd { border-color: rgba(255,122,48,0.45); }
    .day-item-head {
      display: flex;
      align-items: center;
      justify-content: space-between;
      margin-bottom: 4px;
      gap: 8px;
    }
    .day-item-time {
      color: #c7d1e8;
      font-size: 10px;
      font-weight: 700;
      letter-spacing: 0.06em;
    }
    .day-item-text {
      font-size: 11px;
      line-height: 1.35;
      color: #dee5f6;
      margin-bottom: 5px;
      word-break: break-word;
    }
    .day-item-meta {
      display: flex;
      gap: 8px;
      flex-wrap: wrap;
      font-size: 9px;
      color: #8fa0c1;
    }
    .calendar-head {
      display: flex;
      justify-content: space-between;
      align-items: center;
      padding: 10px 12px;
      border-bottom: 1px solid #242b3b;
      background: #171d28;
    }
    .calendar-title {
      font-size: 11px;
      letter-spacing: 0.08em;
      text-transform: uppercase;
      color: #c7d1e8;
      font-weight: 700;
    }
    .calendar-sub {
      font-size: 10px;
      color: var(--text-secondary);
    }
    .calendar-controls {
      display: flex;
      align-items: center;
      gap: 6px;
    }
    .month-link {
      color: var(--text-primary);
      text-decoration: none;
      border: 1px solid #34405a;
      border-radius: 8px;
      padding: 4px 7px;
      font-size: 10px;
      line-height: 1;
      background: #202839;
    }
    .month-go {
      display: inline-flex;
      align-items: center;
      text-decoration: none;
      border: 1px solid #8a4a1f;
      background: var(--accent-orange);
      color: #161616;
      border-radius: 7px;
      padding: 4px 7px;
      font-size: 10px;
      font-weight: 700;
      text-transform: lowercase;
      line-height: 1;
    }
    .weekday-row {
      display: grid;
      grid-template-columns: repeat(7, minmax(0, 1fr));
      border-bottom: 1px solid #202839;
      min-width: 700px;
    }
    .weekday {
      padding: 8px 8px;
      font-size: 9px;
      letter-spacing: 0.08em;
      text-transform: uppercase;
      color: #7080a1;
      border-right: 1px solid #1e2534;
      background: #151b26;
    }
    .weekday:last-child { border-right: 0; }
    .week-row {
      display: grid;
      grid-template-columns: repeat(7, minmax(0, 1fr));
      border-bottom: 1px solid #202839;
      min-width: 700px;
    }
    .week-row:last-child { border-bottom: 0; }
    .day-cell {
      min-height: 106px;
      min-width: 100px;
      border-right: 1px solid #1e2534;
      padding: 8px 8px 6px;
      background: #141923;
    }
    .day-cell:last-child { border-right: 0; }
    .day-cell.outside { background: #111621; }
    .day-cell.selected {
      background: #182031;
      box-shadow: inset 0 0 0 1px rgba(255,122,48,0.35);
    }
    .day-link {
      display: block;
      text-decoration: none;
      color: inherit;
      min-height: 90px;
    }
    .day-head {
      display: flex;
      justify-content: space-between;
      align-items: center;
      margin-bottom: 5px;
    }
    .day-num {
      font-size: 10px;
      color: #a8b4cf;
    }
    .day-num.today {
      color: #171717;
      background: var(--accent-orange);
      border-radius: 999px;
      min-width: 20px;
      text-align: center;
      padding: 2px 6px;
      font-weight: 700;
    }
    .day-events {
      display: flex;
      flex-direction: column;
      gap: 4px;
    }
    .day-event {
      border-radius: 7px;
      padding: 4px 5px;
      background: #1d2432;
      border: 1px solid #2a3244;
      font-size: 9px;
      color: #bdc8e0;
      line-height: 1.25;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    .day-event.live { border-color: rgba(54,211,191,0.4); }
    .day-event.schd { border-color: rgba(255,122,48,0.4); }
    .event-time {
      color: #90a1c2;
      margin-right: 4px;
    }
    .more {
      font-size: 9px;
      color: #7f8fad;
      margin-top: 1px;
    }
    .list {
      margin-top: 14px;
      display: flex;
      flex-direction: column;
      gap: 10px;
      padding-bottom: 20px;
    }
    .card {
      border: 1px solid #1e2430;
      border-radius: var(--radius);
      background: linear-gradient(180deg, #171a22 0%, #151820 100%);
      padding: 12px 14px;
      display: flex;
      justify-content: space-between;
      gap: 12px;
      align-items: center;
    }
    .card.scheduled {
      border-color: rgba(255, 122, 48, 0.28);
      box-shadow: inset 0 0 0 1px rgba(255, 122, 48, 0.08);
    }
    .card.published {
      border-color: rgba(54, 211, 191, 0.25);
      box-shadow: inset 0 0 0 1px rgba(54, 211, 191, 0.07);
    }
    .card.draft {
      border-color: #283041;
      box-shadow: inset 0 0 0 1px rgba(133, 148, 182, 0.05);
    }
    .card-left {
      display: flex;
      gap: 10px;
      align-items: flex-start;
      min-width: 0;
      flex: 1;
    }
    .status {
      width: 40px;
      flex: 0 0 auto;
      text-align: left;
      padding-top: 3px;
    }
    .status .label {
      display: block;
      margin-top: 3px;
      font-size: 9px;
      font-weight: 700;
      letter-spacing: 0.08em;
      color: var(--text-secondary);
      text-transform: uppercase;
    }
    .content {
      min-width: 0;
    }
    .content .text {
      font-size: 11px;
      line-height: 1.4;
      color: var(--text-primary);
      word-break: break-word;
    }
    .meta {
      margin-top: 5px;
      font-size: 10px;
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
      border: 1px solid #2f3648;
      background: var(--bg-elevated);
      color: var(--text-secondary);
      border-radius: 999px;
      padding: 6px 10px;
      font-size: 10px;
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
      border: 1px solid var(--border);
      border-radius: 14px;
      background: #141923;
      overflow: hidden;
      max-width: 760px;
    }
    .editor-head {
      padding: 10px 12px;
      border-bottom: 1px solid #242b3b;
      background: #171d28;
      font-size: 11px;
      letter-spacing: 0.08em;
      text-transform: uppercase;
      color: #c7d1e8;
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
      font-size: 10px;
      color: #9aa8c7;
      text-transform: uppercase;
      letter-spacing: 0.08em;
    }
    .field textarea {
      min-height: 150px;
      resize: vertical;
      border-radius: 10px;
      border: 1px solid #364058;
      background: #11141b;
      color: var(--text-primary);
      font: inherit;
      font-size: 12px;
      line-height: 1.45;
      padding: 10px;
    }
    .field input[type=datetime-local] {
      min-width: 0;
      width: 100%;
      max-width: 280px;
    }
    .editor-actions {
      display: flex;
      gap: 8px;
      flex-wrap: wrap;
      align-items: center;
    }
    .btn-secondary {
      border: 1px solid #364058;
      background: #1b2230;
      color: #c2cde6;
    }
    .alert {
      border-radius: 10px;
      padding: 8px 10px;
      font-size: 11px;
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
      border: 1px solid #364058;
      color: #c2cde6;
      background: #1b2230;
      border-radius: 999px;
      padding: 6px 10px;
      font-size: 10px;
      text-transform: lowercase;
      text-decoration: none;
      font-weight: 700;
    }
    .ghost-toggle {
      width: 34px;
      height: 18px;
      border-radius: 999px;
      background: #202531;
      border: 1px solid #2f3648;
      position: relative;
    }
    .ghost-toggle::after {
      content: "";
      width: 12px;
      height: 12px;
      border-radius: 999px;
      background: #414b62;
      position: absolute;
      top: 2px;
      left: 2px;
    }
    .status-live { color: var(--accent-teal); }
    .status-schd { color: var(--accent-orange); }
    .status-drft { color: #7f8ca8; }
    .meta-accent { color: var(--accent-orange); }
    .meta-soft { color: #7f8ca8; }
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
      border-radius: 8px;
      border: 1px solid #364058;
      background: #11141b;
      color: var(--text-primary);
      font: inherit;
      font-size: 11px;
    }
    button {
      border: 1px solid #8a4a1f;
      background: var(--accent-orange);
      color: #161616;
      border-radius: 999px;
      padding: 6px 10px;
      font-size: 10px;
      font-weight: 700;
      text-transform: lowercase;
      cursor: pointer;
    }
    .empty {
      border: 1px dashed #2f3649;
      background: #161a23;
      border-radius: 12px;
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
    @media (max-width: 980px) {
      .app { flex-direction: column; }
      .sidebar {
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
        flex-direction: row;
        overflow-x: auto;
        padding-bottom: 2px;
      }
      .nav-item {
        white-space: nowrap;
        flex: 0 0 auto;
      }
      .main {
        padding: 16px 12px 18px;
      }
      .header {
        flex-direction: column;
        align-items: flex-start;
        gap: 10px;
      }
      h1 { font-size: 34px; }
      .subtitle { font-size: 11px; }
      .create-pill {
        padding: 8px 12px;
        font-size: 10px;
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
      .calendar-head {
        padding: 8px 10px;
      }
      .calendar-controls {
        gap: 4px;
      }
      .month-link, .month-go {
        padding: 4px 6px;
        font-size: 9px;
      }
      .day-cell { min-height: 82px; }
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
      .editor-actions {
        width: 100%;
      }
      .editor-actions button, .editor-actions .ghost-btn {
        width: 100%;
        justify-content: center;
        text-align: center;
      }
    }
    @media (max-width: 520px) {
      h1 { font-size: 30px; }
      .stats { grid-template-columns: 1fr; }
      .status { width: 34px; }
      .content .text { font-size: 10px; }
      .meta { font-size: 9px; gap: 8px; }
    }
  </style>
</head>
<body data-view="{{.View}}">
  <div class="app">
    <aside class="sidebar">
      <div class="logo">
        <span class="logo-dot"></span>
        <span>post_flow</span>
      </div>
      <nav class="nav">
        <a class="nav-item {{if eq .View "publications"}}active{{end}}" href="/?view=publications&month={{.CurrentMonthParam}}">// publications</a>
        <a class="nav-item {{if eq .View "calendar"}}active{{end}}" href="/?view=calendar&month={{.CurrentMonthParam}}&day={{.SelectedDayKey}}">// calendar</a>
        <a class="nav-item {{if eq .View "drafts"}}active{{end}}" href="/?view=drafts">// drafts</a>
        <a class="nav-item {{if eq .View "create"}}active{{end}}" href="/?view=create">// create</a>
        <div class="nav-item">// dlq</div>
      </nav>
    </aside>
    <main class="main">
      <header class="header">
        <div>
          <h1>PUBLICATIONS</h1>
          <p class="subtitle">// manage your social posts and draft queue</p>
        </div>
        <a class="create-pill" href="/?view=create">create_post</a>
      </header>
      {{if eq .View "publications"}}
      <div class="tabs">
        <button type="button" class="tab filter-chip active" data-filter="published" aria-pressed="true"><span class="dot live"></span> published</button>
        <button type="button" class="tab filter-chip active" data-filter="scheduled" aria-pressed="true"><span class="dot scheduled"></span> scheduled</button>
        <button type="button" class="tab filter-chip active" data-filter="draft" aria-pressed="true"><span class="dot draft"></span> draft mode</button>
      </div>
      {{end}}
      {{if eq .View "publications"}}
      <div class="stats">
        <div class="stat"><div class="k">scheduled</div><div class="v">{{.ScheduledCount}}</div></div>
        <div class="stat"><div class="k">drafts</div><div class="v">{{.DraftCount}}</div></div>
        <div class="stat"><div class="k">published</div><div class="v">{{.PublishedCount}}</div></div>
        <div class="stat"><div class="k">next run</div><div class="v" style="font-size:12px;">{{.NextRunLabel}}</div></div>
      </div>
      {{end}}

      {{if eq .View "calendar"}}
      <div class="calendar-layout">
      <div class="calendar-wrap">
        <div class="calendar-head">
          <div>
            <div class="calendar-title">{{.CalendarMonthLabel}}</div>
            <div class="calendar-sub">{{len .Items}} posts en el mes</div>
          </div>
          <div class="calendar-controls">
            <a class="month-link" href="/?view=calendar&month={{.PrevMonthParam}}&day={{.SelectedDayKey}}">&lt;</a>
            <a class="month-go" href="/?view=calendar&month={{.TodayMonthParam}}&day={{.TodayDayKey}}">today</a>
            <a class="month-link" href="/?view=calendar&month={{.NextMonthParam}}&day={{.SelectedDayKey}}">&gt;</a>
          </div>
        </div>
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
            <div class="day-cell {{if not .InCurrentMonth}}outside{{end}} {{if .IsSelected}}selected{{end}}">
              <a class="day-link" href="/?view=calendar&month={{$.CurrentMonthParam}}&day={{.DateKey}}">
                <div class="day-head">
                  <span class="day-num {{if .IsToday}}today{{end}}">{{.DayNumber}}</span>
                </div>
                <div class="day-events">
                  {{range .Events}}
                  <div class="day-event {{.StatusClass}}" data-status="{{.StatusKey}}">
                    <span class="event-time">{{.TimeLabel}}</span>{{.TextPreview}}
                  </div>
                  {{end}}
                  {{if gt .OverflowCount 0}}
                  <div class="more">+{{.OverflowCount}} more</div>
                  {{end}}
                </div>
              </a>
            </div>
            {{end}}
          </div>
          {{end}}
        </div>
      </div>
      <aside class="day-panel">
        <div class="day-panel-head">
          <div class="day-panel-title">DAY DETAIL</div>
          <div class="day-panel-sub">{{.SelectedDayLabel}}</div>
        </div>
        <div class="day-panel-body">
          {{range .SelectedDayItems}}
          <article class="day-item {{.StatusClass}}" data-status="{{.StatusKey}}">
            <div class="day-item-head">
              <span class="day-item-time">{{.TimeLabel}}</span>
              <span class="label status-{{.StatusClass}}">{{.StatusLabel}}</span>
            </div>
            <div class="day-item-text">{{.Text}}</div>
            <div class="day-item-meta">
              <span>{{.Platform}}</span>
              <span>{{.MediaCount}} media</span>
            </div>
          </article>
          {{else}}
          <div class="empty">No hay publicaciones para este día.</div>
          {{end}}
        </div>
      </aside>
      </div>
      {{end}}

      {{if eq .View "publications"}}
      <div class="line">calendar</div>
      <section class="list">
        {{range .Items}}
        <article class="card {{.Status}}" data-status="{{.Status}}">
          <div class="card-left">
            <div class="status">
              {{if eq .Status "published"}}<span class="dot live"></span><span class="label status-live">LIVE</span>{{else if eq .Status "scheduled"}}<span class="dot scheduled"></span><span class="label status-schd">SCHD</span>{{else}}<span class="dot draft"></span><span class="label status-drft">DRFT</span>{{end}}
            </div>
            <div class="content">
              <div class="text">{{.Text}}</div>
              <div class="meta">
                <span class="{{if eq .Status "scheduled"}}meta-accent{{else}}meta-soft{{end}}">{{if .ScheduledAt.IsZero}}no date{{else}}{{.ScheduledAt.Format "2006-01-02 15:04 MST"}}{{end}}</span>
                <span>{{.Platform}}</span>
                <span>{{len .Media}} media</span>
              </div>
            </div>
          </div>
          <div class="card-actions">
            {{if or (eq .Status "scheduled") (eq .Status "draft") (eq .Status "failed") (eq .Status "canceled")}}
            <a class="pill pill-link" href="/?view=create&edit_id={{.ID}}">edit</a>
            {{end}}
            <span class="pill">details</span>
            <span class="ghost-toggle"></span>
          </div>
        </article>
        {{else}}
        <div class="empty">No hay publicaciones en esta ventana de calendario.</div>
        {{end}}
      </section>
      {{end}}

      {{if eq .View "drafts"}}
      <div class="line">draft queue</div>
      <section class="list">
        {{range .Drafts}}
        <article class="card draft" data-status="draft">
          <div class="card-left">
            <div class="status">
              <span class="dot draft"></span>
              <span class="label status-drft">DRFT</span>
            </div>
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
            <a class="pill pill-link" href="/?view=create&edit_id={{.ID}}">edit</a>
            <form method="post" action="/posts/{{.ID}}/schedule">
              <input type="datetime-local" name="scheduled_at_local" required />
              <button type="submit">schedule</button>
            </form>
          </div>
        </article>
        {{else}}
        <div class="empty">No hay borradores aún. Crea ideas por API y aparecerán aquí.</div>
        {{end}}
      </section>
      {{end}}

      {{if eq .View "create"}}
      <div class="line">composer</div>
      <section class="editor">
        <div class="editor-head">{{if .EditingPost}}edit publication{{else}}create publication{{end}}</div>
        <form class="editor-body" method="post" action="{{if .EditingPost}}/posts/{{.EditingPost.ID}}/edit{{else}}/posts{{end}}">
          <input type="hidden" name="platform" value="x" />
          {{if .CreateError}}<div class="alert error">{{.CreateError}}</div>{{end}}
          {{if .CreateSuccess}}<div class="alert success">{{.CreateSuccess}}</div>{{end}}
          <div class="field">
            <label>Text</label>
            <textarea name="text" required placeholder="Write your post...">{{.CreateText}}</textarea>
          </div>
          <div class="field">
            <label>Scheduled At</label>
            <input type="datetime-local" name="scheduled_at_local" value="{{.CreateScheduledLocal}}" />
          </div>
          <div class="editor-actions">
            <button class="btn-secondary" type="submit" name="intent" value="draft">save_draft</button>
            <button type="submit" name="intent" value="schedule">{{if .EditingPost}}update_schedule{{else}}create_scheduled{{end}}</button>
            <a class="ghost-btn" href="/?view=publications">back</a>
          </div>
        </form>
      </section>
      {{end}}
    </main>
  </div>
<script>
(() => {
  const view = document.body.dataset.view || "";
  if (view !== "publications") {
    return;
  }
  const storageKey = "publisher.ui.status-filters.v1";
  const defaultFilters = { published: true, scheduled: true, draft: true };
  let filters = { ...defaultFilters };

  try {
    const raw = localStorage.getItem(storageKey);
    if (raw) {
      const parsed = JSON.parse(raw);
      filters = { ...defaultFilters, ...parsed };
    }
  } catch (_) {}

  const chips = Array.from(document.querySelectorAll("[data-filter]"));
  const items = Array.from(document.querySelectorAll("[data-status]"));

  const save = () => {
    try {
      localStorage.setItem(storageKey, JSON.stringify(filters));
    } catch (_) {}
  };

  const syncChips = () => {
    chips.forEach((chip) => {
      const key = chip.dataset.filter;
      const active = !!filters[key];
      chip.classList.toggle("inactive", !active);
      chip.classList.toggle("active", active);
      chip.setAttribute("aria-pressed", String(active));
    });
  };

  const applyFilters = () => {
    items.forEach((el) => {
      const status = el.dataset.status;
      const visible = filters[status] !== false;
      el.classList.toggle("is-hidden", !visible);
    });
  };

  chips.forEach((chip) => {
    chip.addEventListener("click", () => {
      const key = chip.dataset.filter;
      filters[key] = !filters[key];
      if (!Object.values(filters).some(Boolean)) {
        filters[key] = true;
      }
      save();
      syncChips();
      applyFilters();
    });
  });

  syncChips();
  applyFilters();
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
		View                 string
		Items                []domain.Post
		Drafts               []domain.Post
		EditingPost          *domain.Post
		CreateText           string
		CreateScheduledLocal string
		CreateError          string
		CreateSuccess        string
		ScheduledCount       int
		DraftCount           int
		PublishedCount       int
		FailedCount          int
		NextRunLabel         string
		CalendarMonthLabel   string
		CalendarWeeks        [][]calendarDay
		PrevMonthParam       string
		NextMonthParam       string
		CurrentMonthParam    string
		TodayMonthParam      string
		TodayDayKey          string
		SelectedDayKey       string
		SelectedDayLabel     string
		SelectedDayItems     []dayDetailItem
	}
	_ = t.Execute(w, pageData{
		View:                 view,
		Items:                items,
		Drafts:               drafts,
		EditingPost:          editingPost,
		CreateText:           createText,
		CreateScheduledLocal: createScheduledLocal,
		CreateError:          createError,
		CreateSuccess:        createSuccess,
		ScheduledCount:       scheduledCount,
		DraftCount:           len(drafts),
		PublishedCount:       publishedCount,
		FailedCount:          failedCount,
		NextRunLabel:         nextRunLabel,
		CalendarMonthLabel:   calendarMonthLabel,
		CalendarWeeks:        calendarWeeks,
		PrevMonthParam:       prevMonthParam,
		NextMonthParam:       nextMonthParam,
		CurrentMonthParam:    currentMonthParam,
		TodayMonthParam:      todayMonthParam,
		TodayDayKey:          todayDayKey,
		SelectedDayKey:       selectedDayKey,
		SelectedDayLabel:     selectedDayLabel,
		SelectedDayItems:     selectedDayItems,
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
	}
	if req.Platform == "" {
		req.Platform = "x"
	}
	if raw := strings.TrimSpace(r.FormValue("scheduled_at_local")); raw != "" {
		req.ScheduledAt = raw
	} else {
		req.ScheduledAt = strings.TrimSpace(r.FormValue("scheduled_at"))
	}
	return req, true, nil
}

func parseScheduledAtInput(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}

	if localParsed, err := time.ParseInLocation("2006-01-02T15:04", raw, time.Local); err == nil {
		return localParsed.UTC(), nil
	}

	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("scheduled_at must be RFC3339 or datetime-local: %w", err)
	}
	return parsed.UTC(), nil
}

func createViewURL(editID, text, scheduledAtLocal, errorMsg, successMsg string) string {
	q := url.Values{}
	q.Set("view", "create")
	if strings.TrimSpace(editID) != "" {
		q.Set("edit_id", strings.TrimSpace(editID))
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
