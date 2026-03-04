package dlq

import (
	"context"
	"errors"
	"strings"

	"github.com/antoniolg/postflow/internal/domain"
)

const (
	DefaultListLimit = 100
	MaxListLimit     = 500
)

var (
	ErrDeadLetterIDRequired = errors.New("invalid dead letter id")
	ErrIDsRequired          = errors.New("ids are required")
)

type Store interface {
	ListDeadLetters(ctx context.Context, limit int) ([]domain.DeadLetter, error)
	RequeueDeadLetter(ctx context.Context, deadLetterID string) (domain.Post, error)
	DeleteDeadLetter(ctx context.Context, deadLetterID string) error
}

type Service struct {
	Store Store
}

type BulkResult struct {
	Selected int
	Success  int
	Failed   int
}

func ClampListLimit(limit int) int {
	if limit <= 0 {
		return DefaultListLimit
	}
	if limit > MaxListLimit {
		return MaxListLimit
	}
	return limit
}

func NormalizeIDs(ids []string) []string {
	cleaned := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		cleaned = append(cleaned, id)
	}
	return cleaned
}

func (s Service) List(ctx context.Context, limit int) ([]domain.DeadLetter, error) {
	return s.Store.ListDeadLetters(ctx, ClampListLimit(limit))
}

func (s Service) Requeue(ctx context.Context, deadLetterID string) (domain.Post, error) {
	deadLetterID = strings.TrimSpace(deadLetterID)
	if deadLetterID == "" {
		return domain.Post{}, ErrDeadLetterIDRequired
	}
	return s.Store.RequeueDeadLetter(ctx, deadLetterID)
}

func (s Service) Delete(ctx context.Context, deadLetterID string) error {
	deadLetterID = strings.TrimSpace(deadLetterID)
	if deadLetterID == "" {
		return ErrDeadLetterIDRequired
	}
	return s.Store.DeleteDeadLetter(ctx, deadLetterID)
}

func (s Service) BulkRequeue(ctx context.Context, ids []string) (BulkResult, error) {
	cleaned := NormalizeIDs(ids)
	if len(cleaned) == 0 {
		return BulkResult{}, ErrIDsRequired
	}
	result := BulkResult{Selected: len(cleaned)}
	for _, id := range cleaned {
		if _, err := s.Store.RequeueDeadLetter(ctx, id); err != nil {
			result.Failed++
			continue
		}
		result.Success++
	}
	return result, nil
}

func (s Service) BulkDelete(ctx context.Context, ids []string) (BulkResult, error) {
	cleaned := NormalizeIDs(ids)
	if len(cleaned) == 0 {
		return BulkResult{}, ErrIDsRequired
	}
	result := BulkResult{Selected: len(cleaned)}
	for _, id := range cleaned {
		if err := s.Store.DeleteDeadLetter(ctx, id); err != nil {
			result.Failed++
			continue
		}
		result.Success++
	}
	return result, nil
}
