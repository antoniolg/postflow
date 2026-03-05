package api

import (
	"sort"
	"strings"
	"time"

	"github.com/antoniolg/postflow/internal/domain"
)

type publicationGroupState struct {
	group       publicationGroupItem
	platformSet map[domain.Platform]struct{}
	accountSet  map[string]struct{}
	postSet     map[string]struct{}
}

func groupPublicationsByContent(posts []domain.Post, threadLabelFor func(domain.Post) string) []publicationGroupItem {
	if len(posts) == 0 {
		return nil
	}

	indexByKey := make(map[string]int, len(posts))
	states := make([]publicationGroupState, 0, len(posts))

	for _, post := range posts {
		threadLabel := strings.TrimSpace(threadLabelFor(post))
		groupKey := publicationGroupKey(post, threadLabel)
		idx, ok := indexByKey[groupKey]
		if !ok {
			idx = len(states)
			indexByKey[groupKey] = idx
			states = append(states, publicationGroupState{
				group: publicationGroupItem{
					PrimaryPostID:   post.ID,
					PostIDs:         make([]string, 0, 2),
					PrimaryPlatform: post.Platform,
					Platforms:       make([]domain.Platform, 0, 2),
					AccountIDs:      make([]string, 0, 2),
					PostCount:       0,
					ScheduledAt:     post.ScheduledAt,
					Text:            strings.TrimSpace(post.Text),
					ThreadLabel:     threadLabel,
					MediaCount:      len(post.Media),
				},
				platformSet: make(map[domain.Platform]struct{}, 2),
				accountSet:  make(map[string]struct{}, 2),
				postSet:     make(map[string]struct{}, 2),
			})
		}

		state := &states[idx]
		state.group.PostCount++
		postID := strings.TrimSpace(post.ID)
		if postID != "" {
			if _, exists := state.postSet[postID]; !exists {
				state.postSet[postID] = struct{}{}
				state.group.PostIDs = append(state.group.PostIDs, postID)
			}
		}
		if len(post.Media) > state.group.MediaCount {
			state.group.MediaCount = len(post.Media)
		}
		if _, exists := state.platformSet[post.Platform]; !exists {
			state.platformSet[post.Platform] = struct{}{}
			state.group.Platforms = append(state.group.Platforms, post.Platform)
		}
		accountID := strings.TrimSpace(post.AccountID)
		if accountID != "" {
			if _, exists := state.accountSet[accountID]; !exists {
				state.accountSet[accountID] = struct{}{}
				state.group.AccountIDs = append(state.group.AccountIDs, accountID)
			}
		}
	}

	groups := make([]publicationGroupItem, 0, len(states))
	for _, state := range states {
		state.group.MultiPlatform = len(state.group.Platforms) > 1
		groups = append(groups, state.group)
	}

	return groups
}

func publicationGroupKey(post domain.Post, threadLabel string) string {
	var b strings.Builder
	b.Grow(128)
	if post.ScheduledAt.IsZero() {
		b.WriteString("no-scheduled-at")
	} else {
		b.WriteString(post.ScheduledAt.UTC().Format(time.RFC3339Nano))
	}
	b.WriteByte('|')
	b.WriteString(strings.ReplaceAll(strings.TrimSpace(post.Text), "\r\n", "\n"))
	b.WriteByte('|')
	b.WriteString(threadLabel)
	b.WriteByte('|')
	b.WriteString(publicationMediaSignature(post.Media))
	return b.String()
}

func publicationMediaSignature(media []domain.Media) string {
	if len(media) == 0 {
		return "no-media"
	}
	parts := make([]string, 0, len(media))
	for _, item := range media {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			id = strings.TrimSpace(item.StoragePath)
		}
		if id == "" {
			id = strings.TrimSpace(item.OriginalName)
		}
		parts = append(parts, id)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}
