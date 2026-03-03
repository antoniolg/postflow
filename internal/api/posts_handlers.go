package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	postsapp "github.com/antoniolg/publisher/internal/application/posts"
	"github.com/antoniolg/publisher/internal/db"
	"github.com/antoniolg/publisher/internal/domain"
)

type createPostRequest struct {
	AccountID   string   `json:"account_id"`
	AccountIDs  []string `json:"account_ids"`
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

	accountIDs := postsapp.NormalizeAccountIDs(req.AccountID, req.AccountIDs)
	if len(accountIDs) == 0 {
		if fromForm {
			http.Redirect(w, r, createViewURL("", req.Text, req.ScheduledAt, req.ReturnTo, "account_id is required", ""), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, errors.New("account_id is required"))
		return
	}

	text := strings.TrimSpace(req.Text)
	if text == "" {
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

	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	createService := postsapp.CreateService{
		Store:             s.Store,
		Registry:          s.providerRegistry(),
		DefaultMaxRetries: s.DefaultMaxRetries,
	}
	createOut, err := createService.Create(r.Context(), postsapp.CreateInput{
		AccountIDs:     accountIDs,
		Text:           text,
		ScheduledAt:    scheduledAt,
		MediaIDs:       req.MediaIDs,
		MaxAttempts:    req.MaxAttempts,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		status, message := mapCreatePostError(err, fromForm)
		if fromForm {
			http.Redirect(w, r, createViewURL("", req.Text, req.ScheduledAt, req.ReturnTo, message, ""), http.StatusSeeOther)
			return
		}
		writeError(w, status, errors.New(message))
		return
	}

	if fromForm {
		successMsg := "post updated"
		if len(createOut.Items) > 1 {
			if createOut.CreatedCount > 0 {
				successMsg = fmt.Sprintf("%d posts created", createOut.CreatedCount)
			} else {
				successMsg = "posts updated"
			}
		} else if createOut.CreatedCount > 0 {
			successMsg = "post created"
		}
		http.Redirect(w, r, createViewURL("", "", "", req.ReturnTo, "", successMsg), http.StatusSeeOther)
		return
	}

	if len(createOut.Items) == 1 {
		if createOut.Items[0].Created {
			writeJSON(w, http.StatusCreated, createOut.Items[0].Post)
			return
		}
		writeJSON(w, http.StatusOK, createOut.Items[0].Post)
		return
	}

	items := make([]domain.Post, 0, len(createOut.Items))
	for _, item := range createOut.Items {
		items = append(items, item.Post)
	}
	if createOut.CreatedCount > 0 {
		writeJSON(w, http.StatusCreated, map[string]any{
			"items":         items,
			"created_count": createOut.CreatedCount,
			"total":         len(items),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":         items,
		"created_count": createOut.CreatedCount,
		"total":         len(items),
	})
}

func mapCreatePostError(err error, fromForm bool) (int, string) {
	if errors.Is(err, postsapp.ErrIdempotencyKeyTooLong) && !fromForm {
		return http.StatusBadRequest, "Idempotency-Key too long (max 128 chars)"
	}
	if postsapp.IsValidationError(err) {
		return http.StatusBadRequest, err.Error()
	}
	return http.StatusInternalServerError, err.Error()
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

func (s Server) handleListDraftsJSON(w http.ResponseWriter, r *http.Request) {
	drafts, err := s.Store.ListDrafts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	limit := defaultMCPListLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		var parsed int
		if _, scanErr := fmt.Sscanf(raw, "%d", &parsed); scanErr != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, errors.New("limit must be a positive integer"))
			return
		}
		limit = clampMCPListLimit(parsed)
	}
	if len(drafts) > limit {
		drafts = drafts[:limit]
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"count":  len(drafts),
		"drafts": drafts,
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
	case strings.HasSuffix(r.URL.Path, "/delete"):
		s.handleDeletePost(w, r)
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
	svc := postsapp.MutationsService{Store: s.Store}
	if err := svc.Cancel(r.Context(), postID); err != nil {
		if errors.Is(err, postsapp.ErrPostIDRequired) {
			writeError(w, http.StatusBadRequest, errors.New("post id is required"))
			return
		}
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": postID, "status": string(domain.PostStatusCanceled)})
}

func (s Server) handleDeletePost(w http.ResponseWriter, r *http.Request) {
	postID, err := extractPostIDFromPath(r.URL.Path, "delete")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	fromForm := !strings.Contains(strings.ToLower(r.Header.Get("content-type")), "application/json")
	returnTo := sanitizeReturnTo(strings.TrimSpace(r.FormValue("return_to")))
	if returnTo == "" {
		returnTo = "/?view=calendar"
	}

	svc := postsapp.MutationsService{Store: s.Store}
	if err := svc.DeleteEditable(r.Context(), postID); err != nil {
		if fromForm {
			http.Redirect(w, r, withQueryValue(returnTo, "error", "post not deletable"), http.StatusSeeOther)
			return
		}
		if errors.Is(err, postsapp.ErrPostIDRequired) {
			writeError(w, http.StatusBadRequest, errors.New("post id is required"))
			return
		}
		if errors.Is(err, db.ErrPostNotDeletable) {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	if fromForm {
		http.Redirect(w, r, withQueryValue(returnTo, "success", "post deleted"), http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": postID})
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
	svc := postsapp.MutationsService{Store: s.Store}
	post, err := svc.ScheduleDraft(r.Context(), postID, scheduledAt.UTC())
	if errors.Is(err, postsapp.ErrPostIDRequired) {
		writeError(w, http.StatusBadRequest, errors.New("post id is required"))
		return
	}
	if errors.Is(err, postsapp.ErrScheduledAtNeeded) {
		writeError(w, http.StatusBadRequest, errors.New("scheduled_at is required"))
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, err)
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
	scheduledAtRaw := strings.TrimSpace(r.FormValue("scheduled_at"))
	if scheduledAtRaw == "" {
		scheduledAtRaw = strings.TrimSpace(r.FormValue("scheduled_at_local"))
	}
	if !fromForm {
		var body struct {
			Text             string `json:"text"`
			Intent           string `json:"intent"`
			ScheduledAt      string `json:"scheduled_at"`
			ScheduledAtLocal string `json:"scheduled_at_local"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			if text == "" {
				text = strings.TrimSpace(body.Text)
			}
			if intent == "" {
				intent = strings.ToLower(strings.TrimSpace(body.Intent))
			}
			if scheduledAtRaw == "" {
				scheduledAtRaw = strings.TrimSpace(body.ScheduledAt)
			}
			if scheduledAtRaw == "" {
				scheduledAtRaw = strings.TrimSpace(body.ScheduledAtLocal)
			}
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
	svc := postsapp.MutationsService{Store: s.Store}
	post, err := svc.UpdateEditable(r.Context(), postsapp.EditInput{
		PostID:      postID,
		Text:        text,
		Intent:      intent,
		ScheduledAt: scheduledAt,
	}, time.Now)
	if errors.Is(err, postsapp.ErrScheduledAtNeeded) {
		if fromForm {
			http.Redirect(w, r, createViewURL(postID, text, scheduledAtRaw, returnTo, "scheduled_at is required to schedule", ""), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, errors.New("scheduled_at is required"))
		return
	}
	if err != nil {
		if fromForm {
			http.Redirect(w, r, createViewURL(postID, text, scheduledAtRaw, returnTo, err.Error(), ""), http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusConflict, err)
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
