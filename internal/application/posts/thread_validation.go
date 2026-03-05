package posts

import (
	"context"

	"github.com/antoniolg/postflow/internal/domain"
	"github.com/antoniolg/postflow/internal/postflow"
)

type segmentMediaStore interface {
	GetMediaByIDs(ctx context.Context, ids []string) ([]domain.Media, error)
}

func loadThreadSegmentMediaByID(ctx context.Context, store segmentMediaStore, segments []ThreadSegmentInput) (map[string]domain.Media, error) {
	mediaIDs := uniqueSegmentMediaIDs(segments)
	if len(mediaIDs) == 0 {
		return map[string]domain.Media{}, nil
	}
	mediaItems, err := store.GetMediaByIDs(ctx, mediaIDs)
	if err != nil {
		return nil, err
	}
	mediaByID := make(map[string]domain.Media, len(mediaItems))
	for _, item := range mediaItems {
		mediaByID[item.ID] = item
	}
	return mediaByID, nil
}

func validateThreadSegmentsForAccount(ctx context.Context, store segmentMediaStore, provider postflow.Provider, account domain.SocialAccount, segments []ThreadSegmentInput) error {
	mediaByID, err := loadThreadSegmentMediaByID(ctx, store, segments)
	if err != nil {
		return err
	}
	for idx, segment := range segments {
		stepMedia, err := mediaItemsForSegment(segment.MediaIDs, mediaByID)
		if err != nil {
			return err
		}
		if idx == 0 {
			if _, err := provider.ValidateDraft(ctx, account, postflow.Draft{Text: segment.Text, Media: stepMedia}); err != nil {
				return err
			}
			continue
		}
		if err := validateFollowUpSegment(account.Platform, stepMedia); err != nil {
			return err
		}
	}
	return nil
}
