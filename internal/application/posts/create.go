package posts

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/antoniolg/postflow/internal/application/ports"
	"github.com/antoniolg/postflow/internal/db"
	"github.com/antoniolg/postflow/internal/domain"
)

var (
	ErrAccountIDRequired     = errors.New("account_id is required")
	ErrTextRequired          = errors.New("text is required")
	ErrThreadTooLong         = errors.New("thread has too many segments")
	ErrAccountNotFound       = errors.New("account not found")
	ErrAccountNotConnected   = errors.New("account is not connected")
	ErrProviderNotConfigured = errors.New("provider is not configured for account platform")
	ErrIdempotencyKeyTooLong = errors.New("idempotency key too long (max 128 chars)")
	ErrStoreNotConfigured    = errors.New("post create store is not configured")
	ErrRegistryNotConfigured = errors.New("post provider registry is not configured")
)

const MaxThreadSegments = 500

type Store interface {
	GetAccount(ctx context.Context, id string) (domain.SocialAccount, error)
	GetMediaByIDs(ctx context.Context, ids []string) ([]domain.Media, error)
	GetPostByIdempotencyKey(ctx context.Context, idempotencyKey string) (domain.Post, error)
	ListThreadPosts(ctx context.Context, rootPostID string) ([]domain.Post, error)
	CreatePost(ctx context.Context, params db.CreatePostParams) (db.CreatePostResult, error)
	DeletePostEditable(ctx context.Context, id string) error
}

type CreateService struct {
	Store             Store
	Registry          ports.ProviderRegistry
	DefaultMaxRetries int
}

type CreateInput struct {
	AccountIDs     []string
	Text           string
	ScheduledAt    time.Time
	MediaIDs       []string
	Segments       []ThreadSegmentInput
	MaxAttempts    int
	IdempotencyKey string
}

type ThreadSegmentInput struct {
	Text     string
	MediaIDs []string
}

type CreateItem struct {
	Post    domain.Post
	Created bool
}

type CreateOutput struct {
	Items        []CreateItem
	CreatedCount int
}

type ValidationError struct {
	Err error
}

func (e ValidationError) Error() string {
	if e.Err == nil {
		return "validation error"
	}
	return e.Err.Error()
}

func (e ValidationError) Unwrap() error {
	return e.Err
}

func IsValidationError(err error) bool {
	var target ValidationError
	return errors.As(err, &target)
}

func NormalizeAccountIDs(primary string, many []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(many)+1)
	add := func(raw string) {
		id := strings.TrimSpace(raw)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for _, id := range many {
		add(id)
	}
	add(primary)
	return out
}

