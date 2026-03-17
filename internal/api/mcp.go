package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	dlqapp "github.com/antoniolg/postflow/internal/application/dlq"
	postsapp "github.com/antoniolg/postflow/internal/application/posts"
	"github.com/antoniolg/postflow/internal/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultMCPListLimit = 200
	maxMCPListLimit     = 500
)

func (s Server) newMCPHandler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "postflow-mcp",
		Version: s.appVersion(),
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "postflow_health",
		Description: "Health check for the PostFlow service.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, s.mcpHealthTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "postflow_list_schedule",
		Description: "List scheduled publications by default, or raw posts with view=posts. Supports RFC3339 from/to filters.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, s.mcpListScheduleTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "postflow_list_drafts",
		Description: "List draft posts (no scheduled date).",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, s.mcpListDraftsTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "postflow_list_accounts",
		Description: "List connected/registered accounts.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, s.mcpListAccountsTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "postflow_create_static_account",
		Description: "Create or update a static account and store encrypted credentials.",
		Annotations: &mcp.ToolAnnotations{
			IdempotentHint: false,
		},
	}, s.mcpCreateStaticAccountTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "postflow_connect_account",
		Description: "Mark account as connected if it has saved credentials.",
		Annotations: &mcp.ToolAnnotations{
			IdempotentHint: false,
		},
	}, s.mcpConnectAccountTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "postflow_disconnect_account",
		Description: "Mark account as disconnected.",
		Annotations: &mcp.ToolAnnotations{
			IdempotentHint: false,
		},
	}, s.mcpDisconnectAccountTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "postflow_set_x_premium",
		Description: "Set X premium flag for an X account.",
		Annotations: &mcp.ToolAnnotations{
			IdempotentHint: false,
		},
	}, s.mcpSetXPremiumTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "postflow_delete_account",
		Description: "Delete a disconnected account with no linked posts.",
		Annotations: &mcp.ToolAnnotations{
			IdempotentHint: false,
		},
	}, s.mcpDeleteAccountTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "postflow_list_failed",
		Description: "List failed posts from dead letters with latest error details.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, s.mcpListFailedTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "postflow_create_post",
		Description: "Create a post or thread as draft (no scheduled_at) or scheduled (with scheduled_at).",
		Annotations: &mcp.ToolAnnotations{
			IdempotentHint: false,
		},
	}, s.mcpCreatePostTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "postflow_cancel_post",
		Description: "Cancel a scheduled post.",
		Annotations: &mcp.ToolAnnotations{
			IdempotentHint: false,
		},
	}, s.mcpCancelPostTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "postflow_schedule_post",
		Description: "Schedule a draft post by ID.",
		Annotations: &mcp.ToolAnnotations{
			IdempotentHint: false,
		},
	}, s.mcpSchedulePostTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "postflow_edit_post",
		Description: "Edit a single post or replace an editable thread via segments, plus optional intent/scheduled date.",
		Annotations: &mcp.ToolAnnotations{
			IdempotentHint: false,
		},
	}, s.mcpEditPostTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "postflow_delete_post",
		Description: "Delete an editable post (draft/scheduled/failed/canceled).",
		Annotations: &mcp.ToolAnnotations{
			IdempotentHint: false,
		},
	}, s.mcpDeletePostTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "postflow_validate_post",
		Description: "Validate a post or thread payload without creating it.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, s.mcpValidatePostTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "postflow_upload_media",
		Description: "Upload media and return media_id. Provide content_base64.",
		Annotations: &mcp.ToolAnnotations{
			IdempotentHint: false,
		},
	}, s.mcpUploadMediaTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "postflow_list_media",
		Description: "List uploaded media with usage status (in use or deletable).",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, s.mcpListMediaTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "postflow_delete_media",
		Description: "Delete an uploaded media item if it is not attached to any post.",
		Annotations: &mcp.ToolAnnotations{
			IdempotentHint: false,
		},
	}, s.mcpDeleteMediaTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "postflow_requeue_failed",
		Description: "Requeue one failed dead-letter post back to scheduled.",
		Annotations: &mcp.ToolAnnotations{
			IdempotentHint: false,
		},
	}, s.mcpRequeueFailedTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "postflow_delete_failed",
		Description: "Delete one failed dead-letter post entry.",
		Annotations: &mcp.ToolAnnotations{
			IdempotentHint: false,
		},
	}, s.mcpDeleteFailedTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "postflow_set_timezone",
		Description: "Set UI timezone (IANA name, e.g. Europe/Madrid).",
		Annotations: &mcp.ToolAnnotations{
			IdempotentHint: false,
		},
	}, s.mcpSetTimezoneTool)

	base := mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{
		// Run MCP transport in stateless mode to avoid in-memory session affinity
		// requirements behind load balancers/proxies.
		Stateless: true,
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && !mcpAcceptHeaderSupportsJSONAndSSE(r.Header.Values("Accept")) {
			r2 := r.Clone(r.Context())
			r2.Header = r.Header.Clone()
			r2.Header.Set("Accept", "application/json, text/event-stream")
			r = r2
		}
		base.ServeHTTP(w, r)
	})
}

