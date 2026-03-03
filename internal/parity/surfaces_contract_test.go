package parity_test

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRequiredCapabilitiesBehaviorParity(t *testing.T) {
	env := newParityEnv(t)

	t.Run("schedule.list", func(t *testing.T) {
		scheduledAt := time.Now().UTC().Add(30 * time.Minute).Round(time.Second)
		postID := env.apiCreatePost("schedule parity", scheduledAt, nil)

		from := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
		to := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)

		assertContainsID(t, "api schedule list", env.apiScheduleListIDs(from, to), postID)
		assertContainsID(t, "cli schedule list", env.cliScheduleListIDs(from, to), postID)
		assertContainsID(t, "mcp schedule list", env.mcpScheduleListIDs(from, to), postID)
	})

	t.Run("posts.create", func(t *testing.T) {
		apiID := env.apiCreatePost("created via api", time.Time{}, nil)
		cliID := env.cliCreatePost("created via cli")
		mcpID := env.mcpCreatePost("created via mcp")

		assertPostText(t, env.store, apiID, "created via api")
		assertPostText(t, env.store, cliID, "created via cli")
		assertPostText(t, env.store, mcpID, "created via mcp")
	})

	t.Run("posts.schedule", func(t *testing.T) {
		scheduledAt := time.Now().UTC().Add(40 * time.Minute).Round(time.Second)
		apiID := env.apiCreatePost("schedule via api", time.Time{}, nil)
		cliID := env.apiCreatePost("schedule via cli", time.Time{}, nil)
		mcpID := env.apiCreatePost("schedule via mcp", time.Time{}, nil)

		env.apiSchedulePost(apiID, scheduledAt)
		env.cliSchedulePost(cliID, scheduledAt)
		env.mcpSchedulePost(mcpID, scheduledAt)

		for _, postID := range []string{apiID, cliID, mcpID} {
			post, err := env.store.GetPost(t.Context(), postID)
			if err != nil {
				t.Fatalf("get post %s: %v", postID, err)
			}
			if got := string(post.Status); got != "scheduled" {
				t.Fatalf("expected scheduled status for %s, got %s", postID, got)
			}
		}
	})

	t.Run("posts.edit", func(t *testing.T) {
		scheduledAt := time.Now().UTC().Add(50 * time.Minute).Round(time.Second)
		apiID := env.apiCreatePost("edit start api", time.Time{}, nil)
		cliID := env.apiCreatePost("edit start cli", time.Time{}, nil)
		mcpID := env.apiCreatePost("edit start mcp", time.Time{}, nil)

		env.apiEditPost(apiID, "edit done api", "schedule", scheduledAt)
		env.cliEditPost(cliID, "edit done cli", "schedule", scheduledAt)
		env.mcpEditPost(mcpID, "edit done mcp", "schedule", scheduledAt)

		assertPostText(t, env.store, apiID, "edit done api")
		assertPostText(t, env.store, cliID, "edit done cli")
		assertPostText(t, env.store, mcpID, "edit done mcp")
		for _, postID := range []string{apiID, cliID, mcpID} {
			post, err := env.store.GetPost(t.Context(), postID)
			if err != nil {
				t.Fatalf("get post %s: %v", postID, err)
			}
			if got := string(post.Status); got != "scheduled" {
				t.Fatalf("expected scheduled status for %s, got %s", postID, got)
			}
		}
	})

	t.Run("posts.delete", func(t *testing.T) {
		apiID := env.apiCreatePost("delete via api", time.Now().UTC().Add(20*time.Minute), nil)
		cliID := env.apiCreatePost("delete via cli", time.Now().UTC().Add(21*time.Minute), nil)
		mcpID := env.apiCreatePost("delete via mcp", time.Now().UTC().Add(22*time.Minute), nil)

		env.apiDeletePost(apiID)
		env.cliDeletePost(cliID)
		env.mcpDeletePost(mcpID)

		for _, postID := range []string{apiID, cliID, mcpID} {
			if _, err := env.store.GetPost(t.Context(), postID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("expected post %s to be deleted, err=%v", postID, err)
			}
		}
	})

	t.Run("posts.validate", func(t *testing.T) {
		apiValid := env.apiValidatePost("validate via api")
		cliValid := env.cliValidatePost("validate via cli")
		mcpValid := env.mcpValidatePost("validate via mcp")
		if !apiValid || !cliValid || !mcpValid {
			t.Fatalf("expected valid=true on all surfaces, got api=%t cli=%t mcp=%t", apiValid, cliValid, mcpValid)
		}
	})

	t.Run("failed.list", func(t *testing.T) {
		dlqID := env.seedFailedDeadLetter("failed list parity")
		assertContainsID(t, "api failed list", env.apiDLQListIDs(), dlqID)
		assertContainsID(t, "cli failed list", env.cliDLQListIDs(), dlqID)
		assertContainsID(t, "mcp failed list", env.mcpFailedListIDs(), dlqID)
	})

	t.Run("dlq.requeue", func(t *testing.T) {
		apiDLQ := env.seedFailedDeadLetter("requeue via api")
		cliDLQ := env.seedFailedDeadLetter("requeue via cli")
		mcpDLQ := env.seedFailedDeadLetter("requeue via mcp")

		env.apiRequeueDLQ(apiDLQ)
		env.cliRequeueDLQ(cliDLQ)
		env.mcpRequeueDLQ(mcpDLQ)

		after := env.apiDLQListIDs()
		assertNotContainsID(t, "api requeue removed item", after, apiDLQ)
		assertNotContainsID(t, "cli requeue removed item", after, cliDLQ)
		assertNotContainsID(t, "mcp requeue removed item", after, mcpDLQ)
	})

	t.Run("dlq.delete", func(t *testing.T) {
		apiDLQ := env.seedFailedDeadLetter("delete via api")
		cliDLQ := env.seedFailedDeadLetter("delete via cli")
		mcpDLQ := env.seedFailedDeadLetter("delete via mcp")

		env.apiDeleteDLQ(apiDLQ)
		env.cliDeleteDLQ(cliDLQ)
		env.mcpDeleteDLQ(mcpDLQ)

		after := env.apiDLQListIDs()
		assertNotContainsID(t, "api delete removed item", after, apiDLQ)
		assertNotContainsID(t, "cli delete removed item", after, cliDLQ)
		assertNotContainsID(t, "mcp delete removed item", after, mcpDLQ)
	})

	t.Run("media.upload_list_delete", func(t *testing.T) {
		apiMedia := env.apiUploadMedia(env.tempFile)
		cliMedia := env.cliUploadMedia(env.tempFile)
		mcpMedia := env.mcpUploadMedia("from-mcp")

		apiList := env.apiListMediaIDs()
		cliList := env.cliListMediaIDs()
		mcpList := env.mcpListMediaIDs()
		for _, mediaID := range []string{apiMedia, cliMedia, mcpMedia} {
			assertContainsID(t, "api media list", apiList, mediaID)
			assertContainsID(t, "cli media list", cliList, mediaID)
			assertContainsID(t, "mcp media list", mcpList, mediaID)
		}

		env.apiDeleteMedia(apiMedia)
		env.cliDeleteMedia(cliMedia)
		env.mcpDeleteMedia(mcpMedia)

		after := env.apiListMediaIDs()
		for _, mediaID := range []string{apiMedia, cliMedia, mcpMedia} {
			assertNotContainsID(t, "media deleted", after, mediaID)
		}
	})
}

func assertContainsID(t *testing.T, label string, ids []string, expected string) {
	t.Helper()
	expected = strings.TrimSpace(expected)
	for _, id := range ids {
		if strings.TrimSpace(id) == expected {
			return
		}
	}
	t.Fatalf("%s does not contain %q; got=%v", label, expected, ids)
}

func assertNotContainsID(t *testing.T, label string, ids []string, banned string) {
	t.Helper()
	banned = strings.TrimSpace(banned)
	for _, id := range ids {
		if strings.TrimSpace(id) == banned {
			t.Fatalf("%s still contains %q; got=%v", label, banned, ids)
		}
	}
}
