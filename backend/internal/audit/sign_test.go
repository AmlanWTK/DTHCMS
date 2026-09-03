package audit_test

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/audit"
)

// Criterion 4: the export PDF verifies against its signature — and nothing else does.

func testSigner(t *testing.T) *audit.Signer {
	t.Helper()
	s, err := audit.NewSigner("test-key-1", bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAnExportVerifiesAndATamperedOneDoesNot(t *testing.T) {
	signer := testSigner(t)
	at := time.Date(2026, 9, 3, 4, 42, 0, 0, time.UTC)
	events := []audit.Event{
		{Seq: 2, RecordedAt: at.Add(time.Minute), Hash: bytes.Repeat([]byte{2}, 32), Entry: audit.Entry{
			Kind: "role.granted", ActorCode: "A001", TargetCode: "N006", Details: map[string]any{"role": "NUTRITIONIST"}}},
		{Seq: 1, RecordedAt: at, Hash: bytes.Repeat([]byte{1}, 32), Entry: audit.Entry{
			Kind: "session.login", ActorCode: "A001"}},
	}
	out := audit.BuildExport(signer, events, audit.ExportOptions{
		FacilityName: "DTHC Faridpur", RequestedBy: "A001", GeneratedAt: at,
		Filter: map[string]string{"person": "N006"}, Chain: audit.Verification{OK: true, Checked: 2, HeadSeq: 2},
	})

	if !bytes.HasPrefix(out.PDF, []byte("%PDF-1.4")) || !bytes.Contains(out.PDF, []byte("%%EOF")) {
		t.Fatal("the export is not a PDF")
	}
	if !bytes.Contains(out.PDF, []byte("A001 granted NUTRITIONIST to N006")) {
		t.Error("the sentence is not in the file")
	}
	if !bytes.Contains(out.PDF, []byte("#1  2026-09-03 10:42:00  session.login")) {
		t.Error("the entries are not printed oldest first in Dhaka time")
	}
	if out.Signature.KeyID != "test-key-1" || out.Signature.Algorithm != audit.Algorithm {
		t.Errorf("signature metadata: %+v", out.Signature)
	}

	if err := audit.Verify(signer.PublicKey(), out.PDF, out.Signature); err != nil {
		t.Fatalf("the export does not verify against its own signature: %v", err)
	}

	// One byte changed anywhere: refused.
	tampered := append([]byte(nil), out.PDF...)
	i := bytes.Index(tampered, []byte("NUTRITIONIST"))
	tampered[i] = 'M'
	if err := audit.Verify(signer.PublicKey(), tampered, out.Signature); err == nil {
		t.Fatal("a tampered export verified")
	}

	// Another clinic's key: refused.
	other, _ := audit.NewSigner("other", bytes.Repeat([]byte{8}, 32))
	if err := audit.Verify(other.PublicKey(), out.PDF, out.Signature); err == nil {
		t.Fatal("verified against the wrong key")
	}

	// A signature with its digest edited: refused before the key is even consulted.
	bad := out.Signature
	bad.Digest = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0}, 32))
	if err := audit.Verify(signer.PublicKey(), out.PDF, bad); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("a wrong digest was not refused as such: %v", err)
	}
}

func TestTheSameTrailProducesTheSameBytes(t *testing.T) {
	// The signature is over the bytes; a renderer that varied between runs would make
	// every stored signature meaningless.
	signer := testSigner(t)
	at := time.Date(2026, 9, 3, 4, 42, 0, 0, time.UTC)
	events := []audit.Event{{Seq: 1, RecordedAt: at, Hash: bytes.Repeat([]byte{1}, 32), Entry: audit.Entry{Kind: "session.login", ActorCode: "A001"}}}
	opts := audit.ExportOptions{FacilityName: "x", RequestedBy: "A001", GeneratedAt: at,
		Filter: map[string]string{"kind": "session.login", "actor": "A001"}, Chain: audit.Verification{OK: true}}
	a := audit.BuildExport(signer, events, opts)
	b := audit.BuildExport(signer, events, opts)
	if !bytes.Equal(a.PDF, b.PDF) || a.Signature != b.Signature {
		t.Fatal("two renders of the same trail differ")
	}
}

func TestASeedOfTheWrongSizeIsRefused(t *testing.T) {
	if _, err := audit.NewSigner("k", []byte("short")); err == nil {
		t.Fatal("a short seed was accepted")
	}
	if _, err := audit.NewSigner("", bytes.Repeat([]byte{1}, 32)); err == nil {
		t.Fatal("a key without an id was accepted")
	}
}
