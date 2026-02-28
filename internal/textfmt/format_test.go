package textfmt

import "testing"

func TestMarkdownToPreviewHTMLSupportsBoldItalicAndEscaping(t *testing.T) {
	input := "Hola **mundo** y *equipo* <script>alert(1)</script>"
	got := MarkdownToPreviewHTML(input)
	want := "Hola <strong>mundo</strong> y <em>equipo</em> &lt;script&gt;alert(1)&lt;/script&gt;"
	if got != want {
		t.Fatalf("preview html = %q, want %q", got, want)
	}
}

func TestMarkdownToPreviewHTMLSupportsMultiline(t *testing.T) {
	input := "linea1\nlinea2"
	got := MarkdownToPreviewHTML(input)
	want := "linea1<br>linea2"
	if got != want {
		t.Fatalf("preview html multiline = %q, want %q", got, want)
	}
}

func TestMarkdownToPreviewHTMLSupportsUnderscoreMarkers(t *testing.T) {
	input := "Hola __mundo__ y _equipo_"
	got := MarkdownToPreviewHTML(input)
	want := "Hola <strong>mundo</strong> y <em>equipo</em>"
	if got != want {
		t.Fatalf("preview html underscore markers = %q, want %q", got, want)
	}
}

func TestMarkdownToPreviewHTMLDoesNotTreatSnakeCaseAsItalic(t *testing.T) {
	input := "usa variable snake_case aqui"
	got := MarkdownToPreviewHTML(input)
	want := "usa variable snake_case aqui"
	if got != want {
		t.Fatalf("preview html snake_case = %q, want %q", got, want)
	}
}

func TestMarkdownToRTFSupportsBoldItalicAndEscaping(t *testing.T) {
	input := "Hola **mundo** y *equipo* {ok}"
	got := MarkdownToRTF(input)
	want := "{\\rtf1\\ansi\\deff0 Hola \\b mundo\\b0  y \\i equipo\\i0  \\{ok\\}}"
	if got != want {
		t.Fatalf("rtf = %q, want %q", got, want)
	}
}

func TestMarkdownToRTFSupportsUnderscoreMarkers(t *testing.T) {
	input := "Hola __mundo__ y _equipo_"
	got := MarkdownToRTF(input)
	want := "{\\rtf1\\ansi\\deff0 Hola \\b mundo\\b0  y \\i equipo\\i0 }"
	if got != want {
		t.Fatalf("rtf underscore markers = %q, want %q", got, want)
	}
}

func TestMarkdownToRTFSupportsUnicodeAndNewline(t *testing.T) {
	input := "línea 1\nlínea 2"
	got := MarkdownToRTF(input)
	want := "{\\rtf1\\ansi\\deff0 l\\u237?nea 1\\line l\\u237?nea 2}"
	if got != want {
		t.Fatalf("rtf unicode/newline = %q, want %q", got, want)
	}
}

func TestMarkdownToUnicodeStyledSupportsBoldAndItalic(t *testing.T) {
	input := "Hola **mundo** _equipo_"
	got := MarkdownToUnicodeStyled(input)
	want := "Hola 𝗺𝘂𝗻𝗱𝗼 𝑒𝑞𝑢𝑖𝑝𝑜"
	if got != want {
		t.Fatalf("unicode styled = %q, want %q", got, want)
	}
}

func TestMarkdownToUnicodeStyledSupportsEscapedMarkers(t *testing.T) {
	input := `Literal \*asterisco\* y \_guion\_`
	got := MarkdownToUnicodeStyled(input)
	want := `Literal *asterisco* y _guion_`
	if got != want {
		t.Fatalf("unicode escaped = %q, want %q", got, want)
	}
}
