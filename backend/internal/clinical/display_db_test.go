package clinical_test

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The display units, checked against the database (CP44, [R-08]).
//
// `/v1/observations/units` deliberately does not return conversion factors: the conversion
// that decides what is *stored* happens once, in the database, so a client cannot arrive at
// a different canonical value from the one the server will write.
//
// Display is a different problem. A tablet in a corridor with no signal still has to draw
// "69.5 kg / 153.2 lb", so the display factors live in `@dthcms/clinical-calc`. Two copies of
// a conversion factor is one copy that drifts, so this test reads the TypeScript source and
// compares every number in it with `core.unit`.
//
// Reading a `.ts` file from a Go test is unusual and worth defending: the alternative is a
// build step that exports the table to JSON, which is a build step that can be stale, and a
// generated file nobody reads. The regexes below are narrow enough to fail loudly if the
// file's shape changes, which is the right failure — a check that silently stopped checking
// would be worse than no check.

const displaySource = "../../../packages/clinical-calc/src/display.ts"

var (
	pairPattern = regexp.MustCompile(
		`'?([A-Za-z0-9\[\]/#{}._%-]+)'?:\s*\{\s*unit:\s*'([^']+)',\s*factor:\s*([-0-9.]+),\s*offset:\s*([-0-9.]+),\s*decimals:\s*(\d+)\s*\}`)
	decimalsPattern = regexp.MustCompile(`'?([A-Za-z0-9\[\]/#{}._%-]+)'?:\s*(\d+),`)
)

func readDisplaySource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(displaySource))
	if err != nil {
		t.Fatalf("the display table is the client's half of the conversion contract "+
			"and could not be read: %v", err)
	}
	return string(raw)
}

func TestTheDisplayUnitsAgreeWithTheDatabase(t *testing.T) {
	h := newAPI(t)
	source := readDisplaySource(t)

	// Only the DISPLAY_PAIRS block: CANONICAL_DECIMALS below it has a different shape and
	// the same regex would happily half-match it.
	start := strings.Index(source, "export const DISPLAY_PAIRS")
	end := strings.Index(source, "export const CANONICAL_DECIMALS")
	if start < 0 || end < 0 || end < start {
		t.Fatal("display.ts no longer has the shape this check reads; update the check " +
			"rather than deleting it — it is the only thing keeping two copies of every " +
			"conversion factor in step")
	}
	matches := pairPattern.FindAllStringSubmatch(source[start:end], -1)
	if len(matches) < 6 {
		t.Fatalf("only %d display pairs were parsed out of display.ts", len(matches))
	}

	for _, m := range matches {
		canonical, secondary := m[1], m[2]
		factor, err := strconv.ParseFloat(m[3], 64)
		if err != nil {
			t.Fatalf("%s: %v", canonical, err)
		}
		offset, err := strconv.ParseFloat(m[4], 64)
		if err != nil {
			t.Fatalf("%s: %v", canonical, err)
		}

		t.Run(canonical+"→"+secondary, func(t *testing.T) {
			var dbFactor, dbOffset float64
			var dbDimension, canonicalDimension string
			if err := h.SQL.QueryRow(
				`SELECT factor, "offset", dimension FROM core.unit WHERE code = $1`,
				secondary).Scan(&dbFactor, &dbOffset, &dbDimension); err != nil {
				t.Fatalf("%s is not a unit the database knows: %v", secondary, err)
			}
			if err := h.SQL.QueryRow(
				`SELECT dimension FROM core.unit WHERE code = $1 AND is_canonical`,
				canonical).Scan(&canonicalDimension); err != nil {
				t.Fatalf("%s is not a canonical unit: %v", canonical, err)
			}
			if dbDimension != canonicalDimension {
				t.Errorf("the display pairs %s with %s, but they measure %s and %s",
					canonical, secondary, canonicalDimension, dbDimension)
			}
			if math.Abs(dbFactor-factor) > 1e-12 {
				t.Errorf("factor: display.ts has %v, core.unit has %v", factor, dbFactor)
			}
			if math.Abs(dbOffset-offset) > 1e-12 {
				t.Errorf("offset: display.ts has %v, core.unit has %v", offset, dbOffset)
			}
		})
	}
}

func TestTheDisplayDecimalsAgreeWithTheDatabase(t *testing.T) {
	// Precision is a property of the unit, not of the screen: a weight in kg is 69.5 and the
	// same weight in grams is not 69500.0. A screen that rounded differently from the record
	// would produce two people reading the same value aloud and disagreeing.
	h := newAPI(t)
	source := readDisplaySource(t)

	start := strings.Index(source, "export const CANONICAL_DECIMALS")
	if start < 0 {
		t.Fatal("display.ts no longer declares CANONICAL_DECIMALS")
	}
	end := strings.Index(source[start:], "});")
	if end < 0 {
		t.Fatal("CANONICAL_DECIMALS is not closed where this check expects")
	}

	matches := decimalsPattern.FindAllStringSubmatch(source[start:start+end], -1)
	if len(matches) < 15 {
		t.Fatalf("only %d canonical units were parsed out of display.ts", len(matches))
	}
	for _, m := range matches {
		unit := m[1]
		want, err := strconv.Atoi(m[2])
		if err != nil {
			t.Fatal(err)
		}
		var got int
		if err := h.SQL.QueryRow(
			`SELECT decimals FROM core.unit WHERE code = $1`, unit).Scan(&got); err != nil {
			t.Errorf("%s is in the display table but not in core.unit: %v", unit, err)
			continue
		}
		if got != want {
			t.Errorf("%s: display.ts writes %d decimals, core.unit says %d", unit, want, got)
		}
	}
}

