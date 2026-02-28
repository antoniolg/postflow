package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	postsapp "github.com/antoniolg/publisher/internal/application/posts"
	"github.com/antoniolg/publisher/internal/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultMCPListLimit = 200
	maxMCPListLimit     = 500
)

func (s Server) newMCPHandler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "publisher-mcp",
		Version: "0.1.0",
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "publisher_list_schedule",
		Description: "List posts in the schedule window. Supports RFC3339 from/to filters.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, s.mcpListScheduleTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "publisher_list_drafts",
		Description: "List draft posts (no scheduled date).",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, s.mcpListDraftsTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "publisher_list_failed",
		Description: "List failed posts from dead letters with latest error details.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, s.mcpListFailedTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "publisher_create_post",
		Description: "Create a post as draft (no scheduled_at) or scheduled (with scheduled_at).",
		Annotations: &mcp.ToolAnnotations{
			IdempotentHint: false,
		},
	}, s.mcpCreatePostTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "publisher_upload_media",
		Description: "Upload media and return media_id. Provide either content_base64 or file_path.",
		Annotations: &mcp.ToolAnnotations{
			IdempotentHint: false,
		},
	}, s.mcpUploadMediaTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "publisher_list_media",
		Description: "List uploaded media with usage status (in use or deletable).",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}, s.mcpListMediaTool)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "publisher_delete_media",
		Description: "Delete an uploaded media item if it is not attached to any post.",
		Annotations: &mcp.ToolAnnotations{
			IdempotentHint: false,
		},
	}, s.mcpDeleteMediaTool)

	base := mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server {
		return server
	}, nil)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Compatibility shim for clients that send only application/json.
		// Streamable HTTP requires both JSON and event-stream in Accept.
		if r.Method == http.MethodPost && strings.EqualFold(strings.TrimSpace(r.Header.Get("Accept")), "application/json") {
			r2 := r.Clone(r.Context())
			r2.Header = r.Header.Clone()
			r2.Header.Set("Accept", "application/json, text/event-stream")
			r = r2
		}
		base.ServeHTTP(w, r)
	})
}

type mcpListScheduleInput struct {
	From  string `json:"from,omitempty" jsonschema:"RFC3339 start date filter (optional)."`
	To    string `json:"to,omitempty" jsonschema:"RFC3339 end date filter (optional)."`
	Limit int    `json:"limit,omitempty" jsonschema:"Max items to return (1-500). Default: 200."`
}

type mcpListDraftsInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"Max items to return (1-500). Default: 200."`
}

type mcpListFailedInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"Max items to return (1-500). Default: 200."`
}

type mcpCreatePostInput struct {
	AccountID      string   `json:"account_id" jsonschema:"Target connected account ID."`
	Text           string   `json:"text" jsonschema:"Post text content."`
	ScheduledAt    string   `json:"scheduled_at,omitempty" jsonschema:"RFC3339 or datetime-local value. Empty means draft."`
	MediaIDs       []string `json:"media_ids,omitempty" jsonschema:"Existing media IDs to attach."`
	MaxAttempts    int      `json:"max_attempts,omitempty" jsonschema:"Max publish retries. Default from server config."`
	IdempotencyKey string   `json:"idempotency_key,omitempty" jsonschema:"Optional idempotency key (max 128 chars)."`
}

