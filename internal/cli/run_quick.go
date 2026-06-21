package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// runPostsQuick handles "postflow posts quick" — a single-command way to
// create and optionally schedule a post, designed for automation (Hermes,
// cron jobs, shell scripts).
//
// Usage:
//
//	# Create a draft post
//	postflow posts quick --account-id acc_xxx --text "Hello world"
//
//	# Create and schedule for later
//	postflow posts quick --account-id acc_xxx --text "Hello world" --scheduled-at "2026-07-01T10:00:00Z"
//
//	# Use named accounts via ACCOUNT_MAP env var
//	export ACCOUNT_MAP='default=acc_123 twitter=acc_456'
//	postflow posts quick --account twitter --text "Tweet!"
func runPostsQuick(ctx context.Context, client *APIClient, cfg config, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("posts quick", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var accountFlag string
	var accountIDFlag string
	var text string
	var scheduledAt string
	var segmentsJSON string

	fs.StringVar(&accountFlag, "account", "", "Account name (from ACCOUNT_MAP env var) or raw account ID")
	fs.StringVar(&accountIDFlag, "account-id", "", "Direct account ID (overrides --account)")
	fs.StringVar(&text, "text", "", "Post text content")
	fs.StringVar(&scheduledAt, "scheduled-at", "", "Schedule time (RFC3339, e.g. 2026-07-01T10:00:00Z). Omit to create a draft.")
	fs.StringVar(&segmentsJSON, "segments-json", "", "Thread segments JSON (alternative to --text)")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Resolve account
	resolvedAccountID := resolveAccount(accountFlag, accountIDFlag)
	if resolvedAccountID == "" {
		fmt.Fprintln(stderr, "--account or --account-id is required")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Usage:")
		fmt.Fprintln(stderr, "  postflow posts quick --account-id acc_xxx --text 'Hello world'")
		fmt.Fprintln(stderr, "  postflow posts quick --account default --text 'Hello'")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Account map (optional):")
		fmt.Fprintln(stderr, "  export ACCOUNT_MAP='default=acc_123 twitter=acc_456'")
		fmt.Fprintln(stderr, "  postflow posts quick --account twitter --text 'Tweet!'")
		return 2
	}

	if text == "" && segmentsJSON == "" {
		fmt.Fprintln(stderr, "--text or --segments-json is required")
		return 2
	}

	// Step 1: Create the post
	createReq := map[string]interface{}{
		"account_id": resolvedAccountID,
	}
	if strings.TrimSpace(text) != "" {
		createReq["text"] = strings.TrimSpace(text)
	}
	if strings.TrimSpace(segmentsJSON) != "" {
		// Parse segments JSON and send as a proper array (not a string)
		var segments []interface{}
		if err := json.Unmarshal([]byte(segmentsJSON), &segments); err != nil {
			fmt.Fprintf(stderr, "invalid --segments-json: %v\n", err)
			return 2
		}
		if len(segments) == 0 {
			fmt.Fprintln(stderr, "--segments-json cannot be empty")
			return 2
		}
		createReq["segments"] = segments
	}

	var createResp map[string]interface{}
	if err := client.Post(ctx, "/posts", createReq, &createResp); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}

	postID, _ := createResp["id"].(string)
	if postID == "" {
		fmt.Fprintln(stderr, "no post ID returned from create")
		return 1
	}

	platform, _ := createResp["platform"].(string)
	status, _ := createResp["status"].(string)

	// Step 2: If scheduled-at provided, schedule it
	if strings.TrimSpace(scheduledAt) != "" {
		schedReq := map[string]interface{}{
			"scheduled_at": scheduledAt,
		}
		if err := client.Post(ctx, "/posts/"+postID+"/schedule", schedReq, nil); err != nil {
			fmt.Fprintf(stderr, "post created (%s) but scheduling failed: %v\n", postID, err)
			printOutput(stdout, cfg.asJSON, map[string]interface{}{
			"post_id":      postID,
			"account_id":   resolvedAccountID,
			"platform":     platform,
			"status":       "created",
			"text":         text,
			"scheduled_at": scheduledAt,
			"error":        err.Error(),
		}, func() {
			fmt.Fprintf(stdout, "post_id: %s\n", postID)
			fmt.Fprintf(stdout, "account_id: %s\n", resolvedAccountID)
			fmt.Fprintf(stdout, "platform: %s\n", platform)
			fmt.Fprintf(stdout, "status: created (unscheduled)\n")
			fmt.Fprintf(stdout, "text: %s\n", text)
			fmt.Fprintf(stdout, "scheduled_at: %s\n", scheduledAt)
		})
			return 1
		}
		status = "scheduled"
	}

	// Output
	printOutput(stdout, cfg.asJSON, map[string]interface{}{
		"post_id":      postID,
		"account_id":   resolvedAccountID,
		"platform":     platform,
		"status":       status,
		"text":         text,
		"created_at":   createResp["created_at"],
		"scheduled_at": scheduledAt,
	}, func() {
		fmt.Fprintf(stdout, "post_id: %s\n", postID)
		fmt.Fprintf(stdout, "account_id: %s\n", resolvedAccountID)
		fmt.Fprintf(stdout, "platform: %s\n", platform)
		fmt.Fprintf(stdout, "status: %s\n", status)
		fmt.Fprintf(stdout, "text: %s\n", text)
		if scheduledAt != "" {
			fmt.Fprintf(stdout, "scheduled_at: %s\n", scheduledAt)
		}
	})

	return 0
}

// resolveAccount resolves the account ID from either a named alias (via
// ACCOUNT_MAP env var) or a direct account ID.
//
// ACCOUNT_MAP format: 'name1=acc_xxx name2=acc_yyy'
func resolveAccount(accountFlag, accountIDFlag string) string {
	// Direct account ID always wins
	if strings.TrimSpace(accountIDFlag) != "" {
		return strings.TrimSpace(accountIDFlag)
	}

	name := strings.TrimSpace(accountFlag)
	if name == "" {
		return ""
	}

	// Check ACCOUNT_MAP env var
	accountMap := os.Getenv("ACCOUNT_MAP")
	if accountMap == "" {
		// If ACCOUNT_MAP is not set, treat the flag value as a raw account ID
		// (e.g., "acc_123" or any string)
		return name
	}

	// Parse ACCOUNT_MAP
	for _, pair := range strings.Fields(accountMap) {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 2 && parts[0] == name {
			return parts[1]
		}
	}

	// Not found in ACCOUNT_MAP — if it looks like an account ID, use it anyway
	if strings.HasPrefix(name, "acc_") {
		return name
	}

	return ""
}
