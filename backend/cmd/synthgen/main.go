// Command synthgen generates a fictional patient population from a clinician-authored
// case-mix profile.
//
// Nothing it emits is derived from a real patient. The profile it samples is an aggregate
// clinical impression — "roughly a third present for weight and metabolic problems" — not an
// extract, and the generator is documented in internal/platform/synthetic.
//
// Every run is reproducible. The seed and the as-of date are inputs rather than defaults
// read from the clock, so a dataset can be cited: a defect found in patient 4,183 of seed 42
// is reachable by anyone who runs the same command.
//
// Usage:
//
//	synthgen -n 5000                       5,000 patients as NDJSON on stdout
//	synthgen -n 5000 -out cohort.ndjson    the same, to a file
//	synthgen -summary                      the distributions, next to the profile's
//	synthgen -review -n 40 -out review.html  a page a clinician can read and mark up
//	synthgen -case hypoglycaemia           one patient built to exercise that scenario
//	synthgen -list-cases                   the scenarios -case understands
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/synthetic"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "synthgen:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		count     = flag.Int("n", 100, "how many patients to generate")
		seed      = flag.Int64("seed", 1, "seed; the same seed and profile always give the same people")
		profile   = flag.String("profile", "internal/testdata/profile.v1.json", "path to the case-mix profile")
		out       = flag.String("out", "", "write here instead of stdout")
		asOfFlag  = flag.String("as-of", "", "anchor date, YYYY-MM-DD (default: today, UTC)")
		summary   = flag.Bool("summary", false, "print the generated distributions beside the profile's")
		review    = flag.Bool("review", false, "write an HTML page for clinical review instead of data")
		caseName  = flag.String("case", "", "generate patients built to exercise one named scenario")
		withCases = flag.Bool("with-cases", false, "append one of every named scenario to the output")
		listCases = flag.Bool("list-cases", false, "list the scenarios -case understands")
	)
	flag.Parse()

	if *listCases {
		for _, c := range synthetic.TestCases() {
			fmt.Println(c)
		}
		return nil
	}

	asOf := time.Now().UTC().Truncate(24 * time.Hour)
	if *asOfFlag != "" {
		parsed, err := time.Parse("2006-01-02", *asOfFlag)
		if err != nil {
			return fmt.Errorf("-as-of must be YYYY-MM-DD: %w", err)
		}
		asOf = parsed
	}

	p, err := synthetic.LoadProfile(*profile)
	if err != nil {
		return err
	}
	g := synthetic.New(p, *seed, asOf)

	people, err := generate(g, *caseName, *count)
	if err != nil {
		return err
	}

	// The sampled patients answer "does the everyday register read right". The named
	// scenarios answer "does the hard case read right", and they are what a reviewer will
	// actually argue with — an eGFR of 22, a TSH of 140, seven medicines at once.
	if *withCases && *caseName == "" {
		for _, name := range synthetic.TestCases() {
			q, err := g.GenerateCase(name)
			if err != nil {
				return err
			}
			people = append(people, q)
		}
	}

	sink, closeSink, err := openSink(*out)
	if err != nil {
		return err
	}
	defer closeSink()

	switch {
	case *summary:
		synthetic.WriteSummary(sink, p, people)
	case *review:
		command := fmt.Sprintf("synthgen -review -n %d -seed %d -as-of %s",
			*count, *seed, asOf.Format("2006-01-02"))
		if *withCases {
			command += " -with-cases"
		}
		if *profile != "internal/testdata/profile.v1.json" {
			command += " -profile " + *profile
		}
		if err := synthetic.WriteReview(sink, p, people, *seed, asOf, command); err != nil {
			return err
		}
	default:
		if err := writeNDJSON(sink, people); err != nil {
			return err
		}
	}

	if *out != "" {
		fmt.Fprintf(os.Stderr, "%d patients, seed %d, as of %s → %s\n",
			len(people), *seed, asOf.Format("2006-01-02"), *out)
	}
	return nil
}

func generate(g *synthetic.Generator, caseName string, count int) ([]synthetic.Patient, error) {
	if count < 1 {
		return nil, fmt.Errorf("-n must be at least 1, got %d", count)
	}
	if caseName == "" {
		return g.Generate(count), nil
	}

	people := make([]synthetic.Patient, 0, count)
	for i := 0; i < count; i++ {
		q, err := g.GenerateCase(caseName)
		if err != nil {
			return nil, fmt.Errorf("%w\navailable: %s", err, strings.Join(synthetic.TestCases(), ", "))
		}
		people = append(people, q)
	}
	return people, nil
}

func openSink(path string) (io.Writer, func(), error) {
	if path == "" {
		w := bufio.NewWriter(os.Stdout)
		return w, func() { _ = w.Flush() }, nil
	}

	f, err := os.Create(path) //nolint:gosec // the operator names the output file
	if err != nil {
		return nil, nil, fmt.Errorf("creating %s: %w", path, err)
	}
	w := bufio.NewWriter(f)
	return w, func() { _ = w.Flush(); _ = f.Close() }, nil
}

// writeNDJSON emits one patient per line.
//
// Newline-delimited rather than a JSON array so the output streams: a 200,000-patient load
// fixture is read a line at a time by the loader and never held whole in memory, at either end.
func writeNDJSON(w io.Writer, people []synthetic.Patient) error {
	enc := json.NewEncoder(w)
	for _, q := range people {
		if err := enc.Encode(q); err != nil {
			return fmt.Errorf("encoding %s: %w", q.ID, err)
		}
	}
	return nil
}
