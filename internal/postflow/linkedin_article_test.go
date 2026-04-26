package postflow

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/antoniolg/postflow/internal/domain"
)

func TestExtractFirstURL(t *testing.T) {
	got := extractFirstURL(`Start (https://example.com/one), then https://example.com/two`)
	if got != "https://example.com/one" {
		t.Fatalf("expected first cleaned url, got %q", got)
	}
}

func TestLinkedInPublishUsesArticlePostForFirstURLWithoutMedia(t *testing.T) {
	var sawArticle bool
	var sawImageInit bool
	var sawImageUpload bool
	var sawUGC bool
	var sawLinkedInVersion string
	var sawArticleSource string
	var sawArticleTitle string
	var sawArticleDescription string
	var sawThumbnail string
	var sawThumbnailAlt string
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/article":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<html><head>
				<meta property="og:title" content="Example OG title">
				<meta property="og:description" content="Example OG description">
				<meta property="og:image" content="/images/cover.jpg">
				<meta property="og:image:alt" content="Cover alt text">
			</head><body>hello</body></html>`)
		case r.URL.Path == "/images/cover.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("jpeg-binary"))
		case r.URL.Path == "/rest/images" && r.URL.Query().Get("action") == "initializeUpload":
			sawImageInit = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"value":{"image":"urn:li:image:thumb_1","uploadUrl":"`+serverURL+`/upload-thumb"}}`)
		case r.URL.Path == "/upload-thumb":
			sawImageUpload = true
			w.WriteHeader(http.StatusCreated)
		case r.URL.Path == "/rest/posts":
			sawArticle = true
			sawLinkedInVersion = r.Header.Get("LinkedIn-Version")
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode article payload: %v", err)
			}
			content, _ := payload["content"].(map[string]any)
			article, _ := content["article"].(map[string]any)
			sawArticleSource, _ = article["source"].(string)
			sawArticleTitle, _ = article["title"].(string)
			sawArticleDescription, _ = article["description"].(string)
			sawThumbnail, _ = article["thumbnail"].(string)
			sawThumbnailAlt, _ = article["thumbnailAltText"].(string)
			w.Header().Set("x-restli-id", "urn:li:share:123456")
			w.WriteHeader(http.StatusCreated)
		case r.URL.Path == "/v2/ugcPosts":
			sawUGC = true
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	provider := newUnsafeLinkedInArticleTestProvider(server.URL)
	result, err := provider.Publish(context.Background(), domain.SocialAccount{
		Platform:          domain.PlatformLinkedIn,
		ExternalAccountID: "member_1",
	}, Credentials{AccessToken: "token-1"}, domain.Post{
		Text: "Read this " + server.URL + "/article",
	}, PublishOptions{})
	if err != nil {
		t.Fatalf("publish article post: %v", err)
	}
	if !sawArticle {
		t.Fatalf("expected article post publish path")
	}
	if sawUGC {
		t.Fatalf("did not expect ugc fallback when article publish succeeds")
	}
	if !sawImageInit || !sawImageUpload {
		t.Fatalf("expected thumbnail upload path to run")
	}
	if sawLinkedInVersion != linkedInRESTVersion {
		t.Fatalf("expected LinkedIn-Version %q, got %q", linkedInRESTVersion, sawLinkedInVersion)
	}
	if sawArticleSource != server.URL+"/article" {
		t.Fatalf("unexpected article source %q", sawArticleSource)
	}
	if sawArticleTitle != "Example OG title" {
		t.Fatalf("unexpected article title %q", sawArticleTitle)
	}
	if sawArticleDescription != "Example OG description" {
		t.Fatalf("unexpected article description %q", sawArticleDescription)
	}
	if sawThumbnail != "urn:li:image:thumb_1" {
		t.Fatalf("unexpected thumbnail urn %q", sawThumbnail)
	}
	if sawThumbnailAlt != "Cover alt text" {
		t.Fatalf("unexpected thumbnail alt %q", sawThumbnailAlt)
	}
	if result.ExternalID != "urn:li:share:123456" {
		t.Fatalf("unexpected external id %q", result.ExternalID)
	}
	if result.PublishedURL != "https://www.linkedin.com/feed/update/urn:li:share:123456/" {
		t.Fatalf("unexpected published url %q", result.PublishedURL)
	}
}

