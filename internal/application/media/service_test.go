package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/antoniolg/publisher/internal/db"
	"github.com/antoniolg/publisher/internal/domain"
)

type fakeStore struct {
	lastListLimit int
	listItems     []db.MediaWithUsage
	deleteInput   string
	deleteOutput  domain.Media
	deleteErr     error
}

func (f *fakeStore) ListMedia(_ context.Context, limit int) ([]db.MediaWithUsage, error) {
	f.lastListLimit = limit
	return f.listItems, nil
}

func (f *fakeStore) DeleteMediaIfUnused(_ context.Context, mediaID string) (domain.Media, error) {
	f.deleteInput = mediaID
	if f.deleteErr != nil {
		return domain.Media{}, f.deleteErr
	}
	return f.deleteOutput, nil
}

func TestClampListLimit(t *testing.T) {
	if got := ClampListLimit(0); got != DefaultListLimit {
		t.Fatalf("expected default limit, got %d", got)
	}
	if got := ClampListLimit(-10); got != DefaultListLimit {
		t.Fatalf("expected default limit for negative value, got %d", got)
	}
	if got := ClampListLimit(MaxListLimit + 10); got != MaxListLimit {
		t.Fatalf("expected max limit cap, got %d", got)
	}
	if got := ClampListLimit(123); got != 123 {
		t.Fatalf("expected explicit limit to be preserved, got %d", got)
	}
}

func TestListUsesClampedLimit(t *testing.T) {
	store := &fakeStore{}
	svc := Service{Store: store}

	if _, err := svc.List(t.Context(), MaxListLimit+1000); err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if store.lastListLimit != MaxListLimit {
		t.Fatalf("expected clamped limit %d, got %d", MaxListLimit, store.lastListLimit)
	}
}

func TestDeleteValidatesInput(t *testing.T) {
	svc := Service{Store: &fakeStore{}}
	if _, err := svc.Delete(t.Context(), "   "); !errors.Is(err, ErrMediaIDRequired) {
		t.Fatalf("expected ErrMediaIDRequired, got %v", err)
	}
}

func TestDeleteRemovesFileWhenPresent(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "clip.mp4")
	if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	store := &fakeStore{
		deleteOutput: domain.Media{
			ID:          "med_1",
			StoragePath: path,
		},
	}
	svc := Service{Store: store}

	deleted, err := svc.Delete(t.Context(), "med_1")
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if deleted.ID != "med_1" {
		t.Fatalf("unexpected deleted media: %+v", deleted)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected file to be removed, stat err=%v", statErr)
	}
}

func TestDeleteIgnoresMissingFile(t *testing.T) {
	store := &fakeStore{
		deleteOutput: domain.Media{
			ID:          "med_2",
			StoragePath: filepath.Join(t.TempDir(), "missing.png"),
		},
	}
	svc := Service{Store: store}

	if _, err := svc.Delete(t.Context(), "med_2"); err != nil {
		t.Fatalf("delete failed when file missing: %v", err)
	}
}
