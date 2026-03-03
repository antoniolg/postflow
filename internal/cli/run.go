package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"
)

const Version = "dev"

type config struct {
	baseURL string
	token   string
	timeout time.Duration
	asJSON  bool
}

type scheduleResponse struct {
	From  string    `json:"from"`
	To    string    `json:"to"`
	Items []postDTO `json:"items"`
}

type postDTO struct {
	ID          string `json:"id"`
	AccountID   string `json:"account_id"`
	Platform    string `json:"platform"`
	Status      string `json:"status"`
	Text        string `json:"text"`
	ScheduledAt string `json:"scheduled_at"`
}

type dlqListResponse struct {
	Items []deadLetterDTO `json:"items"`
	Count int             `json:"count"`
}

type deadLetterDTO struct {
	ID        string `json:"id"`
	PostID    string `json:"post_id"`
	Reason    string `json:"reason"`
	LastError string `json:"last_error"`
}

type mediaListResponse struct {
	Count int        `json:"count"`
	Items []mediaDTO `json:"items"`
}

type mediaDTO struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	OriginalName string `json:"original_name"`
	MimeType     string `json:"mime_type"`
	SizeBytes    int64  `json:"size_bytes"`
	UsageCount   int    `json:"usage_count"`
	InUse        bool   `json:"in_use"`
}

type stringListFlag []string

func (s *stringListFlag) String() string { return strings.Join(*s, ",") }

func (s *stringListFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("empty value")
	}
	*s = append(*s, value)
	return nil
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	cfg, rest, handled, code := parseGlobalArgs(args, stdout, stderr)
	if handled {
		return code
	}
	if len(rest) == 0 {
		printHelp(stderr)
		return 2
	}

	client := NewAPIClient(cfg.baseURL, cfg.token, cfg.timeout)

	switch rest[0] {
	case "help":
		printHelp(stdout)
		return 0
	case "health":
		return runHealth(ctx, client, cfg, rest[1:], stdout, stderr)
	case "schedule":
		return runSchedule(ctx, client, cfg, rest[1:], stdout, stderr)
	case "drafts":
		return runDrafts(ctx, client, cfg, rest[1:], stdout, stderr)
	case "posts":
		return runPosts(ctx, client, cfg, rest[1:], stdout, stderr)
	case "accounts":
		return runAccounts(ctx, client, cfg, rest[1:], stdout, stderr)
	case "settings":
		return runSettings(ctx, client, cfg, rest[1:], stdout, stderr)
	case "dlq":
		return runDLQ(ctx, client, cfg, rest[1:], stdout, stderr)
	case "media":
		return runMedia(ctx, client, cfg, rest[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n\n", rest[0])
		printHelp(stderr)
		return 2
	}
}

func parseGlobalArgs(args []string, stdout, stderr io.Writer) (config, []string, bool, int) {
	cfg := config{
		baseURL: envOrDefault("PUBLISHER_BASE_URL", "http://localhost:8080"),
		token:   strings.TrimSpace(os.Getenv("PUBLISHER_API_TOKEN")),
		timeout: 15 * time.Second,
	}

	fs := flag.NewFlagSet("postflow", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&cfg.baseURL, "base-url", cfg.baseURL, "Publisher API base URL")
	fs.StringVar(&cfg.token, "api-token", cfg.token, "Publisher API token (or env PUBLISHER_API_TOKEN)")
	fs.DurationVar(&cfg.timeout, "timeout", cfg.timeout, "HTTP timeout (e.g. 10s)")
	fs.BoolVar(&cfg.asJSON, "json", false, "Print raw JSON output")
	showVersion := fs.Bool("version", false, "Print version")
	showHelp := fs.Bool("help", false, "Show help")
	fs.Usage = func() { printHelp(stderr) }

	if err := fs.Parse(args); err != nil {
		return config{}, nil, true, 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, Version)
		return config{}, nil, true, 0
	}
	if *showHelp {
		printHelp(stdout)
		return config{}, nil, true, 0
	}
	return cfg, fs.Args(), false, 0
}