func mcpAcceptHeaderSupportsJSONAndSSE(values []string) bool {
	if len(values) == 0 {
		return false
	}

	var jsonOK bool
	var streamOK bool
	for _, headerValue := range values {
		for _, part := range strings.Split(headerValue, ",") {
			token := strings.ToLower(strings.TrimSpace(part))
			if token == "" {
				continue
			}
			token = strings.SplitN(token, ";", 2)[0]
			switch token {
			case "application/json", "application/*":
				jsonOK = true
			case "text/event-stream", "text/*":
				streamOK = true
			case "*/*":
				jsonOK = true
				streamOK = true
			}
		}
	}
	return jsonOK && streamOK
}

type mcpListScheduleInput struct {
	From  string `json:"from,omitempty" jsonschema:"RFC3339 start date filter (optional)."`
	To    string `json:"to,omitempty" jsonschema:"RFC3339 end date filter (optional)."`
	Limit int    `json:"limit,omitempty" jsonschema:"Max items to return (1-500). Default: 200."`
	View  string `json:"view,omitempty" jsonschema:"Optional view: publications (default) or posts."`
}

type mcpListDraftsInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"Max items to return (1-500). Default: 200."`
}

type mcpListFailedInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"Max items to return (1-500). Default: 200."`
}

type mcpCreatePostInput struct {
	AccountID      string                  `json:"account_id" jsonschema:"Target connected account ID."`
	Text           string                  `json:"text" jsonschema:"Post text content."`
	ScheduledAt    string                  `json:"scheduled_at,omitempty" jsonschema:"RFC3339 or datetime-local value. Empty means draft."`
	MediaIDs       []string                `json:"media_ids,omitempty" jsonschema:"Existing media IDs to attach."`
	Segments       []mcpThreadSegmentInput `json:"segments,omitempty" jsonschema:"Optional thread segments [{text, media_ids}] where first segment is the root post."`
	MaxAttempts    int                     `json:"max_attempts,omitempty" jsonschema:"Max publish retries. Default from server config."`
	IdempotencyKey string                  `json:"idempotency_key,omitempty" jsonschema:"Optional idempotency key (max 128 chars)."`
}

type mcpValidatePostInput struct {
	AccountID   string                  `json:"account_id" jsonschema:"Target connected account ID."`
	Text        string                  `json:"text" jsonschema:"Post text content."`
	ScheduledAt string                  `json:"scheduled_at,omitempty" jsonschema:"RFC3339 value. Empty means draft."`
	MediaIDs    []string                `json:"media_ids,omitempty" jsonschema:"Existing media IDs to validate."`
	Segments    []mcpThreadSegmentInput `json:"segments,omitempty" jsonschema:"Optional thread segments [{text, media_ids}] where first segment is the root post."`
	MaxAttempts int                     `json:"max_attempts,omitempty" jsonschema:"Max publish retries. Default from server config."`
}

