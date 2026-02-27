package publisher

import (
	"strings"

	"github.com/antoniolg/publisher/internal/textfmt"
)

func formatPostTextForPublish(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	return textfmt.MarkdownToRTF(trimmed)
}
