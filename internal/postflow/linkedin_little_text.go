package postflow

import (
	"regexp"
	"strings"
	"unicode"
)

// linkedInLittleTextReserved holds the characters reserved by LinkedIn's
// "little" text format, which the Posts API uses for `commentary`. An unescaped
// reserved character can be parsed as markup or silently cut the rest of the
// post, so the docs require escaping all of them even outside elements:
// https://learn.microsoft.com/en-us/linkedin/marketing/community-management/shares/little-text-format
const linkedInLittleTextReserved = `\|{}@[]()<>#*_~`

var linkedInLittleTextURLPattern = regexp.MustCompile(`(?i)\b(?:https?://|www\.)[^\s<>"']+`)

// escapeLinkedInLittleText turns plain post text into little text for the Posts
// API by backslash-escaping every reserved character, except a '#' that opens a
// hashtag, which LinkedIn parses as a HashtagElement and renders as a link.
//
// URLs are escaped too. The little grammar has no URL element, so a link is
// plain Text, and LinkedIn parses little text before it detects and shortens
// links: unescaped underscores inside a URL have been parsed as markup and
// dropped from the lnkd.in target, and an unescaped "(" truncates the post.
// An escape such as `\_` parses back to "_", so the detected link keeps the
// original URL. The only URL-specific rule is that '#' is always escaped inside
// a URL, so a fragment like "/docs#setup" does not turn into a hashtag.
func escapeLinkedInLittleText(text string) string {
	if !strings.ContainsAny(text, linkedInLittleTextReserved) {
		return text
	}
	urls := linkedInLittleTextURLPattern.FindAllStringIndex(text, -1)
	var out strings.Builder
	out.Grow(len(text) + len(text)/8)
	var prev rune
	for i, r := range text {
		for len(urls) > 0 && urls[0][1] <= i {
			urls = urls[1:]
		}
		inURL := len(urls) > 0 && urls[0][0] <= i
		if strings.ContainsRune(linkedInLittleTextReserved, r) &&
			(r != '#' || inURL || !opensLinkedInHashtag(prev, text[i+1:])) {
			out.WriteByte('\\')
		}
		out.WriteRune(r)
		prev = r
	}
	return out.String()
}

// opensLinkedInHashtag reports whether a '#' preceded by prev and followed by
// rest starts a hashtag: it must begin a word, and the word after it must start
// with a letter or digit and contain a letter. Anything else ("C#", "PR #63",
// "#️⃣") is escaped to render literally, because a '#' that LinkedIn cannot
// parse as a hashtag is an unescaped reserved character.
func opensLinkedInHashtag(prev rune, rest string) bool {
	if isLinkedInHashtagRune(prev) {
		return false
	}
	hasLetter := false
	for j, r := range rest {
		if !isLinkedInHashtagRune(r) || (j == 0 && unicode.IsMark(r)) {
			break
		}
		hasLetter = hasLetter || unicode.IsLetter(r)
	}
	return hasLetter
}

func isLinkedInHashtagRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r)
}
