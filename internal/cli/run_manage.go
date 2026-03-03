package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
)

type healthResponse struct {
	Status string `json:"status"`
}

type draftsListResponse struct {
	Count  int       `json:"count"`
	Drafts []postDTO `json:"drafts"`
}

type accountsListResponse struct {
	Count int          `json:"count"`
	Items []accountDTO `json:"items"`
}

type accountDTO struct {
	ID                string `json:"id"`
	Platform          string `json:"platform"`
	DisplayName       string `json:"display_name"`
	ExternalAccountID string `json:"external_account_id"`
	XPremium          bool   `json:"x_premium"`
	AuthMethod        string `json:"auth_method"`
	Status            string `json:"status"`
	LastError         string `json:"last_error"`
}

func runHealth(ctx context.Context, client *APIClient, cfg config, args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "usage: postflow health")
		return 2
	}
	var out healthResponse
	if err := client.Get(ctx, "/healthz", nil, &out); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	printOutput(stdout, cfg.asJSON, out, func() {
		fmt.Fprintf(stdout, "status: %s\n", strings.TrimSpace(out.Status))
	})
	return 0
}

func runDrafts(ctx context.Context, client *APIClient, cfg config, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "list" {
		fmt.Fprintln(stderr, "usage: postflow drafts list [--limit N]")
		return 2
	}
	fs := flag.NewFlagSet("drafts list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	limit := fs.Int("limit", 200, "Max number of draft posts")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	query := url.Values{}
	if *limit > 0 {
		query.Set("limit", fmt.Sprintf("%d", *limit))
	}
	var out draftsListResponse
	if err := client.Get(ctx, "/drafts", query, &out); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	printOutput(stdout, cfg.asJSON, out, func() {
		fmt.Fprintf(stdout, "count: %d\n", out.Count)
		for _, draft := range out.Drafts {
			fmt.Fprintf(stdout, "- [%s] %s · %s\n", draft.Platform, draft.ID, oneLine(draft.Text, 90))
		}
	})
	return 0
}

func runPostsSchedule(ctx context.Context, client *APIClient, cfg config, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("posts schedule", flag.ContinueOnError)
	fs.SetOutput(stderr)
	id := fs.String("id", "", "Draft post ID")
	scheduledAt := fs.String("scheduled-at", "", "Scheduled datetime (RFC3339 or datetime-local)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	postID := strings.TrimSpace(*id)
	if postID == "" {
		fmt.Fprintln(stderr, "--id is required")
		return 2
	}
	scheduled := strings.TrimSpace(*scheduledAt)
	if scheduled == "" {
		fmt.Fprintln(stderr, "--scheduled-at is required")
		return 2
	}

	payload := map[string]any{"scheduled_at": scheduled}
	var out map[string]any
	if err := client.Post(ctx, "/posts/"+postID+"/schedule", payload, &out); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	printOutput(stdout, cfg.asJSON, out, func() {
		fmt.Fprintf(stdout, "scheduled post: %s\n", postID)
	})
	return 0
}

func runPostsEdit(ctx context.Context, client *APIClient, cfg config, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("posts edit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	id := fs.String("id", "", "Editable post ID")
	text := fs.String("text", "", "Updated post text")
	intent := fs.String("intent", "", "Optional intent: draft|schedule|publish_now")
	scheduledAt := fs.String("scheduled-at", "", "Optional scheduled datetime (RFC3339 or datetime-local)")
	replaceMedia := fs.Bool("replace-media", false, "Replace media IDs. Use with repeated --media-id; can be empty to clear all media")
	var mediaIDs stringListFlag
	fs.Var(&mediaIDs, "media-id", "Replacement media ID (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	postID := strings.TrimSpace(*id)
	if postID == "" {
		fmt.Fprintln(stderr, "--id is required")
		return 2
	}
	content := strings.TrimSpace(*text)
	if content == "" {
		fmt.Fprintln(stderr, "--text is required")
		return 2
	}

	payload := map[string]any{"text": content}
	if trimmed := strings.TrimSpace(*intent); trimmed != "" {
		payload["intent"] = trimmed
	}
	if trimmed := strings.TrimSpace(*scheduledAt); trimmed != "" {
		payload["scheduled_at"] = trimmed
	}
	if *replaceMedia {
		payload["media_ids"] = append([]string{}, mediaIDs...)
	}

	var out map[string]any
	if err := client.Post(ctx, "/posts/"+postID+"/edit", payload, &out); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	printOutput(stdout, cfg.asJSON, out, func() {
		fmt.Fprintf(stdout, "edited post: %s\n", postID)
	})
	return 0
}

func runPostsDelete(ctx context.Context, client *APIClient, cfg config, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("posts delete", flag.ContinueOnError)
	fs.SetOutput(stderr)
	id := fs.String("id", "", "Editable post ID")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	postID := strings.TrimSpace(*id)
	if postID == "" {
		fmt.Fprintln(stderr, "--id is required")
		return 2
	}

	var out map[string]any
	if err := client.Post(ctx, "/posts/"+postID+"/delete", map[string]any{}, &out); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	printOutput(stdout, cfg.asJSON, out, func() {
		fmt.Fprintf(stdout, "deleted post: %s\n", postID)
	})
	return 0
}

func runPostsCancel(ctx context.Context, client *APIClient, cfg config, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("posts cancel", flag.ContinueOnError)
	fs.SetOutput(stderr)
	id := fs.String("id", "", "Post ID")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	postID := strings.TrimSpace(*id)
	if postID == "" {
		fmt.Fprintln(stderr, "--id is required")
		return 2
	}
	var out map[string]any
	if err := client.Post(ctx, "/posts/"+postID+"/cancel", nil, &out); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	printOutput(stdout, cfg.asJSON, out, func() {
		fmt.Fprintf(stdout, "canceled post: %s\n", postID)
	})
	return 0
}

func runAccounts(ctx context.Context, client *APIClient, cfg config, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: postflow accounts <list|create-static|connect|disconnect|x-premium|delete> [flags]")
		return 2
	}
	switch args[0] {
	case "list":
		var out accountsListResponse
		if err := client.Get(ctx, "/accounts", nil, &out); err != nil {
			fmt.Fprintln(stderr, err.Error())
			return 1
		}
		printOutput(stdout, cfg.asJSON, out, func() {
			fmt.Fprintf(stdout, "count: %d\n", out.Count)
			for _, item := range out.Items {
				fmt.Fprintf(stdout, "- %s %s [%s] status=%s premium=%t\n", item.ID, item.Platform, item.ExternalAccountID, item.Status, item.XPremium)
			}
		})
		return 0
	case "create-static":
		return runAccountsCreateStatic(ctx, client, cfg, args[1:], stdout, stderr)
	case "connect":
		return runAccountStatusMutation(ctx, client, cfg, args[1:], stdout, stderr, "connect")
	case "disconnect":
		return runAccountStatusMutation(ctx, client, cfg, args[1:], stdout, stderr, "disconnect")
	case "x-premium":
		return runAccountSetXPremium(ctx, client, cfg, args[1:], stdout, stderr)
	case "delete":
		return runAccountDelete(ctx, client, cfg, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown accounts subcommand: %s\n", args[0])
		return 2
	}
}

func runAccountsCreateStatic(ctx context.Context, client *APIClient, cfg config, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("accounts create-static", flag.ContinueOnError)
	fs.SetOutput(stderr)
	platform := fs.String("platform", "", "Platform: x|linkedin|facebook|instagram")
	displayName := fs.String("display-name", "", "Display name")
	externalAccountID := fs.String("external-account-id", "", "External account id")
	premiumRaw := fs.String("x-premium", "", "Optional: true|false for x accounts")
	var credentialPairs stringListFlag
	fs.Var(&credentialPairs, "credential", "Credential key=value (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	credentials, err := parseCredentialPairs(credentialPairs)
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 2
	}
	if len(credentials) == 0 {
		fmt.Fprintln(stderr, "--credential is required")
		return 2
	}

	payload := map[string]any{
		"platform":            strings.TrimSpace(*platform),
		"display_name":        strings.TrimSpace(*displayName),
		"external_account_id": strings.TrimSpace(*externalAccountID),
		"credentials":         credentials,
	}
	var out accountDTO
	if err := client.Post(ctx, "/accounts/static", payload, &out); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}

	if strings.TrimSpace(*premiumRaw) != "" {
		enabled, parseErr := strconv.ParseBool(strings.TrimSpace(*premiumRaw))
		if parseErr != nil {
			fmt.Fprintln(stderr, "--x-premium must be true|false")
			return 2
		}
		if err := client.Post(ctx, "/accounts/"+strings.TrimSpace(out.ID)+"/x-premium", map[string]any{"x_premium": enabled}, nil); err != nil {
			fmt.Fprintln(stderr, err.Error())
			return 1
		}
		out.XPremium = enabled
	}

	printOutput(stdout, cfg.asJSON, out, func() {
		fmt.Fprintf(stdout, "account upserted: %s (%s)\n", strings.TrimSpace(out.ID), strings.TrimSpace(out.Platform))
	})
	return 0
}

func runAccountStatusMutation(ctx context.Context, client *APIClient, cfg config, args []string, stdout, stderr io.Writer, action string) int {
	fs := flag.NewFlagSet("accounts "+action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	id := fs.String("id", "", "Account ID")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	accountID := strings.TrimSpace(*id)
	if accountID == "" {
		fmt.Fprintln(stderr, "--id is required")
		return 2
	}
	var out map[string]any
	if err := client.Post(ctx, "/accounts/"+accountID+"/"+action, nil, &out); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	printOutput(stdout, cfg.asJSON, out, func() {
		fmt.Fprintf(stdout, "%s account: %s\n", action, accountID)
	})
	return 0
}

func runAccountSetXPremium(ctx context.Context, client *APIClient, cfg config, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("accounts x-premium", flag.ContinueOnError)
	fs.SetOutput(stderr)
	id := fs.String("id", "", "Account ID")
	enabledRaw := fs.String("enabled", "", "true|false")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	accountID := strings.TrimSpace(*id)
	if accountID == "" {
		fmt.Fprintln(stderr, "--id is required")
		return 2
	}
	if strings.TrimSpace(*enabledRaw) == "" {
		fmt.Fprintln(stderr, "--enabled is required")
		return 2
	}
	enabled, err := strconv.ParseBool(strings.TrimSpace(*enabledRaw))
	if err != nil {
		fmt.Fprintln(stderr, "--enabled must be true|false")
		return 2
	}
	var out map[string]any
	if err := client.Post(ctx, "/accounts/"+accountID+"/x-premium", map[string]any{"x_premium": enabled}, &out); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	printOutput(stdout, cfg.asJSON, out, func() {
		fmt.Fprintf(stdout, "x premium updated: %s -> %t\n", accountID, enabled)
	})
	return 0
}

func runAccountDelete(ctx context.Context, client *APIClient, cfg config, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("accounts delete", flag.ContinueOnError)
	fs.SetOutput(stderr)
	id := fs.String("id", "", "Account ID")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	accountID := strings.TrimSpace(*id)
	if accountID == "" {
		fmt.Fprintln(stderr, "--id is required")
		return 2
	}
	var out map[string]any
	if err := client.Delete(ctx, "/accounts/"+accountID, &out); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	printOutput(stdout, cfg.asJSON, out, func() {
		fmt.Fprintf(stdout, "deleted account: %s\n", accountID)
	})
	return 0
}

func runSettings(ctx context.Context, client *APIClient, cfg config, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "set-timezone" {
		fmt.Fprintln(stderr, "usage: postflow settings set-timezone --timezone <IANA>")
		return 2
	}
	fs := flag.NewFlagSet("settings set-timezone", flag.ContinueOnError)
	fs.SetOutput(stderr)
	timezone := fs.String("timezone", "", "IANA timezone, e.g. Europe/Madrid")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if strings.TrimSpace(*timezone) == "" {
		fmt.Fprintln(stderr, "--timezone is required")
		return 2
	}
	var out map[string]any
	if err := client.Post(ctx, "/settings/timezone", map[string]any{"timezone": strings.TrimSpace(*timezone)}, &out); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	printOutput(stdout, cfg.asJSON, out, func() {
		fmt.Fprintf(stdout, "timezone updated: %s\n", strings.TrimSpace(*timezone))
	})
	return 0
}

func parseCredentialPairs(values []string) (map[string]string, error) {
	credentials := make(map[string]string, len(values))
	for _, raw := range values {
		parts := strings.SplitN(strings.TrimSpace(raw), "=", 2)
		if len(parts) != 2 {
			return nil, errors.New("--credential must be key=value")
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key == "" || value == "" {
			return nil, errors.New("--credential must include non-empty key and value")
		}
		credentials[key] = value
	}
	return credentials, nil
}
