package postflow

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAuthorizationHeaderIncludesOAuthFields(t *testing.T) {
	signer := newOAuth1Signer(oauth1Credentials{
		ConsumerKey:    "ck",
		ConsumerSecret: "cs",
		Token:          "tk",
		TokenSecret:    "ts",
	})
	signer.nonce = func() string { return "nonce123" }
	signer.now = func() time.Time { return time.Unix(1700000000, 0) }

	header, err := signer.AuthorizationHeader(http.MethodPost, "https://api.twitter.com/1.1/statuses/update.json", map[string]string{"status": "hola"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, expected := range []string{
		"OAuth ",
		"oauth_consumer_key=\"ck\"",
		"oauth_token=\"tk\"",
		"oauth_signature_method=\"HMAC-SHA1\"",
		"oauth_timestamp=\"1700000000\"",
		"oauth_nonce=\"nonce123\"",
		"oauth_signature=\"",
	} {
		if !strings.Contains(header, expected) {
			t.Fatalf("expected header to contain %q, got %s", expected, header)
		}
	}
}

func TestSignatureChangesWithBodyParams(t *testing.T) {
	signer := newOAuth1Signer(oauth1Credentials{
		ConsumerKey:    "ck",
		ConsumerSecret: "cs",
		Token:          "tk",
		TokenSecret:    "ts",
	})
	signer.nonce = func() string { return "same" }
	signer.now = func() time.Time { return time.Unix(1700000000, 0) }

	h1, err := signer.AuthorizationHeader(http.MethodPost, "https://api.twitter.com/1.1/statuses/update.json", map[string]string{"status": "a"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	h2, err := signer.AuthorizationHeader(http.MethodPost, "https://api.twitter.com/1.1/statuses/update.json", map[string]string{"status": "b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h1 == h2 {
		t.Fatalf("expected different signatures when params change")
	}
}
