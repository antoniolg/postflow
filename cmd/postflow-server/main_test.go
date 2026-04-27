package main

import (
	"net/url"
	"strings"
	"testing"

	"github.com/antoniolg/postflow/internal/domain"
	"github.com/antoniolg/postflow/internal/secure"
)

func TestBuildSignedMediaURLBuilderIncludesStableExtension(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	cipher, err := secure.NewCipher(key, 1)
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	builder := buildSignedMediaURLBuilder("https://postflow.example/", cipher)
	if builder == nil {
		t.Fatalf("expected media URL builder")
	}

	rawURL, err := builder(domain.Media{
		ID:           "med_1",
		OriginalName: "review.jpeg",
		MimeType:     "image/jpeg",
	})
	if err != nil {
		t.Fatalf("build media url: %v", err)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse media url: %v", err)
	}
	parts := strings.Split(parsed.Path, "/")
	if len(parts) != 7 || parts[1] != "media" || parts[2] != "med_1" || parts[3] != "content" || parts[6] != "med_1.jpg" {
		t.Fatalf("expected signed media URL path with stable extension, got %q", parsed.Path)
	}
	exp := parts[4]
	sig := parts[5]
	if exp == "" || sig == "" {
		t.Fatalf("expected signed path credentials, got %q", parsed.Path)
	}
	if parsed.RawQuery != "" {
		t.Fatalf("did not expect media URL query string, got %q", parsed.RawQuery)
	}
	if !cipher.VerifyString("med_1:"+exp, sig) {
		t.Fatalf("expected valid signature for media URL")
	}
}

func TestSignedMediaFilenameFallsBackToSafeOriginalExtension(t *testing.T) {
	got := signedMediaFilename(domain.Media{
		ID:           "med_video",
		OriginalName: "render.final.MOV",
	})
	if got != "med_video.mov" {
		t.Fatalf("unexpected signed media filename %q", got)
	}
}
