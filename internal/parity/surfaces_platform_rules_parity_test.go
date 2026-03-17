package parity_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/antoniolg/postflow/internal/domain"
)

func TestPlatformRulesParityInstagramRequiresMediaOnCreate(t *testing.T) {
	env := newParityEnv(t)
	instagramAccountID := env.apiCreateStaticAccount("instagram", "parity_instagram_create_media", map[string]any{
		"access_token": "tok_ig_create_media",
	})

	raw, status := env.apiJSON(http.MethodPost, "/posts", map[string]any{
		"account_id": instagramAccountID,
		"text":       "instagram without media",
	}, "application/json")
	if status != http.StatusBadRequest {
		t.Fatalf("api expected 400, got %d body=%s", status, string(raw))
	}
	assertContainsAny(t, "api", apiErrorMessage(raw), "instagram", "media")

	code, _, stderr := env.runCLIResult("posts", "create", "--account-id", instagramAccountID, "--text", "instagram without media")
	if code == 0 {
		t.Fatalf("cli expected non-zero exit when instagram media is missing")
	}
	assertContainsAny(t, "cli", stderr, "instagram", "media")

	msg := env.mcpCallToolError("postflow_create_post", map[string]any{
		"account_id": instagramAccountID,
		"text":       "instagram without media",
	})
	assertContainsAny(t, "mcp", msg, "instagram", "media")
}

func TestPlatformRulesParityInstagramRejectsEditRemovingMedia(t *testing.T) {
	env := newParityEnv(t)
	instagramAccountID := env.apiCreateStaticAccount("instagram", "parity_instagram_edit_media", map[string]any{
		"access_token": "tok_ig_edit_media",
	})
	mediaID := createParityImageMedia(t, env, "ig_edit_media.jpg")

	apiPostID := env.apiCreatePostForAccount(instagramAccountID, "caption api", time.Now().UTC().Add(30*time.Minute).Round(time.Second), []string{mediaID})
	cliPostID := env.apiCreatePostForAccount(instagramAccountID, "caption cli", time.Now().UTC().Add(31*time.Minute).Round(time.Second), []string{mediaID})
	mcpPostID := env.apiCreatePostForAccount(instagramAccountID, "caption mcp", time.Now().UTC().Add(32*time.Minute).Round(time.Second), []string{mediaID})

	raw, status := env.apiJSON(http.MethodPost, "/posts/"+strings.TrimSpace(apiPostID)+"/edit", map[string]any{
		"text":      "caption api edited",
		"media_ids": []string{},
	}, "application/json")
	if status != http.StatusBadRequest && status != http.StatusConflict {
		t.Fatalf("api expected 400/409, got %d body=%s", status, string(raw))
	}
	assertContainsAny(t, "api", apiErrorMessage(raw), "instagram", "media")

	code, _, stderr := env.runCLIResult("posts", "edit", "--id", cliPostID, "--text", "caption cli edited", "--replace-media")
	if code == 0 {
		t.Fatalf("cli expected non-zero exit when removing instagram media")
	}
	assertContainsAny(t, "cli", stderr, "instagram", "media")

	msg := env.mcpCallToolError("postflow_edit_post", map[string]any{
		"post_id":   mcpPostID,
		"text":      "caption mcp edited",
		"media_ids": []string{},
	})
	assertContainsAny(t, "mcp", msg, "instagram", "media")

	for _, id := range []string{apiPostID, cliPostID, mcpPostID} {
		post, err := env.store.GetPost(t.Context(), strings.TrimSpace(id))
		if err != nil {
			t.Fatalf("get post %s: %v", id, err)
		}
		if len(post.Media) != 1 || strings.TrimSpace(post.Media[0].ID) != mediaID {
			t.Fatalf("expected original media to remain on %s after failed edit, got %#v", id, post.Media)
		}
	}
}

