package api

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestResolveMCPUploadContentRejectsOversizedContent(t *testing.T) {
	oldMax := maxMediaUploadBytes
	maxMediaUploadBytes = 4
	t.Cleanup(func() { maxMediaUploadBytes = oldMax })

	payload := base64.StdEncoding.EncodeToString([]byte("large"))
	_, _, err := resolveMCPUploadContent(mcpUploadMediaInput{
		OriginalName:  "large.txt",
		ContentBase64: payload,
	})
	if err == nil {
		t.Fatalf("expected oversized upload to fail")
	}
	if !strings.Contains(err.Error(), "exceeds max size") {
		t.Fatalf("expected max size error, got %v", err)
	}
}