func runSchedule(ctx context.Context, client *APIClient, cfg config, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "list" {
		fmt.Fprintln(stderr, "usage: postflow schedule list [--from RFC3339] [--to RFC3339]")
		return 2
	}
	fs := flag.NewFlagSet("schedule list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var from string
	var to string
	fs.StringVar(&from, "from", "", "From date (RFC3339)")
	fs.StringVar(&to, "to", "", "To date (RFC3339)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	query := url.Values{}
	if strings.TrimSpace(from) != "" {
		query.Set("from", strings.TrimSpace(from))
	}
	if strings.TrimSpace(to) != "" {
		query.Set("to", strings.TrimSpace(to))
	}
	var out scheduleResponse
	if err := client.Get(ctx, "/schedule", query, &out); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	printOutput(stdout, cfg.asJSON, out, func() {
		fmt.Fprintf(stdout, "from: %s\n", out.From)
		fmt.Fprintf(stdout, "to:   %s\n", out.To)
		fmt.Fprintf(stdout, "items: %d\n", len(out.Items))
		for _, item := range out.Items {
			fmt.Fprintf(stdout, "- [%s] %s %s · %s\n", item.Status, item.ScheduledAt, item.ID, oneLine(item.Text, 90))
		}
	})
	return 0
}

func runPosts(ctx context.Context, client *APIClient, cfg config, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: postflow posts <create|validate|schedule|edit|delete|cancel> [flags]")
		return 2
	}
	switch args[0] {
	case "create":
		return runPostsCreate(ctx, client, cfg, args[1:], stdout, stderr)
	case "validate":
		return runPostsValidate(ctx, client, cfg, args[1:], stdout, stderr)
	case "schedule":
		return runPostsSchedule(ctx, client, cfg, args[1:], stdout, stderr)
	case "edit":
		return runPostsEdit(ctx, client, cfg, args[1:], stdout, stderr)
	case "delete":
		return runPostsDelete(ctx, client, cfg, args[1:], stdout, stderr)
	case "cancel":
		return runPostsCancel(ctx, client, cfg, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown posts subcommand: %s\n", args[0])
		return 2
	}
}

func runPostsCreate(ctx context.Context, client *APIClient, cfg config, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("posts create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var accountID string
	var text string
	var scheduledAt string
	var maxAttempts int
	var idempotencyKey string
	var mediaIDs stringListFlag
	fs.StringVar(&accountID, "account-id", "", "Target account ID")
	fs.StringVar(&text, "text", "", "Post content")
	fs.StringVar(&scheduledAt, "scheduled-at", "", "Scheduled datetime (RFC3339)")
	fs.IntVar(&maxAttempts, "max-attempts", 0, "Max publish retries")
	fs.StringVar(&idempotencyKey, "idempotency-key", "", "Idempotency key")
	fs.Var(&mediaIDs, "media-id", "Media ID (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(text) == "" {
		fmt.Fprintln(stderr, "--text is required")
		return 2
	}
	if strings.TrimSpace(accountID) == "" {
		fmt.Fprintln(stderr, "--account-id is required")
		return 2
	}

	payload := map[string]any{
		"account_id": strings.TrimSpace(accountID),
		"text":       text,
		"media_ids":  []string(mediaIDs),
	}
	if strings.TrimSpace(scheduledAt) != "" {
		payload["scheduled_at"] = strings.TrimSpace(scheduledAt)
	}
	if maxAttempts > 0 {
		payload["max_attempts"] = maxAttempts
	}
	headers := map[string]string{}
	if strings.TrimSpace(idempotencyKey) != "" {
		headers["Idempotency-Key"] = strings.TrimSpace(idempotencyKey)
	}

	var out map[string]any
	var err error
	if len(headers) > 0 {
		err = client.PostWithHeaders(ctx, "/posts", payload, &out, headers)
	} else {
		err = client.Post(ctx, "/posts", payload, &out)
	}
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	printOutput(stdout, cfg.asJSON, out, func() {
		fmt.Fprintf(stdout, "post created/updated: %v\n", out["id"])
	})
	return 0
}

func runPostsValidate(ctx context.Context, client *APIClient, cfg config, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("posts validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var accountID string
	var text string
	var scheduledAt string
	var maxAttempts int
	var mediaIDs stringListFlag
	fs.StringVar(&accountID, "account-id", "", "Target account ID")
	fs.StringVar(&text, "text", "", "Post content")
	fs.StringVar(&scheduledAt, "scheduled-at", "", "Scheduled datetime (RFC3339)")
	fs.IntVar(&maxAttempts, "max-attempts", 0, "Max publish retries")
	fs.Var(&mediaIDs, "media-id", "Media ID (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(text) == "" {
		fmt.Fprintln(stderr, "--text is required")
		return 2
	}
	if strings.TrimSpace(accountID) == "" {
		fmt.Fprintln(stderr, "--account-id is required")
		return 2
	}

	payload := map[string]any{
		"account_id": strings.TrimSpace(accountID),
		"text":       text,
		"media_ids":  []string(mediaIDs),
	}
	if strings.TrimSpace(scheduledAt) != "" {
		payload["scheduled_at"] = strings.TrimSpace(scheduledAt)
	}
	if maxAttempts > 0 {
		payload["max_attempts"] = maxAttempts
	}

	var out map[string]any
	if err := client.Post(ctx, "/posts/validate", payload, &out); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	printOutput(stdout, cfg.asJSON, out, func() {
		fmt.Fprintf(stdout, "valid: %v\n", out["valid"])
	})
	return 0
}

func runDLQ(ctx context.Context, client *APIClient, cfg config, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: postflow dlq <list|requeue|delete> [flags]")
		return 2
	}
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("dlq list", flag.ContinueOnError)
		fs.SetOutput(stderr)
		limit := fs.Int("limit", 100, "Max number of dead letters")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		query := url.Values{}
		if *limit > 0 {
			query.Set("limit", fmt.Sprintf("%d", *limit))
		}
		var out dlqListResponse
		if err := client.Get(ctx, "/dlq", query, &out); err != nil {
			fmt.Fprintln(stderr, err.Error())
			return 1
		}
		printOutput(stdout, cfg.asJSON, out, func() {
			fmt.Fprintf(stdout, "count: %d\n", out.Count)
			for _, item := range out.Items {
				fmt.Fprintf(stdout, "- %s post=%s reason=%s err=%s\n", item.ID, item.PostID, item.Reason, oneLine(item.LastError, 70))
			}
		})
		return 0
	case "requeue":
		fs := flag.NewFlagSet("dlq requeue", flag.ContinueOnError)
		fs.SetOutput(stderr)
		id := fs.String("id", "", "Dead letter ID")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if strings.TrimSpace(*id) == "" {
			fmt.Fprintln(stderr, "--id is required")
			return 2
		}
		var out map[string]any
		if err := client.Post(ctx, "/dlq/"+strings.TrimSpace(*id)+"/requeue", nil, &out); err != nil {
			fmt.Fprintln(stderr, err.Error())
			return 1
		}
		printOutput(stdout, cfg.asJSON, out, func() {
			fmt.Fprintf(stdout, "requeued: %v\n", out["dead_letter_id"])
		})
		return 0
	case "delete":
		fs := flag.NewFlagSet("dlq delete", flag.ContinueOnError)
		fs.SetOutput(stderr)
		id := fs.String("id", "", "Dead letter ID")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if strings.TrimSpace(*id) == "" {
			fmt.Fprintln(stderr, "--id is required")
			return 2
		}
		var out map[string]any
		if err := client.Post(ctx, "/dlq/"+strings.TrimSpace(*id)+"/delete", nil, &out); err != nil {
			fmt.Fprintln(stderr, err.Error())
			return 1
		}
		printOutput(stdout, cfg.asJSON, out, func() {
			fmt.Fprintf(stdout, "deleted: %v\n", out["dead_letter_id"])
		})
		return 0
	default:
		fmt.Fprintf(stderr, "unknown dlq subcommand: %s\n", args[0])
		return 2
	}
}

func runMedia(ctx context.Context, client *APIClient, cfg config, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: postflow media <list|upload|delete> [flags]")
		return 2
	}
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("media list", flag.ContinueOnError)
		fs.SetOutput(stderr)
		limit := fs.Int("limit", 100, "Max number of media items")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		query := url.Values{}
		if *limit > 0 {
			query.Set("limit", fmt.Sprintf("%d", *limit))
		}
		var out mediaListResponse
		if err := client.Get(ctx, "/media", query, &out); err != nil {
			fmt.Fprintln(stderr, err.Error())
			return 1
		}
		printOutput(stdout, cfg.asJSON, out, func() {
			fmt.Fprintf(stdout, "count: %d\n", out.Count)
			for _, item := range out.Items {
				fmt.Fprintf(stdout, "- %s kind=%s size=%d in_use=%t\n", item.ID, item.Kind, item.SizeBytes, item.InUse)
			}
		})
		return 0
	case "upload":
		fs := flag.NewFlagSet("media upload", flag.ContinueOnError)
		fs.SetOutput(stderr)
		filePath := fs.String("file", "", "Path to file")
		kind := fs.String("kind", "video", "Media kind")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if strings.TrimSpace(*filePath) == "" {
			fmt.Fprintln(stderr, "--file is required")
			return 2
		}
		fields := map[string]string{"kind": strings.TrimSpace(*kind)}
		var out map[string]any
		if err := client.PostMultipartFile(ctx, "/media", "file", strings.TrimSpace(*filePath), fields, &out); err != nil {
			fmt.Fprintln(stderr, err.Error())
			return 1
		}
		printOutput(stdout, cfg.asJSON, out, func() {
			fmt.Fprintf(stdout, "uploaded media: %v\n", out["id"])
		})
		return 0
	case "delete":
		fs := flag.NewFlagSet("media delete", flag.ContinueOnError)
		fs.SetOutput(stderr)
		id := fs.String("id", "", "Media ID")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if strings.TrimSpace(*id) == "" {
			fmt.Fprintln(stderr, "--id is required")
			return 2
		}
		var out map[string]any
		if err := client.Delete(ctx, "/media/"+strings.TrimSpace(*id), &out); err != nil {
			fmt.Fprintln(stderr, err.Error())
			return 1
		}
		printOutput(stdout, cfg.asJSON, out, func() {
			fmt.Fprintf(stdout, "deleted media: %s\n", strings.TrimSpace(*id))
		})
		return 0
	default:
		fmt.Fprintf(stderr, "unknown media subcommand: %s\n", args[0])
		return 2
	}
}