func (s CreateService) Create(ctx context.Context, in CreateInput) (CreateOutput, error) {
	if s.Store == nil {
		return CreateOutput{}, ErrStoreNotConfigured
	}
	if s.Registry == nil {
		return CreateOutput{}, ErrRegistryNotConfigured
	}

	accountIDs := NormalizeAccountIDs("", in.AccountIDs)
	if len(accountIDs) == 0 {
		return CreateOutput{}, ValidationError{Err: ErrAccountIDRequired}
	}

	segments, err := normalizeSegments(in)
	if err != nil {
		return CreateOutput{}, ValidationError{Err: err}
	}

	maxAttempts := in.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = s.DefaultMaxRetries
		if maxAttempts <= 0 {
			maxAttempts = 3
		}
	}

	idempotencyKey := strings.TrimSpace(in.IdempotencyKey)
	if len(idempotencyKey) > 128 {
		return CreateOutput{}, ValidationError{Err: ErrIdempotencyKeyTooLong}
	}

	targetAccounts := make([]domain.SocialAccount, 0, len(accountIDs))
	for _, accountID := range accountIDs {
		account, err := s.Store.GetAccount(ctx, accountID)
		if err != nil {
			return CreateOutput{}, ValidationError{Err: ErrAccountNotFound}
		}
		if account.Status != domain.AccountStatusConnected {
			return CreateOutput{}, ValidationError{Err: ErrAccountNotConnected}
		}
		provider, ok := s.Registry.Get(account.Platform)
		if !ok {
			return CreateOutput{}, ValidationError{Err: ErrProviderNotConfigured}
		}
		if err := validateThreadSegmentsForAccount(ctx, s.Store, provider, account, segments); err != nil {
			return CreateOutput{}, ValidationError{Err: err}
		}
		targetAccounts = append(targetAccounts, account)
	}

	out := CreateOutput{
		Items: make([]CreateItem, 0, len(targetAccounts)),
	}
	createdIDs := make([]string, 0, len(targetAccounts))
	for _, account := range targetAccounts {
		stepOneIdem := scopeIdempotencyKeyStep(idempotencyKey, account.ID, 1)
		if stepOneIdem != "" {
			existingStepOne, err := s.Store.GetPostByIdempotencyKey(ctx, stepOneIdem)
			if err == nil {
				rootID := existingStepOne.ID
				if existingStepOne.RootPostID != nil && strings.TrimSpace(*existingStepOne.RootPostID) != "" {
					rootID = strings.TrimSpace(*existingStepOne.RootPostID)
				}
				existingThread, err := s.Store.ListThreadPosts(ctx, rootID)
				if err != nil {
					return CreateOutput{}, err
				}
				for _, post := range existingThread {
					out.Items = append(out.Items, CreateItem{Post: post, Created: false})
				}
				continue
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return CreateOutput{}, err
			}
		}

		threadGroupID, err := db.NewID("thd")
		if err != nil {
			return CreateOutput{}, err
		}
		var rootPostID string
		var previousPostID *string
		for idx, segment := range segments {
			position := idx + 1
			stepResult, err := s.Store.CreatePost(ctx, db.CreatePostParams{
				Post: domain.Post{
					AccountID:      account.ID,
					Platform:       account.Platform,
					Text:           segment.Text,
					Status:         defaultStatusForScheduledAt(in.ScheduledAt),
					ScheduledAt:    in.ScheduledAt,
					MaxAttempts:    maxAttempts,
					ThreadGroupID:  threadGroupID,
					ThreadPosition: position,
					ParentPostID:   previousPostID,
					RootPostID:     ptrIfNotEmpty(rootPostID),
				},
				MediaIDs:       segment.MediaIDs,
				IdempotencyKey: scopeIdempotencyKeyStep(idempotencyKey, account.ID, position),
			})
			if err != nil {
				if rollbackErr := s.rollbackCreatedPosts(ctx, createdIDs); rollbackErr != nil {
					return CreateOutput{}, fmt.Errorf("%w (rollback failed: %v)", err, rollbackErr)
				}
				return CreateOutput{}, err
			}
			if stepResult.Created {
				out.CreatedCount++
				createdIDs = append(createdIDs, stepResult.Post.ID)
			}
			if position == 1 {
				rootPostID = stepResult.Post.ID
			}
			previousPostID = ptrIfNotEmpty(stepResult.Post.ID)
			out.Items = append(out.Items, CreateItem{
				Post:    stepResult.Post,
				Created: stepResult.Created,
			})
		}
	}

	return out, nil
}

func normalizeMediaIDs(mediaIDs []string) []string {
	if len(mediaIDs) == 0 {
		return nil
	}
	out := make([]string, 0, len(mediaIDs))
	for _, raw := range mediaIDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		out = append(out, id)
	}
	return out
}

func normalizeSegments(in CreateInput) ([]ThreadSegmentInput, error) {
	if len(in.Segments) == 0 {
		text := strings.TrimSpace(in.Text)
		if text == "" {
			return nil, ErrTextRequired
		}
		return []ThreadSegmentInput{{
			Text:     text,
			MediaIDs: normalizeMediaIDs(in.MediaIDs),
		}}, nil
	}
	if len(in.Segments) > MaxThreadSegments {
		return nil, fmt.Errorf("%w (max %d)", ErrThreadTooLong, MaxThreadSegments)
	}
	segments := make([]ThreadSegmentInput, 0, len(in.Segments))
	for idx, raw := range in.Segments {
		text := strings.TrimSpace(raw.Text)
		if text == "" {
			return nil, fmt.Errorf("segment %d text is required", idx+1)
		}
		segments = append(segments, ThreadSegmentInput{
			Text:     text,
			MediaIDs: normalizeMediaIDs(raw.MediaIDs),
		})
	}
	return segments, nil
}