func TestEveryCanonicalUnitHasADisplayDecision(t *testing.T) {
	// The gap this catches: a unit added to the registry and not to the display table. Such
	// a value would render with a default precision nobody chose — which looks fine, right
	// up until somebody notices a creatinine printed to one decimal.
	h := newAPI(t)
	source := readDisplaySource(t)

	rows, err := h.SQL.Query(`SELECT code FROM core.unit WHERE is_canonical ORDER BY code`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(source, "'"+code+"'") && !strings.Contains(source, code+":") {
			t.Errorf("%s is a canonical unit with no entry in the display table; a value in "+
				"it would render with a precision nobody chose", code)
		}
	}
}

// The entry units, checked against the database (CP45).
//
// The same discipline as the display pairs above, for the other direction. These factors
// decide nothing that is stored — a write still posts the number and the unit as typed, and
// `core.to_canonical` does the conversion that lands in the record. What they decide is the
// BMI on screen while the operator is still typing, and a panel that converted 154 lb with
// the wrong factor would show a clinically different number from the one saved a moment
// later. So they are the database's numbers, and this fails if they stop being.
func TestTheEntryUnitsAgreeWithTheDatabase(t *testing.T) {
	h := newAPI(t)
	source := readDisplaySource(t)

	start := strings.Index(source, "export const ENTRY_UNITS")
	end := strings.Index(source, "export function toCanonical")
	if start < 0 || end < 0 || end < start {
		t.Fatal("display.ts no longer has the shape this check reads; update the check " +
			"rather than deleting it")
	}
	block := source[start:end]

	pattern := regexp.MustCompile(
		`'?([A-Za-z0-9\[\]/#{}._%-]+)'?:\s*\{\s*canonical:\s*'([^']+)',\s*factor:\s*([-0-9.]+),\s*offset:\s*([-0-9.]+)\s*\}`)
	matches := pattern.FindAllStringSubmatch(block, -1)
	if len(matches) < 10 {
		t.Fatalf("found %d entry units in display.ts, which is fewer than a station form "+
			"needs — the regex has probably stopped matching", len(matches))
	}

	for _, match := range matches {
		unit, canonical := match[1], match[2]
		factor, err := strconv.ParseFloat(match[3], 64)
		if err != nil {
			t.Fatalf("%s: unreadable factor %q", unit, match[3])
		}
		offset, err := strconv.ParseFloat(match[4], 64)
		if err != nil {
			t.Fatalf("%s: unreadable offset %q", unit, match[4])
		}

		var dbFactor, dbOffset float64
		var dimension string
		row := h.SQL.QueryRow(
			`SELECT factor, "offset", dimension FROM core.unit WHERE code = $1`, unit)
		if err := row.Scan(&dbFactor, &dbOffset, &dimension); err != nil {
			t.Errorf("%s is an entry unit the registry does not have: %v", unit, err)
			continue
		}
		if math.Abs(dbFactor-factor) > 1e-12 {
			t.Errorf("%s: display.ts says factor %v, core.unit says %v", unit, factor, dbFactor)
		}
		if math.Abs(dbOffset-offset) > 1e-12 {
			t.Errorf("%s: display.ts says offset %v, core.unit says %v", unit, offset, dbOffset)
		}

		// And the canonical unit named must be the dimension's actual canonical unit,
		// because a factor that is right for the wrong target is still the wrong number.
		var canonicalInDB string
		if err := h.SQL.QueryRow(
			`SELECT code FROM core.unit WHERE dimension = $1 AND is_canonical`,
			dimension).Scan(&canonicalInDB); err != nil {
			t.Errorf("%s: no canonical unit for dimension %s: %v", unit, dimension, err)
			continue
		}
		if canonicalInDB != canonical {
			t.Errorf("%s: display.ts converts to %s, the registry's canonical is %s",
				unit, canonical, canonicalInDB)
		}
	}

	// Every unit a station form offers has to be in the table, or the panel goes blank at
	// the moment an operator switches the selector.
	for _, unit := range []string{"cm", "m", "in", "[ft_i]", "kg", "g", "[lb_av]",
		"mm[Hg]", "kPa", "Cel", "[degF]", "/min", "%"} {
		if !strings.Contains(block, "'"+unit+"'") && !strings.Contains(block, unit+":") {
			t.Errorf("%s can be entered at a station and is not in ENTRY_UNITS", unit)
		}
	}
}
