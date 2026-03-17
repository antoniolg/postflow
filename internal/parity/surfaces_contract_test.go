package parity_test

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/db"
	"github.com/antoniolg/postflow/internal/domain"
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

	t.Run("schedule.list publications keeps one item per network thread", func(t *testing.T) {
		scheduledAt := time.Now().UTC().Add(35 * time.Minute).Round(time.Second)
		linkedIn := mustCreateParityAccount(t, env, domain.PlatformLinkedIn, "li-pubs")
		instagram := mustCreateParityAccount(t, env, domain.PlatformInstagram, "ig-pubs")
		facebook := mustCreateParityAccount(t, env, domain.PlatformFacebook, "fb-pubs")
		media, err := env.store.CreateMedia(t.Context(), domain.Media{
			Kind:         "image",
			OriginalName: "parity-card.png",
			StoragePath:  env.tempFile,
			MimeType:     "image/png",
			SizeBytes:    128,
		})
		if err != nil {
			t.Fatalf("create parity media: %v", err)
		}

		for _, accountID := range []string{linkedIn, instagram, facebook} {
			raw, status := env.apiJSON(http.MethodPost, "/posts", map[string]any{
				"account_id":   accountID,
				"scheduled_at": scheduledAt.UTC().Format(time.RFC3339),
				"segments": []map[string]any{
					{"text": "cross-network root", "media_ids": []string{media.ID}},
					{"text": "cross-network reply"},
				},
			}, "application/json")
			if status != http.StatusCreated && status != http.StatusOK {
				t.Fatalf("create thread for account %s status=%d body=%s", accountID, status, string(raw))
			}
		}

		from := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
		to := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)

		for label, items := range map[string][]parityScheduledPublication{
			"api": env.apiScheduleListPublicationsDetailed(from, to),
			"cli": env.cliScheduleListPublicationsDetailed(from, to),
			"mcp": env.mcpScheduleListPublicationsDetailed(from, to),
		} {
			var matched []parityScheduledPublication
			for _, item := range items {
				if item.AccountID == linkedIn || item.AccountID == instagram || item.AccountID == facebook {
					matched = append(matched, item)
				}
			}
			if len(matched) != 3 {
				t.Fatalf("%s expected 3 network-specific publications, got %d (%+v)", label, len(matched), matched)
			}
			for _, item := range matched {
				if item.SegmentCount != 2 {
					t.Fatalf("%s expected segment_count=2 for %+v", label, item)
				}
				if len(item.Segments) != 2 {
					t.Fatalf("%s expected two reconstructed segments for %+v", label, item)
				}
			}
		}
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

	t.Run("posts.thread.create", func(t *testing.T) {
		threadSegments := []map[string]any{
			{"text": "thread root"},
			{"text": "thread follow up 1"},
			{"text": "thread follow up 2"},
		}
		apiOut := env.apiCreateThreadDetailed(threadSegments)
		cliOut := env.cliCreateThreadDetailed(`[{"text":"thread root"},{"text":"thread follow up 1"},{"text":"thread follow up 2"}]`)
		mcpOut := env.mcpCreateThreadDetailed(threadSegments)

		assertThreadCreateOutput(t, "api", apiOut, 3)
		assertThreadCreateOutput(t, "cli", cliOut, 3)
		assertThreadCreateOutput(t, "mcp", mcpOut, 3)

		assertThreadTexts(t, env, "api", apiOut.RootID, []string{"thread root", "thread follow up 1", "thread follow up 2"})
		assertThreadTexts(t, env, "cli", cliOut.RootID, []string{"thread root", "thread follow up 1", "thread follow up 2"})
		assertThreadTexts(t, env, "mcp", mcpOut.RootID, []string{"thread root", "thread follow up 1", "thread follow up 2"})
	})

	t.Run("posts.thread.validate", func(t *testing.T) {
		threadSegments := []map[string]any{
			{"text": "validate root"},
			{"text": "validate follow up 1"},
			{"text": "validate follow up 2"},
		}
		apiValid := env.apiValidateThread(threadSegments)
		cliValid := env.cliValidateThread(`[{"text":"validate root"},{"text":"validate follow up 1"},{"text":"validate follow up 2"}]`)
		mcpValid := env.mcpValidateThread(threadSegments)
		if !apiValid || !cliValid || !mcpValid {
			t.Fatalf("expected valid=true on thread validate on all surfaces, got api=%t cli=%t mcp=%t", apiValid, cliValid, mcpValid)
		}
	})

	t.Run("posts.thread.edit", func(t *testing.T) {
		apiOut := env.apiCreateThreadDetailed([]map[string]any{
			{"text": "api edit root"},
			{"text": "api edit reply"},
		})
		cliOut := env.cliCreateThreadDetailed(`[{"text":"cli edit root"},{"text":"cli edit reply"}]`)
		mcpOut := env.mcpCreateThreadDetailed([]map[string]any{
			{"text": "mcp edit root"},
			{"text": "mcp edit reply"},
		})

		env.apiEditThread(apiOut.RootID, []map[string]any{
			{"text": "api edit root updated"},
			{"text": "api edit reply updated"},
			{"text": "api edit reply extra"},
		})
		env.cliEditThread(cliOut.RootID, `[{"text":"cli edit root updated"},{"text":"cli edit reply updated"},{"text":"cli edit reply extra"}]`)
		env.mcpEditThread(mcpOut.RootID, []map[string]any{
			{"text": "mcp edit root updated"},
			{"text": "mcp edit reply updated"},
			{"text": "mcp edit reply extra"},
		})

		assertThreadTexts(t, env, "api edit", apiOut.RootID, []string{"api edit root updated", "api edit reply updated", "api edit reply extra"})
		assertThreadTexts(t, env, "cli edit", cliOut.RootID, []string{"cli edit root updated", "cli edit reply updated", "cli edit reply extra"})
		assertThreadTexts(t, env, "mcp edit", mcpOut.RootID, []string{"mcp edit root updated", "mcp edit reply updated", "mcp edit reply extra"})
	})

	t.Run("posts.thread.list_metadata", func(t *testing.T) {
		scheduledAt := time.Now().UTC().Add(75 * time.Minute).Round(time.Second)
		threadOut := env.apiCreateScheduledThreadDetailed([]map[string]any{
			{"text": "metadata root"},
			{"text": "metadata reply 1"},
			{"text": "metadata reply 2"},
		}, scheduledAt)

		from := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
		to := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)

		assertListedThreadMetadata(t, "api schedule list", env.apiScheduleListPostsDetailed(from, to), threadOut)
		assertListedThreadMetadata(t, "cli schedule list", env.cliScheduleListPostsDetailed(from, to), threadOut)
		assertListedThreadMetadata(t, "mcp schedule list", env.mcpScheduleListPostsDetailed(from, to), threadOut)
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

func mustCreateParityAccount(t *testing.T, env *parityEnv, platform domain.Platform, externalID string) string {
	t.Helper()
	account, err := env.store.UpsertAccount(t.Context(), db.UpsertAccountParams{
		Platform:          platform,
		DisplayName:       strings.ToUpper(string(platform)) + " parity",
		ExternalAccountID: externalID,
		AuthMethod:        domain.AuthMethodStatic,
		Status:            domain.AccountStatusConnected,
	})
	if err != nil {
		t.Fatalf("upsert parity account for %s: %v", platform, err)
	}
	return account.ID
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

func assertThreadLength(t *testing.T, env *parityEnv, source string, ids []string, expected int) {
	t.Helper()
	if len(ids) == 0 {
		t.Fatalf("%s thread create returned no ids", source)
	}
	rootID := strings.TrimSpace(ids[0])
	posts, err := env.store.ListThreadPosts(t.Context(), rootID)
	if err != nil {
		t.Fatalf("%s list thread posts: %v", source, err)
	}
	if len(posts) != expected {
		t.Fatalf("%s expected thread length %d, got %d", source, expected, len(posts))
	}
	expectedTexts := []string{"thread root", "thread follow up 1", "thread follow up 2"}
	for i := 0; i < expected; i++ {
		expectedText := expectedTexts[i]
		if strings.TrimSpace(posts[i].Text) != expectedText {
			t.Fatalf("%s expected step %d text %q, got %q", source, i+1, expectedText, strings.TrimSpace(posts[i].Text))
		}
	}
}

func assertThreadCreateOutput(t *testing.T, source string, out parityThreadCreateOutput, expectedSteps int) {
	t.Helper()
	if strings.TrimSpace(out.RootID) == "" {
		t.Fatalf("%s create output missing root_id", source)
	}
	if out.TotalSteps != expectedSteps {
		t.Fatalf("%s expected total_steps=%d, got %d", source, expectedSteps, out.TotalSteps)
	}
	if len(out.Items) != expectedSteps {
		t.Fatalf("%s expected %d thread items in response, got %d", source, expectedSteps, len(out.Items))
	}
	foundRoot := false
	for _, item := range out.Items {
		if strings.TrimSpace(item.ThreadGroupID) == "" {
			t.Fatalf("%s expected thread_group_id in create item %s", source, item.ID)
		}
		if item.ThreadPosition == 0 {
			t.Fatalf("%s expected thread_position in create item %s", source, item.ID)
		}
		if strings.TrimSpace(item.ID) == strings.TrimSpace(out.RootID) {
			foundRoot = true
			if item.ThreadPosition != 1 {
				t.Fatalf("%s expected root create item to be position 1, got %d", source, item.ThreadPosition)
			}
		}
	}
	if !foundRoot {
		t.Fatalf("%s create output missing root item %s", source, out.RootID)
	}
}

func assertThreadTexts(t *testing.T, env *parityEnv, source string, rootID string, expectedTexts []string) {
	t.Helper()
	posts, err := env.store.ListThreadPosts(t.Context(), strings.TrimSpace(rootID))
	if err != nil {
		t.Fatalf("%s list thread posts: %v", source, err)
	}
	if len(posts) != len(expectedTexts) {
		t.Fatalf("%s expected thread length %d, got %d", source, len(expectedTexts), len(posts))
	}
	for idx, expectedText := range expectedTexts {
		if strings.TrimSpace(posts[idx].Text) != expectedText {
			t.Fatalf("%s expected step %d text %q, got %q", source, idx+1, expectedText, strings.TrimSpace(posts[idx].Text))
		}
	}
}

func assertListedThreadMetadata(t *testing.T, source string, listed []parityThreadPost, created parityThreadCreateOutput) {
	t.Helper()
	if len(created.Items) == 0 {
		t.Fatalf("%s missing created thread items for metadata assertion", source)
	}
	listedByID := make(map[string]parityThreadPost, len(listed))
	for _, item := range listed {
		listedByID[strings.TrimSpace(item.ID)] = item
	}
	groupID := ""
	for _, createdItem := range created.Items {
		itemID := strings.TrimSpace(createdItem.ID)
		got, ok := listedByID[itemID]
		if !ok {
			t.Fatalf("%s missing thread item %s from list output", source, itemID)
		}
		if got.ThreadPosition != createdItem.ThreadPosition {
			t.Fatalf("%s expected thread_position=%d for %s, got %d", source, createdItem.ThreadPosition, itemID, got.ThreadPosition)
		}
		if strings.TrimSpace(got.ThreadGroupID) == "" {
			t.Fatalf("%s expected thread_group_id for %s", source, itemID)
		}
		if groupID == "" {
			groupID = strings.TrimSpace(got.ThreadGroupID)
		} else if strings.TrimSpace(got.ThreadGroupID) != groupID {
			t.Fatalf("%s expected consistent thread_group_id, got %q and %q", source, groupID, got.ThreadGroupID)
		}
		if createdItem.ThreadPosition > 1 && strings.TrimSpace(got.RootPostID) != strings.TrimSpace(created.RootID) {
			t.Fatalf("%s expected root_post_id=%s for %s, got %s", source, created.RootID, itemID, got.RootPostID)
		}
	}
}
