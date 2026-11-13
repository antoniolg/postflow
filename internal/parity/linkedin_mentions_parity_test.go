package parity_test

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/antoniolg/postflow/internal/domain"
)

// fakeLinkedInPostsHandler stands in for the LinkedIn Posts API: it returns one
// post with a mention for any organization author.
func fakeLinkedInPostsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/rest/posts" || !strings.HasPrefix(r.URL.Query().Get("author"), "urn:li:organization:") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"elements":[{"id":"urn:li:share:7","commentary":"Con @[Ana\\_Dev](urn:li:person:Dk968RHxiO)"}]}`)
	})
}

func (e *parityEnv) seedLinkedInOrganizationAccount(externalID string) string {
	e.t.Helper()
	raw, status := e.apiJSON(http.MethodPost, "/accounts/static", map[string]any{
		"platform":            "linkedin",
		"account_kind":        string(domain.AccountKindOrganization),
		"display_name":        "Parity page",
		"external_account_id": externalID,
		"credentials":         map[string]any{"access_token": "tok_" + externalID},
	}, "application/json")
	if status != http.StatusCreated && status != http.StatusOK {
		e.t.Fatalf("create linkedin organization account status=%d body=%s", status, string(raw))
	}
	var out struct {
		ID string `json:"id"`
	}
	mustJSON(e.t, raw, &out)
	return out.ID
}

type parityMentions struct {
	Count int `json:"count"`
	Items []struct {
		Name    string `json:"name"`
		URN     string `json:"urn"`
		PostURN string `json:"post_urn"`
	} `json:"items"`
}

func TestLinkedInMentionsParity(t *testing.T) {
	env := newParityEnv(t)
	id := env.seedLinkedInOrganizationAccount("110315077")

	raw, status := env.apiJSON(http.MethodGet, "/accounts/"+id+"/linkedin/mentions?limit=5", nil, "")
	if status != http.StatusOK {
		t.Fatalf("api status=%d body=%s", status, raw)
	}
	var fromAPI, fromCLI, fromMCP parityMentions
	mustJSON(t, raw, &fromAPI)
	mustJSON(t, env.runCLI("linkedin", "mentions", "--account-id", id, "--limit", "5"), &fromCLI)
	structured, _ := json.Marshal(env.mcpCallTool("postflow_list_linkedin_mentions", map[string]any{"account_id": id, "limit": 5}))
	mustJSON(t, structured, &fromMCP)

	if fromAPI.Count != 1 || fromAPI.Items[0].Name != "Ana_Dev" || fromAPI.Items[0].URN != "urn:li:person:Dk968RHxiO" || fromAPI.Items[0].PostURN != "urn:li:share:7" {
		t.Fatalf("unexpected api mentions: %+v", fromAPI)
	}
	if !reflect.DeepEqual(fromAPI, fromCLI) || !reflect.DeepEqual(fromAPI, fromMCP) {
		t.Fatalf("surfaces differ\napi=%+v\ncli=%+v\nmcp=%+v", fromAPI, fromCLI, fromMCP)
	}
}

func TestLinkedInMentionsRejectsNonOrganizationAccountsOnEverySurface(t *testing.T) {
	env := newParityEnv(t)
	id := env.account.ID // an X account

	raw, status := env.apiJSON(http.MethodGet, "/accounts/"+id+"/linkedin/mentions", nil, "")
	if status != http.StatusBadRequest || !strings.Contains(string(raw), "linkedin organization") {
		t.Fatalf("api: expected 400 about linkedin organization accounts, got %d %s", status, raw)
	}
	code, _, stderr := env.runCLIResult("linkedin", "mentions", "--account-id", id)
	if code != 1 || !strings.Contains(stderr, "linkedin organization") {
		t.Fatalf("cli: expected exit 1 about linkedin organization accounts, got %d %q", code, stderr)
	}
	if msg := env.mcpCallToolError("postflow_list_linkedin_mentions", map[string]any{"account_id": id}); !strings.Contains(msg, "linkedin organization") {
		t.Fatalf("mcp: expected an error about linkedin organization accounts, got %q", msg)
	}
	if _, status := env.apiJSON(http.MethodGet, "/accounts/acc_missing/linkedin/mentions", nil, ""); status != http.StatusNotFound {
		t.Fatalf("api: expected 404 for a missing account, got %d", status)
	}
}
