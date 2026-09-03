// Command auditverify checks an exported audit trail against its signature, offline.
//
//	go run ./tools/auditverify -key <base64 public key> report.pdf report.pdf.sig.json
//
// The key is what GET /v1/audit/signing-key returns and what the operations guide prints.
// The signature file is the three headers the export came with, as JSON:
//
//	{"key_id": "...", "algorithm": "ed25519-sha256", "sha256": "...", "signature": "..."}
//
// Exit status 0 means the file is, byte for byte, the one the system signed. Anything
// else is printed and the status is 1. Nothing here needs the server or the database: a
// regulator with the PDF, the signature and the key can run it on their own machine.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/AmlanWTK/DTHCMS/backend/internal/audit"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("auditverify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	key := fs.String("key", "", "base64 Ed25519 public key (from /v1/audit/signing-key)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *key == "" || fs.NArg() != 2 {
		_, _ = fmt.Fprintln(stderr, "usage: auditverify -key <public key> <file.pdf> <signature.json>")
		return 2
	}
	file, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "reading the file:", err)
		return 1
	}
	raw, err := os.ReadFile(fs.Arg(1))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "reading the signature:", err)
		return 1
	}
	var sig audit.Signature
	if err := json.Unmarshal(raw, &sig); err != nil {
		_, _ = fmt.Fprintln(stderr, "the signature file is not the JSON the export produced:", err)
		return 1
	}
	if err := audit.Verify(*key, file, sig); err != nil {
		_, _ = fmt.Fprintln(stderr, "NOT VERIFIED:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "verified: %s is the file signed with key %s\n", fs.Arg(0), sig.KeyID)
	return 0
}
