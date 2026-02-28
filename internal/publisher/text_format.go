package publisher

import (
	"regexp"
	"strings"

	"github.com/antoniolg/publisher/internal/textfmt"
)

func formatPostTextForPublish(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if looksLikeRTF(trimmed) {
		trimmed = strings.TrimSpace(rtfToPlainText(trimmed))
	}
	return textfmt.MarkdownToUnicodeStyled(trimmed)
}

var rtfControlWordRe = regexp.MustCompile(`\\[a-zA-Z]+-?\d* ?`)

func looksLikeRTF(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "{\\rtf")
}

func rtfToPlainText(raw string) string {
	// Best-effort unwrapping to avoid publishing literal RTF blobs.
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
