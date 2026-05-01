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

func TestMCPUploadMediaRejectsActiveContent(t *testing.T) {
	content := base64.StdEncoding.EncodeToString([]byte("<!doctype html><script>alert(1)</script>"))
	_, err := detectUploadedMimeType("text/html", "payload.html", []byte("<!doctype html><script>alert(1)</script>"))
	if err == nil {
		t.Fatalf("expected active content mime detection to fail")
	}

	_, _, err = resolveMCPUploadContent(mcpUploadMediaInput{
		OriginalName:  "payload.html",
		ContentBase64: content,
	})
	if err != nil {
		t.Fatalf("resolve content should only decode payload: %v", err)
	}
}
