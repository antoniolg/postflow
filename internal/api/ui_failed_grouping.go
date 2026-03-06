package api

import (
	"strings"
	"time"

	"github.com/antoniolg/postflow/internal/domain"
)

type failedPostEnvelope struct {
	Post         domain.Post
	DeadLetterID string
	FailedAt     time.Time
	LastError    string
}

func groupFailedPosts(items []failedPostEnvelope, threadLabelFor func(domain.Post) string, uiLoc *time.Location, noDateLabel string) []publicationGroupItem {
	if len(items) == 0 {
		return nil
	}
	indexByKey := make(map[string]int, len(items))
	out := make([]publicationGroupItem, 0, len(items))
	platformSets := make([]map[domain.Platform]struct{}, 0, len(items))
	accountSets := make([]map[string]struct{}, 0, len(items))

	for _, item := range items {
		threadLabel := strings.TrimSpace(threadLabelFor(item.Post))
		key := publicationGroupKey(item.Post, threadLabel)
		idx, exists := indexByKey[key]
		if !exists {
			idx = len(out)
			indexByKey[key] = idx
			scheduledAtLabel := strings.TrimSpace(noDateLabel)
			if scheduledAtLabel == "" {
				scheduledAtLabel = "no date"
			}
			if !item.Post.ScheduledAt.IsZero() {
				localTime := item.Post.ScheduledAt
				if uiLoc != nil {
					localTime = localTime.In(uiLoc)
				}
				scheduledAtLabel = localTime.Format("2006-01-02 15:04 MST")
			}
			failedAt := item.FailedAt
			if uiLoc != nil {
				failedAt = failedAt.In(uiLoc)
			}
			out = append(out, publicationGroupItem{
				PrimaryPostID:    item.Post.ID,
				DeadLetterIDs:    make([]string, 0, 2),
				Platforms:        make([]domain.Platform, 0, 2),
				AccountIDs:       make([]string, 0, 2),
				PostCount:        0,
				ScheduledAtLabel: scheduledAtLabel,
				Text:             strings.TrimSpace(item.Post.Text),
				ThreadLabel:      threadLabel,
				MediaCount:       len(item.Post.Media),
				Attempts:         item.Post.Attempts,
				MaxAttempts:      item.Post.MaxAttempts,
				FailedAtLabel:    failedAt.Format("2006-01-02 15:04 MST"),
				LastError:        strings.TrimSpace(item.LastError),
			})
			platformSets = append(platformSets, make(map[domain.Platform]struct{}, 2))
			accountSets = append(accountSets, make(map[string]struct{}, 2))
		}

		group := &out[idx]
		group.PostCount++
		if len(item.Post.Media) > group.MediaCount {
			group.MediaCount = len(item.Post.Media)
		}
		if item.Post.Attempts > group.Attempts {
			group.Attempts = item.Post.Attempts
		}
		if item.Post.MaxAttempts > group.MaxAttempts {
			group.MaxAttempts = item.Post.MaxAttempts
		}
		if group.LastError == "" {
			group.LastError = strings.TrimSpace(item.LastError)
		}
		group.DeadLetterIDs = append(group.DeadLetterIDs, item.DeadLetterID)

		if _, ok := platformSets[idx][item.Post.Platform]; !ok {
			platformSets[idx][item.Post.Platform] = struct{}{}
			group.Platforms = append(group.Platforms, item.Post.Platform)
		}

		accountID := strings.TrimSpace(item.Post.AccountID)
		if accountID != "" {
			if _, ok := accountSets[idx][accountID]; !ok {
				accountSets[idx][accountID] = struct{}{}
				group.AccountIDs = append(group.AccountIDs, accountID)
			}
		}
	}

	for i := range out {
		out[i].DeadLetterIDsCSV = strings.Join(out[i].DeadLetterIDs, ",")
	}
	return out
}
