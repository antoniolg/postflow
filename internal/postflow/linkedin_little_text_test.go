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

func TestEscapeLinkedInLittleText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "plain text", in: "Hola, mundo. 99% seguro!", want: "Hola, mundo. 99% seguro!"},
		{
			name: "parentheses that truncated a post",
			in:   "Mi primera idea fue pasárselas a un subagente con un modelo barato (en mi caso GPT-6 Luna). Pero aparecieron dos problemas:",
			want: `Mi primera idea fue pasárselas a un subagente con un modelo barato \(en mi caso GPT-6 Luna\). Pero aparecieron dos problemas:`,
		},
		{name: "brackets braces and pipe", in: "[beta] {draft} a|b", want: `\[beta\] \{draft\} a\|b`},
		{name: "angle brackets and tildes", in: "<3 ~20 min ~aprox~ ->", want: `\<3 \~20 min \~aprox\~ -\>`},
		{name: "underscores outside urls", in: "snake_case, _cursiva_ y __init__", want: `snake\_case, \_cursiva\_ y \_\_init\_\_`},
		{
			name: "underscores inside url",
			in:   "Lee https://example.com/taller_de_ia?utm_source=linkedin&utm_medium=social",
			want: `Lee https://example.com/taller\_de\_ia?utm\_source=linkedin&utm\_medium=social`,
		},
		{
			name: "parentheses inside and around url",
			in:   "(ver https://en.wikipedia.org/wiki/Go_(lenguaje))",
			want: `\(ver https://en.wikipedia.org/wiki/Go\_\(lenguaje\)\)`,
		},
		{name: "at signs", in: "Escríbeme a hola@devexpert.io o a @antoniolg", want: `Escríbeme a hola\@devexpert.io o a \@antoniolg`},
		{name: "mention syntax stays literal", in: "@[DevExpert](urn:li:organization:123)", want: `\@\[DevExpert\]\(urn:li:organization:123\)`},
		{name: "asterisks", in: "*no* es negrita y 2*3=6", want: `\*no\* es negrita y 2\*3=6`},
		{name: "backslashes", in: `C:\Users\antonio y \(ya\)`, want: `C:\\Users\\antonio y \\\(ya\\\)`},
		{name: "emoji", in: "1️⃣ Primero 🚀 (ya) 👩🏽‍💻", want: `1️⃣ Primero 🚀 \(ya\) 👩🏽‍💻`},
		{name: "keycap emoji with reserved characters", in: "#️⃣ y *️⃣", want: `\#️⃣ y \*️⃣`},
		{name: "hashtags", in: "#IA, #programación, #OSDay26 y #100DaysOfCode.", want: "#IA, #programación, #OSDay26 y #100DaysOfCode."},
		{name: "hashtag inside parentheses", in: "(#IA)", want: `\(#IA\)`},
		{name: "hashtag after url", in: "https://example.com #IA", want: "https://example.com #IA"},
		{name: "hashtag with underscore", in: "#machine_learning", want: `#machine\_learning`},
		{name: "hash without word", in: "C# y F# son # lenguajes", want: `C\# y F\# son \# lenguajes`},
		{name: "numeric hash", in: "PR #63 quedó #1", want: `PR \#63 quedó \#1`},
		{name: "hash inside word", in: "C#10 y foo#bar", want: `C\#10 y foo\#bar`},
		{name: "repeated hash", in: "##IA", want: `\##IA`},
		{
			name: "hash inside urls",
			in:   "https://example.com/docs#setup, https://example.com/#/ruta y www.example.com/#faq",
			want: `https://example.com/docs\#setup, https://example.com/\#/ruta y www.example.com/\#faq`,
		},
		{name: "fullwidth hash is not reserved", in: "＃IA", want: "＃IA"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := escapeLinkedInLittleText(tt.in)
			if got != tt.want {
				t.Fatalf("escapeLinkedInLittleText(%q)\n got %q\nwant %q", tt.in, got, tt.want)
			}
			if visible := unescapeLinkedInLittleTextForTest(got); visible != tt.in {
				t.Fatalf("escaping changed the visible text: %q became %q", tt.in, visible)
			}
		})
	}
}

