// Package formulary is the clinic's medicine catalogue and the history of what each medicine
// cost (CP75, §10, §16.1, D-56).
//
// # The question this package exists to answer
//
// Not "what does metformin cost" — "what did *this* metformin cost on the day it was
// prescribed". §12.3's affordability research is about how patients in Faridpur bear the cost of
// semaglutide and tirzepatide over months, and a cost computed at today's price answers a
// question nobody asked: a patient who stopped in March because the price rose in February is
// invisible to an analysis that prices March at February's number.
//
// So a price is never a column on a product. It is a row with a date range, and the range is the
// data. Recording a new price closes the old row's range and opens a new one; nothing is ever
// edited, and `core.medication_price_is_immutable` refuses it in the database as well as here.
// [PriceOn] is the whole of criterion 1 and it is four lines of SQL, which is the point — the
// correctness lives in the schema rather than in anybody remembering to filter.
//
// # Money
//
// [Money] is integer **poisha**, 1 BDT = 100 poisha, carried as int64 and rendered by
// [Money.String]. Never a float64: 0.34 has no float representation, and a research extract that
// sums a year of unit prices would be wrong by an unpredictable amount that looks like rounding
// until somebody checks it against a receipt.
//
// [ParseMoney] refuses anything finer than a poisha rather than rounding it. A price that was
// silently rounded is a price nobody can reconcile against the source it came from, and the whole
// of the monthly review is somebody reconciling prices against sources.
//
// # Unverified means unverified
//
// The 250 seeded prices are published MRP read off medex.com.bd, not what this clinic charges,
// and nobody has checked them. Every one carries [VerificationProvisional] and no recorder, the
// API returns both facts on every price it serves, and the screens draw them. Clearing one is
// what the monthly review is for, and it requires a person to record a new price with their own
// name on it — the database will not let a seeded row become VERIFIED.
//
// # No patient ever appears here
//
// A formulary holds trade names, strengths and prices. There is no patient in this package, no
// patient in its tables, and nothing here logs, traces or counts who looked up what. CP76's
// autocomplete and CP80's prescriptions are where a medicine meets a person; this is the
// catalogue, and keeping it free of patients is what lets the pharmacist — a role §4.4 blinds
// from clinical data — own it.
package formulary

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Money is an amount in Bangladeshi poisha. One taka is a hundred of them.
//
// An integer minor unit rather than a float or a decimal, for the reasons in the package note.
// The type exists so that a bare int64 cannot be passed where poisha are wanted and arrive as
// taka — a hundredfold error that would look entirely plausible on a semaglutide pen.
type Money int64

// Taka builds an amount from whole and fractional taka. For tests and for constants.
func Taka(taka int64, poisha int64) Money { return Money(taka*100 + poisha) }

// String renders "1234.50" — no symbol, no thousands separator, always two decimals.
//
// No symbol because the interface draws ৳ or Tk beside it depending on the reader's language,
// and a symbol baked into the number is one the Bangla screen cannot change. Always two decimals
// because a price list where some rows read "5" and others "5.00" is one a person reads a
// decimal point into the wrong column of.
func (m Money) String() string {
	sign := ""
	n := int64(m)
	if n < 0 {
		sign, n = "-", -n
	}
	return fmt.Sprintf("%s%d.%02d", sign, n/100, n%100)
}

// ErrMoneyPrecision is a price with more precision than a poisha.
//
// Refused rather than rounded. A CSV that says 0.335 per tablet is a per-unit figure somebody
// derived by dividing a pack price, and quietly storing 0.34 makes the pack price stop adding up
// — which is discovered, if ever, by a pharmacist who no longer trusts the screen.
var ErrMoneyPrecision = errors.New("formulary: a price cannot be finer than one poisha")

// ErrMoneyFormat is text that is not a number.
var ErrMoneyFormat = errors.New("formulary: that is not an amount")

// ErrMoneyRange is a price at or below zero, or one implausibly large.
var ErrMoneyRange = errors.New("formulary: that is not a plausible price")

// MaxPrice is the ceiling, matching the database's CHECK: one hundred thousand taka for one
// dispensed unit. The most expensive thing in the seed is a semaglutide pen at 14,259 BDT, so
// this is a decimal-point guard rather than a business rule.
const MaxPrice = Money(10_000_000)

