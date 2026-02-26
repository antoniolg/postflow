package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpListMediaInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"Max items to return (1-500). Default: 200."`
}

type mcpMediaSummary struct {
	MediaID      string `json:"media_id"`
	Platform     string `json:"platform"`
	Kind         string `json:"kind"`
	OriginalName string `json:"original_name"`
	MimeType     string `json:"mime_type"`
	SizeBytes    int64  `json:"size_bytes"`
	CreatedAt    string `json:"created_at,omitempty"`
	UsageCount   int    `json:"usage_count"`
	InUse        bool   `json:"in_use"`
	PreviewURL   string `json:"preview_url,omitempty"`
}

type mcpListMediaOutput struct {
	Count int               `json:"count"`
	Items []mcpMediaSummary `json:"items"`
}

type mcpDeleteMediaInput struct {
	MediaID string `json:"media_id" jsonschema:"Media ID to delete. Must not be in use by any post."`
}

type mcpDeleteMediaOutput struct {
	Deleted bool            `json:"deleted"`
	Media   mcpMediaSummary `json:"media"`
}

func toMCPMediaSummary(item mediaListItem) mcpMediaSummary {
	return mcpMediaSummary{
		MediaID:      item.ID,
		Platform:     item.Platform,
		Kind:         item.Kind,
		OriginalName: item.OriginalName,
		MimeType:     item.MimeType,
		SizeBytes:    item.SizeBytes,
		CreatedAt:    item.CreatedAt,
		UsageCount:   item.UsageCount,
		InUse:        item.InUse,
		PreviewURL:   item.PreviewURL,
	}
}

func (s Server) mcpListMediaTool(ctx context.Context, _ *mcp.CallToolRequest, in mcpListMediaInput) (*mcp.CallToolResult, mcpListMediaOutput, error) {
	items, err := s.listMediaItems(ctx, in.Limit, time.UTC)
	if err != nil {
		return nil, mcpListMediaOutput{}, err
	}
	out := mcpListMediaOutput{
		Count: len(items),
		Items: make([]mcpMediaSummary, 0, len(items)),
	}
	for _, item := range items {
		out.Items = append(out.Items, toMCPMediaSummary(item))
	}
	return nil, out, nil
}

func (s Server) mcpDeleteMediaTool(ctx context.Context, _ *mcp.CallToolRequest, in mcpDeleteMediaInput) (*mcp.CallToolResult, mcpDeleteMediaOutput, error) {
	mediaID := strings.TrimSpace(in.MediaID)
	if mediaID == "" {
		return nil, mcpDeleteMediaOutput{}, fmt.Errorf("media_id is required")
	}
	deleted, err := s.deleteMediaByID(ctx, mediaID, time.UTC)
	if err != nil {
		return nil, mcpDeleteMediaOutput{}, errors.New(mediaDeleteErrorMessage(err))
	}
	return nil, mcpDeleteMediaOutput{
		Deleted: true,
		Media:   toMCPMediaSummary(deleted),
	}, nil
}
