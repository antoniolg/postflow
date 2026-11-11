package postflow

import (
	"regexp"
	"strings"
	"unicode/utf16"
)

// linkedInMentionPattern matches a mention written in LinkedIn's little text
// syntax, @[Display name](urn:li:person:ID) or
// @[Display name](urn:li:organization:ID). LinkedIn only links the display name
// when it matches the member's first, last or full name, or the organization's
// full name, case sensitive; otherwise it renders as plain text.
var linkedInMentionPattern = regexp.MustCompile(`@\[([^\[\]\n]+)\]\((urn:li:(?:person|organization):[A-Za-z0-9_-]+)\)`)

// linkedInMentionsToAttributes turns the mentions in text into the attributed
// text that ugcPosts and comments expect: each mention is replaced by its
// display name, and an attribute links that range to the mentioned entity.
// Offsets count UTF-16 code units, as LinkedIn measures text.
func linkedInMentionsToAttributes(text string) (string, []map[string]any) {
	matches := linkedInMentionPattern.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text, nil
	}
	var out strings.Builder
	attributes := make([]map[string]any, 0, len(matches))
	offset, last := 0, 0
	for _, m := range matches {
		before, name, urn := text[last:m[0]], text[m[2]:m[3]], text[m[4]:m[5]]
		out.WriteString(before)
		out.WriteString(name)
		offset += utf16Len(before)
		attributes = append(attributes, map[string]any{
			"start":  offset,
			"length": utf16Len(name),
			"value":  linkedInAttributedEntity(urn),
		})
		offset += utf16Len(name)
		last = m[1]
	}
	out.WriteString(text[last:])
	return out.String(), attributes
}

// linkedInAttributedText builds an AttributedText payload (shareCommentary or
// a comment message) from post text that may contain mentions, and returns the
// text as rendered too.
func linkedInAttributedText(text string) (map[string]any, string) {
	plain, attributes := linkedInMentionsToAttributes(text)
	payload := map[string]any{"text": plain}
	if len(attributes) > 0 {
		payload["attributes"] = attributes
	}
	return payload, plain
}

func linkedInAttributedEntity(urn string) map[string]any {
	if strings.HasPrefix(urn, "urn:li:organization:") {
		return map[string]any{"com.linkedin.common.CompanyAttributedEntity": map[string]any{"company": urn}}
	}
	return map[string]any{"com.linkedin.common.MemberAttributedEntity": map[string]any{"member": urn}}
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}
