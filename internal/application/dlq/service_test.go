package dlq

import (
	"context"
	"errors"
	"testing"

	"github.com/antoniolg/postflow/internal/domain"
)

type fakeStore struct {
	lastListLimit int
	requeueErrIDs map[string]error
	deleteErrIDs  map[string]error
	requeuedIDs   []string
	deletedIDs    []string
}

func (f *fakeStore) ListDeadLetters(_ context.Context, limit int) ([]domain.DeadLetter, error) {
	f.lastListLimit = limit
	return nil, nil
}

func (f *fakeStore) RequeueDeadLetter(_ context.Context, deadLetterID string) (domain.Post, error) {
	f.requeuedIDs = append(f.requeuedIDs, deadLetterID)
	if err := f.requeueErrIDs[deadLetterID]; err != nil {
		return domain.Post{}, err
	}
	return domain.Post{ID: "pst_" + deadLetterID}, nil
}

func (f *fakeStore) DeleteDeadLetter(_ context.Context, deadLetterID string) error {
	f.deletedIDs = append(f.deletedIDs, deadLetterID)
	if err := f.deleteErrIDs[deadLetterID]; err != nil {
		return err
	}
	return nil
}

func TestNormalizeIDs(t *testing.T) {
	got := NormalizeIDs([]string{" dlq_1 ", "", "dlq_2", "dlq_1", "  "})
	if len(got) != 2 {
		t.Fatalf("expected 2 ids, got %d (%v)", len(got), got)
	}
	if got[0] != "dlq_1" || got[1] != "dlq_2" {
		t.Fatalf("unexpected normalized ids: %v", got)
	}
}

func TestListUsesClampedLimit(t *testing.T) {
	store := &fakeStore{}
	svc := Service{Store: store}
	if _, err := svc.List(t.Context(), MaxListLimit+10); err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if store.lastListLimit != MaxListLimit {
		t.Fatalf("expected clamped limit %d, got %d", MaxListLimit, store.lastListLimit)
	}
}

func TestRequeueAndDeleteValidateID(t *testing.T) {
	svc := Service{Store: &fakeStore{}}
	if _, err := svc.Requeue(t.Context(), " "); !errors.Is(err, ErrDeadLetterIDRequired) {
		t.Fatalf("expected ErrDeadLetterIDRequired on requeue, got %v", err)
	}
	if err := svc.Delete(t.Context(), " "); !errors.Is(err, ErrDeadLetterIDRequired) {
		t.Fatalf("expected ErrDeadLetterIDRequired on delete, got %v", err)
	}
}

func TestBulkRequeueCountsSuccessAndFailures(t *testing.T) {
	store := &fakeStore{
		requeueErrIDs: map[string]error{
			"dlq_2": errors.New("boom"),
		},
	}
	svc := Service{Store: store}
	result, err := svc.BulkRequeue(t.Context(), []string{"dlq_1", "dlq_2", "dlq_1"})
	if err != nil {
		t.Fatalf("bulk requeue failed: %v", err)
	}
	if result.Selected != 2 || result.Success != 1 || result.Failed != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestBulkDeleteCountsSuccessAndFailures(t *testing.T) {
	store := &fakeStore{
		deleteErrIDs: map[string]error{
			"dlq_1": errors.New("cannot delete"),
		},
	}
	svc := Service{Store: store}
	result, err := svc.BulkDelete(t.Context(), []string{"dlq_1", "dlq_2", "dlq_2"})
	if err != nil {
		t.Fatalf("bulk delete failed: %v", err)
	}
	if result.Selected != 2 || result.Success != 1 || result.Failed != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestBulkOpsRequireIDs(t *testing.T) {
	svc := Service{Store: &fakeStore{}}
	if _, err := svc.BulkRequeue(t.Context(), []string{" ", ""}); !errors.Is(err, ErrIDsRequired) {
		t.Fatalf("expected ErrIDsRequired for bulk requeue, got %v", err)
	}
	if _, err := svc.BulkDelete(t.Context(), []string{}); !errors.Is(err, ErrIDsRequired) {
		t.Fatalf("expected ErrIDsRequired for bulk delete, got %v", err)
	}
}