// ParseMoney reads "5", "5.0", "5.00", "১২" is *not* accepted — see the note.
//
// ASCII digits only, matching the design system's decision that measurements, doses, dates and
// identifiers use ASCII numerals in both interfaces. A price list that arrived with Bengali
// numerals in some cells and ASCII in others is a price list nobody can sort, and the clinic's
// own spreadsheets are ASCII.
func ParseMoney(text string) (Money, error) {
	trimmed := strings.TrimSpace(text)
	trimmed = strings.TrimPrefix(trimmed, "৳")
	trimmed = strings.TrimPrefix(strings.TrimSpace(trimmed), "Tk")
	trimmed = strings.TrimSpace(strings.ReplaceAll(trimmed, ",", ""))
	if trimmed == "" {
		return 0, ErrMoneyFormat
	}

	whole, frac, hasFrac := strings.Cut(trimmed, ".")
	if whole == "" {
		whole = "0"
	}
	takaPart, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		// A number too large for an int64 is a price, not a typing error in the sense
		// ErrMoneyFormat means — it is a decimal point in the wrong place or a pasted
		// identifier, and "that is not a plausible price" is what the person needs to read.
		if errors.Is(err, strconv.ErrRange) {
			return 0, ErrMoneyRange
		}
		return 0, ErrMoneyFormat
	}
	if takaPart < 0 {
		return 0, ErrMoneyFormat
	}
	var poishaPart int64
	if hasFrac {
		// Trailing zeros beyond two places are not extra precision — "5.000" is five taka.
		trimmedFrac := strings.TrimRight(frac, "0")
		if trimmedFrac == "" {
			trimmedFrac = "0"
		}
		if len(trimmedFrac) > 2 {
			return 0, ErrMoneyPrecision
		}
		padded := (trimmedFrac + "00")[:2]
		poishaPart, err = strconv.ParseInt(padded, 10, 64)
		if err != nil || poishaPart < 0 {
			return 0, ErrMoneyFormat
		}
	}
	// Overflow before multiplication rather than after: a sixteen-digit price in a spreadsheet
	// cell should be a named error, not a negative amount.
	if takaPart > int64(MaxPrice)/100 {
		return 0, ErrMoneyRange
	}
	amount := Money(takaPart*100 + poishaPart)
	if amount <= 0 || amount > MaxPrice {
		return 0, ErrMoneyRange
	}
	return amount, nil
}

// Verification states. A price nobody has checked must never look like one somebody approved.
const (
	// VerificationProvisional is the state of every seeded price and of anything imported
	// without somebody saying they had checked it.
	VerificationProvisional = "PROVISIONAL"
	// VerificationVerified means a person confirmed this is what the clinic charges. The
	// database refuses it on a row with no recorder.
	VerificationVerified = "VERIFIED"
)

// Where a price row came from.
const (
	OriginSeed   = "SEED"   // the migration's 250 published MRPs; the only origin with no person
	OriginManual = "MANUAL" // somebody typed it into the price form
	OriginImport = "IMPORT" // a CSV
	OriginReview = "REVIEW" // the monthly review confirmed or corrected it
)

// The module's permissions. None of them is sensitive; see the migration's note — §4.4 blinds
// the pharmacist from diagnoses, and the pharmacist is the person §16.1 puts in charge of this.
const (
	PermRead   = "formulary.read"
	PermWrite  = "formulary.write"
	PermReview = "formulary.price.review"
)

