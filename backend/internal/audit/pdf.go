package audit

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"
)

// A PDF writer just large enough for an audit trail: A4 pages of monospaced lines with a
// header. Written by hand rather than imported, because the file has to be byte-for-byte
// reproducible for the signature to mean anything, and a dependency that changes its
// output between minor versions would silently break every stored signature.
//
// Text is set in Courier from the standard fourteen, which means WinAnsi only: a character
// outside Latin-1 is replaced with "?". The English rendering is what the export carries;
// the Bengali sentence is in the viewer and, once a font is embedded (D-73), here too.

const (
	pageWidth  = 595.28 // A4, points
	pageHeight = 841.89
	marginLeft = 40.0
	marginTop  = 50.0
	lineHeight = 11.0
	bodySize   = 8.0
	titleSize  = 13.0
	linesPage  = 66
)

type pdfDoc struct {
	title string
	meta  []string
	lines []string
}

// pdfEscape makes a string safe inside a PDF literal string.
func pdfEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '(' || r == ')' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 32:
			b.WriteByte(' ')
		case r > 255:
			b.WriteByte('?')
		case r > 126:
			// WinAnsi octal escape.
			fmt.Fprintf(&b, "\\%03o", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// wrap breaks a line at spaces so it fits the column; a word longer than the column is
// cut rather than lost.
func wrap(s string, width int) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := ""
		for _, w := range words {
			for len([]rune(w)) > width {
				if line != "" {
					out = append(out, line)
					line = ""
				}
				out = append(out, string([]rune(w)[:width]))
				w = string([]rune(w)[width:])
			}
			switch {
			case line == "":
				line = w
			case len([]rune(line))+1+len([]rune(w)) <= width:
				line += " " + w
			default:
				out = append(out, line)
				line = w
			}
		}
		out = append(out, line)
	}
	return out
}

// render writes the document. Objects: catalog, pages, font, then one page + one content
// stream per page. No compression, no dates, no ids — nothing that would make two renders
// of the same content differ.
func (d *pdfDoc) render() []byte {
	// Paginate: title and meta on the first page, then body lines.
	type page struct{ lines []string }
	var pages []page
	var cur []string
	flush := func() {
		pages = append(pages, page{lines: cur})
		cur = nil
	}
	for _, l := range d.lines {
		if len(cur) >= linesPage {
			flush()
		}
		cur = append(cur, l)
	}
	if len(cur) > 0 || len(pages) == 0 {
		flush()
	}

	var objects [][]byte
	add := func(body string) int {
		objects = append(objects, []byte(body))
		return len(objects)
	}
	// Reserve 1: catalog, 2: pages, 3: font (body), 4: font (bold title).
	add("")
	add("")
	add("<< /Type /Font /Subtype /Type1 /BaseFont /Courier /Encoding /WinAnsiEncoding >>")
	add("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>")

	var pageIDs []int
	for i, p := range pages {
		var content bytes.Buffer
		y := pageHeight - marginTop
		if i == 0 {
			fmt.Fprintf(&content, "BT /F2 %.1f Tf %.2f %.2f Td (%s) Tj ET\n", titleSize, marginLeft, y, pdfEscape(d.title))
			y -= lineHeight * 1.8
			for _, m := range d.meta {
				fmt.Fprintf(&content, "BT /F1 %.1f Tf %.2f %.2f Td (%s) Tj ET\n", bodySize, marginLeft, y, pdfEscape(m))
				y -= lineHeight
			}
			y -= lineHeight * 0.6
		}
		for _, l := range p.lines {
			fmt.Fprintf(&content, "BT /F1 %.1f Tf %.2f %.2f Td (%s) Tj ET\n", bodySize, marginLeft, y, pdfEscape(l))
			y -= lineHeight
		}
		footer := fmt.Sprintf("Page %d of %d", i+1, len(pages))
		fmt.Fprintf(&content, "BT /F1 %.1f Tf %.2f %.2f Td (%s) Tj ET\n", bodySize, marginLeft, 28.0, pdfEscape(footer))

		contentID := add(fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", content.Len(), content.String()))
		pageID := add(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.2f %.2f] /Resources << /Font << /F1 3 0 R /F2 4 0 R >> >> /Contents %d 0 R >>",
			pageWidth, pageHeight, contentID))
		pageIDs = append(pageIDs, pageID)
	}

	kids := make([]string, 0, len(pageIDs))
	for _, id := range pageIDs {
		kids = append(kids, fmt.Sprintf("%d 0 R", id))
	}
	objects[0] = []byte("<< /Type /Catalog /Pages 2 0 R >>")
	objects[1] = []byte(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pageIDs)))

	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, len(objects))
	for i, obj := range objects {
		offsets[i] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, off := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return out.Bytes()
}

// ascii keeps the report readable when a name is in Bengali: the code beside it carries
// the identity, and a run of question marks says "there was a name here".
func ascii(s string) string {
	return strings.Map(func(r rune) rune {
		if r > unicode.MaxLatin1 {
			return '?'
		}
		return r
	}, s)
}