func printOutput(w io.Writer, asJSON bool, payload any, human func()) {
	if asJSON {
		raw, _ := json.MarshalIndent(payload, "", "  ")
		fmt.Fprintln(w, string(raw))
		return
	}
	human()
}

func oneLine(text string, maxLen int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if maxLen <= 0 || len(text) <= maxLen {
		return text
	}
	if maxLen < 4 {
		return text[:maxLen]
	}
	return text[:maxLen-3] + "..."
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value != "" {
		return value
	}
	return fallback
}

func printHelp(w io.Writer) {
	fmt.Fprintln(w, `postflow - CLI for PostFlow HTTP API

Usage:
  postflow [global flags] <command> [subcommand] [flags]

Global flags:
  --base-url string      Publisher API base URL (default: $PUBLISHER_BASE_URL or http://localhost:8080)
  --api-token string     API token (default: $PUBLISHER_API_TOKEN)
  --timeout duration     HTTP timeout (default: 15s)
  --json                 Print raw JSON output
  --version              Print version
  --help                 Show this help

Commands:
  health                 Check service health via /healthz
  schedule list          List scheduled posts from /schedule
  drafts list            List drafts via /drafts
  posts create           Create post via /posts
  posts validate         Validate payload via /posts/validate
  posts schedule         Schedule a draft via /posts/{id}/schedule
  posts edit             Edit an editable post via /posts/{id}/edit
  posts delete           Delete an editable post via /posts/{id}/delete
  posts cancel           Cancel scheduled post via /posts/{id}/cancel
  accounts list          List accounts via /accounts
  accounts create-static Create/update static account via /accounts/static
  accounts connect       Mark account connected via /accounts/{id}/connect
  accounts disconnect    Mark account disconnected via /accounts/{id}/disconnect
  accounts x-premium     Set X premium via /accounts/{id}/x-premium
  accounts delete        Delete disconnected account via /accounts/{id}
  settings set-timezone  Set UI timezone via /settings/timezone
  dlq list               List failed dead letters via /dlq
  dlq requeue            Requeue one dead letter via /dlq/{id}/requeue
  dlq delete             Delete one dead letter via /dlq/{id}/delete
  media list             List media via /media
  media upload           Upload media via /media (multipart)
  media delete           Delete media via /media/{id}

Examples:
  postflow schedule list --from 2026-03-01T00:00:00Z --to 2026-03-31T23:59:59Z
  postflow posts create --account-id acc_abc123 --text "hello world" --scheduled-at 2026-03-01T10:00:00Z
  postflow posts validate --account-id acc_abc123 --text "draft check" --scheduled-at 2026-03-01T10:00:00Z
  postflow posts schedule --id pst_abc123 --scheduled-at 2026-03-01T10:00:00Z
  postflow posts edit --id pst_abc123 --text "updated copy" --intent schedule --scheduled-at 2026-03-01T10:30:00Z
  postflow posts delete --id pst_abc123
  postflow dlq list --limit 50
  postflow dlq requeue --id dlq_abc123`)
}