type mcpThreadSegmentInput struct {
	Text     string   `json:"text"`
	MediaIDs []string `json:"media_ids,omitempty"`
}

type mcpPostSummary struct {
	ID             string `json:"id"`
	AccountID      string `json:"account_id"`
	Platform       string `json:"platform"`
	Status         string `json:"status"`
	Text           string `json:"text"`
	ScheduledAt    string `json:"scheduled_at,omitempty"`
	PublishedAt    string `json:"published_at,omitempty"`
	UpdatedAt      string `json:"updated_at"`
	MediaCount     int    `json:"media_count"`
	Attempts       int    `json:"attempts"`
	MaxAttempts    int    `json:"max_attempts"`
	ThreadGroupID  string `json:"thread_group_id,omitempty"`
	ThreadPosition int    `json:"thread_position,omitempty"`
	ParentPostID   string `json:"parent_post_id,omitempty"`
	RootPostID     string `json:"root_post_id,omitempty"`
}

type mcpListScheduleOutput struct {
	Count int                       `json:"count"`
	From  string                    `json:"from"`
	To    string                    `json:"to"`
	View  string                    `json:"view"`
	Items []mcpScheduledPublication `json:"items,omitempty"`
	Posts []mcpPostSummary          `json:"posts,omitempty"`
}

type mcpScheduledPublication struct {
	PublicationID string                        `json:"publication_id"`
	RootPostID    string                        `json:"root_post_id"`
	ThreadGroupID string                        `json:"thread_group_id,omitempty"`
	AccountID     string                        `json:"account_id"`
	Platform      string                        `json:"platform"`
	Status        string                        `json:"status"`
	ScheduledAt   string                        `json:"scheduled_at,omitempty"`
	SegmentCount  int                           `json:"segment_count"`
	MediaCount    int                           `json:"media_count"`
	HasMedia      bool                          `json:"has_media"`
	Segments      []mcpScheduledPublicationStep `json:"segments"`
}

type mcpScheduledPublicationStep struct {
	PostID     string   `json:"post_id"`
	Position   int      `json:"position"`
	Text       string   `json:"text"`
	MediaCount int      `json:"media_count"`
	MediaIDs   []string `json:"media_ids,omitempty"`
}

type mcpListDraftsOutput struct {
	Count  int              `json:"count"`
	Drafts []mcpPostSummary `json:"drafts"`
}

type mcpFailedSummary struct {
	DeadLetterID string `json:"dead_letter_id"`
	PostID       string `json:"post_id"`
	Status       string `json:"status"`
	Text         string `json:"text"`
	LastError    string `json:"last_error"`
	Reason       string `json:"reason"`
	Attempts     int    `json:"attempts"`
	MaxAttempts  int    `json:"max_attempts"`
	ScheduledAt  string `json:"scheduled_at,omitempty"`
	FailedAt     string `json:"failed_at"`
}

type mcpListFailedOutput struct {
	Count int                `json:"count"`
	Items []mcpFailedSummary `json:"items"`
}

type mcpCreatePostOutput struct {
	Created    bool             `json:"created"`
	Post       mcpPostSummary   `json:"post"`
	Items      []mcpPostSummary `json:"items,omitempty"`
	RootID     string           `json:"root_id,omitempty"`
	TotalSteps int              `json:"total_steps,omitempty"`
}

type mcpValidatePostOutput struct {
	Valid      bool           `json:"valid"`
	Normalized normalizedPost `json:"normalized"`
	Warnings   []string       `json:"warnings"`
}

type mcpFailedMutationInput struct {
	DeadLetterID string `json:"dead_letter_id" jsonschema:"Dead letter ID."`
}

