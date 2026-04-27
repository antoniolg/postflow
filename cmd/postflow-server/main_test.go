package main

import (
	"net/url"
	"testing"

	"github.com/antoniolg/postflow/internal/domain"
)

func TestBuildPublicMediaURLBuilderIncludesStableExtension(t *testing.T) {
	builder := buildPublicMediaURLBuilder("https://postflow.example/")
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
	if parsed.Path != "/uploads/med_1/med_1.jpg" {
		t.Fatalf("expected public uploads media URL path with stable extension, got %q", parsed.Path)
	}
	if parsed.RawQuery != "" {
		t.Fatalf("did not expect media URL query string, got %q", parsed.RawQuery)
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