func TestLinkedInArticlePostEscapesLittleTextCommentary(t *testing.T) {
	var commentary, source, title string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/article":
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, `<html><head><meta property="og:title" content="Subagentes (guía)"></head></html>`)
		case "/rest/posts":
			var payload struct {
				Commentary string `json:"commentary"`
				Content    struct {
					Article struct {
						Source string `json:"source"`
						Title  string `json:"title"`
					} `json:"article"`
				} `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode article payload: %v", err)
			}
			commentary = payload.Commentary
			source = payload.Content.Article.Source
			title = payload.Content.Article.Title
			w.Header().Set("x-restli-id", "urn:li:share:1")
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	link := server.URL + "/article?utm_source=linkedin&utm_medium=social"
	provider := newUnsafeLinkedInArticleTestProvider(server.URL)
	_, err := provider.Publish(context.Background(), domain.SocialAccount{
		Platform:          domain.PlatformLinkedIn,
		ExternalAccountID: "member_1",
	}, Credentials{AccessToken: "token-1"}, domain.Post{
		Text: "Un subagente con un modelo barato (en mi caso GPT-6 Luna). Pero aparecieron dos problemas. #IA\n\nLo tengo en un gist: " + link,
	}, PublishOptions{})
	if err != nil {
		t.Fatalf("publish article post: %v", err)
	}
	wantCommentary := `Un subagente con un modelo barato \(en mi caso GPT-6 Luna\). Pero aparecieron dos problemas. #IA` +
		"\n\nLo tengo en un gist: " + server.URL + `/article?utm\_source=linkedin&utm\_medium=social`
	if commentary != wantCommentary {
		t.Fatalf("unexpected commentary\n got %q\nwant %q", commentary, wantCommentary)
	}
	if source != link {
		t.Fatalf("expected plain article source %q, got %q", link, source)
	}
	if title != "Subagentes (guía)" {
		t.Fatalf("expected plain article title, got %q", title)
	}
}

func TestLinkedInPlainTextPathsDoNotEscapeLittleText(t *testing.T) {
	const text = "Plan (beta) para snake_case, a|b y [x] con @antoniolg #IA"
	tests := []struct {
		name string
		opts PublishOptions
	}{
		{name: "ugc post", opts: PublishOptions{}},
		{name: "comment", opts: PublishOptions{Mode: PublishModeComment, ParentExternalID: "urn:li:ugcPost:1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sent string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload struct {
					SpecificContent struct {
						ShareContent struct {
							ShareCommentary struct {
								Text string `json:"text"`
							} `json:"shareCommentary"`
						} `json:"com.linkedin.ugc.ShareContent"`
					} `json:"specificContent"`
					Message struct {
						Text string `json:"text"`
					} `json:"message"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Errorf("decode payload: %v", err)
				}
				switch {
				case r.URL.Path == "/v2/ugcPosts":
					sent = payload.SpecificContent.ShareContent.ShareCommentary.Text
					w.Header().Set("x-restli-id", "urn:li:share:1")
					w.WriteHeader(http.StatusCreated)
				case strings.HasSuffix(r.URL.Path, "/comments"):
					sent = payload.Message.Text
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
			if sent != text {
				t.Fatalf("expected plain text %q, got %q", text, sent)
			}
		})
	}
}

// unescapeLinkedInLittleTextForTest drops little text escapes to recover the
// text LinkedIn renders.
func unescapeLinkedInLittleTextForTest(text string) string {
	var out strings.Builder
	escaped := false
	for _, r := range text {
		if r == '\\' && !escaped {
			escaped = true
			continue
		}
		escaped = false
		out.WriteRune(r)
	}
	return out.String()
}
