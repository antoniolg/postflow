package main

import (
	"net/url"
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
	if parsed.Path != "/media/med_1/content/med_1.jpg" {
		t.Fatalf("expected media URL path to include stable extension, got %q", parsed.Path)
	}
	exp := parsed.Query().Get("exp")
	sig := parsed.Query().Get("sig")
	if exp == "" || sig == "" {
		t.Fatalf("expected signed query params, got %q", parsed.RawQuery)
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