type mcpPostSummary struct {
	ID          string `json:"id"`
	AccountID   string `json:"account_id"`
	Platform    string `json:"platform"`
	Status      string `json:"status"`
	Text        string `json:"text"`
	ScheduledAt string `json:"scheduled_at,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
	UpdatedAt   string `json:"updated_at"`
	MediaCount  int    `json:"media_count"`
	Attempts    int    `json:"attempts"`
	MaxAttempts int    `json:"max_attempts"`
}

type mcpListScheduleOutput struct {
	Count int              `json:"count"`
	From  string           `json:"from"`
	To    string           `json:"to"`
	Posts []mcpPostSummary `json:"posts"`
}

type mcpListDraftsOutput struct {
	Count  int              `json:"count"`
	Drafts []mcpPostSummary `json:"drafts"`
}

type mcpFailedSummary struct {
	DeadLetterID string `json:"dead_letter_id"`
	PostID       string `json:"post_id"`
	Status       string `json:"status"`
	Text         string `json:"text"`
	LastError    string `json:"last_error"`
	Reason       string `json:"reason"`
	Attempts     int    `json:"attempts"`
	MaxAttempts  int    `json:"max_attempts"`
	ScheduledAt  string `json:"scheduled_at,omitempty"`
	FailedAt     string `json:"failed_at"`
}

type mcpListFailedOutput struct {
	Count int                `json:"count"`
	Items []mcpFailedSummary `json:"items"`
}

type mcpCreatePostOutput struct {
	Created bool           `json:"created"`
	Post    mcpPostSummary `json:"post"`
}

func (s Server) mcpListScheduleTool(ctx context.Context, _ *mcp.CallToolRequest, in mcpListScheduleInput) (*mcp.CallToolResult, mcpListScheduleOutput, error) {
	from, to, err := parseRange(ctx, strings.TrimSpace(in.From), strings.TrimSpace(in.To))
	if err != nil {
		return nil, mcpListScheduleOutput{}, err
	}
	items, err := s.Store.ListSchedule(ctx, from, to)
	if err != nil {
		return nil, mcpListScheduleOutput{}, err
	}

	limit := clampMCPListLimit(in.Limit)
	if len(items) > limit {
		items = items[:limit]
	}

	out := mcpListScheduleOutput{
		Count: len(items),
		From:  from.UTC().Format(time.RFC3339),
		To:    to.UTC().Format(time.RFC3339),
		Posts: make([]mcpPostSummary, 0, len(items)),
	}
	for _, item := range items {
		out.Posts = append(out.Posts, toMCPPostSummary(item))
	}
	return nil, out, nil
}

func (s Server) mcpListDraftsTool(ctx context.Context, _ *mcp.CallToolRequest, in mcpListDraftsInput) (*mcp.CallToolResult, mcpListDraftsOutput, error) {
	drafts, err := s.Store.ListDrafts(ctx)
	if err != nil {
		return nil, mcpListDraftsOutput{}, err
	}

	limit := clampMCPListLimit(in.Limit)
	if len(drafts) > limit {
		drafts = drafts[:limit]
	}

	out := mcpListDraftsOutput{
		Count:  len(drafts),
		Drafts: make([]mcpPostSummary, 0, len(drafts)),
	}
	for _, item := range drafts {
		out.Drafts = append(out.Drafts, toMCPPostSummary(item))
	}
	return nil, out, nil
}

func (s Server) mcpListFailedTool(ctx context.Context, _ *mcp.CallToolRequest, in mcpListFailedInput) (*mcp.CallToolResult, mcpListFailedOutput, error) {
	limit := clampMCPListLimit(in.Limit)
	deadLetters, err := s.Store.ListDeadLetters(ctx, limit)
	if err != nil {
		return nil, mcpListFailedOutput{}, err
	}

	out := mcpListFailedOutput{
		Items: make([]mcpFailedSummary, 0, len(deadLetters)),
	}
	for _, item := range deadLetters {
		post, err := s.Store.GetPost(ctx, item.PostID)
		if err != nil {
			continue
		}
		out.Items = append(out.Items, mcpFailedSummary{
			DeadLetterID: item.ID,
			PostID:       post.ID,
			Status:       string(post.Status),
			Text:         strings.TrimSpace(post.Text),
			LastError:    strings.TrimSpace(item.LastError),
			Reason:       strings.TrimSpace(item.Reason),
			Attempts:     post.Attempts,
			MaxAttempts:  post.MaxAttempts,
			ScheduledAt:  formatMCPTime(post.ScheduledAt),
			FailedAt:     formatMCPTime(item.AttemptedAt),
		})
	}
	out.Count = len(out.Items)
	return nil, out, nil
}

func (s Server) mcpCreatePostTool(ctx context.Context, _ *mcp.CallToolRequest, in mcpCreatePostInput) (*mcp.CallToolResult, mcpCreatePostOutput, error) {
	if strings.TrimSpace(in.AccountID) == "" {
		return nil, mcpCreatePostOutput{}, fmt.Errorf("account_id is required")
	}

	text := strings.TrimSpace(in.Text)
	if text == "" {
		return nil, mcpCreatePostOutput{}, fmt.Errorf("text is required")
	}

	uiLoc, _, _, err := s.resolveUILocation(ctx)
	if err != nil {
		return nil, mcpCreatePostOutput{}, err
	}
	scheduledAt, err := parseScheduledAtInputInLocation(strings.TrimSpace(in.ScheduledAt), uiLoc)
	if err != nil {
		return nil, mcpCreatePostOutput{}, err
	}

	mediaIDs := cleanMCPMediaIDs(in.MediaIDs)

	createService := postsapp.CreateService{
		Store:             s.Store,
		Registry:          s.providerRegistry(),
		DefaultMaxRetries: s.DefaultMaxRetries,
	}
	createOut, err := createService.Create(ctx, postsapp.CreateInput{
		AccountIDs:     []string{in.AccountID},
		Text:           text,
		ScheduledAt:    scheduledAt,
		MediaIDs:       mediaIDs,
		MaxAttempts:    in.MaxAttempts,
		IdempotencyKey: strings.TrimSpace(in.IdempotencyKey),
	})
	if err != nil {
		return nil, mcpCreatePostOutput{}, err
	}
	if len(createOut.Items) != 1 {
		return nil, mcpCreatePostOutput{}, fmt.Errorf("expected single create result, got %d", len(createOut.Items))
	}
	item := createOut.Items[0]

	return nil, mcpCreatePostOutput{
		Created: item.Created,
		Post:    toMCPPostSummary(item.Post),
	}, nil
}

func toMCPPostSummary(post domain.Post) mcpPostSummary {
	return mcpPostSummary{
		ID:          post.ID,
		AccountID:   post.AccountID,
		Platform:    string(post.Platform),
		Status:      string(post.Status),
		Text:        strings.TrimSpace(post.Text),
		ScheduledAt: formatMCPTime(post.ScheduledAt),
		PublishedAt: formatMCPTimePtr(post.PublishedAt),
		UpdatedAt:   formatMCPTime(post.UpdatedAt),
		MediaCount:  len(post.Media),
		Attempts:    post.Attempts,
		MaxAttempts: post.MaxAttempts,
	}
}

func formatMCPTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func formatMCPTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return formatMCPTime(*t)
}

func clampMCPListLimit(limit int) int {
	if limit <= 0 {
		return defaultMCPListLimit
	}
	if limit > maxMCPListLimit {
		return maxMCPListLimit
	}
	return limit
}

func cleanMCPMediaIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func (s Server) mcpSettingsInfo(r *http.Request) (url string, authHint string, configJSON string, claudeCommand string, codexCommand string, codexConfigTOML string) {
	url = requestBaseURL(r) + "/mcp"
	authHeader := ""

	apiTokenConfigured := strings.TrimSpace(s.APIToken) != ""
	basicConfigured := strings.TrimSpace(s.UIBasicUser) != "" || strings.TrimSpace(s.UIBasicPass) != ""
	switch {
	case apiTokenConfigured:
		authHint = "auth: usa Authorization: Bearer <API_TOKEN> (recomendado)."
		authHeader = "Bearer <API_TOKEN>"
	case basicConfigured:
		authHint = "auth: endpoint protegido con Basic Auth. Usa Authorization: Basic <BASE64_USER_PASS>."
		authHeader = "Basic <BASE64_USER_PASS>"
	default:
		authHint = "auth: endpoint abierto. Recomendado configurar API_TOKEN."
	}

	claudeCommand = fmt.Sprintf("claude mcp add -t http publisher %s", url)
	if authHeader != "" {
		claudeCommand = fmt.Sprintf(`%s --header "Authorization: %s"`, claudeCommand, authHeader)
	}
	codexCommand = fmt.Sprintf("codex mcp add publisher --url %s", url)

	serverCfg := map[string]any{
		"transport": "streamable_http",
		"url":       url,
	}
	if authHeader != "" {
		serverCfg["headers"] = map[string]string{
			"Authorization": authHeader,
		}
	}
	raw, err := json.MarshalIndent(map[string]any{
		"mcpServers": map[string]any{
			"publisher": serverCfg,
		},
	}, "", "  ")
	if err != nil {
		configJSON = `{"mcpServers":{"publisher":{"transport":"streamable_http","url":"` + url + `"}}}`
	} else {
		configJSON = string(raw)
	}

	if apiTokenConfigured {
		codexConfigTOML = strings.TrimSpace(fmt.Sprintf(`
[mcp_servers.publisher]
url = %q
bearer_token_env_var = "PUBLISHER_API_TOKEN"
`, url))
	} else {
		codexConfigTOML = strings.TrimSpace(fmt.Sprintf(`
[mcp_servers.publisher]
url = %q
`, url))
	}

	return url, authHint, configJSON, claudeCommand, codexCommand, codexConfigTOML
}

func requestBaseURL(r *http.Request) string {
	if r == nil {
		return "http://localhost:8080"
	}

	scheme := "http"
	if proto := firstCSVHeaderValue(r.Header.Get("X-Forwarded-Proto")); proto != "" {
		switch strings.ToLower(proto) {
		case "http", "https":
			scheme = strings.ToLower(proto)
		}
	} else if r.TLS != nil {
		scheme = "https"
	}

	host := firstCSVHeaderValue(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if host == "" {
		host = "localhost:8080"
	}

	return scheme + "://" + host
}

func firstCSVHeaderValue(raw string) string {
	if raw == "" {
		return ""
	}
	parts := strings.Split(raw, ",")
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[0])
}
