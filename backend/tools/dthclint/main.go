// Command dthclint enforces the DTHCMS architecture rules that a compiler cannot.
//
// Two checks run today:
//
//	arch — module boundaries. A domain module may import only the modules its entry in
//	       architecture.json allows. This is what keeps the modular monolith modular:
//	       documented boundaries decay within weeks, enforced ones do not.
//
//	testonly — test-only doors. A function marked //dthclint:testonly may be called from
//	       _test.go files and nothing else. It is how a constructor that exists for tests
//	       stays out of production code, where it would undo the very guarantee it was
//	       written around (CP24: eventstore.ActorForTest).
//
//	phi  — patient data in logs. Logging a patient's name, national ID, phone or address
//	       writes identifiable health data to a system that is neither access-controlled
//	       nor covered by the clinical audit trail. It is one of the most common and most
//	       damaging mistakes in clinical software, and it is easy to make by accident.
//
// Usage:
//
//	go run ./tools/dthclint all      # both checks (what CI runs)
//	go run ./tools/dthclint arch
//	go run ./tools/dthclint testonly
//	go run ./tools/dthclint phi
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	root := flag.String("root", ".", "backend module root to inspect")
	flag.Parse()

	check := "all"
	if args := flag.Args(); len(args) > 0 {
		check = args[0]
	}

	absRoot, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dthclint: %v\n", err)
		os.Exit(2)
	}

	var findings []Finding

	switch check {
	case "arch":
		findings, err = RunArch(absRoot)
	case "phi":
		findings, err = RunPHI(absRoot)
	case "testonly":
		findings, err = RunTestOnly(absRoot)
	case "all":
		for _, run := range []func(string) ([]Finding, error){RunArch, RunPHI, RunTestOnly} {
			var found []Finding
			found, err = run(absRoot)
			if err != nil {
				break
			}
			findings = append(findings, found...)
		}
	default:
		fmt.Fprintf(os.Stderr, "dthclint: unknown check %q (want arch, phi, testonly or all)\n", check)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "dthclint: %v\n", err)
		os.Exit(2)
	}

	if len(findings) == 0 {
		fmt.Printf("dthclint: %s — no violations\n", check)
		return
	}

	for _, f := range findings {
		fmt.Fprintln(os.Stderr, f.String())
	}
	fmt.Fprintf(os.Stderr, "\ndthclint: %d violation(s)\n", len(findings))
	os.Exit(1)
}

// Finding is a single rule violation, formatted so editors can jump to it.
type Finding struct {
	Check   string
	File    string
	Line    int
	Message string
	Hint    string
}

func (f Finding) String() string {
	s := fmt.Sprintf("%s:%d: [%s] %s", f.File, f.Line, f.Check, f.Message)
	if f.Hint != "" {
		s += "\n    " + f.Hint
	}
	return s
}
