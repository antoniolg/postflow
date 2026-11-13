package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
)

type linkedInMentionDTO struct {
	Name    string `json:"name"`
	URN     string `json:"urn"`
	PostURN string `json:"post_urn"`
}

type linkedInMentionsResponse struct {
	AccountID string               `json:"account_id"`
	Count     int                  `json:"count"`
	Items     []linkedInMentionDTO `json:"items"`
}

func runLinkedIn(ctx context.Context, client *APIClient, cfg config, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: postflow linkedin <mentions> [flags]")
		return 2
	}
	switch args[0] {
	case "mentions":
		fs := flag.NewFlagSet("linkedin mentions", flag.ContinueOnError)
		fs.SetOutput(stderr)
		accountID := fs.String("account-id", "", "LinkedIn organization account ID")
		limit := fs.Int("limit", 0, "How many recent posts to read (default 20, max 100)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		id := strings.TrimSpace(*accountID)
		if id == "" {
			fmt.Fprintln(stderr, "--account-id is required")
			return 2
		}
		query := url.Values{}
		if *limit > 0 {
			query.Set("limit", strconv.Itoa(*limit))
		}
		var out linkedInMentionsResponse
		if err := client.Get(ctx, "/accounts/"+url.PathEscape(id)+"/linkedin/mentions", query, &out); err != nil {
			fmt.Fprintln(stderr, err.Error())
			return 1
		}
		printOutput(stdout, cfg.asJSON, out, func() {
			fmt.Fprintf(stdout, "count: %d\n", out.Count)
			for _, item := range out.Items {
				fmt.Fprintf(stdout, "- @[%s](%s) post=%s\n", item.Name, item.URN, item.PostURN)
			}
		})
		return 0
	default:
		fmt.Fprintf(stderr, "unknown linkedin subcommand: %s\n", args[0])
		return 2
	}
}