// The module's sentinels.
var (
	// ErrNotFound is a product, import or review this facility does not have.
	ErrNotFound = errors.New("formulary: no such thing here")
	// ErrDuplicateProduct is a product whose trade name, strength, form, manufacturer and
	// dispensing unit already exist. All five: Ansulin R in a vial and Ansulin R in a
	// cartridge are two products at two prices.
	ErrDuplicateProduct = errors.New("formulary: that product is already in the formulary")
	// ErrUnknownGeneric is a generic name this clinic has not registered.
	//
	// An import does **not** create one. Every CP77 rule and every CP78 duplicate-therapy
	// check keys off the generic, so a spreadsheet that could invent "Metformin HCl" beside
	// "Metformin hydrochloride" would silently split one molecule's safety rules in two.
	ErrUnknownGeneric = errors.New("formulary: no such generic")
	// ErrUnknownVocabulary is a dosage form or dispensing unit that is not in the catalogue.
	ErrUnknownVocabulary = errors.New("formulary: no such form or unit")
	// ErrPriceNotYetSet is a product with no price covering the day asked about. Distinct
	// from ErrNotFound: the product exists, and "we have never priced this" is a different
	// thing for a screen to say than "no such medicine".
	ErrPriceNotYetSet = errors.New("formulary: no price was in force on that day")
	// ErrAmbiguousPrice is more than one price covering one day.
	//
	// It cannot happen while the EXCLUDE constraint holds, which is exactly why it is an error
	// rather than a tie broken by ORDER BY: reaching it means the as-of query's date filter is
	// wrong, and the alternative to failing is pricing every prescription at whichever row the
	// planner returned first — silently, and for as long as nobody checks by hand.
	ErrAmbiguousPrice = errors.New("formulary: more than one price covers that day")
	// ErrPriceOverlaps is a backdated price whose period would cover a day another price
	// already covers.
	ErrPriceOverlaps = errors.New("formulary: another price already covers that day")
	// ErrEffectiveFromInPast is a price backdated before the current one began.
	ErrEffectiveBeforeCurrent = errors.New("formulary: that date is before the current price began")
	// ErrWithdrawn is a write against a product somebody has withdrawn.
	ErrWithdrawn = errors.New("formulary: that product has been withdrawn")
	// ErrNoOpenReview is a completion for a review cycle that is not open.
	ErrNoOpenReview = errors.New("formulary: no review cycle is open")
)

// Store reads and writes the formulary.
type Store struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

// NewStore builds one.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool, q: dbgen.New(pool)} }

// Named is one entry of a bilingual vocabulary.
type Named struct {
	Code   string `json:"code"`
	NameEN string `json:"name_en"`
	NameBN string `json:"name_bn"`
}

// MedicationClass is a therapeutic class with its (usually absent) ATC code.
type MedicationClass struct {
	Named
	// ATCCode is null on all 33 seeded classes. D-56 chose a curated formulary over the
	// national database, and an ATC code guessed from a class name is a fabricated
	// regulatory identifier — the same reason every DGDA number here is null.
	ATCCode  string `json:"atc_code,omitempty"`
	Ordering int    `json:"ordering"`
}

// Catalogue is the three vocabularies a picker and a CSV import both need.
type Catalogue struct {
	Classes []MedicationClass `json:"classes"`
	Forms   []Named           `json:"forms"`
	Units   []Named           `json:"units"`
}

// Catalogue reads the vocabularies. Identical for everybody in the building, and holding no
// patient, so it is cacheable and carries no facility scope.
func (s *Store) Catalogue(ctx context.Context) (Catalogue, error) {
	out := Catalogue{Classes: []MedicationClass{}, Forms: []Named{}, Units: []Named{}}

	classes, err := s.q.MedicationClasses(ctx)
	if err != nil {
		return Catalogue{}, err
	}
	for _, row := range classes {
		entry := MedicationClass{
			Named:    Named{Code: row.Code, NameEN: row.NameEn, NameBN: row.NameBn},
			Ordering: int(row.Ordering),
		}
		if row.AtcCode != nil {
			entry.ATCCode = *row.AtcCode
		}
		out.Classes = append(out.Classes, entry)
	}

	forms, err := s.q.MedicationForms(ctx)
	if err != nil {
		return Catalogue{}, err
	}
	for _, row := range forms {
		out.Forms = append(out.Forms, Named{Code: row.Code, NameEN: row.NameEn, NameBN: row.NameBn})
	}

	units, err := s.q.DispenseUnits(ctx)
	if err != nil {
		return Catalogue{}, err
	}
	for _, row := range units {
		out.Units = append(out.Units, Named{Code: row.Code, NameEN: row.NameEn, NameBN: row.NameBn})
	}
	return out, nil
}

// Generic is a molecule — or, for a fixed-dose combination, the several molecules this
// formulary holds under one name.
type Generic struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	ClassCode   string    `json:"class_code"`
	ClassNameEN string    `json:"class_name_en"`
	ClassNameBN string    `json:"class_name_bn"`
	ATCCode     string    `json:"atc_code,omitempty"`
	IsActive    bool      `json:"is_active"`
	Products    int       `json:"product_count"`

	// Components are the molecules this medicine contains, in the order the pack names them
	// (CP78, migration 00058). One entry for a single-agent medicine; several for a
	// fixed-dose combination.
	Components []string `json:"components"`
	// ComponentsDetermined says whether somebody has written the molecules out. **False means
	// the safety engine answers "cannot verify" for any duplicate-therapy question involving
	// this medicine** — never "no duplicate". See [Composition].
	ComponentsDetermined bool `json:"components_determined"`
	// ComponentsSource is where the decomposition came from, and is empty when nobody has
	// made one.
	ComponentsSource string `json:"components_source,omitempty"`
}

