package api

import (
	"context"
	"errors"
	"strings"
	"time"

	postsapp "github.com/antoniolg/postflow/internal/application/posts"
	"github.com/antoniolg/postflow/internal/db"
	"github.com/antoniolg/postflow/internal/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpCancelPostInput struct {
	PostID string `json:"post_id" jsonschema:"Scheduled post ID."`
}

type mcpCancelPostOutput struct {
	PostID   string `json:"post_id"`
	Status   string `json:"status"`
	Canceled bool   `json:"canceled"`
}

type mcpSchedulePostInput struct {
	PostID      string `json:"post_id" jsonschema:"Draft post ID."`
	ScheduledAt string `json:"scheduled_at" jsonschema:"RFC3339 or datetime-local value in UI timezone."`
}

type mcpSchedulePostOutput struct {
	PostID string         `json:"post_id"`
	Status string         `json:"status"`
	Post   mcpPostSummary `json:"post"`
}

type mcpEditPostInput struct {
	PostID      string                  `json:"post_id" jsonschema:"Editable post ID."`
	PostIDs     []string                `json:"post_ids,omitempty" jsonschema:"Optional additional editable post IDs to update with the same edit."`
	Text        string                  `json:"text" jsonschema:"Updated post text content for single-post edits."`
	Intent      string                  `json:"intent,omitempty" jsonschema:"Optional intent: draft|schedule|publish_now."`
	ScheduledAt string                  `json:"scheduled_at,omitempty" jsonschema:"Optional RFC3339 or datetime-local value. If omitted with empty intent, current scheduling is preserved."`
	MediaIDs    []string                `json:"media_ids,omitempty" jsonschema:"Optional replacement media IDs for single-post edits. Pass [] to remove all media."`
	Segments    []mcpThreadSegmentInput `json:"segments,omitempty" jsonschema:"Optional thread segments [{text, media_ids}] where the first segment is the root post."`
}

type mcpEditPostOutput struct {
	PostID string           `json:"post_id"`
	Status string           `json:"status"`
	Post   mcpPostSummary   `json:"post"`
	Count  int              `json:"count,omitempty"`
	Posts  []mcpPostSummary `json:"posts,omitempty"`
}

type mcpDeletePostInput struct {
	PostID string `json:"post_id" jsonschema:"Editable post ID."`
}

type mcpDeletePostOutput struct {
	PostID  string `json:"post_id"`
	Deleted bool   `json:"deleted"`
}

func (s Server) mcpCancelPostTool(ctx context.Context, _ *mcp.CallToolRequest, in mcpCancelPostInput) (*mcp.CallToolResult, mcpCancelPostOutput, error) {
	postID := strings.TrimSpace(in.PostID)
	if postID == "" {
		return nil, mcpCancelPostOutput{}, errors.New("post_id is required")
	}
	svc := postsapp.MutationsService{Store: s.Store}
	if err := svc.Cancel(ctx, postID); err != nil {
		if errors.Is(err, postsapp.ErrPostIDRequired) {
			return nil, mcpCancelPostOutput{}, errors.New("post id is required")
		}
		return nil, mcpCancelPostOutput{}, err
	}
	return nil, mcpCancelPostOutput{
		PostID:   postID,
		Status:   string(domain.PostStatusCanceled),
		Canceled: true,
	}, nil
}

func (s Server) mcpSchedulePostTool(ctx context.Context, _ *mcp.CallToolRequest, in mcpSchedulePostInput) (*mcp.CallToolResult, mcpSchedulePostOutput, error) {
	postID := strings.TrimSpace(in.PostID)
	if postID == "" {
		return nil, mcpSchedulePostOutput{}, errors.New("post_id is required")
	}

	uiLoc, _, _, err := s.resolveUILocation(ctx)
	if err != nil {
		return nil, mcpSchedulePostOutput{}, err
	}
	scheduledAt, err := parseScheduledAtInputInLocation(strings.TrimSpace(in.ScheduledAt), uiLoc)
	if err != nil {
		return nil, mcpSchedulePostOutput{}, err
	}

	svc := postsapp.MutationsService{Store: s.Store}
	post, err := svc.ScheduleDraft(ctx, postID, scheduledAt)
	if err != nil {
		switch {
		case errors.Is(err, postsapp.ErrPostIDRequired):
			return nil, mcpSchedulePostOutput{}, errors.New("post id is required")
		case errors.Is(err, postsapp.ErrScheduledAtNeeded):
			return nil, mcpSchedulePostOutput{}, errors.New("scheduled_at is required")
		default:
			return nil, mcpSchedulePostOutput{}, err
		}
	}
	return nil, mcpSchedulePostOutput{
		PostID: postID,
		Status: string(post.Status),
		Post:   toMCPPostSummary(post),
	}, nil
}