func TestLinkedInPublishUsesFirstURLOnlyForArticleMode(t *testing.T) {
	var requestedPaths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPaths = append(requestedPaths, r.URL.Path)
		switch {
		case r.URL.Path == "/first":
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, `<html><head><title>First title</title></head></html>`)
		case r.URL.Path == "/rest/posts":
			w.Header().Set("x-restli-id", "urn:li:ugcPost:9988")
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider := newUnsafeLinkedInArticleTestProvider(server.URL)
	_, err := provider.Publish(context.Background(), domain.SocialAccount{
		Platform:          domain.PlatformLinkedIn,
		ExternalAccountID: "member_1",
	}, Credentials{AccessToken: "token-1"}, domain.Post{
		Text: "first " + server.URL + "/first then " + server.URL + "/second",
	}, PublishOptions{})
	if err != nil {
		t.Fatalf("publish article with multiple links: %v", err)
	}
	for _, path := range requestedPaths {
		if path == "/second" {
			t.Fatalf("did not expect second url to be fetched")
		}
	}
}

func TestLinkedInPublishSkipsArticleModeWhenMediaExists(t *testing.T) {
	var sawArticle bool
	var sawUGC bool
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/posts":
			sawArticle = true
			w.WriteHeader(http.StatusCreated)
		case r.URL.Path == "/v2/assets" && r.URL.Query().Get("action") == "registerUpload":
			_, _ = io.WriteString(w, `{"value":{"asset":"urn:li:digitalmediaAsset:123","uploadMechanism":{"upload":{"uploadUrl":"`+serverURL+`/upload"}}}}`)
		case r.URL.Path == "/upload":
			w.WriteHeader(http.StatusCreated)
		case r.URL.Path == "/v2/ugcPosts":
			sawUGC = true
			w.Header().Set("x-restli-id", "li_post_media_1")
			w.WriteHeader(http.StatusCreated)
		case r.URL.Path == "/v2/ugcPosts/li_post_media_1":
			_, _ = io.WriteString(w, `{"permalink":"https://www.linkedin.com/feed/update/urn:li:activity:111/"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	imagePath := writeLinkedInTestFile(t, "media.jpg", []byte("image"))
	provider := newUnsafeLinkedInArticleTestProvider(server.URL)
	_, err := provider.Publish(context.Background(), domain.SocialAccount{
		Platform:          domain.PlatformLinkedIn,
		ExternalAccountID: "member_1",
	}, Credentials{AccessToken: "token-1"}, domain.Post{
		Text: "link " + server.URL + "/article",
		Media: []domain.Media{{
			ID:           "med_1",
			Kind:         "image",
			OriginalName: "media.jpg",
			StoragePath:  imagePath,
			MimeType:     "image/jpeg",
		}},
	}, PublishOptions{})
	if err != nil {
		t.Fatalf("publish with media: %v", err)
	}
	if sawArticle {
		t.Fatalf("did not expect article path when media exists")
	}
	if !sawUGC {
		t.Fatalf("expected ugc path when media exists")
	}
}

func TestLinkedInPublishFallsBackToUGCWhenArticleMetadataFetchFails(t *testing.T) {
	assertLinkedInArticleFallback(t, func(serverURL string) string {
		return serverURL + "/missing"
	})
}

func TestLinkedInPublishFallsBackToUGCWhenArticleTitleCannotBeDerived(t *testing.T) {
	var sawUGC bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/article":
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, `<html><head><meta property="og:description" content="desc only"></head></html>`)
		case r.URL.Path == "/v2/ugcPosts":
			sawUGC = true
			w.Header().Set("x-restli-id", "li_post_1")
			w.WriteHeader(http.StatusCreated)
		case r.URL.Path == "/v2/ugcPosts/li_post_1":
			_, _ = io.WriteString(w, `{"permalink":"https://www.linkedin.com/feed/update/urn:li:activity:111/"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider := newUnsafeLinkedInArticleTestProvider(server.URL)
	_, err := provider.Publish(context.Background(), domain.SocialAccount{
		Platform:          domain.PlatformLinkedIn,
		ExternalAccountID: "member_1",
	}, Credentials{AccessToken: "token-1"}, domain.Post{
		Text: "read " + server.URL + "/article",
	}, PublishOptions{})
	if err != nil {
		t.Fatalf("publish fallback without title: %v", err)
	}
	if !sawUGC {
		t.Fatalf("expected ugc fallback when title is missing")
	}
}

func TestLinkedInPublishPublishesArticleWithoutThumbnailWhenImageUploadFails(t *testing.T) {
	var sawThumbnail bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/article":
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, `<html><head>
				<meta property="og:title" content="Article title">
				<meta property="og:image" content="/images/cover.jpg">
			</head></html>`)
		case r.URL.Path == "/images/cover.jpg":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, `not-an-image`)
		case r.URL.Path == "/rest/posts":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode article payload: %v", err)
			}
			content, _ := payload["content"].(map[string]any)
			article, _ := content["article"].(map[string]any)
			_, sawThumbnail = article["thumbnail"]
			w.Header().Set("x-restli-id", "urn:li:share:without-thumb")
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider := newUnsafeLinkedInArticleTestProvider(server.URL)
	_, err := provider.Publish(context.Background(), domain.SocialAccount{
		Platform:          domain.PlatformLinkedIn,
		ExternalAccountID: "member_1",
	}, Credentials{AccessToken: "token-1"}, domain.Post{
		Text: "read " + server.URL + "/article",
	}, PublishOptions{})
	if err != nil {
		t.Fatalf("publish article without thumbnail: %v", err)
	}
	if sawThumbnail {
		t.Fatalf("did not expect thumbnail when upload fails")
	}
}

