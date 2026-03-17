package posts

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/domain"
)

type scheduleListStoreStub struct {
	scheduled []domain.Post
	threads   map[string][]domain.Post
}

func (s scheduleListStoreStub) ListSchedule(context.Context, time.Time, time.Time) ([]domain.Post, error) {
	return append([]domain.Post(nil), s.scheduled...), nil
}

func (s scheduleListStoreStub) ListThreadPosts(_ context.Context, rootPostID string) ([]domain.Post, error) {
	items, ok := s.threads[rootPostID]
	if !ok {
		return nil, errors.New("thread not found")
	}
	return append([]domain.Post(nil), items...), nil
}

func TestParseScheduleListView(t *testing.T) {
	view, err := ParseScheduleListView("")
	if err != nil {
		t.Fatalf("parse empty view: %v", err)
	}
	if view != ScheduleListViewPublications {
		t.Fatalf("expected default publications, got %q", view)
	}

	view, err = ParseScheduleListView("posts")
	if err != nil {
		t.Fatalf("parse posts view: %v", err)
	}
	if view != ScheduleListViewPosts {
		t.Fatalf("expected posts, got %q", view)
	}

	if _, err := ParseScheduleListView("campaign"); !errors.Is(err, ErrInvalidScheduleListView) {
		t.Fatalf("expected invalid view error, got %v", err)
	}
}

func TestScheduleListServiceGroupsByThreadRootPerPlatform(t *testing.T) {
	scheduledAt := time.Date(2026, 3, 19, 16, 15, 0, 0, time.UTC)
	linkedInRoot := "pst_linkedin_root"
	instagramRoot := "pst_instagram_root"
	facebookRoot := "pst_facebook_root"

	store := scheduleListStoreStub{
		scheduled: []domain.Post{
			{ID: linkedInRoot, AccountID: "acc_li", Platform: domain.PlatformLinkedIn, Text: "hello", Status: domain.PostStatusScheduled, ScheduledAt: scheduledAt, ThreadGroupID: "thd_li", ThreadPosition: 1},
			{ID: "pst_linkedin_reply", AccountID: "acc_li", Platform: domain.PlatformLinkedIn, Text: "reply", Status: domain.PostStatusScheduled, ScheduledAt: scheduledAt, ThreadGroupID: "thd_li", ThreadPosition: 2, RootPostID: ptr("pst_linkedin_root")},
			{ID: instagramRoot, AccountID: "acc_ig", Platform: domain.PlatformInstagram, Text: "hello", Status: domain.PostStatusScheduled, ScheduledAt: scheduledAt, ThreadGroupID: "thd_ig", ThreadPosition: 1},
			{ID: "pst_instagram_reply", AccountID: "acc_ig", Platform: domain.PlatformInstagram, Text: "reply", Status: domain.PostStatusScheduled, ScheduledAt: scheduledAt, ThreadGroupID: "thd_ig", ThreadPosition: 2, RootPostID: ptr("pst_instagram_root")},
			{ID: facebookRoot, AccountID: "acc_fb", Platform: domain.PlatformFacebook, Text: "hello", Status: domain.PostStatusScheduled, ScheduledAt: scheduledAt, ThreadGroupID: "thd_fb", ThreadPosition: 1},
			{ID: "pst_facebook_reply", AccountID: "acc_fb", Platform: domain.PlatformFacebook, Text: "reply", Status: domain.PostStatusScheduled, ScheduledAt: scheduledAt, ThreadGroupID: "thd_fb", ThreadPosition: 2, RootPostID: ptr("pst_facebook_root")},
		},
		threads: map[string][]domain.Post{
			linkedInRoot: {
				{ID: linkedInRoot, AccountID: "acc_li", Platform: domain.PlatformLinkedIn, Text: "hello", Status: domain.PostStatusScheduled, ScheduledAt: scheduledAt, ThreadGroupID: "thd_li", ThreadPosition: 1, Media: []domain.Media{{ID: "med_li"}}},
				{ID: "pst_linkedin_reply", AccountID: "acc_li", Platform: domain.PlatformLinkedIn, Text: "reply", Status: domain.PostStatusScheduled, ScheduledAt: scheduledAt, ThreadGroupID: "thd_li", ThreadPosition: 2, RootPostID: ptr(linkedInRoot)},
			},
			instagramRoot: {
				{ID: instagramRoot, AccountID: "acc_ig", Platform: domain.PlatformInstagram, Text: "hello", Status: domain.PostStatusScheduled, ScheduledAt: scheduledAt, ThreadGroupID: "thd_ig", ThreadPosition: 1},
				{ID: "pst_instagram_reply", AccountID: "acc_ig", Platform: domain.PlatformInstagram, Text: "reply", Status: domain.PostStatusScheduled, ScheduledAt: scheduledAt, ThreadGroupID: "thd_ig", ThreadPosition: 2, RootPostID: ptr(instagramRoot)},
			},
			facebookRoot: {
				{ID: facebookRoot, AccountID: "acc_fb", Platform: domain.PlatformFacebook, Text: "hello", Status: domain.PostStatusScheduled, ScheduledAt: scheduledAt, ThreadGroupID: "thd_fb", ThreadPosition: 1},
				{ID: "pst_facebook_reply", AccountID: "acc_fb", Platform: domain.PlatformFacebook, Text: "reply", Status: domain.PostStatusScheduled, ScheduledAt: scheduledAt, ThreadGroupID: "thd_fb", ThreadPosition: 2, RootPostID: ptr(facebookRoot)},
			},
		},
	}

	service := ScheduleListService{Store: store}
	out, err := service.List(t.Context(), scheduledAt.Add(-time.Hour), scheduledAt.Add(time.Hour), ScheduleListViewPublications)
	if err != nil {
		t.Fatalf("list publications: %v", err)
	}
	if len(out.Publications) != 3 {
		t.Fatalf("expected 3 publications, got %d", len(out.Publications))
	}

	first := out.Publications[0]
	if first.Platform != domain.PlatformLinkedIn {
		t.Fatalf("expected first platform linkedin, got %s", first.Platform)
	}
	if first.PublicationID != linkedInRoot || first.RootPostID != linkedInRoot {
		t.Fatalf("expected linkedin root identifiers, got publication_id=%q root_post_id=%q", first.PublicationID, first.RootPostID)
	}
	if first.SegmentCount != 2 {
		t.Fatalf("expected 2 segments, got %d", first.SegmentCount)
	}
	if !first.HasMedia || first.MediaCount != 1 {
		t.Fatalf("expected media on first publication, got has_media=%t media_count=%d", first.HasMedia, first.MediaCount)
	}
	if len(first.Segments) != 2 || len(first.Segments[0].MediaIDs) != 1 || len(first.Segments[1].MediaIDs) != 0 {
		t.Fatalf("unexpected segment media reconstruction: %#v", first.Segments)
	}
}