type mcpRequeueFailedOutput struct {
	DeadLetterID string         `json:"dead_letter_id"`
	Post         mcpPostSummary `json:"post"`
}

type mcpDeleteFailedOutput struct {
	DeadLetterID string `json:"dead_letter_id"`
	Deleted      bool   `json:"deleted"`
}

func (s Server) mcpListScheduleTool(ctx context.Context, _ *mcp.CallToolRequest, in mcpListScheduleInput) (*mcp.CallToolResult, mcpListScheduleOutput, error) {
	from, to, err := parseRange(ctx, strings.TrimSpace(in.From), strings.TrimSpace(in.To))
	if err != nil {
		return nil, mcpListScheduleOutput{}, err
	}
	view, err := postsapp.ParseScheduleListView(in.View)
	if err != nil {
		return nil, mcpListScheduleOutput{}, err
	}
	svc := postsapp.ScheduleListService{Store: s.Store}
	listOut, err := svc.List(ctx, from, to, view)
	if err != nil {
		return nil, mcpListScheduleOutput{}, err
	}

	limit := clampMCPListLimit(in.Limit)

	out := mcpListScheduleOutput{
		View: string(view),
		From: from.UTC().Format(time.RFC3339),
		To:   to.UTC().Format(time.RFC3339),
	}
	if view == postsapp.ScheduleListViewPosts {
		items := listOut.Posts
		if len(items) > limit {
			items = items[:limit]
		}
		out.Count = len(items)
		out.Posts = make([]mcpPostSummary, 0, len(items))
		for _, item := range items {
			out.Posts = append(out.Posts, toMCPPostSummary(item))
		}
		return nil, out, nil
	}

	items := listOut.Publications
	if len(items) > limit {
		items = items[:limit]
	}
	out.Count = len(items)
	out.Items = make([]mcpScheduledPublication, 0, len(items))
	for _, item := range items {
		out.Items = append(out.Items, toMCPScheduledPublication(item))
	}
	return nil, out, nil
}

func (s Server) mcpListDraftsTool(ctx context.Context, _ *mcp.CallToolRequest, in mcpListDraftsInput) (*mcp.CallToolResult, mcpListDraftsOutput, error) {
	drafts, err := s.Store.ListDrafts(ctx)
	if err != nil {
		return nil, mcpListDraftsOutput{}, err
	}

	limit := clampMCPListLimit(in.Limit)
	if len(drafts) > limit {
		drafts = drafts[:limit]
	}

	out := mcpListDraftsOutput{
		Count:  len(drafts),
		Drafts: make([]mcpPostSummary, 0, len(drafts)),
	}
	for _, item := range drafts {
		out.Drafts = append(out.Drafts, toMCPPostSummary(item))
	}
	return nil, out, nil
}

func (s Server) mcpListFailedTool(ctx context.Context, _ *mcp.CallToolRequest, in mcpListFailedInput) (*mcp.CallToolResult, mcpListFailedOutput, error) {
	limit := clampMCPListLimit(in.Limit)
	deadLetters, err := s.Store.ListDeadLetters(ctx, limit)
	if err != nil {
		return nil, mcpListFailedOutput{}, err
	}

	out := mcpListFailedOutput{
		Items: make([]mcpFailedSummary, 0, len(deadLetters)),
	}
	for _, item := range deadLetters {
		post, err := s.Store.GetPost(ctx, item.PostID)
		if err != nil {
			continue
		}
		out.Items = append(out.Items, mcpFailedSummary{
			DeadLetterID: item.ID,
			PostID:       post.ID,
			Status:       string(post.Status),
			Text:         strings.TrimSpace(post.Text),
			LastError:    strings.TrimSpace(item.LastError),
			Reason:       strings.TrimSpace(item.Reason),
			Attempts:     post.Attempts,
			MaxAttempts:  post.MaxAttempts,
			ScheduledAt:  formatMCPTime(post.ScheduledAt),
			FailedAt:     formatMCPTime(item.AttemptedAt),
		})
	}
	out.Count = len(out.Items)
	return nil, out, nil
}

