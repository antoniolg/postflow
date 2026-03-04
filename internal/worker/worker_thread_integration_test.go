package worker

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/db"
	"github.com/antoniolg/postflow/internal/domain"
	"github.com/antoniolg/postflow/internal/postflow"
)

func TestWorkerPublishesThreadInOrderAcrossCycles(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name            string
		platform        domain.Platform
		expectedChildOp postflow.PublishMode
	}{
		{name: "x uses reply mode for thread child", platform: domain.PlatformX, expectedChildOp: postflow.PublishModeReply},
		{name: "linkedin uses comment mode for thread child", platform: domain.PlatformLinkedIn, expectedChildOp: postflow.PublishModeComment},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := openWorkerTestStore(t)
			account := createWorkerTestAccountForPlatform(t, store, tc.platform)
			rootPost, childPost := createWorkerThreadPosts(t, store, account.ID, tc.platform)
			provider := &recordingWorkerProvider{platform: tc.platform}
			creds := workerCredentialsStore{worker: Worker{Store: store, Cipher: newWorkerTestCipher(t)}}

			runPublishCycleOnce(t, store, postflow.NewProviderRegistry(provider), creds, 5*time.Second, 2*time.Second)

			rootAfterFirstRun, err := store.GetPost(t.Context(), rootPost.ID)
			if err != nil {
				t.Fatalf("get root post after first run: %v", err)
			}
			if rootAfterFirstRun.Status != domain.PostStatusPublished {
				t.Fatalf("expected root status=published after first run, got %s", rootAfterFirstRun.Status)
			}
			if rootAfterFirstRun.ExternalID == nil || strings.TrimSpace(*rootAfterFirstRun.ExternalID) == "" {
				t.Fatalf("expected root external id after first run")
			}

			childAfterFirstRun, err := store.GetPost(t.Context(), childPost.ID)
			if err != nil {
				t.Fatalf("get child post after first run: %v", err)
			}
			if childAfterFirstRun.Status != domain.PostStatusScheduled {
				t.Fatalf("expected child to remain scheduled until next cycle, got %s", childAfterFirstRun.Status)
			}

			if len(provider.calls) != 1 {
				t.Fatalf("expected one publish call after first run, got %d", len(provider.calls))
			}
			if provider.calls[0].postID != rootPost.ID {
				t.Fatalf("expected root publish first, got post %s", provider.calls[0].postID)
			}
			if provider.calls[0].mode != postflow.PublishModeRoot {
				t.Fatalf("expected root mode for first publish, got %s", provider.calls[0].mode)
			}

			runPublishCycleOnce(t, store, postflow.NewProviderRegistry(provider), creds, 5*time.Second, 2*time.Second)

			childAfterSecondRun, err := store.GetPost(t.Context(), childPost.ID)
			if err != nil {
				t.Fatalf("get child post after second run: %v", err)
			}
			if childAfterSecondRun.Status != domain.PostStatusPublished {
				t.Fatalf("expected child status=published after second run, got %s", childAfterSecondRun.Status)
			}
			if childAfterSecondRun.ExternalID == nil || strings.TrimSpace(*childAfterSecondRun.ExternalID) == "" {
				t.Fatalf("expected child external id after second run")
			}

			if len(provider.calls) != 2 {
				t.Fatalf("expected two publish calls after second run, got %d", len(provider.calls))
			}
			second := provider.calls[1]
			if second.postID != childPost.ID {
				t.Fatalf("expected child publish second, got post %s", second.postID)
			}
			if second.mode != tc.expectedChildOp {
				t.Fatalf("expected child publish mode %s, got %s", tc.expectedChildOp, second.mode)
			}
			if second.parentExternalID != strings.TrimSpace(*rootAfterFirstRun.ExternalID) {
				t.Fatalf("expected child parent_external_id=%s, got %s", strings.TrimSpace(*rootAfterFirstRun.ExternalID), second.parentExternalID)
			}

			dlq, err := store.ListDeadLetters(t.Context(), 10)
			if err != nil {
				t.Fatalf("list dead letters: %v", err)
			}
			if len(dlq) != 0 {
				t.Fatalf("expected no dead letters for successful thread publish, got %d", len(dlq))
			}
		})
	}
}

type workerPublishCall struct {
	postID           string
	mode             postflow.PublishMode
	parentExternalID string
}

type recordingWorkerProvider struct {
	platform domain.Platform
	calls    []workerPublishCall
}

func (p *recordingWorkerProvider) Platform() domain.Platform {
	return p.platform
}

func (p *recordingWorkerProvider) ValidateDraft(context.Context, domain.SocialAccount, postflow.Draft) ([]string, error) {
	return nil, nil
}

func (p *recordingWorkerProvider) Publish(_ context.Context, _ domain.SocialAccount, _ postflow.Credentials, post domain.Post, opts postflow.PublishOptions) (string, error) {
	p.calls = append(p.calls, workerPublishCall{
		postID:           post.ID,
		mode:             opts.Mode,
		parentExternalID: strings.TrimSpace(opts.ParentExternalID),
	})
	return "ext_" + strings.TrimSpace(post.ID), nil
}

func (p *recordingWorkerProvider) RefreshIfNeeded(_ context.Context, _ domain.SocialAccount, credentials postflow.Credentials) (postflow.Credentials, bool, error) {
	return credentials, false, nil
}

func createWorkerThreadPosts(t *testing.T, store *db.Store, accountID string, platform domain.Platform) (domain.Post, domain.Post) {
	t.Helper()

	threadGroupID := "thd_" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "_")
	rootCreated, err := store.CreatePost(t.Context(), db.CreatePostParams{
		Post: domain.Post{
			AccountID:      accountID,
			Platform:       platform,
			Text:           "thread root",
			Status:         domain.PostStatusScheduled,
			ScheduledAt:    time.Now().UTC().Add(-2 * time.Minute),
			MaxAttempts:    3,
			ThreadGroupID:  threadGroupID,
			ThreadPosition: 1,
		},
	})
	if err != nil {
		t.Fatalf("create thread root: %v", err)
	}

	rootID := strings.TrimSpace(rootCreated.Post.ID)
	childCreated, err := store.CreatePost(t.Context(), db.CreatePostParams{
		Post: domain.Post{
			AccountID:      accountID,
			Platform:       platform,
			Text:           "thread child",
			Status:         domain.PostStatusScheduled,
			ScheduledAt:    time.Now().UTC().Add(-1 * time.Minute),
			MaxAttempts:    3,
			ThreadGroupID:  threadGroupID,
			ThreadPosition: 2,
			ParentPostID:   &rootID,
			RootPostID:     &rootID,
		},
	})
	if err != nil {
		t.Fatalf("create thread child: %v", err)
	}

	return rootCreated.Post, childCreated.Post
}
