package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/audit"
)

func TestTheVerifierAcceptsTheRealThingAndNothingElse(t *testing.T) {
	signer, err := audit.NewSigner("k1", bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	out := audit.BuildExport(signer, nil, audit.ExportOptions{
		FacilityName: "x", RequestedBy: "A001", GeneratedAt: time.Date(2026, 9, 3, 4, 0, 0, 0, time.UTC),
		Chain: audit.Verification{OK: true},
	})
	dir := t.TempDir()
	pdf := filepath.Join(dir, "report.pdf")
	sig := filepath.Join(dir, "report.pdf.sig.json")
	if err := os.WriteFile(pdf, out.PDF, 0o600); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out.Signature)
	if err := os.WriteFile(sig, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	devnull, _ := os.Open(os.DevNull)
	defer func() { _ = devnull.Close() }()

	if code := run([]string{"-key", signer.PublicKey(), pdf, sig}, devnull, devnull); code != 0 {
		t.Fatalf("a real export exited %d", code)
	}
	tampered := append([]byte(nil), out.PDF...)
	tampered[100] ^= 1
	if err := os.WriteFile(pdf, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"-key", signer.PublicKey(), pdf, sig}, devnull, devnull); code != 1 {
		t.Fatalf("a tampered export exited %d", code)
	}
	if code := run([]string{pdf}, devnull, devnull); code != 2 {
		t.Fatalf("bad usage exited %d", code)
	}
}