func TestScheduleListServicePostsViewKeepsFlatPosts(t *testing.T) {
	scheduledAt := time.Date(2026, 3, 19, 16, 15, 0, 0, time.UTC)
	items := []domain.Post{
		{ID: "pst_root", AccountID: "acc_1", Platform: domain.PlatformX, Text: "root", Status: domain.PostStatusScheduled, ScheduledAt: scheduledAt, ThreadGroupID: "thd_1", ThreadPosition: 1},
		{ID: "pst_reply", AccountID: "acc_1", Platform: domain.PlatformX, Text: "reply", Status: domain.PostStatusScheduled, ScheduledAt: scheduledAt, ThreadGroupID: "thd_1", ThreadPosition: 2, ParentPostID: ptr("pst_root"), RootPostID: ptr("pst_root")},
	}
	service := ScheduleListService{
		Store: scheduleListStoreStub{
			scheduled: items,
			threads: map[string][]domain.Post{
				"pst_root": items,
			},
		},
	}

	out, err := service.List(t.Context(), scheduledAt.Add(-time.Hour), scheduledAt.Add(time.Hour), ScheduleListViewPosts)
	if err != nil {
		t.Fatalf("list posts: %v", err)
	}
	if len(out.Posts) != 2 {
		t.Fatalf("expected 2 posts, got %d", len(out.Posts))
	}
	if out.Posts[1].ThreadPosition != 2 {
		t.Fatalf("expected raw thread metadata to be preserved, got %+v", out.Posts[1])
	}
}

func ptr(value string) *string {
	return &value
}
