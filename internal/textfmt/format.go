package textfmt

import (
	"strconv"
	"strings"
	"unicode"
)

func MarkdownToPreviewHTML(input string) string {
	source := normalizeMarkdownInput(input)
	var out strings.Builder
	var plain strings.Builder
	boldOpen := false
	italicOpen := false
	var boldMarker rune
	var italicMarker rune

	flushPlain := func() {
		if plain.Len() == 0 {
			return
		}
		out.WriteString(escapeHTML(plain.String()))
		plain.Reset()
	}

	runes := []rune(source)
	for i := 0; i < len(runes); {
		if isEscapedDelimiter(runes, i) {
			plain.WriteRune(runes[i+1])
			i += 2
			continue
		}
		if runes[i] == '\n' {
			flushPlain()
			out.WriteString("<br>")
			i++
			continue
		}
		if italicOpen && runes[i] == italicMarker && canUseAsClosing(runes, i, italicMarker, 1) {
			flushPlain()
			out.WriteString("</em>")
			italicOpen = false
			italicMarker = 0
			i++
			continue
		}
		if boldOpen && i+1 < len(runes) && runes[i] == boldMarker && runes[i+1] == boldMarker && canUseAsClosing(runes, i, boldMarker, 2) {
			flushPlain()
			out.WriteString("</strong>")
			boldOpen = false
			boldMarker = 0
			i += 2
			continue
		}
		if !boldOpen &&
			i+1 < len(runes) &&
			isMarkerRune(runes[i]) &&
			runes[i+1] == runes[i] &&
			canUseAsOpening(runes, i, runes[i], 2) &&
			hasClosingDoubleDelimiter(runes, i+2, runes[i]) {
			flushPlain()
			out.WriteString("<strong>")
			boldOpen = true
			boldMarker = runes[i]
			i += 2
			continue
		}
		if !italicOpen &&
			isMarkerRune(runes[i]) &&
			canUseAsOpening(runes, i, runes[i], 1) &&
			hasClosingSingleDelimiter(runes, i+1, runes[i]) {
			flushPlain()
			out.WriteString("<em>")
			italicOpen = true
			italicMarker = runes[i]
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
	var boldMarker rune
	var italicMarker rune

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
		if isEscapedDelimiter(runes, i) {
			plain.WriteRune(runes[i+1])
			i += 2
			continue
		}
		if runes[i] == '\n' {
			flushPlain()
			out.WriteString("\\line ")
			i++
			continue
		}
		if italicOpen && runes[i] == italicMarker && canUseAsClosing(runes, i, italicMarker, 1) {
			flushPlain()
			out.WriteString("\\i0 ")
			italicOpen = false
			italicMarker = 0
			i++
			continue
		}
		if boldOpen && i+1 < len(runes) && runes[i] == boldMarker && runes[i+1] == boldMarker && canUseAsClosing(runes, i, boldMarker, 2) {
			flushPlain()
			out.WriteString("\\b0 ")
			boldOpen = false
			boldMarker = 0
			i += 2
			continue
		}
		if !boldOpen &&
			i+1 < len(runes) &&
			isMarkerRune(runes[i]) &&
			runes[i+1] == runes[i] &&
			canUseAsOpening(runes, i, runes[i], 2) &&
			hasClosingDoubleDelimiter(runes, i+2, runes[i]) {
			flushPlain()
			out.WriteString("\\b ")
			boldOpen = true
			boldMarker = runes[i]
			i += 2
			continue
		}
		if !italicOpen &&
			isMarkerRune(runes[i]) &&
			canUseAsOpening(runes, i, runes[i], 1) &&
			hasClosingSingleDelimiter(runes, i+1, runes[i]) {
			flushPlain()
			out.WriteString("\\i ")
			italicOpen = true
			italicMarker = runes[i]
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

func hasClosingDoubleDelimiter(runes []rune, start int, marker rune) bool {
	for i := start; i < len(runes)-1; i++ {
		if isEscapedDelimiter(runes, i) {
			i++
			continue
		}
		if runes[i] == marker && runes[i+1] == marker && canUseAsClosing(runes, i, marker, 2) {
			return true
		}
	}
	return false
}

func hasClosingSingleDelimiter(runes []rune, start int, marker rune) bool {
	for i := start; i < len(runes); i++ {
		if isEscapedDelimiter(runes, i) {
			i++
			continue
		}
		if runes[i] == marker && canUseAsClosing(runes, i, marker, 1) {
			return true
		}
	}
	return false
}

func isEscapedDelimiter(runes []rune, i int) bool {
	if i+1 >= len(runes) || runes[i] != '\\' {
		return false
	}
	return isMarkerRune(runes[i+1])
}

func isMarkerRune(r rune) bool {
	return r == '*' || r == '_'
}

func canUseAsOpening(runes []rune, idx int, marker rune, width int) bool {
	if marker != '_' {
		return true
	}
	nextIdx := idx + width
	if nextIdx >= len(runes) || unicode.IsSpace(runes[nextIdx]) {
		return false
	}
	if idx == 0 {
		return true
	}
	return !isWordRune(runes[idx-1])
}

func canUseAsClosing(runes []rune, idx int, marker rune, width int) bool {
	if marker != '_' {
		return true
	}
	prevIdx := idx - 1
	if prevIdx < 0 || unicode.IsSpace(runes[prevIdx]) {
		return false
	}
	nextIdx := idx + width
	if nextIdx >= len(runes) {
		return true
	}
	return !isWordRune(runes[nextIdx])
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
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