func (s Server) mcpCreatePostTool(ctx context.Context, _ *mcp.CallToolRequest, in mcpCreatePostInput) (*mcp.CallToolResult, mcpCreatePostOutput, error) {
	if strings.TrimSpace(in.AccountID) == "" {
		return nil, mcpCreatePostOutput{}, fmt.Errorf("account_id is required")
	}

	text := strings.TrimSpace(in.Text)
	segments := normalizeMCPThreadSegments(in.Segments)
	if len(segments) > 0 {
		text = strings.TrimSpace(segments[0].Text)
	}
	if text == "" && len(segments) == 0 {
		return nil, mcpCreatePostOutput{}, fmt.Errorf("text is required")
	}

	uiLoc, _, _, err := s.resolveUILocation(ctx)
	if err != nil {
		return nil, mcpCreatePostOutput{}, err
	}
	scheduledAt, err := parseScheduledAtInputInLocation(strings.TrimSpace(in.ScheduledAt), uiLoc)
	if err != nil {
		return nil, mcpCreatePostOutput{}, err
	}

	mediaIDs := cleanMCPMediaIDs(in.MediaIDs)

	createService := postsapp.CreateService{
		Store:             s.Store,
		Registry:          s.providerRegistry(),
		DefaultMaxRetries: s.DefaultMaxRetries,
	}
	createOut, err := createService.Create(ctx, postsapp.CreateInput{
		AccountIDs:     []string{in.AccountID},
		Text:           text,
		ScheduledAt:    scheduledAt,
		MediaIDs:       mediaIDs,
		Segments:       toAppSegments(segments),
		MaxAttempts:    in.MaxAttempts,
		IdempotencyKey: strings.TrimSpace(in.IdempotencyKey),
	})
	if err != nil {
		return nil, mcpCreatePostOutput{}, err
	}
	if len(createOut.Items) != 1 && len(segments) == 0 {
		return nil, mcpCreatePostOutput{}, fmt.Errorf("expected single create result, got %d", len(createOut.Items))
	}
	item := createOut.Items[0]
	if len(segments) > 0 {
		for _, candidate := range createOut.Items {
			if candidate.Post.ThreadPosition == 1 {
				item = candidate
				break
			}
		}
	}

	out := mcpCreatePostOutput{
		Created: item.Created,
		Post:    toMCPPostSummary(item.Post),
	}
	if len(createOut.Items) > 1 {
		out.Items = make([]mcpPostSummary, 0, len(createOut.Items))
		for _, createdItem := range createOut.Items {
			out.Items = append(out.Items, toMCPPostSummary(createdItem.Post))
		}
	}
	if len(segments) > 1 {
		out.RootID = strings.TrimSpace(item.Post.ID)
		out.TotalSteps = len(createOut.Items)
	}
	return nil, out, nil
}

func (s Server) mcpValidatePostTool(ctx context.Context, _ *mcp.CallToolRequest, in mcpValidatePostInput) (*mcp.CallToolResult, mcpValidatePostOutput, error) {
	segments := normalizeMCPThreadSegments(in.Segments)
	out, err := s.validatePost(ctx, validatePostInput{
		AccountID:   strings.TrimSpace(in.AccountID),
		Text:        strings.TrimSpace(in.Text),
		ScheduledAt: strings.TrimSpace(in.ScheduledAt),
		MediaIDs:    cleanMCPMediaIDs(in.MediaIDs),
		Segments:    segments,
		MaxAttempts: in.MaxAttempts,
	})
	if err != nil {
		return nil, mcpValidatePostOutput{}, err
	}
	return nil, mcpValidatePostOutput{
		Valid:      out.Valid,
		Normalized: out.Normalized,
		Warnings:   out.Warnings,
	}, nil
}