func uniqueSegmentMediaIDs(segments []ThreadSegmentInput) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, segment := range segments {
		for _, mediaID := range segment.MediaIDs {
			trimmed := strings.TrimSpace(mediaID)
			if trimmed == "" {
				continue
			}
			if _, ok := seen[trimmed]; ok {
				continue
			}
			seen[trimmed] = struct{}{}
			out = append(out, trimmed)
		}
	}
	return out
}

func mediaItemsForSegment(mediaIDs []string, mediaByID map[string]domain.Media) ([]domain.Media, error) {
	if len(mediaIDs) == 0 {
		return nil, nil
	}
	items := make([]domain.Media, 0, len(mediaIDs))
	for _, raw := range mediaIDs {
		mediaID := strings.TrimSpace(raw)
		if mediaID == "" {
			continue
		}
		media, ok := mediaByID[mediaID]
		if !ok {
			return nil, fmt.Errorf("media not found: %s", mediaID)
		}
		items = append(items, media)
	}
	return items, nil
}

func validateFollowUpSegment(platform domain.Platform, media []domain.Media) error {
	switch platform {
	case domain.PlatformX:
		if len(media) > 4 {
			return fmt.Errorf("x thread replies support up to 4 media items")
		}
		return nil
	case domain.PlatformLinkedIn:
		if len(media) > 0 {
			return fmt.Errorf("linkedin thread comments do not support media in this release")
		}
		return nil
	case domain.PlatformFacebook:
		if len(media) > 0 {
			return fmt.Errorf("facebook thread comments do not support media in this release")
		}
		return nil
	case domain.PlatformInstagram:
		if len(media) > 0 {
			return fmt.Errorf("instagram thread comments do not support media in this release")
		}
		return nil
	default:
		if len(media) > 0 {
			return fmt.Errorf("thread follow-up media is not supported for platform %s", platform)
		}
		return nil
	}
}

func defaultStatusForScheduledAt(scheduledAt time.Time) domain.PostStatus {
	if scheduledAt.IsZero() {
		return domain.PostStatusDraft
	}
	return domain.PostStatusScheduled
}

func (s CreateService) rollbackCreatedPosts(ctx context.Context, postIDs []string) error {
	rollbackErrors := make([]string, 0, len(postIDs))
	for _, postID := range postIDs {
		postID = strings.TrimSpace(postID)
		if postID == "" {
			continue
		}
		if err := s.Store.DeletePostEditable(ctx, postID); err != nil {
			rollbackErrors = append(rollbackErrors, postID+": "+err.Error())
		}
	}
	if len(rollbackErrors) > 0 {
		return errors.New(strings.Join(rollbackErrors, "; "))
	}
	return nil
}

func scopeIdempotencyKeyStep(base, accountID string, step int) string {
	base = strings.TrimSpace(base)
	accountID = strings.TrimSpace(accountID)
	if base == "" || accountID == "" || step <= 0 {
		return base
	}
	scoped := fmt.Sprintf("%s:%s:%d", base, accountID, step)
	if len(scoped) <= 128 {
		return scoped
	}
	digest := sha256.Sum256([]byte(scoped))
	suffix := fmt.Sprintf("%x", digest[:8])
	prefixLen := 128 - 1 - len(suffix)
	if prefixLen < 1 {
		return suffix
	}
	if len(base) > prefixLen {
		base = base[:prefixLen]
	}
	return base + ":" + suffix
}

func ptrIfNotEmpty(raw string) *string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