// Generics lists the molecules, with how many products this facility holds for each.
func (s *Store) Generics(ctx context.Context, facility uuid.UUID) ([]Generic, error) {
	rows, err := s.q.Generics(ctx, facility)
	if err != nil {
		return nil, err
	}
	// The molecules, read once and attached, rather than a query per generic. 59 rows joined
	// to 73 component rows is one round trip; the same answer assembled per generic is sixty.
	byGeneric, err := s.componentsByGeneric(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]Generic, 0, len(rows))
	for _, row := range rows {
		entry := Generic{
			ID: row.ID, Name: row.Name, ClassCode: row.ClassCode,
			ClassNameEN: row.ClassNameEn, ClassNameBN: row.ClassNameBn,
			IsActive: row.IsActive, Products: int(row.ProductCount),
			Components:       byGeneric[row.ID],
			ComponentsSource: row.ComponentsSource,
		}
		// Both halves, deliberately. A row that says DETERMINED and has no molecules is the
		// one shape that must not read as "composed of nothing" — invariant 116 refuses it in
		// the database, and this refuses it again at the point of use.
		entry.ComponentsDetermined = row.ComponentsStatus == componentsDetermined &&
			len(entry.Components) > 0
		if row.AtcCode != nil {
			entry.ATCCode = *row.AtcCode
		}
		out = append(out, entry)
	}
	return out, nil
}

// componentsDetermined is the one value of `core.generic.components_status` that means somebody
// has written the molecules out. Everything else, including a value this build has never heard
// of, is undetermined — which is the direction an unknown enum member has to fail.
const componentsDetermined = "DETERMINED"

// Composition is what one medicine is made of, as the safety engine needs it.
//
// # Why this exists at all
//
// `core.generic` holds "Sitagliptin + Metformin hydrochloride" as a single name, so CP77's
// duplicate-therapy rule — which compared generic names — could not see that Siglimet beside
// Comet is metformin twice. docs/medication-rules.md §10 called that the single largest hole in
// the seeded rule set. This type is the closure: a drug carries its molecule set, and a duplicate
// is a non-empty intersection of two molecule sets rather than an equality of two names.
//
// # Determined is not "has components"
//
// [Composition.Determined] is the fail-closed flag and it is the field callers must branch on. A
// composition that is not determined makes a duplicate question answer *cannot verify*. It must
// never make one answer *no duplicate*: a molecule set nobody has written is a set that
// intersects nothing, and "intersects nothing" and "is not a duplicate" are the same value with
// two very different meanings.
type Composition struct {
	// Generic is the name as `core.generic.name` spells it.
	Generic string `json:"generic"`
	// Class is the therapeutic class code.
	Class string `json:"class"`
	// Components are the molecules, in pack order. Lowercase comparison; the spelling here is
	// the display spelling, and invariant 117 keeps one molecule to one spelling.
	Components []string `json:"components"`
	// Determined is status == DETERMINED **and** at least one molecule. See the type comment.
	Determined bool `json:"determined"`
}

// Compositions is every medicine's molecules, keyed by lowercased generic name.
//
// Keyed by name rather than by id because that is what the caller has: a rule names molecules by
// `core.generic.name`, a prescription item resolves to one, and an item naming a medicine this
// formulary does not hold has no id to key on — and that item is precisely the one whose
// composition must come back undetermined rather than absent-and-ignored.
func (s *Store) Compositions(ctx context.Context) (map[string]Composition, error) {
	rows, err := s.q.GenericComponents(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Composition, len(rows))
	for _, row := range rows {
		key := strings.ToLower(strings.TrimSpace(row.Name))
		entry, seen := out[key]
		if !seen {
			entry = Composition{Generic: row.Name, Class: row.ClassCode}
		}
		// The LEFT JOIN gives one row with a null molecule for a generic nobody has
		// decomposed. That is the undetermined case and it arrives here as no components.
		if row.Molecule != nil && strings.TrimSpace(*row.Molecule) != "" {
			entry.Components = append(entry.Components, *row.Molecule)
		}
		entry.Determined = row.ComponentsStatus == componentsDetermined && len(entry.Components) > 0
		out[key] = entry
	}
	return out, nil
}

