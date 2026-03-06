package api

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/antoniolg/postflow/internal/domain"
)

type logicalThreadState struct {
	group           publicationGroupItem
	platformSet     map[domain.Platform]struct{}
	accountSet      map[string]struct{}
	postSet         map[string]struct{}
	deadLetterSet   map[string]struct{}
	totalPosts      int
	publishedPosts  int
	publishingPosts int
	failedPosts     int
	scheduledPosts  int
	draftPosts      int
	stepStatus      map[int]*logicalThreadStepStatus
}

type logicalThreadStepStatus struct {
	total     int
	published int
}

func groupPublicationThreads(
	threads [][]domain.Post,
	failedByPostID map[string]failedPostEnvelope,
	uiLoc *time.Location,
	stepCountLabel func(int) string,
	stepProgressLabel func(int, int) string,
	noDateLabel string,
) []publicationGroupItem {
	if len(threads) == 0 {
		return nil
	}

	indexByKey := make(map[string]int, len(threads))
	states := make([]logicalThreadState, 0, len(threads))

	for _, rawThread := range threads {
		thread := sortedThreadPosts(rawThread)
		if len(thread) == 0 {
			continue
		}
		signature := logicalPublicationSignature(thread)
		idx, ok := indexByKey[signature]
		if !ok {
			root := thread[0]
			scheduledAtLabel := strings.TrimSpace(noDateLabel)
			if scheduledAtLabel == "" {
				scheduledAtLabel = "no date"
			}
			if !root.ScheduledAt.IsZero() {
				localTime := root.ScheduledAt
				if uiLoc != nil {
					localTime = localTime.In(uiLoc)
				}
				scheduledAtLabel = localTime.Format("2006-01-02 15:04 MST")
			}
			followUps := buildFollowUpStepPreviews(thread)
			mediaCount := 0
			for _, post := range thread {
				mediaCount += len(post.Media)
			}
			idx = len(states)
			indexByKey[signature] = idx
			states = append(states, logicalThreadState{
				group: publicationGroupItem{
					PrimaryPostID:    strings.TrimSpace(root.ID),
					PostIDs:          make([]string, 0, len(thread)),
					PrimaryPlatform:  root.Platform,
					Platforms:        make([]domain.Platform, 0, 4),
					AccountIDs:       make([]string, 0, 4),
					PostCount:        0,
					ScheduledAt:      root.ScheduledAt,
					ScheduledAtLabel: scheduledAtLabel,
					Text:             strings.TrimSpace(root.Text),
					ThreadLabel:      stepCountLabel(len(thread)),
					StepCount:        len(thread),
					FollowUpSteps:    followUps,
					MediaCount:       mediaCount,
				},
				platformSet:   make(map[domain.Platform]struct{}, 4),
				accountSet:    make(map[string]struct{}, 4),
				postSet:       make(map[string]struct{}, len(thread)),
				deadLetterSet: make(map[string]struct{}, len(thread)),
				stepStatus:    make(map[int]*logicalThreadStepStatus, len(thread)),
			})
		}

		state := &states[idx]
		state.group.PostCount++
		for _, post := range thread {
			postID := strings.TrimSpace(post.ID)
			if postID != "" {
				if _, exists := state.postSet[postID]; !exists {
					state.postSet[postID] = struct{}{}
					state.group.PostIDs = append(state.group.PostIDs, postID)
				}
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

			state.totalPosts++
			switch post.Status {
			case domain.PostStatusPublished:
				state.publishedPosts++
			case domain.PostStatusPublishing:
				state.publishingPosts++
			case domain.PostStatusFailed:
				state.failedPosts++
			case domain.PostStatusScheduled:
				state.scheduledPosts++
			case domain.PostStatusDraft:
				state.draftPosts++
			}

			position := post.ThreadPosition
			if position <= 0 {
				position = 1
			}
			step := state.stepStatus[position]
			if step == nil {
				step = &logicalThreadStepStatus{}
				state.stepStatus[position] = step
			}
			step.total++
			if post.Status == domain.PostStatusPublished {
				step.published++
			}

			if failed, ok := failedByPostID[postID]; ok {
				if failed.DeadLetterID != "" {
					if _, exists := state.deadLetterSet[failed.DeadLetterID]; !exists {
						state.deadLetterSet[failed.DeadLetterID] = struct{}{}
						state.group.DeadLetterIDs = append(state.group.DeadLetterIDs, failed.DeadLetterID)
					}
				}
				if strings.TrimSpace(state.group.LastError) == "" {
					state.group.LastError = strings.TrimSpace(failed.LastError)
				}
				if failedAt := failed.FailedAt; !failedAt.IsZero() {
					if uiLoc != nil {
						failedAt = failedAt.In(uiLoc)
					}
					if state.group.FailedAtLabel == "" {
						state.group.FailedAtLabel = failedAt.Format("2006-01-02 15:04 MST")
					}
				}
				if post.Attempts > state.group.Attempts {
					state.group.Attempts = post.Attempts
				}
				if post.MaxAttempts > state.group.MaxAttempts {
					state.group.MaxAttempts = post.MaxAttempts
				}
			}
		}
	}

	out := make([]publicationGroupItem, 0, len(states))
	for _, state := range states {
		group := state.group
		group.MultiPlatform = len(group.Platforms) > 1
		group.DeadLetterIDsCSV = strings.Join(group.DeadLetterIDs, ",")
		group.StatusKey = aggregatePublicationGroupStatus(state)
		if group.StatusKey == "publishing" && group.StepCount > 1 {
			completedSteps := 0
			for _, step := range state.stepStatus {
				if step != nil && step.total > 0 && step.published == step.total {
					completedSteps++
				}
			}
			if completedSteps > 0 && completedSteps < group.StepCount {
				group.ProgressLabel = stepProgressLabel(completedSteps, group.StepCount)
			}
		}
		out = append(out, group)
	}
	return out
}

func aggregatePublicationGroupStatus(state logicalThreadState) string {
	if state.failedPosts > 0 {
		return "failed"
	}
	if state.totalPosts > 0 && state.publishedPosts == state.totalPosts {
		return "published"
	}
	if state.publishingPosts > 0 || (state.publishedPosts > 0 && state.publishedPosts < state.totalPosts) {
		return "publishing"
	}
	if state.scheduledPosts > 0 {
		return "scheduled"
	}
	if state.draftPosts > 0 {
		return "draft"
	}
	return "scheduled"
}

func buildFollowUpStepPreviews(thread []domain.Post) []publicationStepPreview {
	if len(thread) <= 1 {
		return nil
	}
	out := make([]publicationStepPreview, 0, len(thread)-1)
	for _, post := range thread[1:] {
		text := strings.TrimSpace(post.Text)
		if text == "" {
			text = "..."
		}
		position := post.ThreadPosition
		if position <= 0 {
			position = len(out) + 2
		}
		out = append(out, publicationStepPreview{
			Position:   position,
			Text:       text,
			MediaCount: len(post.Media),
		})
	}
	return out
}

func logicalPublicationSignature(thread []domain.Post) string {
	var b strings.Builder
	for _, post := range sortedThreadPosts(thread) {
		position := post.ThreadPosition
		if position <= 0 {
			position = 1
		}
		b.WriteString(strings.TrimSpace(post.ScheduledAt.UTC().Format(time.RFC3339Nano)))
		b.WriteByte('|')
		b.WriteString(strings.TrimSpace(strings.ReplaceAll(post.Text, "\r\n", "\n")))
		b.WriteByte('|')
		b.WriteString(publicationMediaSignature(post.Media))
		b.WriteByte('|')
		b.WriteString(strconv.Itoa(position))
		b.WriteByte(';')
	}
	return b.String()
}

func sortedThreadPosts(posts []domain.Post) []domain.Post {
	if len(posts) == 0 {
		return nil
	}
	out := append([]domain.Post(nil), posts...)
	sort.SliceStable(out, func(i, j int) bool {
		leftPos := out[i].ThreadPosition
		rightPos := out[j].ThreadPosition
		if leftPos <= 0 {
			leftPos = 1
		}
		if rightPos <= 0 {
			rightPos = 1
		}
		if leftPos != rightPos {
			return leftPos < rightPos
		}
		if !out[i].ScheduledAt.Equal(out[j].ScheduledAt) {
			return out[i].ScheduledAt.Before(out[j].ScheduledAt)
		}
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return strings.TrimSpace(out[i].ID) < strings.TrimSpace(out[j].ID)
	})
	return out
}

func filterPublicationGroupsByStatus(groups []publicationGroupItem, wanted ...string) []publicationGroupItem {
	if len(groups) == 0 || len(wanted) == 0 {
		return nil
	}
	allowed := make(map[string]struct{}, len(wanted))
	for _, item := range wanted {
		allowed[strings.TrimSpace(item)] = struct{}{}
	}
	out := make([]publicationGroupItem, 0, len(groups))
	for _, group := range groups {
		if _, ok := allowed[strings.TrimSpace(group.StatusKey)]; ok {
			out = append(out, group)
		}
	}
	return out
}

func groupCalendarEventsFromPublicationGroups(groups []publicationGroupItem) []calendarEvent {
	if len(groups) == 0 {
		return nil
	}
	out := make([]calendarEvent, 0, len(groups))
	for _, group := range groups {
		if group.ScheduledAt.IsZero() {
			continue
		}
		statusClass, statusLabel, statusKey := calendarStatusMetaFromGroup(group.StatusKey)
		preview := strings.TrimSpace(group.Text)
		if len(preview) > 56 {
			preview = preview[:53] + "..."
		}
		out = append(out, calendarEvent{
			TimeLabel:     group.ScheduledAt.Format("15:04"),
			StatusClass:   statusClass,
			StatusLabel:   statusLabel,
			StatusKey:     statusKey,
			TextPreview:   preview,
			ThreadLabel:   group.ThreadLabel,
			Platform:      group.PrimaryPlatform,
			Platforms:     append([]domain.Platform(nil), group.Platforms...),
			PostCount:     group.PostCount,
			MultiPlatform: group.MultiPlatform,
		})
	}
	return out
}

func orderedThreadRootsFromPosts(posts []domain.Post) []string {
	if len(posts) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(posts))
	out := make([]string, 0, len(posts))
	for _, post := range posts {
		rootID := postThreadRootID(post)
		if rootID == "" {
			rootID = strings.TrimSpace(post.ID)
		}
		if rootID == "" {
			continue
		}
		if _, exists := seen[rootID]; exists {
			continue
		}
		seen[rootID] = struct{}{}
		out = append(out, rootID)
	}
	return out
}

func orderedThreadRootsFromFailedEnvelopes(items []failedPostEnvelope) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		rootID := postThreadRootID(item.Post)
		if rootID == "" {
			rootID = strings.TrimSpace(item.Post.ID)
		}
		if rootID == "" {
			continue
		}
		if _, exists := seen[rootID]; exists {
			continue
		}
		seen[rootID] = struct{}{}
		out = append(out, rootID)
	}
	return out
}

func threadBundlesFromRoots(rootIDs []string, threadPostsByRoot map[string][]domain.Post) [][]domain.Post {
	if len(rootIDs) == 0 {
		return nil
	}
	out := make([][]domain.Post, 0, len(rootIDs))
	seen := make(map[string]struct{}, len(rootIDs))
	for _, rootID := range rootIDs {
		rootID = strings.TrimSpace(rootID)
		if rootID == "" {
			continue
		}
		if _, exists := seen[rootID]; exists {
			continue
		}
		seen[rootID] = struct{}{}
		thread := sortedThreadPosts(threadPostsByRoot[rootID])
		if len(thread) == 0 {
			continue
		}
		out = append(out, thread)
	}
	return out
}
