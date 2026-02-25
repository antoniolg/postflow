package publisher

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type oauth1Credentials struct {
	ConsumerKey    string
	ConsumerSecret string
	Token          string
	TokenSecret    string
}

type oauth1Signer struct {
	creds oauth1Credentials
	nonce func() string
	now   func() time.Time
}

func newOAuth1Signer(creds oauth1Credentials) oauth1Signer {
	return oauth1Signer{
		creds: creds,
		nonce: randomNonce,
		now:   time.Now,
	}
}

func (s oauth1Signer) AuthorizationHeader(method, rawURL string, signatureParams map[string]string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	oauthParams := map[string]string{
		"oauth_consumer_key":     s.creds.ConsumerKey,
		"oauth_nonce":            s.nonce(),
		"oauth_signature_method": "HMAC-SHA1",
		"oauth_timestamp":        fmt.Sprintf("%d", s.now().Unix()),
		"oauth_token":            s.creds.Token,
		"oauth_version":          "1.0",
	}

	all := map[string]string{}
	for k, vals := range u.Query() {
		if len(vals) == 0 {
			continue
		}
		all[k] = vals[0]
	}
	for k, v := range signatureParams {
		all[k] = v
	}
	for k, v := range oauthParams {
		all[k] = v
	}

	base := signatureBaseString(method, normalizeURL(u), normalizeParams(all))
	key := pctEncode(s.creds.ConsumerSecret) + "&" + pctEncode(s.creds.TokenSecret)
	h := hmac.New(sha1.New, []byte(key))
	_, _ = h.Write([]byte(base))
	oauthParams["oauth_signature"] = base64.StdEncoding.EncodeToString(h.Sum(nil))

	return authHeader(oauthParams), nil
}

func signRequest(req *http.Request, signer oauth1Signer, signatureParams map[string]string) error {
	header, err := signer.AuthorizationHeader(req.Method, req.URL.String(), signatureParams)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", header)
	return nil
}

func normalizeURL(u *url.URL) string {
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Host)
	if strings.Contains(host, ":") {
		parts := strings.Split(host, ":")
		port := parts[len(parts)-1]
		if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
			host = parts[0]
		}
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	return scheme + "://" + host + path
}

func normalizeParams(params map[string]string) string {
	type kv struct{ k, v string }
	items := make([]kv, 0, len(params))
	for k, v := range params {
		items = append(items, kv{k: pctEncode(k), v: pctEncode(v)})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].k == items[j].k {
			return items[i].v < items[j].v
		}
		return items[i].k < items[j].k
	})
	pairs := make([]string, 0, len(items))
	for _, it := range items {
		pairs = append(pairs, it.k+"="+it.v)
	}
	return strings.Join(pairs, "&")
}

func signatureBaseString(method, rawURL, normalizedParams string) string {
	return strings.ToUpper(method) + "&" + pctEncode(rawURL) + "&" + pctEncode(normalizedParams)
}

func authHeader(oauthParams map[string]string) string {
	keys := make([]string, 0, len(oauthParams))
	for k := range oauthParams {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=\"%s\"", pctEncode(k), pctEncode(oauthParams[k])))
	}
	return "OAuth " + strings.Join(parts, ", ")
}

func pctEncode(s string) string {
	encoded := url.QueryEscape(s)
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	encoded = strings.ReplaceAll(encoded, "*", "%2A")
	encoded = strings.ReplaceAll(encoded, "%7E", "~")
	return encoded
}

func randomNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("fallback%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
