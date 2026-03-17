package posts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/db"
	"github.com/antoniolg/postflow/internal/domain"
	"github.com/antoniolg/postflow/internal/postflow"
)

type fakeStore struct {
	accounts           map[string]domain.SocialAccount
	media              map[string]domain.Media
	createCalls        []db.CreatePostParams
	deletedIDs         []string
	createErrAt        int
	createErr          error
	createCount        int
	idempotencyPostIDs map[string]string
	postsByID          map[string]domain.Post
}

func (f *fakeStore) GetAccount(_ context.Context, id string) (domain.SocialAccount, error) {
	account, ok := f.accounts[id]
	if !ok {
		return domain.SocialAccount{}, sql.ErrNoRows
	}
	return account, nil
}

func (f *fakeStore) GetMediaByIDs(_ context.Context, ids []string) ([]domain.Media, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	out := make([]domain.Media, 0, len(ids))
	for _, id := range ids {
		item, ok := f.media[id]
		if !ok {
			return nil, fmt.Errorf("media not found: %s", id)
		}
		out = append(out, item)
	}
	return out, nil
}

func (f *fakeStore) CreatePost(_ context.Context, params db.CreatePostParams) (db.CreatePostResult, error) {
	f.createCount++
	f.createCalls = append(f.createCalls, params)
	if f.createErrAt > 0 && f.createCount == f.createErrAt {
		if f.createErr != nil {
			return db.CreatePostResult{}, f.createErr
		}
		return db.CreatePostResult{}, errors.New("create failed")
	}
	post := params.Post
	post.ID = fmt.Sprintf("pst_%d", f.createCount)
	if f.postsByID == nil {
		f.postsByID = make(map[string]domain.Post)
	}
	f.postsByID[post.ID] = post
	if trimmed := strings.TrimSpace(params.IdempotencyKey); trimmed != "" {
		if f.idempotencyPostIDs == nil {
			f.idempotencyPostIDs = make(map[string]string)
		}
		f.idempotencyPostIDs[trimmed] = post.ID
	}
	return db.CreatePostResult{
		Post:    post,
		Created: true,
	}, nil
}

func (f *fakeStore) GetPostByIdempotencyKey(_ context.Context, idempotencyKey string) (domain.Post, error) {
	if f.idempotencyPostIDs == nil {
		return domain.Post{}, sql.ErrNoRows
	}
	postID, ok := f.idempotencyPostIDs[strings.TrimSpace(idempotencyKey)]
	if !ok {
		return domain.Post{}, sql.ErrNoRows
	}
	post, ok := f.postsByID[postID]
	if !ok {
		return domain.Post{}, sql.ErrNoRows
	}
	return post, nil
}

func (f *fakeStore) ListThreadPosts(_ context.Context, rootPostID string) ([]domain.Post, error) {
	if f.postsByID == nil {
		return nil, nil
	}
	rootPostID = strings.TrimSpace(rootPostID)
	out := make([]domain.Post, 0)
	for _, post := range f.postsByID {
		if strings.TrimSpace(post.ID) == rootPostID {
			out = append(out, post)
			continue
		}
		if post.RootPostID != nil && strings.TrimSpace(*post.RootPostID) == rootPostID {
			out = append(out, post)
		}
	}
	return out, nil
}

func (f *fakeStore) DeletePostEditable(_ context.Context, id string) error {
	f.deletedIDs = append(f.deletedIDs, id)
	return nil
}

type fakeRegistry struct {
	providers map[domain.Platform]postflow.Provider
}

func (f fakeRegistry) Get(platform domain.Platform) (postflow.Provider, bool) {
	provider, ok := f.providers[platform]
	return provider, ok
}

type fakeProvider struct {
	platform    domain.Platform
	validateErr error
}

func (f fakeProvider) Platform() domain.Platform {
	return f.platform
}

func (f fakeProvider) ValidateDraft(context.Context, domain.SocialAccount, postflow.Draft) ([]string, error) {
	if f.validateErr != nil {
		return nil, f.validateErr
	}
	return nil, nil
}

func (f fakeProvider) Publish(context.Context, domain.SocialAccount, postflow.Credentials, domain.Post, postflow.PublishOptions) (postflow.PublishResult, error) {
	return postflow.PublishResult{}, nil
}

func (f fakeProvider) RefreshIfNeeded(context.Context, domain.SocialAccount, postflow.Credentials) (postflow.Credentials, bool, error) {
	return postflow.Credentials{}, false, nil
}

func TestCreateValidationErrors(t *testing.T) {
	service := CreateService{
		Store: &fakeStore{},
		Registry: fakeRegistry{
			providers: map[domain.Platform]postflow.Provider{
				domain.PlatformX: fakeProvider{platform: domain.PlatformX},
			},
		},
		DefaultMaxRetries: 3,
	}

	_, err := service.Create(t.Context(), CreateInput{
		Text: "hola",
	})
	if !errors.Is(err, ErrAccountIDRequired) {
		t.Fatalf("expected ErrAccountIDRequired, got %v", err)
	}
	if !IsValidationError(err) {
		t.Fatalf("expected validation error wrapper")
	}

	_, err = service.Create(t.Context(), CreateInput{
		AccountIDs: []string{"acc_1"},
	})
	if !errors.Is(err, ErrTextRequired) {
		t.Fatalf("expected ErrTextRequired, got %v", err)
	}

	tooLong := ""
	for len(tooLong) <= 128 {
		tooLong += "x"
	}
	_, err = service.Create(t.Context(), CreateInput{
		AccountIDs:     []string{"acc_1"},
		Text:           "hola",
		IdempotencyKey: tooLong,
	})
	if !errors.Is(err, ErrIdempotencyKeyTooLong) {
		t.Fatalf("expected ErrIdempotencyKeyTooLong, got %v", err)
	}
}

