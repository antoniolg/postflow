package api

import (
	"context"
	"encoding/base64"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/antoniolg/postflow/internal/db"
	"github.com/antoniolg/postflow/internal/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpUploadMediaInput struct {
	Kind          string `json:"kind,omitempty" jsonschema:"Media kind. Defaults to video."`
	OriginalName  string `json:"original_name,omitempty" jsonschema:"Original filename (recommended for extension/mime detection)."`
	MimeType      string `json:"mime_type,omitempty" jsonschema:"Optional mime type override, e.g. image/png."`
	ContentBase64 string `json:"content_base64,omitempty" jsonschema:"Base64-encoded file content."`
}

type mcpUploadMediaOutput struct {
	MediaID      string `json:"media_id"`
	Kind         string `json:"kind"`
	OriginalName string `json:"original_name"`
	MimeType     string `json:"mime_type"`
	SizeBytes    int64  `json:"size_bytes"`
}

func (s Server) mcpUploadMediaTool(ctx context.Context, _ *mcp.CallToolRequest, in mcpUploadMediaInput) (*mcp.CallToolResult, mcpUploadMediaOutput, error) {
	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	if kind == "" {
		kind = "video"
	}

	content, originalName, err := resolveMCPUploadContent(in)
	if err != nil {
		return nil, mcpUploadMediaOutput{}, err
	}
	if len(content) == 0 {
		return nil, mcpUploadMediaOutput{}, fmt.Errorf("media content is empty")
	}

	name := sanitizeName(originalName)
	if name == "" {
		name = "upload.bin"
	}
	mediaID, err := db.NewID("med")
	if err != nil {
		return nil, mcpUploadMediaOutput{}, err
	}

	storageDir := filepath.Join(s.DataDir, "media")
	if err := os.MkdirAll(storageDir, 0o755); err != nil {
		return nil, mcpUploadMediaOutput{}, err
	}
	storagePath := filepath.Join(storageDir, mediaID+"_"+name)
	if err := os.WriteFile(storagePath, content, 0o644); err != nil {
		return nil, mcpUploadMediaOutput{}, err
	}

	mimeType := strings.TrimSpace(in.MimeType)
	if mimeType == "" {
		mimeType = mime.TypeByExtension(filepath.Ext(name))
	}
	if mimeType == "" {
		mimeType = http.DetectContentType(content)
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	created, err := s.Store.CreateMedia(ctx, domain.Media{
		ID:           mediaID,
		Kind:         kind,
		OriginalName: originalName,
		StoragePath:  storagePath,
		MimeType:     mimeType,
		SizeBytes:    int64(len(content)),
	})
	if err != nil {
		return nil, mcpUploadMediaOutput{}, err
	}

	return nil, mcpUploadMediaOutput{
		MediaID:      created.ID,
		Kind:         created.Kind,
		OriginalName: created.OriginalName,
		MimeType:     created.MimeType,
		SizeBytes:    created.SizeBytes,
	}, nil
}

func resolveMCPUploadContent(in mcpUploadMediaInput) ([]byte, string, error) {
	base64Content := strings.TrimSpace(in.ContentBase64)
	originalName := strings.TrimSpace(in.OriginalName)

	if base64Content == "" {
		return nil, "", fmt.Errorf("content_base64 is required")
	}

	decoded, err := base64.StdEncoding.DecodeString(base64Content)
	if err != nil {
		return nil, "", fmt.Errorf("decode content_base64: %w", err)
	}
	if originalName == "" {
		originalName = "upload.bin"
	}
	return decoded, originalName, nil
}
