package formulary_test

import (
	"errors"
	"testing"

	"github.com/AmlanWTK/DTHCMS/backend/internal/formulary"
)

// Money (CP75).
//
// The constraint is "money is not a float", and the reason is not abstract: 0.34 has no float64
// representation, and the affordability research in §12.3 sums a year of unit prices per patient.
// A representation that is wrong in the sixteenth decimal place is wrong by whole taka once it
// has been multiplied by a monthly quantity and added up across a cohort — and it is wrong in a
// way that looks like rounding until somebody checks it against a receipt.
//
// So the representation is integer poisha, and the interesting cases are all at the parser: what
// a spreadsheet cell can contain, and what must be refused rather than silently made to fit.

func TestParseMoneyAcceptsWhatASpreadsheetCellHolds(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want formulary.Money
		why  string
	}{
		{"5", 500, "a whole number of taka"},
		{"5.0", 500, "one decimal place"},
		{"5.00", 500, "two decimal places"},
		{"5.000", 500, "trailing zeros are not extra precision — 5.000 is five taka"},
		{"0.34", 34, "thirty-four poisha, the cheapest thing in the seed"},
		{"0.3", 30, "one decimal place is tenths of a taka, not hundredths"},
		{".5", 50, "a leading decimal point, which Excel produces"},
		{"14259", 1425900, "the most expensive thing in the seed, a semaglutide pen"},
		{"14,259.00", 1425900, "thousands separators, which a pasted price carries"},
		{"৳ 12.50", 1250, "a taka sign, which somebody will type"},
		{"Tk 12.50", 1250, "the other taka sign"},
		{"  7.25  ", 725, "surrounding whitespace"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got, err := formulary.ParseMoney(tc.in)
			if err != nil {
				t.Fatalf("%q: %v — %s", tc.in, err, tc.why)
			}
			if got != tc.want {
				t.Errorf("%q parsed to %d poisha, want %d — %s", tc.in, got, tc.want, tc.why)
			}
		})
	}
}

// TestParseMoneyRefusesRatherThanRounds is the half that matters.
//
// A price of 0.335 per tablet is a per-unit figure somebody derived by dividing a pack price.
// Quietly storing 0.34 makes the pack price stop adding up, and that is discovered — if ever —
// by a pharmacist who has stopped trusting the screen.
func TestParseMoneyRefusesRatherThanRounds(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want error
		why  string
	}{
		{"0.335", formulary.ErrMoneyPrecision, "finer than a poisha: refused, not rounded to 0.34"},
		{"5.125", formulary.ErrMoneyPrecision, "three decimal places"},
		{"0.001", formulary.ErrMoneyPrecision, "a tenth of a poisha"},
		{"six taka", formulary.ErrMoneyFormat, "words"},
		{"", formulary.ErrMoneyFormat, "an empty cell"},
		{"-5.00", formulary.ErrMoneyFormat, "a negative price"},
		{"0", formulary.ErrMoneyRange, "free is not a price this clinic charges"},
		{"0.00", formulary.ErrMoneyRange, "the same, written out"},
		{"100001", formulary.ErrMoneyRange, "above the ceiling: a decimal point in the wrong place"},
		{"99999999999999999999", formulary.ErrMoneyRange, "an overflow, caught before the multiplication"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got, err := formulary.ParseMoney(tc.in)
			if err == nil {
				t.Fatalf("%q parsed to %d poisha; it must be refused — %s", tc.in, got, tc.why)
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("%q gave %v, want %v — %s", tc.in, err, tc.want, tc.why)
			}
		})
	}
}

// TestMoneyRendersWithTwoDecimalsAndNoSymbol.
//
// Always two decimals, because a price list where some rows read "5" and others "5.00" is one a
// person reads a decimal point into the wrong column of. No symbol, because the interface draws
// ৳ or Tk depending on the reader's language and a symbol baked into the number is one the
// Bangla screen cannot change.
func TestMoneyRendersWithTwoDecimalsAndNoSymbol(t *testing.T) {
	for _, tc := range []struct {
		in   formulary.Money
		want string
	}{
		{500, "5.00"},
		{34, "0.34"},
		{5, "0.05"},
		{1425900, "14259.00"},
		{1250, "12.50"},
		{0, "0.00"},
	} {
		if got := tc.in.String(); got != tc.want {
			t.Errorf("%d poisha renders as %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestMoneyRoundTripsThroughText is the property the whole representation exists for: a price
// that goes into the database and comes back out is the same price, exactly, every time.
func TestMoneyRoundTripsThroughText(t *testing.T) {
	for amount := formulary.Money(1); amount <= 2000; amount++ {
		parsed, err := formulary.ParseMoney(amount.String())
		if err != nil {
			t.Fatalf("%s: %v", amount, err)
		}
		if parsed != amount {
			t.Fatalf("%d poisha rendered as %q and parsed back to %d", amount, amount.String(), parsed)
		}
	}
}

// TestTakaBuildsFromWholeAndFraction, so a constant in a test reads as an amount.
func TestTakaBuildsFromWholeAndFraction(t *testing.T) {
	if got := formulary.Taka(12, 50); got != 1250 {
		t.Errorf("Taka(12, 50) is %d poisha, want 1250", got)
	}
}
