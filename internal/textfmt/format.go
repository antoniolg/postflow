package textfmt

import (
	"strconv"
	"strings"
)

func MarkdownToPreviewHTML(input string) string {
	source := normalizeMarkdownInput(input)
	var out strings.Builder
	var plain strings.Builder
	boldOpen := false
	italicOpen := false

	flushPlain := func() {
		if plain.Len() == 0 {
			return
		}
		out.WriteString(escapeHTML(plain.String()))
		plain.Reset()
	}

	runes := []rune(source)
	for i := 0; i < len(runes); {
		if runes[i] == '\\' && i+1 < len(runes) && runes[i+1] == '*' {
			plain.WriteRune('*')
			i += 2
			continue
		}
		if runes[i] == '\n' {
			flushPlain()
			out.WriteString("<br>")
			i++
			continue
		}
		if runes[i] == '*' && italicOpen {
			flushPlain()
			out.WriteString("</em>")
			italicOpen = false
			i++
			continue
		}
		if runes[i] == '*' && i+1 < len(runes) && runes[i+1] == '*' && boldOpen {
			flushPlain()
			out.WriteString("</strong>")
			boldOpen = false
			i += 2
			continue
		}
		if runes[i] == '*' && i+1 < len(runes) && runes[i+1] == '*' && hasClosingDoubleAsterisk(runes, i+2) {
			flushPlain()
			out.WriteString("<strong>")
			boldOpen = true
			i += 2
			continue
		}
		if runes[i] == '*' && hasClosingSingleAsterisk(runes, i+1) {
			flushPlain()
			out.WriteString("<em>")
			italicOpen = true
			i++
			continue
		}
		plain.WriteRune(runes[i])
		i++
	}

	flushPlain()
	if italicOpen {
		out.WriteString("</em>")
	}
	if boldOpen {
		out.WriteString("</strong>")
	}
	return out.String()
}

func MarkdownToRTF(input string) string {
	source := normalizeMarkdownInput(input)
	var out strings.Builder
	var plain strings.Builder
	boldOpen := false
	italicOpen := false

	out.WriteString("{\\rtf1\\ansi\\deff0 ")

	flushPlain := func() {
		if plain.Len() == 0 {
			return
		}
		out.WriteString(escapeRTFText(plain.String()))
		plain.Reset()
	}

	runes := []rune(source)
	for i := 0; i < len(runes); {
		if runes[i] == '\\' && i+1 < len(runes) && runes[i+1] == '*' {
			plain.WriteRune('*')
			i += 2
			continue
		}
		if runes[i] == '\n' {
			flushPlain()
			out.WriteString("\\line ")
			i++
			continue
		}
		if runes[i] == '*' && italicOpen {
			flushPlain()
			out.WriteString("\\i0 ")
			italicOpen = false
			i++
			continue
		}
		if runes[i] == '*' && i+1 < len(runes) && runes[i+1] == '*' && boldOpen {
			flushPlain()
			out.WriteString("\\b0 ")
			boldOpen = false
			i += 2
			continue
		}
		if runes[i] == '*' && i+1 < len(runes) && runes[i+1] == '*' && hasClosingDoubleAsterisk(runes, i+2) {
			flushPlain()
			out.WriteString("\\b ")
			boldOpen = true
			i += 2
			continue
		}
		if runes[i] == '*' && hasClosingSingleAsterisk(runes, i+1) {
			flushPlain()
			out.WriteString("\\i ")
			italicOpen = true
			i++
			continue
		}
		plain.WriteRune(runes[i])
		i++
	}

	flushPlain()
	if italicOpen {
		out.WriteString("\\i0 ")
	}
	if boldOpen {
		out.WriteString("\\b0 ")
	}
	out.WriteString("}")
	return out.String()
}

func normalizeMarkdownInput(input string) string {
	return strings.ReplaceAll(strings.ReplaceAll(input, "\r\n", "\n"), "\r", "\n")
}

func hasClosingDoubleAsterisk(runes []rune, start int) bool {
	for i := start; i < len(runes)-1; i++ {
		if runes[i] == '\\' {
			i++
			continue
		}
		if runes[i] == '*' && runes[i+1] == '*' {
			return true
		}
	}
	return false
}

func hasClosingSingleAsterisk(runes []rune, start int) bool {
	for i := start; i < len(runes); i++ {
		if runes[i] == '\\' {
			i++
			continue
		}
		if runes[i] == '*' {
			return true
		}
	}
	return false
}

func escapeHTML(input string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	)
	return replacer.Replace(input)
}

func escapeRTFText(input string) string {
	var out strings.Builder
	for _, r := range input {
		switch r {
		case '\\':
			out.WriteString("\\\\")
		case '{':
			out.WriteString("\\{")
		case '}':
			out.WriteString("\\}")
		default:
			if r >= 32 && r <= 126 {
				out.WriteRune(r)
				continue
			}
			out.WriteString("\\u")
			out.WriteString(strconv.Itoa(int(r)))
			out.WriteByte('?')
		}
	}
	return out.String()
}
