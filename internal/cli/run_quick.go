package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

type quickCreateRequest struct {
	AccountID   string `json:"account_id"`
	Text        string `json:"text"`
	ScheduledAt string `json:"scheduled_at,omitempty"`
}

type quickCreateResponse struct {
	PostID      string `json:"post_id"`
	AccountID   string `json:"account_id"`
	Platform    string `json:"platform"`
	Status      string `json:"status"`
	Text        string `json:"text"`
	ScheduledAt string `json:"scheduled_at,omitempty"`
	CreatedAt   string `json:"created_at"`
}

// runPostsQuick handles "postflow posts quick" — a single-command way to
// create and optionally schedule a post, designed for automation (Hermes,
// cron jobs, shell scripts).
//
// Usage:
//
//	# Create and schedule for now (publish immediately)
//	postflow posts quick --account-id acc_xxx --text "Hello world"
//
//	# Create and schedule for later
//	postflow posts quick --account-id acc_xxx --text "Hello world" --scheduled-at "2026-07-01T10:00:00Z"
//
//	# Use DISPLAY_NAME env var as account shortcut
//	export PF_ACCOUNT=default
//	postflow posts quick --text "Hello from $PF_ACCOUNT"
//
//	# Use ACCOUNT_MAP env var for named accounts
//	export ACCOUNT_MAP='default=acc_123 twitter=acc_456'
//	postflow posts quick --account default --text "Tweet!"
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
	fs.StringVar(&scheduledAt, "scheduled-at", "", "Schedule time (RFC3339, e.g. 2026-07-01T10:00:00Z). Omit to publish immediately.")
	fs.StringVar(&segmentsJSON, "segments-json", "", "Thread segments JSON (alternative to --text)")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Resolve account
	resolvedAccountID := resolveAccount(accountFlag, accountIDFlag)
	if resolvedAccountID == "" {
		fmt.Fprintln(stderr, "error: --account or --account-id is required")
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
		fmt.Fprintln(stderr, "error: --text or --segments-json is required")
		return 2
	}

	// Step 1: Create the post
	createReq := map[string]interface{}{
		"account_id": resolvedAccountID,
	}
	if text != "" {
		createReq["text"] = text
	}
	if segmentsJSON != "" {
		createReq["segments_json"] = segmentsJSON
	}

	var createResp map[string]interface{}
	if err := client.Post(ctx, "/posts", createReq, &createResp); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}

	postID, _ := createResp["id"].(string)
	if postID == "" {
		fmt.Fprintln(stderr, "error: no post ID returned from create")
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
			fmt.Fprintf(stderr, "Post created (%s) but scheduling failed: %v\n", postID, err)
			fmt.Fprintf(stdout, "post_id: %s\n", postID)
			fmt.Fprintf(stdout, "account_id: %s\n", resolvedAccountID)
			fmt.Fprintf(stdout, "platform: %s\n", platform)
			fmt.Fprintf(stdout, "status: created (unscheduled)\n")
			fmt.Fprintf(stdout, "text: %s\n", text)
			return 1
		}
		status = "scheduled"
	}

	// Output
	fmt.Fprintf(stdout, "post_id: %s\n", postID)
	fmt.Fprintf(stdout, "account_id: %s\n", resolvedAccountID)
	fmt.Fprintf(stdout, "platform: %s\n", platform)
	fmt.Fprintf(stdout, "status: %s\n", status)
	fmt.Fprintf(stdout, "text: %s\n", text)
	if scheduledAt != "" {
		fmt.Fprintf(stdout, "scheduled_at: %s\n", scheduledAt)
	}

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
		// Maybe the flag value is already an account ID (starts with acc_)
		if strings.HasPrefix(name, "acc_") {
			return name
		}
		return ""
	}

	// Parse ACCOUNT_MAP
	for _, pair := range strings.Fields(accountMap) {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 2 && parts[0] == name {
			return parts[1]
		}
	}

	return ""
}