// componentsByGeneric is the same read, keyed by id, for the admin listing.
func (s *Store) componentsByGeneric(ctx context.Context) (map[uuid.UUID][]string, error) {
	rows, err := s.q.GenericComponents(ctx)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID][]string{}
	for _, row := range rows {
		if row.Molecule == nil || strings.TrimSpace(*row.Molecule) == "" {
			continue
		}
		out[row.ID] = append(out[row.ID], *row.Molecule)
	}
	return out, nil
}

// GenericByName resolves a generic case-insensitively, or ErrUnknownGeneric.
func (s *Store) GenericByName(ctx context.Context, name string) (Generic, error) {
	row, err := s.q.GenericByName(ctx, strings.TrimSpace(name))
	if errors.Is(err, pgx.ErrNoRows) {
		return Generic{}, ErrUnknownGeneric
	}
	if err != nil {
		return Generic{}, err
	}
	entry := Generic{
		ID: row.ID, Name: row.Name, ClassCode: row.ClassCode,
		ClassNameEN: row.ClassNameEn, ClassNameBN: row.ClassNameBn, IsActive: row.IsActive,
	}
	// Components are not attached here. This resolves one generic by name for a rule's
	// validation, where the molecules are not read; the safety engine takes the whole map from
	// [Store.Compositions] instead. Leaving the field empty rather than half-filling it means
	// ComponentsDetermined is false, which is the safe direction for a caller that reads it by
	// mistake.
	if row.AtcCode != nil {
		entry.ATCCode = *row.AtcCode
	}
	return entry, nil
}

// Product is one thing a prescription can name.
type Product struct {
	ID        uuid.UUID `json:"id"`
	GenericID uuid.UUID `json:"generic_id"`

	// Latin script, always — this is what is printed on the box the patient is handed, and a
	// transliterated trade name is a different medicine at the pharmacy counter.
	TradeName    string `json:"trade_name"`
	Strength     string `json:"strength"`
	Manufacturer string `json:"manufacturer"`

	GenericName string `json:"generic_name"`
	ClassCode   string `json:"class_code"`
	ClassNameEN string `json:"class_name_en"`
	ClassNameBN string `json:"class_name_bn"`

	FormCode   string `json:"form_code"`
	FormNameEN string `json:"form_name_en"`
	FormNameBN string `json:"form_name_bn"`

	DispenseUnit string `json:"dispense_unit"`
	UnitNameEN   string `json:"unit_name_en"`
	UnitNameBN   string `json:"unit_name_bn"`

	// DGDARegistration is absent on all 250 seeded products. Modelled and left null: a
	// fabricated regulatory identifier in a clinical system is worse than an absent one.
	DGDARegistration string `json:"dgda_registration,omitempty"`

	Notes     string `json:"notes,omitempty"`
	SourceURL string `json:"source_url,omitempty"`

	IsActive        bool       `json:"is_active"`
	WithdrawnAt     *time.Time `json:"withdrawn_at,omitempty"`
	WithdrawnReason string     `json:"withdrawn_reason,omitempty"`

	// Price is what this cost on the day asked about — today, unless a date was named. Nil
	// when nothing has ever been priced, which the screens say in as many words rather than
	// drawing a zero.
	Price *Price `json:"price,omitempty"`
}

// Products is a page of the admin list.
type Products struct {
	Items []Product `json:"items"`
	Total int       `json:"total"`
}

// Search is the admin list's filter.
type Search struct {
	Query string
	Class string
	// ActiveOnly hides withdrawn products. The default on the screen is off, because a
	// pharmacist looking for "the one we stopped stocking" needs to find it.
	ActiveOnly bool
	// UnverifiedOnly is the monthly review's working list: products whose current price
	// nobody has confirmed, and products with no price at all.
	UnverifiedOnly bool
	Limit, Offset  int
}

// MaxPageSize bounds a page. The whole formulary is 250 rows and CP76 holds it in memory; this
// list is for a person reading it, and a person does not read 250 rows in one scroll.
const MaxPageSize = 100