func (s Server) mcpEditPostTool(ctx context.Context, _ *mcp.CallToolRequest, in mcpEditPostInput) (*mcp.CallToolResult, mcpEditPostOutput, error) {
	postID := strings.TrimSpace(in.PostID)
	if postID == "" {
		return nil, mcpEditPostOutput{}, errors.New("post_id is required")
	}
	text := strings.TrimSpace(in.Text)
	segments := normalizeMCPThreadSegments(in.Segments)
	if len(segments) > 0 {
		text = strings.TrimSpace(segments[0].Text)
	}
	if text == "" && len(segments) == 0 {
		return nil, mcpEditPostOutput{}, errors.New("text is required")
	}

	uiLoc, _, _, err := s.resolveUILocation(ctx)
	if err != nil {
		return nil, mcpEditPostOutput{}, err
	}
	scheduledAt, err := parseScheduledAtInputInLocation(strings.TrimSpace(in.ScheduledAt), uiLoc)
	if err != nil {
		return nil, mcpEditPostOutput{}, err
	}

	svc := postsapp.MutationsService{
		Store:    s.Store,
		Registry: s.providerRegistry(),
	}
	mediaIDs := cleanMCPMediaIDs(in.MediaIDs)
	posts, err := svc.UpdateEditableMany(ctx, postsapp.EditInput{
		PostID:       postID,
		PostIDs:      in.PostIDs,
		Text:         text,
		Intent:       strings.ToLower(strings.TrimSpace(in.Intent)),
		ScheduledAt:  scheduledAt,
		MediaIDs:     mediaIDs,
		ReplaceMedia: in.MediaIDs != nil,
		Segments:     toAppSegments(segments),
	}, time.Now)
	if err != nil {
		switch {
		case errors.Is(err, postsapp.ErrPostIDRequired):
			return nil, mcpEditPostOutput{}, errors.New("post id is required")
		case errors.Is(err, postsapp.ErrTextRequired):
			return nil, mcpEditPostOutput{}, errors.New("text is required")
		case errors.Is(err, postsapp.ErrScheduledAtNeeded):
			return nil, mcpEditPostOutput{}, errors.New("scheduled_at is required")
		default:
			return nil, mcpEditPostOutput{}, err
		}
	}
	post := posts[0]
	postSummaries := make([]mcpPostSummary, 0, len(posts))
	for _, item := range posts {
		postSummaries = append(postSummaries, toMCPPostSummary(item))
	}

	return nil, mcpEditPostOutput{
		PostID: postID,
		Status: string(post.Status),
		Post:   toMCPPostSummary(post),
		Count:  len(postSummaries),
		Posts:  postSummaries,
	}, nil
}

func (s Server) mcpDeletePostTool(ctx context.Context, _ *mcp.CallToolRequest, in mcpDeletePostInput) (*mcp.CallToolResult, mcpDeletePostOutput, error) {
	postID := strings.TrimSpace(in.PostID)
	if postID == "" {
		return nil, mcpDeletePostOutput{}, errors.New("post_id is required")
	}
	svc := postsapp.MutationsService{Store: s.Store}
	if err := svc.DeleteEditable(ctx, postID); err != nil {
		switch {
		case errors.Is(err, postsapp.ErrPostIDRequired):
			return nil, mcpDeletePostOutput{}, errors.New("post id is required")
		case errors.Is(err, db.ErrPostNotDeletable):
			return nil, mcpDeletePostOutput{}, errors.New("post not deletable")
		default:
			return nil, mcpDeletePostOutput{}, err
		}
	}
	return nil, mcpDeletePostOutput{
		PostID:  postID,
		Deleted: true,
	}, nil
}
