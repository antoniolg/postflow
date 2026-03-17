package posts

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/antoniolg/postflow/internal/domain"
)

type ScheduleListView string

const (
	ScheduleListViewPublications ScheduleListView = "publications"
	ScheduleListViewPosts        ScheduleListView = "posts"
)

var ErrInvalidScheduleListView = errors.New("view must be one of: publications, posts")

type ScheduleListStore interface {
	ListSchedule(ctx context.Context, from, to time.Time) ([]domain.Post, error)
	ListThreadPosts(ctx context.Context, rootPostID string) ([]domain.Post, error)
}

type PublicationSegment struct {
	PostID     string   `json:"post_id"`
	Position   int      `json:"position"`
	Text       string   `json:"text"`
	MediaCount int      `json:"media_count"`
	MediaIDs   []string `json:"media_ids,omitempty"`
}

type ScheduledPublication struct {
	PublicationID string               `json:"publication_id"`
	RootPostID    string               `json:"root_post_id"`
	ThreadGroupID string               `json:"thread_group_id,omitempty"`
	AccountID     string               `json:"account_id"`
	Platform      domain.Platform      `json:"platform"`
	Status        domain.PostStatus    `json:"status"`
	ScheduledAt   time.Time            `json:"scheduled_at"`
	SegmentCount  int                  `json:"segment_count"`
	MediaCount    int                  `json:"media_count"`
	HasMedia      bool                 `json:"has_media"`
	Segments      []PublicationSegment `json:"segments"`
}

type ScheduleListOutput struct {
	View         ScheduleListView
	Posts        []domain.Post
	Publications []ScheduledPublication
}

type ScheduleListService struct {
	Store ScheduleListStore
}

func ParseScheduleListView(raw string) (ScheduleListView, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(ScheduleListViewPublications):
		return ScheduleListViewPublications, nil
	case string(ScheduleListViewPosts):
		return ScheduleListViewPosts, nil
	default:
		return "", ErrInvalidScheduleListView
	}
}

func (s ScheduleListService) List(ctx context.Context, from, to time.Time, view ScheduleListView) (ScheduleListOutput, error) {
	if s.Store == nil {
		return ScheduleListOutput{}, fmt.Errorf("schedule list store is not configured")
	}
	if view != ScheduleListViewPublications && view != ScheduleListViewPosts {
		return ScheduleListOutput{}, ErrInvalidScheduleListView
	}

	items, err := s.Store.ListSchedule(ctx, from, to)
	if err != nil {
		return ScheduleListOutput{}, err
	}
	if view == ScheduleListViewPosts {
		return ScheduleListOutput{
			View:  view,
			Posts: items,
		}, nil
	}

	rootIDs := orderedRootIDs(items)
	publications := make([]ScheduledPublication, 0, len(rootIDs))
	for _, rootID := range rootIDs {
		threadPosts, err := s.Store.ListThreadPosts(ctx, rootID)
		if err != nil {
			return ScheduleListOutput{}, err
		}
		publications = append(publications, buildScheduledPublication(rootID, threadPosts))
	}

	return ScheduleListOutput{
		View:         view,
		Publications: publications,
	}, nil
}

func orderedRootIDs(posts []domain.Post) []string {
	if len(posts) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(posts))
	out := make([]string, 0, len(posts))
	for _, post := range posts {
		rootID := scheduleRootPostID(post)
		if rootID == "" {
			continue
		}
		if _, ok := seen[rootID]; ok {
			continue
		}
		seen[rootID] = struct{}{}
		out = append(out, rootID)
	}
	return out
}

func buildScheduledPublication(rootID string, threadPosts []domain.Post) ScheduledPublication {
	sorted := sortedPublicationPosts(threadPosts)
	publication := ScheduledPublication{
		PublicationID: strings.TrimSpace(rootID),
		RootPostID:    strings.TrimSpace(rootID),
		Segments:      make([]PublicationSegment, 0, len(sorted)),
	}
	if len(sorted) == 0 {
		return publication
	}

	root := sorted[0]
	publication.PublicationID = strings.TrimSpace(root.ID)
	publication.RootPostID = strings.TrimSpace(root.ID)
	publication.ThreadGroupID = strings.TrimSpace(root.ThreadGroupID)
	publication.AccountID = strings.TrimSpace(root.AccountID)
	publication.Platform = root.Platform
	publication.Status = aggregatePublicationStatus(sorted)
	publication.ScheduledAt = root.ScheduledAt
	publication.SegmentCount = len(sorted)

	for _, post := range sorted {
		mediaIDs := mediaIDsFromMedia(post.Media)
		segment := PublicationSegment{
			PostID:     strings.TrimSpace(post.ID),
			Position:   normalizedThreadPosition(post),
			Text:       strings.TrimSpace(post.Text),
			MediaCount: len(mediaIDs),
			MediaIDs:   mediaIDs,
		}
		publication.Segments = append(publication.Segments, segment)
		publication.MediaCount += segment.MediaCount
	}
	publication.HasMedia = publication.MediaCount > 0
	return publication
}

func sortedPublicationPosts(posts []domain.Post) []domain.Post {
	if len(posts) == 0 {
		return nil
	}
	out := append([]domain.Post(nil), posts...)
	sort.SliceStable(out, func(i, j int) bool {
		leftPos := normalizedThreadPosition(out[i])
		rightPos := normalizedThreadPosition(out[j])
		if leftPos != rightPos {
			return leftPos < rightPos
		}
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return strings.TrimSpace(out[i].ID) < strings.TrimSpace(out[j].ID)
	})
	return out
}

func normalizedThreadPosition(post domain.Post) int {
	if post.ThreadPosition > 0 {
		return post.ThreadPosition
	}
	return 1
}

func scheduleRootPostID(post domain.Post) string {
	if post.RootPostID != nil && strings.TrimSpace(*post.RootPostID) != "" {
		return strings.TrimSpace(*post.RootPostID)
	}
	return strings.TrimSpace(post.ID)
}

func aggregatePublicationStatus(posts []domain.Post) domain.PostStatus {
	var hasPublishing bool
	var hasPublished bool
	var hasScheduled bool
	var hasDraft bool
	for _, post := range posts {
		switch post.Status {
		case domain.PostStatusFailed:
			return domain.PostStatusFailed
		case domain.PostStatusPublishing:
			hasPublishing = true
		case domain.PostStatusPublished:
			hasPublished = true
		case domain.PostStatusScheduled:
			hasScheduled = true
		case domain.PostStatusDraft:
			hasDraft = true
		}
	}
	switch {
	case hasPublishing:
		return domain.PostStatusPublishing
	case hasPublished && (hasScheduled || hasDraft):
		return domain.PostStatusPublishing
	case hasPublished:
		return domain.PostStatusPublished
	case hasScheduled:
		return domain.PostStatusScheduled
	case hasDraft:
		return domain.PostStatusDraft
	default:
		return domain.PostStatusScheduled
	}
}

func mediaIDsFromMedia(media []domain.Media) []string {
	if len(media) == 0 {
		return nil
	}
	out := make([]string, 0, len(media))
	for _, item := range media {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		out = append(out, id)
	}
	return out
}
