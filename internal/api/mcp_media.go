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
	"unicode"

	"github.com/antoniolg/publisher/internal/db"
	"github.com/antoniolg/publisher/internal/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpUploadMediaInput struct {
	Platform      string `json:"platform,omitempty" jsonschema:"Target platform. Only x is supported."`
	Kind          string `json:"kind,omitempty" jsonschema:"Media kind. Defaults to video."`
	OriginalName  string `json:"original_name,omitempty" jsonschema:"Original filename (recommended for extension/mime detection)."`
	MimeType      string `json:"mime_type,omitempty" jsonschema:"Optional mime type override, e.g. image/png."`
	FilePath      string `json:"file_path,omitempty" jsonschema:"Optional absolute/local path on server. Use instead of content_base64."`
	ContentBase64 string `json:"content_base64,omitempty" jsonschema:"Base64-encoded file content. Use instead of file_path."`
}

type mcpUploadMediaOutput struct {
	MediaID      string `json:"media_id"`
	Platform     string `json:"platform"`
	Kind         string `json:"kind"`
	OriginalName string `json:"original_name"`
	MimeType     string `json:"mime_type"`
	SizeBytes    int64  `json:"size_bytes"`
}

func (s Server) mcpUploadMediaTool(ctx context.Context, _ *mcp.CallToolRequest, in mcpUploadMediaInput) (*mcp.CallToolResult, mcpUploadMediaOutput, error) {
	platform := domain.Platform(strings.ToLower(strings.TrimSpace(in.Platform)))
	if platform == "" {
		platform = domain.PlatformX
	}
	if platform != domain.PlatformX {
		return nil, mcpUploadMediaOutput{}, fmt.Errorf("only platform 'x' is supported in this MVP")
	}

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
		Platform:     platform,
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
		Platform:     string(created.Platform),
		Kind:         created.Kind,
		OriginalName: created.OriginalName,
		MimeType:     created.MimeType,
		SizeBytes:    created.SizeBytes,
	}, nil
}

func resolveMCPUploadContent(in mcpUploadMediaInput) ([]byte, string, error) {
	filePath := strings.TrimSpace(in.FilePath)
	base64Content := strings.TrimSpace(in.ContentBase64)
	originalName := strings.TrimSpace(in.OriginalName)

	if filePath != "" && base64Content != "" {
		return nil, "", fmt.Errorf("provide either file_path or content_base64, not both")
	}
	if filePath == "" && base64Content == "" {
		return nil, "", fmt.Errorf("file_path or content_base64 is required")
	}

	if filePath != "" {
		content, err := os.ReadFile(filePath)
		if err != nil {
			return nil, "", fmt.Errorf("read file_path: %w", err)
		}
		if originalName == "" {
			originalName = filepath.Base(filePath)
		}
		if originalName == "" {
			originalName = "upload.bin"
		}
		return content, originalName, nil
	}

	decoded, err := decodeMCPBase64(base64Content)
	if err != nil {
		return nil, "", err
	}
	if originalName == "" {
		originalName = "upload.bin"
	}
	return decoded, originalName, nil
}

func decodeMCPBase64(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("content_base64 is empty")
	}
	if idx := strings.Index(raw, ","); idx > 0 {
		head := raw[:idx]
		if strings.Contains(strings.ToLower(head), ";base64") {
			raw = raw[idx+1:]
		}
	}
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, raw)
	if cleaned == "" {
		return nil, fmt.Errorf("content_base64 is empty")
	}
	if data, err := base64.StdEncoding.DecodeString(cleaned); err == nil {
		return data, nil
	}
	if data, err := base64.RawStdEncoding.DecodeString(cleaned); err == nil {
		return data, nil
	}
	return nil, fmt.Errorf("invalid content_base64")
}
