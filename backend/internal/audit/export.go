package audit

import (
	"encoding/hex"
	"fmt"
	"sort"
	"time"
)

// Export is a trail on paper: the filtered events as sentences, the chain hashes beside
// them so the paper can be matched to the database, and a signature over the whole file.
type Export struct {
	PDF       []byte
	Signature Signature
	Count     int
	// Filename is what the browser saves it as.
	Filename string
}

// ExportOptions say what was asked for, so the report can print it.
type ExportOptions struct {
	FacilityName string
	RequestedBy  string
	Since, Until time.Time
	Filter       map[string]string
	GeneratedAt  time.Time
	// ChainOK reports the verification the exporter ran first; a report over a broken
	// chain says so on its face.
	Chain Verification
}

// BuildExport renders and signs. Events are given newest-first (the viewer's order) and
// printed oldest-first, which is how a reader follows a day.
func BuildExport(signer *Signer, events []Event, opts ExportOptions) Export {
	doc := &pdfDoc{title: "DTHCMS security audit trail"}
	doc.meta = append(doc.meta,
		fmt.Sprintf("Facility: %s", ascii(opts.FacilityName)),
		fmt.Sprintf("Generated: %s (Asia/Dhaka) by %s", opts.GeneratedAt.In(Dhaka).Format("2006-01-02 15:04"), ascii(opts.RequestedBy)),
	)
	if !opts.Since.IsZero() || !opts.Until.IsZero() {
		doc.meta = append(doc.meta, fmt.Sprintf("Period: %s to %s", dateOrOpen(opts.Since), dateOrOpen(opts.Until)))
	}
	keys := make([]string, 0, len(opts.Filter))
	for k := range opts.Filter {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if v := opts.Filter[k]; v != "" {
			doc.meta = append(doc.meta, fmt.Sprintf("Filter: %s = %s", k, ascii(v)))
		}
	}
	chain := "Chain: verified, " + fmt.Sprintf("%d rows, head %d", opts.Chain.Checked, opts.Chain.HeadSeq)
	if !opts.Chain.OK {
		chain = fmt.Sprintf("Chain: VERIFICATION FAILED at row %d (%s)", opts.Chain.BrokenAt, ascii(opts.Chain.Problem))
	}
	doc.meta = append(doc.meta, chain, fmt.Sprintf("Entries: %d", len(events)),
		"Signature: Ed25519 over SHA-256 of this file; key id "+signer.KeyID()+". Verify with tools/auditverify.")

	const width = 100
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		head := fmt.Sprintf("#%d  %s  %s", ev.Seq, ev.RecordedAt.In(Dhaka).Format("2006-01-02 15:04:05"), ev.Kind)
		doc.lines = append(doc.lines, head)
		doc.lines = append(doc.lines, wrap("    "+ascii(Describe(ev, English)), width)...)
		doc.lines = append(doc.lines, "    hash "+hex.EncodeToString(ev.Hash)[:32]+"...", "")
	}
	if len(events) == 0 {
		doc.lines = append(doc.lines, "No entries match.")
	}

	pdf := doc.render()
	return Export{
		PDF: pdf, Signature: signer.Sign(pdf), Count: len(events),
		Filename: fmt.Sprintf("dthcms-audit-%s.pdf", opts.GeneratedAt.In(Dhaka).Format("20060102-1504")),
	}
}

func dateOrOpen(t time.Time) string {
	if t.IsZero() {
		return "open"
	}
	return t.In(Dhaka).Format("2006-01-02")
}