func TestCreateMultipleAccountsScopedIdempotency(t *testing.T) {
	store := &fakeStore{
		accounts: map[string]domain.SocialAccount{
			"acc_a": {ID: "acc_a", Platform: domain.PlatformX, Status: domain.AccountStatusConnected},
			"acc_b": {ID: "acc_b", Platform: domain.PlatformX, Status: domain.AccountStatusConnected},
		},
	}
	service := CreateService{
		Store: store,
		Registry: fakeRegistry{
			providers: map[domain.Platform]postflow.Provider{
				domain.PlatformX: fakeProvider{platform: domain.PlatformX},
			},
		},
		DefaultMaxRetries: 5,
	}

	out, err := service.Create(t.Context(), CreateInput{
		AccountIDs:     []string{"acc_a", "acc_b"},
		Text:           "multi account",
		ScheduledAt:    time.Now().UTC().Add(30 * time.Minute),
		IdempotencyKey: "same-key",
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(out.Items))
	}
	if out.CreatedCount != 2 {
		t.Fatalf("expected created_count=2, got %d", out.CreatedCount)
	}
	if len(store.createCalls) != 2 {
		t.Fatalf("expected 2 create calls, got %d", len(store.createCalls))
	}

	firstKey := store.createCalls[0].IdempotencyKey
	secondKey := store.createCalls[1].IdempotencyKey
	if firstKey == secondKey {
		t.Fatalf("expected scoped idempotency keys, got %q", firstKey)
	}
	if len(firstKey) > 128 || len(secondKey) > 128 {
		t.Fatalf("expected scoped idempotency keys <= 128 chars")
	}
}

func TestCreateRollbackOnSecondAccountFailure(t *testing.T) {
	store := &fakeStore{
		accounts: map[string]domain.SocialAccount{
			"acc_a": {ID: "acc_a", Platform: domain.PlatformX, Status: domain.AccountStatusConnected},
			"acc_b": {ID: "acc_b", Platform: domain.PlatformX, Status: domain.AccountStatusConnected},
		},
		createErrAt: 2,
		createErr:   errors.New("boom"),
	}
	service := CreateService{
		Store: store,
		Registry: fakeRegistry{
			providers: map[domain.Platform]postflow.Provider{
				domain.PlatformX: fakeProvider{platform: domain.PlatformX},
			},
		},
		DefaultMaxRetries: 3,
	}

	_, err := service.Create(t.Context(), CreateInput{
		AccountIDs: []string{"acc_a", "acc_b"},
		Text:       "will rollback",
	})
	if err == nil {
		t.Fatalf("expected create error")
	}
	if len(store.deletedIDs) != 1 {
		t.Fatalf("expected one rollback delete, got %d", len(store.deletedIDs))
	}
	if store.deletedIDs[0] != "pst_1" {
		t.Fatalf("expected rollback of pst_1, got %q", store.deletedIDs[0])
	}
}

func TestCreatePropagatesProviderValidationErrorsAsValidation(t *testing.T) {
	store := &fakeStore{
		accounts: map[string]domain.SocialAccount{
			"acc_1": {ID: "acc_1", Platform: domain.PlatformX, Status: domain.AccountStatusConnected},
		},
	}
	service := CreateService{
		Store: store,
		Registry: fakeRegistry{
			providers: map[domain.Platform]postflow.Provider{
				domain.PlatformX: fakeProvider{
					platform:    domain.PlatformX,
					validateErr: errors.New("post text too long"),
				},
			},
		},
		DefaultMaxRetries: 3,
	}

	_, err := service.Create(t.Context(), CreateInput{
		AccountIDs: []string{"acc_1"},
		Text:       "hola",
	})
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !IsValidationError(err) {
		t.Fatalf("expected validation error wrapper")
	}
	if !strings.Contains(err.Error(), "too long") {
		t.Fatalf("expected provider validation message, got %v", err)
	}
}

func TestCreateRejectsTooManySegments(t *testing.T) {
	store := &fakeStore{
		accounts: map[string]domain.SocialAccount{
			"acc_1": {ID: "acc_1", Platform: domain.PlatformX, Status: domain.AccountStatusConnected},
		},
	}
	service := CreateService{
		Store: store,
		Registry: fakeRegistry{
			providers: map[domain.Platform]postflow.Provider{
				domain.PlatformX: fakeProvider{platform: domain.PlatformX},
			},
		},
		DefaultMaxRetries: 3,
	}

	segments := make([]ThreadSegmentInput, 0, MaxThreadSegments+1)
	for i := 0; i < MaxThreadSegments+1; i++ {
		segments = append(segments, ThreadSegmentInput{Text: fmt.Sprintf("segment %d", i+1)})
	}

	_, err := service.Create(t.Context(), CreateInput{
		AccountIDs: []string{"acc_1"},
		Segments:   segments,
	})
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !errors.Is(err, ErrThreadTooLong) {
		t.Fatalf("expected ErrThreadTooLong, got %v", err)
	}
}
