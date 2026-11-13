package postflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/antoniolg/postflow/internal/domain"
)

// LinkedInPostsMaxCount is the largest page the Posts API finder accepts.
const LinkedInPostsMaxCount = 100

// ErrMentionsUnsupportedAccount means the account cannot list mentions: only
// LinkedIn organization accounts can read their own posts through the API.
var ErrMentionsUnsupportedAccount = errors.New("mentions can only be read from a linkedin organization account")

// Mention is an entity mentioned in a published post, with the URN that the
// network accepts when PostFlow publishes a mention of it.
type Mention struct {
	Name    string `json:"name"`
	URN     string `json:"urn"`
	PostURN string `json:"post_urn"`
}

// MentionLister is implemented by providers that can read the mentions in an
// account's recent posts.
type MentionLister interface {
	ListMentions(ctx context.Context, account domain.SocialAccount, credentials Credentials, limit int) ([]Mention, error)
}

// linkedInReadMentionPattern matches a MentionElement in the little text that
// the Posts API returns as `commentary`. Unlike linkedInMentionPattern, which
// matches what people write, the display name here is little text, so it may
// hold escaped reserved characters such as `\_`, `\(` or `\]`.
var linkedInReadMentionPattern = regexp.MustCompile(`@\[((?:\\.|[^\\\[\]\n])+)\]\((urn:li:(?:person|organization):[A-Za-z0-9_-]+)\)`)

// ListMentions reads the latest posts of a LinkedIn organization and returns
// the people and organizations mentioned in them, most recent first and without
// repeats. The URNs come from this app's API, so they are the ones LinkedIn
// accepts in PostFlow mentions (a person's web profile id does not work).
func (p *LinkedInProvider) ListMentions(ctx context.Context, account domain.SocialAccount, credentials Credentials, limit int) ([]Mention, error) {
	if account.Platform != domain.PlatformLinkedIn ||
		domain.NormalizeAccountKind(account.Platform, account.AccountKind) != domain.AccountKindOrganization {
		return nil, ErrMentionsUnsupportedAccount
	}
	authorURN := linkedinActorURN(account)
	if authorURN == "" {
		return nil, fmt.Errorf("linkedin organization account is missing its external account id")
	}
	if limit <= 0 || limit > LinkedInPostsMaxCount {
		limit = LinkedInPostsMaxCount
	}

	values := url.Values{}
	values.Set("q", "author")
	values.Set("author", authorURN)
	values.Set("count", strconv.Itoa(limit))
	values.Set("sortBy", "LAST_MODIFIED")
	endpoint := strings.TrimRight(p.cfg.APIBaseURL, "/") + "/rest/posts?" + values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(credentials.AccessToken))
	req.Header.Set("X-Restli-Protocol-Version", "2.0.0")
	req.Header.Set("X-RestLi-Method", "FINDER")
	req.Header.Set("LinkedIn-Version", linkedInRESTVersion)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		hint := ""
		if resp.StatusCode == http.StatusForbidden {
			hint = " (reading organization posts needs the r_organization_social scope: reauthorize the account)"
		}
		return nil, fmt.Errorf("linkedin posts read failed: status=%d body=%s%s", resp.StatusCode, strings.TrimSpace(string(body)), hint)
	}
	var out struct {
		Elements []struct {
			ID         string `json:"id"`
			Commentary string `json:"commentary"`
		} `json:"elements"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode linkedin posts: %w", err)
	}

	mentions := make([]Mention, 0)
	seen := make(map[string]struct{})
	for _, post := range out.Elements {
		for _, m := range parseLinkedInLittleTextMentions(post.Commentary) {
			if _, ok := seen[m.URN]; ok {
				continue
			}
			seen[m.URN] = struct{}{}
			m.PostURN = strings.TrimSpace(post.ID)
			mentions = append(mentions, m)
		}
	}
	return mentions, nil
}

// parseLinkedInLittleTextMentions returns the mentions in little text, with
// the display names unescaped.
func parseLinkedInLittleTextMentions(text string) []Mention {
	matches := linkedInReadMentionPattern.FindAllStringSubmatch(text, -1)
	mentions := make([]Mention, 0, len(matches))
	for _, m := range matches {
		mentions = append(mentions, Mention{Name: unescapeLinkedInLittleText(m[1]), URN: m[2]})
	}
	return mentions
}

func unescapeLinkedInLittleText(text string) string {
	if !strings.Contains(text, `\`) {
		return text
	}
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