func TestLinkedInPermalinkUsesUGCPostURNDirectly(t *testing.T) {
	provider := NewLinkedInProvider(LinkedInProviderConfig{APIBaseURL: "https://api.linkedin.example"})
	got := provider.bestEffortLinkedInPermalink(context.Background(), "token-1", "urn:li:ugcPost:123")
	if got != "https://www.linkedin.com/feed/update/urn:li:ugcPost:123/" {
		t.Fatalf("unexpected ugc permalink %q", got)
	}
}

func TestLinkedInPublishDoesNotFetchPrivateArticleURL(t *testing.T) {
	var sawPrivateArticleFetch bool
	var sawUGC bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/private":
			sawPrivateArticleFetch = true
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, `<html><head><title>private</title></head></html>`)
		case r.URL.Path == "/v2/ugcPosts":
			sawUGC = true
			w.Header().Set("x-restli-id", "li_post_private_fallback")
			w.WriteHeader(http.StatusCreated)
		case r.URL.Path == "/v2/ugcPosts/li_post_private_fallback":
			_, _ = io.WriteString(w, `{"permalink":"https://www.linkedin.com/feed/update/urn:li:activity:333/"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider := NewLinkedInProvider(LinkedInProviderConfig{APIBaseURL: server.URL})
	_, err := provider.Publish(context.Background(), domain.SocialAccount{
		Platform:          domain.PlatformLinkedIn,
		ExternalAccountID: "member_1",
	}, Credentials{AccessToken: "token-1"}, domain.Post{
		Text: "read " + server.URL + "/private",
	}, PublishOptions{})
	if err != nil {
		t.Fatalf("publish fallback for private article url: %v", err)
	}
	if sawPrivateArticleFetch {
		t.Fatalf("did not expect private article URL to be fetched")
	}
	if !sawUGC {
		t.Fatalf("expected ugc fallback when article URL is blocked")
	}
}

func TestValidateLinkedInArticleFetchURLRejectsUnsafeTargets(t *testing.T) {
	tests := []string{
		"http://127.0.0.1/article",
		"http://[::1]/article",
		"http://localhost/article",
		"http://169.254.169.254/latest/meta-data",
		"ftp://example.com/article",
	}
	for _, rawURL := range tests {
		t.Run(rawURL, func(t *testing.T) {
			if err := validateLinkedInArticleFetchURL(context.Background(), rawURL); err == nil {
				t.Fatalf("expected %q to be rejected", rawURL)
			}
		})
	}
}

func writeLinkedInTestFile(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := t.TempDir() + "/" + name
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func newUnsafeLinkedInArticleTestProvider(apiBaseURL string) *LinkedInProvider {
	provider := NewLinkedInProvider(LinkedInProviderConfig{APIBaseURL: apiBaseURL})
	provider.allowUnsafeArticleFetches = true
	return provider
}

func assertLinkedInArticleFallback(t *testing.T, link func(serverURL string) string) {
	t.Helper()
	var sawUGC bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/ugcPosts":
			sawUGC = true
			w.Header().Set("x-restli-id", "li_post_fallback_1")
			w.WriteHeader(http.StatusCreated)
		case r.URL.Path == "/v2/ugcPosts/li_post_fallback_1":
			_, _ = io.WriteString(w, `{"permalink":"https://www.linkedin.com/feed/update/urn:li:activity:222/"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider := newUnsafeLinkedInArticleTestProvider(server.URL)
	_, err := provider.Publish(context.Background(), domain.SocialAccount{
		Platform:          domain.PlatformLinkedIn,
		ExternalAccountID: "member_1",
	}, Credentials{AccessToken: "token-1"}, domain.Post{
		Text: "read " + link(server.URL),
	}, PublishOptions{})
	if err != nil {
		t.Fatalf("publish fallback: %v", err)
	}
	if !sawUGC {
		t.Fatalf("expected ugc fallback")
	}
}
