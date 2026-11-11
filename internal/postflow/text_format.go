package postflow

import (
	"regexp"
	"strings"

	"github.com/antoniolg/postflow/internal/textfmt"
)

func formatPostTextForPublish(raw string) string {
	return formatPostTextForPublishKeeping(raw, nil)
}

// formatPostTextForPublishKeeping formats raw like formatPostTextForPublish but
// leaves every match of keep exactly as written. Each match is swapped for a
// private-use placeholder while the Markdown is styled, so emphasis around it
// still works and nothing inside it (letters, digits, underscores) is restyled
// or paired as a delimiter.
func formatPostTextForPublishKeeping(raw string, keep *regexp.Regexp) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if looksLikeRTF(trimmed) {
		trimmed = strings.TrimSpace(rtfToPlainText(trimmed))
	}
	if keep == nil || strings.ContainsFunc(trimmed, isKeptTextPlaceholder) {
		return textfmt.MarkdownToUnicodeStyled(trimmed)
	}
	var kept []string
	protected := keep.ReplaceAllStringFunc(trimmed, func(match string) string {
		if len(kept) >= keptTextPlaceholderCount {
			return match
		}
		kept = append(kept, match)
		return string(keptTextPlaceholderBase + rune(len(kept)-1))
	})
	if len(kept) == 0 {
		return textfmt.MarkdownToUnicodeStyled(trimmed)
	}
	styled := textfmt.MarkdownToUnicodeStyled(protected)
	return restoreKeptText(styled, kept)
}

// Placeholders live in Supplementary Private Use Area-A, which the Markdown
// styler neither restyles nor treats as a word or space rune.
const (
	keptTextPlaceholderBase  rune = 0xF0000
	keptTextPlaceholderCount      = 0xFFFD
)

func isKeptTextPlaceholder(r rune) bool {
	return r >= keptTextPlaceholderBase && r < keptTextPlaceholderBase+keptTextPlaceholderCount
}

func restoreKeptText(styled string, kept []string) string {
	var out strings.Builder
	for _, r := range styled {
		if isKeptTextPlaceholder(r) {
			if idx := int(r - keptTextPlaceholderBase); idx < len(kept) {
				out.WriteString(kept[idx])
				continue
			}
		}
		out.WriteRune(r)
	}
	return out.String()
}

var rtfControlWordRe = regexp.MustCompile(`\\[a-zA-Z]+-?\d* ?`)

func looksLikeRTF(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "{\\rtf")
}

func rtfToPlainText(raw string) string {
	out := raw
	out = strings.ReplaceAll(out, `\line`, "\n")
	out = strings.ReplaceAll(out, `\par`, "\n")
	out = strings.ReplaceAll(out, `\\`, `\`)
	out = strings.ReplaceAll(out, `\{`, "{")
	out = strings.ReplaceAll(out, `\}`, "}")
	out = rtfControlWordRe.ReplaceAllString(out, "")
	out = strings.NewReplacer("{", "", "}", "").Replace(out)
	return strings.TrimSpace(out)
}
