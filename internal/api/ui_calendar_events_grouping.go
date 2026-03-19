package api

import (
	"strings"

	"github.com/antoniolg/postflow/internal/domain"
)

type calendarEventGroupState struct {
	event       calendarEvent
	platformSet map[domain.Platform]struct{}
}

func groupCalendarEventsByContent(posts []domain.Post, threadLabelFor func(domain.Post) string) []calendarEvent {
	if len(posts) == 0 {
		return nil
	}

	indexByKey := make(map[string]int, len(posts))
	states := make([]calendarEventGroupState, 0, len(posts))

	for _, post := range posts {
		if post.ScheduledAt.IsZero() {
			continue
		}
		statusClass, statusLabel, statusKey := calendarStatusMeta(post.Status)
		threadLabel := strings.TrimSpace(threadLabelFor(post))
		groupKey := statusKey + "|" + publicationGroupKey(post, threadLabel)
		idx, ok := indexByKey[groupKey]
		if !ok {
			idx = len(states)
			indexByKey[groupKey] = idx
			states = append(states, calendarEventGroupState{
				event: calendarEvent{
					TimeLabel:     post.ScheduledAt.Format("15:04"),
					StatusClass:   statusClass,
					StatusLabel:   statusLabel,
					StatusKey:     statusKey,
					TextPreview:   compactCalendarPreview(post.Text),
					ThreadLabel:   threadLabel,
					Platform:      post.Platform,
					Platforms:     make([]domain.Platform, 0, 2),
					PostCount:     0,
					MultiPlatform: false,
				},
				platformSet: make(map[domain.Platform]struct{}, 2),
			})
		}

		state := &states[idx]
		state.event.PostCount++
		if _, exists := state.platformSet[post.Platform]; !exists {
			state.platformSet[post.Platform] = struct{}{}
			state.event.Platforms = append(state.event.Platforms, post.Platform)
		}
	}

	out := make([]calendarEvent, 0, len(states))
	for _, state := range states {
		state.event.MultiPlatform = len(state.event.Platforms) > 1
		out = append(out, state.event)
	}
	return out
}

func compactCalendarPreview(raw string) string {
	preview := strings.Join(strings.Fields(raw), " ")
	if len(preview) > 56 {
		preview = preview[:53] + "..."
	}
	return preview
}

func calendarStatusMeta(status domain.PostStatus) (statusClass string, statusLabel string, statusKey string) {
	switch status {
	case domain.PostStatusPublished:
		return "live", "LIVE", "published"
	case domain.PostStatusScheduled:
		return "schd", "SCHD", "scheduled"
	case domain.PostStatusPublishing:
		return "prog", "PROG", "publishing"
	case domain.PostStatusFailed:
		return "fail", "FAIL", "failed"
	case domain.PostStatusCanceled:
		return "cncl", "CNCL", "canceled"
	}
	return "drft", "DRFT", "draft"
}

func calendarStatusMetaFromGroup(statusKey string) (statusClass string, statusLabel string, normalized string) {
	switch strings.TrimSpace(statusKey) {
	case "published":
		return "live", "LIVE", "published"
	case "scheduled":
		return "schd", "SCHD", "scheduled"
	case "publishing":
		return "prog", "PROG", "publishing"
	case "failed":
		return "fail", "FAIL", "failed"
	case "canceled":
		return "cncl", "CNCL", "canceled"
	default:
		return "drft", "DRFT", "draft"
	}
}