func normalizeMCPThreadSegments(raw []mcpThreadSegmentInput) []createPostSegment {
	if len(raw) == 0 {
		return nil
	}
	out := make([]createPostSegment, 0, len(raw))
	for _, segment := range raw {
		out = append(out, createPostSegment{
			Text:     strings.TrimSpace(segment.Text),
			MediaIDs: cleanMCPMediaIDs(segment.MediaIDs),
		})
	}
	return normalizeRequestSegments(out)
}

func (s Server) mcpRequeueFailedTool(ctx context.Context, _ *mcp.CallToolRequest, in mcpFailedMutationInput) (*mcp.CallToolResult, mcpRequeueFailedOutput, error) {
	deadLetterID := strings.TrimSpace(in.DeadLetterID)
	if deadLetterID == "" {
		return nil, mcpRequeueFailedOutput{}, errors.New("dead_letter_id is required")
	}
	svc := dlqapp.Service{Store: s.Store}
	post, err := svc.Requeue(ctx, deadLetterID)
	if err != nil {
		return nil, mcpRequeueFailedOutput{}, err
	}
	return nil, mcpRequeueFailedOutput{
		DeadLetterID: deadLetterID,
		Post:         toMCPPostSummary(post),
	}, nil
}

func (s Server) mcpDeleteFailedTool(ctx context.Context, _ *mcp.CallToolRequest, in mcpFailedMutationInput) (*mcp.CallToolResult, mcpDeleteFailedOutput, error) {
	deadLetterID := strings.TrimSpace(in.DeadLetterID)
	if deadLetterID == "" {
		return nil, mcpDeleteFailedOutput{}, errors.New("dead_letter_id is required")
	}
	svc := dlqapp.Service{Store: s.Store}
	if err := svc.Delete(ctx, deadLetterID); err != nil {
		return nil, mcpDeleteFailedOutput{}, err
	}
	return nil, mcpDeleteFailedOutput{
		DeadLetterID: deadLetterID,
		Deleted:      true,
	}, nil
}

func toMCPPostSummary(post domain.Post) mcpPostSummary {
	parentPostID := ""
	if post.ParentPostID != nil {
		parentPostID = strings.TrimSpace(*post.ParentPostID)
	}
	rootPostID := ""
	if post.RootPostID != nil {
		rootPostID = strings.TrimSpace(*post.RootPostID)
	}
	return mcpPostSummary{
		ID:             post.ID,
		AccountID:      post.AccountID,
		Platform:       string(post.Platform),
		Status:         string(post.Status),
		Text:           strings.TrimSpace(post.Text),
		ScheduledAt:    formatMCPTime(post.ScheduledAt),
		PublishedAt:    formatMCPTimePtr(post.PublishedAt),
		UpdatedAt:      formatMCPTime(post.UpdatedAt),
		MediaCount:     len(post.Media),
		Attempts:       post.Attempts,
		MaxAttempts:    post.MaxAttempts,
		ThreadGroupID:  strings.TrimSpace(post.ThreadGroupID),
		ThreadPosition: post.ThreadPosition,
		ParentPostID:   parentPostID,
		RootPostID:     rootPostID,
	}
}

func toMCPScheduledPublication(item postsapp.ScheduledPublication) mcpScheduledPublication {
	out := mcpScheduledPublication{
		PublicationID: strings.TrimSpace(item.PublicationID),
		RootPostID:    strings.TrimSpace(item.RootPostID),
		ThreadGroupID: strings.TrimSpace(item.ThreadGroupID),
		AccountID:     strings.TrimSpace(item.AccountID),
		Platform:      string(item.Platform),
		Status:        string(item.Status),
		ScheduledAt:   formatMCPTime(item.ScheduledAt),
		SegmentCount:  item.SegmentCount,
		MediaCount:    item.MediaCount,
		HasMedia:      item.HasMedia,
		Segments:      make([]mcpScheduledPublicationStep, 0, len(item.Segments)),
	}
	for _, segment := range item.Segments {
		out.Segments = append(out.Segments, mcpScheduledPublicationStep{
			PostID:     strings.TrimSpace(segment.PostID),
			Position:   segment.Position,
			Text:       strings.TrimSpace(segment.Text),
			MediaCount: segment.MediaCount,
			MediaIDs:   append([]string(nil), segment.MediaIDs...),
		})
	}
	return out
}

func formatMCPTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func formatMCPTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return formatMCPTime(*t)
}

func clampMCPListLimit(limit int) int {
	if limit <= 0 {
		return defaultMCPListLimit
	}
	if limit > maxMCPListLimit {
		return maxMCPListLimit
	}
	return limit
}

func cleanMCPMediaIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func (s Server) mcpSettingsInfo(r *http.Request) (url string, authHint string, configJSON string, claudeCommand string, codexCommand string, codexConfigTOML string) {
	url = requestBaseURL(r) + "/mcp"
	authHeader := ""

	apiTokenConfigured := strings.TrimSpace(s.APIToken) != ""
	basicConfigured := strings.TrimSpace(s.UIBasicUser) != "" || strings.TrimSpace(s.UIBasicPass) != ""
	switch {
	case apiTokenConfigured:
		authHint = "auth: usa Authorization: Bearer <API_TOKEN> (recomendado)."
		authHeader = "Bearer <API_TOKEN>"
	case basicConfigured:
		authHint = "auth: endpoint protegido con Basic Auth. Usa Authorization: Basic <BASE64_USER_PASS>."
		authHeader = "Basic <BASE64_USER_PASS>"
	default:
		authHint = "auth: endpoint abierto. Recomendado configurar API_TOKEN."
	}

	claudeCommand = fmt.Sprintf("claude mcp add -t http postflow %s", url)
	if authHeader != "" {
		claudeCommand = fmt.Sprintf(`%s --header "Authorization: %s"`, claudeCommand, authHeader)
	}
	codexCommand = fmt.Sprintf("codex mcp add postflow --url %s", url)

	serverCfg := map[string]any{
		"transport": "streamable_http",
		"url":       url,
	}
	if authHeader != "" {
		serverCfg["headers"] = map[string]string{
			"Authorization": authHeader,
		}
	}
	raw, err := json.MarshalIndent(map[string]any{
		"mcpServers": map[string]any{
			"postflow": serverCfg,
		},
	}, "", "  ")
	if err != nil {
		configJSON = `{"mcpServers":{"postflow":{"transport":"streamable_http","url":"` + url + `"}}}`
	} else {
		configJSON = string(raw)
	}

	if apiTokenConfigured {
		codexConfigTOML = strings.TrimSpace(fmt.Sprintf(`
[mcp_servers.postflow]
url = %q
bearer_token_env_var = "POSTFLOW_API_TOKEN"
`, url))
	} else {
		codexConfigTOML = strings.TrimSpace(fmt.Sprintf(`
[mcp_servers.postflow]
url = %q
`, url))
	}

	return url, authHint, configJSON, claudeCommand, codexCommand, codexConfigTOML
}

func requestBaseURL(r *http.Request) string {
	if r == nil {
		return "http://localhost:8080"
	}

	scheme := "http"
	if proto := firstCSVHeaderValue(r.Header.Get("X-Forwarded-Proto")); proto != "" {
		switch strings.ToLower(proto) {
		case "http", "https":
			scheme = strings.ToLower(proto)
		}
	} else if r.TLS != nil {
		scheme = "https"
	}

	host := firstCSVHeaderValue(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if host == "" {
		host = "localhost:8080"
	}

	return scheme + "://" + host
}

func firstCSVHeaderValue(raw string) string {
	if raw == "" {
		return ""
	}
	parts := strings.Split(raw, ",")
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[0])
}