// Products lists them, filtered, with the current price of each.
func (s *Store) Products(ctx context.Context, facility uuid.UUID, in Search) (Products, error) {
	limit := in.Limit
	if limit <= 0 || limit > MaxPageSize {
		limit = 50
	}
	offset := in.Offset
	if offset < 0 {
		offset = 0
	}
	rows, err := s.q.SearchProducts(ctx, dbgen.SearchProductsParams{
		FacilityID:       facility,
		PQuery:           strings.TrimSpace(in.Query),
		PClass:           strings.TrimSpace(in.Class),
		PActiveOnly:      in.ActiveOnly,
		PProvisionalOnly: in.UnverifiedOnly,
		PLimit:           int32(limit),  //nolint:gosec // bounded above
		POffset:          int32(offset), //nolint:gosec // bounded by the caller's cursor
	})
	if err != nil {
		return Products{}, err
	}

	out := Products{Items: make([]Product, 0, len(rows))}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		out.Total = int(row.TotalCount)
		product := Product{
			ID: row.ID, GenericID: row.GenericID,
			TradeName: row.TradeName, Strength: row.Strength, Manufacturer: row.Manufacturer,
			GenericName: row.GenericName, ClassCode: row.ClassCode,
			ClassNameEN: row.ClassNameEn, ClassNameBN: row.ClassNameBn,
			FormCode: row.FormCode, FormNameEN: row.FormNameEn, FormNameBN: row.FormNameBn,
			DispenseUnit: row.DispenseUnit, UnitNameEN: row.UnitNameEn, UnitNameBN: row.UnitNameBn,
			IsActive: row.IsActive, WithdrawnAt: row.WithdrawnAt,
		}
		if row.DgdaRegistration != nil {
			product.DGDARegistration = *row.DgdaRegistration
		}
		out.Items = append(out.Items, product)
		ids = append(ids, row.ID)
	}
	if len(ids) == 0 {
		return out, nil
	}

	// One statement for the whole page's prices, rather than one per row or a join whose
	// nullability the generator cannot see. See the note on CurrentPricesFor.
	prices, err := s.q.CurrentPricesFor(ctx, dbgen.CurrentPricesForParams{
		FacilityID: facility, ProductIds: ids,
	})
	if err != nil {
		return Products{}, err
	}
	byProduct := make(map[uuid.UUID]Price, len(prices))
	for _, row := range prices {
		byProduct[row.ProductID] = Price{
			ID: row.ID, ProductID: row.ProductID, Amount: Money(row.UnitPricePoisha),
			AmountBDT: Money(row.UnitPricePoisha).String(),
			From:      day(row.EffectiveFrom), Verification: row.Verification,
			Origin: row.Origin, RecordedAt: row.RecordedAt,
		}
	}
	for i := range out.Items {
		if price, ok := byProduct[out.Items[i].ID]; ok {
			out.Items[i].Price = &price
		}
	}
	return out, nil
}

// Product reads one, with the price in force on the day named.
func (s *Store) Product(ctx context.Context, facility, id uuid.UUID, on time.Time) (Product, error) {
	row, err := s.q.ProductByID(ctx, dbgen.ProductByIDParams{ID: id, FacilityID: facility})
	if errors.Is(err, pgx.ErrNoRows) {
		return Product{}, ErrNotFound
	}
	if err != nil {
		return Product{}, err
	}
	product := Product{
		ID: row.ID, GenericID: row.GenericID,
		TradeName: row.TradeName, Strength: row.Strength, Manufacturer: row.Manufacturer,
		GenericName: row.GenericName, ClassCode: row.ClassCode,
		ClassNameEN: row.ClassNameEn, ClassNameBN: row.ClassNameBn,
		FormCode: row.FormCode, FormNameEN: row.FormNameEn, FormNameBN: row.FormNameBn,
		DispenseUnit: row.DispenseUnit, UnitNameEN: row.UnitNameEn, UnitNameBN: row.UnitNameBn,
		IsActive: row.IsActive, WithdrawnAt: row.WithdrawnAt,
	}
	if row.DgdaRegistration != nil {
		product.DGDARegistration = *row.DgdaRegistration
	}
	if row.Notes != nil {
		product.Notes = *row.Notes
	}
	if row.SourceUrl != nil {
		product.SourceURL = *row.SourceUrl
	}
	if row.WithdrawnReason != nil {
		product.WithdrawnReason = *row.WithdrawnReason
	}

	price, err := s.PriceOn(ctx, facility, id, on)
	switch {
	case errors.Is(err, ErrPriceNotYetSet):
		// Left nil, and that is the answer: a product nobody has priced is a real state, and
		// the screens say so rather than drawing a zero.
	case err != nil:
		return Product{}, err
	default:
		product.Price = &price
	}
	return product, nil
}

// day strips a timestamp to the clinic's calendar day. Prices are daily facts; see the migration.
func day(t time.Time) string { return t.Format("2006-01-02") }