func TestPlatformRulesParityInstagramAllowsPNGMediaOnCreate(t *testing.T) {
	env := newParityEnv(t)
	instagramAccountID := env.apiCreateStaticAccount("instagram", "parity_instagram_png_create", map[string]any{
		"access_token": "tok_ig_png_create",
	})
	mediaID := createParityImageMedia(t, env, "ig_create_media.png")

	apiPostID := env.apiCreatePostForAccount(instagramAccountID, "instagram png api", time.Time{}, []string{mediaID})
	cliPostID := env.cliCreatePostForAccount(instagramAccountID, "instagram png cli", []string{mediaID})
	mcpPostID := env.mcpCreatePostForAccount(instagramAccountID, "instagram png mcp", []string{mediaID})

	for _, id := range []string{apiPostID, cliPostID, mcpPostID} {
		post, err := env.store.GetPost(t.Context(), strings.TrimSpace(id))
		if err != nil {
			t.Fatalf("get post %s: %v", id, err)
		}
		if len(post.Media) != 1 || strings.TrimSpace(post.Media[0].ID) != mediaID {
			t.Fatalf("expected png media %s on %s, got %#v", mediaID, id, post.Media)
		}
		if strings.TrimSpace(post.Media[0].MimeType) != "image/png" {
			t.Fatalf("expected image/png on %s, got %q", id, post.Media[0].MimeType)
		}
	}
}

func TestPlatformRulesParityLinkedInEditReplacesMediaAndKeepsSchedule(t *testing.T) {
	env := newParityEnv(t)
	linkedInAccountID := env.apiCreateStaticAccount("linkedin", "parity_linkedin_edit_media", map[string]any{
		"access_token": "tok_li_edit_media",
	})
	initialMediaID := createParityImageMedia(t, env, "li_initial_media.png")
	replacementMediaID := createParityImageMedia(t, env, "li_replacement_media.png")
	scheduledAt := time.Now().UTC().Add(45 * time.Minute).Round(time.Second)

	apiPostID := env.apiCreatePostForAccount(linkedInAccountID, "linkedin api", scheduledAt, []string{initialMediaID})
	cliPostID := env.apiCreatePostForAccount(linkedInAccountID, "linkedin cli", scheduledAt, []string{initialMediaID})
	mcpPostID := env.apiCreatePostForAccount(linkedInAccountID, "linkedin mcp", scheduledAt, []string{initialMediaID})

	raw, status := env.apiJSON(http.MethodPost, "/posts/"+strings.TrimSpace(apiPostID)+"/edit", map[string]any{
		"text":      "linkedin api edited",
		"media_ids": []string{replacementMediaID},
	}, "application/json")
	if status != http.StatusOK {
		t.Fatalf("api expected 200, got %d body=%s", status, string(raw))
	}

	env.cliEditPostWithMedia(cliPostID, "linkedin cli edited", "", time.Time{}, true, []string{replacementMediaID})
	env.mcpEditPostWithMedia(mcpPostID, "linkedin mcp edited", "", time.Time{}, []string{replacementMediaID}, true)

	for _, id := range []string{apiPostID, cliPostID, mcpPostID} {
		post, err := env.store.GetPost(t.Context(), strings.TrimSpace(id))
		if err != nil {
			t.Fatalf("get post %s: %v", id, err)
		}
		if post.Status != "scheduled" {
			t.Fatalf("expected scheduled status for %s after media edit, got %s", id, post.Status)
		}
		if !post.ScheduledAt.Equal(scheduledAt) {
			t.Fatalf("expected scheduled_at %s for %s, got %s", scheduledAt, id, post.ScheduledAt)
		}
		if len(post.Media) != 1 || strings.TrimSpace(post.Media[0].ID) != replacementMediaID {
			t.Fatalf("expected replacement media on %s, got %#v", id, post.Media)
		}
	}
}

func createParityImageMedia(t *testing.T, env *parityEnv, originalName string) string {
	t.Helper()
	mimeType := "image/png"
	lowerName := strings.ToLower(strings.TrimSpace(originalName))
	if strings.HasSuffix(lowerName, ".jpg") || strings.HasSuffix(lowerName, ".jpeg") {
		mimeType = "image/jpeg"
	}
	item, err := env.store.CreateMedia(t.Context(), domain.Media{
		Kind:         "image",
		OriginalName: strings.TrimSpace(originalName),
		StoragePath:  "/tmp/" + strings.TrimSpace(originalName),
		MimeType:     mimeType,
		SizeBytes:    1024,
	})
	if err != nil {
		t.Fatalf("create media fixture: %v", err)
	}
	return strings.TrimSpace(item.ID)
}
