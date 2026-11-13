package postflow

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/antoniolg/postflow/internal/domain"
)

func TestLinkedInListMentionsReadsOrganizationPosts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/rest/posts" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		query := r.URL.Query()
		if query.Get("q") != "author" || query.Get("author") != "urn:li:organization:42" || query.Get("count") != "5" || query.Get("sortBy") != "LAST_MODIFIED" {
			t.Fatalf("unexpected query %q", r.URL.RawQuery)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer li-token" {
			t.Fatalf("unexpected authorization %q", got)
		}
		if r.Header.Get("LinkedIn-Version") != linkedInRESTVersion || r.Header.Get("X-RestLi-Method") != "FINDER" {
			t.Fatalf("unexpected headers %v", r.Header)
		}
		_, _ = io.WriteString(w, `{"elements":[
			{"id":"urn:li:share:2","commentary":"Gracias @[Ana L\u00f3pez\\_Dev](urn:li:person:Dk968RHxiO) y @[Welcome to La Secta](urn:li:organization:110315077) \\(en directo\\)"},
			{"id":"urn:li:ugcPost:1","commentary":"Con @[Ana](urn:li:person:Dk968RHxiO), @[Juan \\[JS\\]](urn:li:person:Ab_1-x) y @[x](urn:li:company:9)"},
			{"id":"urn:li:share:0"}
		]}`)
	}))
	defer server.Close()

	provider := NewLinkedInProvider(LinkedInProviderConfig{APIBaseURL: server.URL})
	account := domain.SocialAccount{Platform: domain.PlatformLinkedIn, AccountKind: domain.AccountKindOrganization, ExternalAccountID: "42"}
	got, err := provider.ListMentions(context.Background(), account, Credentials{AccessToken: "li-token"}, 5)
	if err != nil {
		t.Fatalf("list mentions: %v", err)
	}
	want := []Mention{
		{Name: "Ana López_Dev", URN: "urn:li:person:Dk968RHxiO", PostURN: "urn:li:share:2"},
		{Name: "Welcome to La Secta", URN: "urn:li:organization:110315077", PostURN: "urn:li:share:2"},
		{Name: "Juan [JS]", URN: "urn:li:person:Ab_1-x", PostURN: "urn:li:ugcPost:1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mentions\n got %+v\nwant %+v", got, want)
	}
}

func TestLinkedInListMentionsRejectsPersonalAccounts(t *testing.T) {
	provider := NewLinkedInProvider(LinkedInProviderConfig{APIBaseURL: "http://127.0.0.1:1"})
	account := domain.SocialAccount{Platform: domain.PlatformLinkedIn, AccountKind: domain.AccountKindPersonal, ExternalAccountID: "DkWdxg_LWJ"}
	if _, err := provider.ListMentions(context.Background(), account, Credentials{AccessToken: "t"}, 10); !errors.Is(err, ErrMentionsUnsupportedAccount) {
		t.Fatalf("expected ErrMentionsUnsupportedAccount, got %v", err)
	}
}

func TestLinkedInListMentionsExplainsForbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"status":403,"message":"Not enough permissions"}`)
	}))
	defer server.Close()

	provider := NewLinkedInProvider(LinkedInProviderConfig{APIBaseURL: server.URL})
	account := domain.SocialAccount{Platform: domain.PlatformLinkedIn, AccountKind: domain.AccountKindOrganization, ExternalAccountID: "42"}
	_, err := provider.ListMentions(context.Background(), account, Credentials{AccessToken: "t"}, 0)
	if err == nil || !strings.Contains(err.Error(), "status=403") || !strings.Contains(err.Error(), "r_organization_social") {
		t.Fatalf("expected a 403 error that names the scope, got %v", err)
	}
}

func TestUnescapeLinkedInLittleText(t *testing.T) {
	for in, want := range map[string]string{
		`plain`:          "plain",
		`a\_b\(c\)`:      "a_b(c)",
		`back\\slash`:    `back\slash`,
		`trailing\`:      "trailing",
		`\@\[x\]\#tag`:   "@[x]#tag",
		`Ana López\_Dev`: "Ana López_Dev",
	} {
		if got := unescapeLinkedInLittleText(in); got != want {
			t.Fatalf("unescape(%q) = %q, want %q", in, got, want)
		}
	}
}
