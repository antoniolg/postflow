package postflow

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/antoniolg/postflow/internal/domain"
)

func TestLinkedInMentionsToAttributes(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		wantText  string
		wantAttrs string
	}{
		{name: "no mentions", in: "Hola @antoniolg [x](y)", wantText: "Hola @antoniolg [x](y)", wantAttrs: "null"},
		{
			name:     "person and organization counted in utf-16 units",
			in:       "🚀 Con @[Antonio Leiva](urn:li:person:Ab_1-x) y @[Welcome to La Secta](urn:li:organization:110315077).",
			wantText: "🚀 Con Antonio Leiva y Welcome to La Secta.",
			wantAttrs: `[{"length":13,"start":7,"value":{"com.linkedin.common.MemberAttributedEntity":{"member":"urn:li:person:Ab_1-x"}}},` +
				`{"length":19,"start":23,"value":{"com.linkedin.common.CompanyAttributedEntity":{"company":"urn:li:organization:110315077"}}}]`,
		},
		{name: "other urn types stay literal", in: "@[X](urn:li:company:1)", wantText: "@[X](urn:li:company:1)", wantAttrs: "null"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, attrs := linkedInMentionsToAttributes(tt.in)
			if text != tt.wantText {
				t.Fatalf("text\n got %q\nwant %q", text, tt.wantText)
			}
			raw, _ := json.Marshal(attrs)
			if string(raw) != tt.wantAttrs {
				t.Fatalf("attributes\n got %s\nwant %s", raw, tt.wantAttrs)
			}
		})
	}
}

func TestLinkedInPlainTextPathsSendMentionsAsAttributes(t *testing.T) {
	const text = "Episodio con @[Antonio](urn:li:person:Ab_1_x) y @[Welcome to La Secta](urn:li:organization:42)"
	const wantText = "Episodio con Antonio y Welcome to La Secta"
	const wantAttrs = `[{"length":7,"start":13,"value":{"com.linkedin.common.MemberAttributedEntity":{"member":"urn:li:person:Ab_1_x"}}},` +
		`{"length":19,"start":23,"value":{"com.linkedin.common.CompanyAttributedEntity":{"company":"urn:li:organization:42"}}}]`
	type attributedText struct {
		Text       string          `json:"text"`
		Attributes json.RawMessage `json:"attributes"`
	}
	tests := []struct {
		name string
		opts PublishOptions
	}{
		{name: "ugc post", opts: PublishOptions{}},
		{name: "comment", opts: PublishOptions{Mode: PublishModeComment, ParentExternalID: "urn:li:ugcPost:1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sent attributedText
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload struct {
					SpecificContent struct {
						ShareContent struct {
							ShareCommentary attributedText `json:"shareCommentary"`
						} `json:"com.linkedin.ugc.ShareContent"`
					} `json:"specificContent"`
					Message attributedText `json:"message"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Errorf("decode payload: %v", err)
				}
				switch {
				case r.URL.Path == "/v2/ugcPosts":
					sent = payload.SpecificContent.ShareContent.ShareCommentary
					w.Header().Set("x-restli-id", "urn:li:share:1")
					w.WriteHeader(http.StatusCreated)
				case strings.HasSuffix(r.URL.Path, "/comments"):
					sent = payload.Message
					_, _ = io.WriteString(w, `{"commentUrn":"urn:li:comment:(urn:li:ugcPost:1,2)"}`)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			provider := NewLinkedInProvider(LinkedInProviderConfig{APIBaseURL: server.URL})
			_, err := provider.Publish(context.Background(), domain.SocialAccount{
				Platform:          domain.PlatformLinkedIn,
				ExternalAccountID: "member_1",
			}, Credentials{AccessToken: "token-1"}, domain.Post{Text: text}, tt.opts)
			if err != nil {
				t.Fatalf("publish: %v", err)
			}
			if sent.Text != wantText {
				t.Fatalf("text\n got %q\nwant %q", sent.Text, wantText)
			}
			if string(sent.Attributes) != wantAttrs {
				t.Fatalf("attributes\n got %s\nwant %s", sent.Attributes, wantAttrs)
			}
		})
	}
}
