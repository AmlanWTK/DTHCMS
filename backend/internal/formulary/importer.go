package formulary

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Bulk import from CSV (CP75 criterion 2).
//
// # Per-row partial, not all-or-nothing, and why
//
// A supplier's price list is two hundred and fifty lines. All-or-nothing means one typo on line
// 187 blocks the 249 correct prices, and what happens next is not "the pharmacist fixes line
// 187" — it is three more uploads, each finding one more problem, and then the prices being
// typed in by hand. A rule whose effect is that people stop using the feature has not made
// anything safer.
//
// The three things that make partial acceptance safe here are all present:
//
//  1. **The rows are independent.** Each line is one product and one price. Nothing on line 188
//     depends on line 187, so a partial result is not a half-applied transaction — it is a
//     smaller set of complete facts. (This is exactly why a ledger append is all-or-nothing and
//     this is not.)
//  2. **Nobody has to guess what landed.** Every line's outcome is a row in
//     `core.formulary_import_row` — accepted, unchanged or rejected, with the line number from
//     the person's own spreadsheet and a reason in both languages — and it is still there
//     tomorrow.
//  3. **Nothing lands by surprise.** The default mode is DRY_RUN: the whole file is validated,
//     the whole report is written, and not one product or price is touched. The pharmacist reads
//     the errors, fixes the file, and only then applies it.
//
// One thing *is* all-or-nothing: the persistence. The accepted rows' effects and the import
// report commit in a single transaction, so a database failure halfway through leaves no import
// that half-happened.
//
// # The import never invents a generic
//
// An unknown generic name is a rejection, not a new molecule. Every CP77 rule and every CP78
// duplicate-therapy check keys off the generic, so a spreadsheet able to create "Metformin HCl"
// beside "Metformin hydrochloride" would silently split one molecule's safety rules across two
// records — and nothing would look wrong until a patient was prescribed both.
//
// # Importing is not verifying
//
// A price from a distributor's list is a fact about the distributor. It lands PROVISIONAL unless
// the person uploading it ticks "these are the prices we charge", which is a separate,
// deliberate assertion with their name on it.

// ImportMode is whether the file is being checked or applied.
const (
	ModeDryRun = "DRY_RUN"
	ModeApply  = "APPLY"
)

// Row outcomes.
const (
	OutcomeCreated   = "CREATED"
	OutcomeUpdated   = "UPDATED"
	OutcomeUnchanged = "UNCHANGED"
	OutcomeRejected  = "REJECTED"
)

// ImportRequest is one upload.
type ImportRequest struct {
	FacilityID uuid.UUID
	Filename   string
	Mode       string

	// Verified marks every price in the file as what the clinic charges rather than what a
	// list says. Off by default, and the person has to mean it.
	Verified bool
	// EffectiveFrom is the day the prices in this file take effect, unless a row names its
	// own. Today, normally.
	EffectiveFrom time.Time

	ActorID uuid.UUID
	Body    io.Reader
}

// ImportRowResult is what happened to one line of the file.
type ImportRowResult struct {
	// Line is the line number in the uploaded file, header included — the number the person's
	// spreadsheet shows them, not the index of this row among the ones that parsed.
	Line int `json:"line"`

	Outcome string `json:"outcome"`
	// Field names the column at fault, where there is one to name.
	Field     string `json:"field,omitempty"`
	MessageEN string `json:"message_en,omitempty"`
	MessageBN string `json:"message_bn,omitempty"`

	TradeName string     `json:"trade_name,omitempty"`
	ProductID *uuid.UUID `json:"product_id,omitempty"`
}

// ImportResult is the whole report.
type ImportResult struct {
	ID       uuid.UUID `json:"id"`
	Filename string    `json:"filename"`
	Mode     string    `json:"mode"`

	Total    int `json:"rows_total"`
	Accepted int `json:"rows_accepted"`
	Rejected int `json:"rows_rejected"`

	Created    int               `json:"products_created"`
	Updated    int               `json:"products_updated"`
	Prices     int               `json:"prices_recorded"`
	Rows       []ImportRowResult `json:"rows"`
	ImportedAt time.Time         `json:"imported_at"`
}

// ImportColumns is the header the importer expects, in any order and in any case.
//
// The same twelve columns the seed workbook has, so that the file a pharmacist gets handed back
// from the export is the file they can edit and upload again.
var ImportColumns = []string{
	"generic", "trade_name", "strength", "form", "manufacturer",
	"unit_price_bdt", "unit", "dgda_reg", "notes", "source_url", "effective_from",
}

var requiredColumns = []string{
	"generic", "trade_name", "strength", "form", "manufacturer", "unit_price_bdt", "unit",
}

// ErrBadHeader is a file whose first line is not a header this importer understands. The only
// whole-file rejection there is: a file whose columns cannot be identified has no rows to report
// per-row errors about, and guessing the column order is how a price lands in the strength.
var ErrBadHeader = errors.New("formulary: the first line is not a header this import understands")

// Import reads the file, validates every row, and — unless this is a dry run — applies the ones
// that passed.
func (s *Store) Import(ctx context.Context, in ImportRequest) (ImportResult, error) {
	if in.ActorID == uuid.Nil {
		return ImportResult{}, errors.New("formulary: an import must name who ran it")
	}
	mode := in.Mode
	if mode != ModeApply {
		mode = ModeDryRun
	}
	effective := in.EffectiveFrom.UTC().Truncate(24 * time.Hour)

	reader := csv.NewReader(in.Body)
	// Variable field counts are handled per row rather than by the reader, so that a short
	// line is a named error on that line instead of an error that ends the whole file.
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true

	header, err := reader.Read()
	if err != nil {
		return ImportResult{}, ErrBadHeader
	}
	index, err := headerIndex(header)
	if err != nil {
		return ImportResult{}, err
	}

	catalogue, err := s.Catalogue(ctx)
	if err != nil {
		return ImportResult{}, err
	}
	forms := codeSet(catalogue.Forms)
	units := codeSet(catalogue.Units)

	// Duplicates *within the file*. The database's unique index catches a clash with what is
	// already there; this catches the same product twice in one upload, which the database
	// would happily accept as an insert followed by an update — leaving the second price
	// silently winning over the first with nothing said about it.
	seen := map[string]int{}

	results := []ImportRowResult{}
	type accepted struct {
		row parsedRow
		at  int
	}
	var toApply []accepted

	line := 1 // the header was line 1
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		line++
		if err != nil {
			results = append(results, reject(line, "", record,
				"This line could not be read as CSV.",
				"এই লাইনটি সিএসভি হিসেবে পড়া যায়নি।"))
			continue
		}
		if len(record) == 1 && strings.TrimSpace(record[0]) == "" {
			continue // a trailing blank line is not an error
		}

		parsed, bad := parseRow(line, record, index, forms, units, effective)
		if bad != nil {
			results = append(results, *bad)
			continue
		}
		if first, dup := seen[parsed.key]; dup {
			duplicate := reject(line, "trade_name", record,
				fmt.Sprintf("The same product is already on line %d of this file.", first),
				fmt.Sprintf("এই ফাইলের %d নম্বর লাইনে একই পণ্য আগেই আছে।", first))
			// The brand name, like every other rejection carries. Without it the row on the
			// report reads "line 6 — the same product is already on line 2", and the person
			// has to open their spreadsheet to find out which product that was.
			duplicate.TradeName = parsed.tradeName
			results = append(results, duplicate)
			continue
		}
		seen[parsed.key] = line

		// The row's slot in the report is claimed now, in file order, and filled in when the
		// row is resolved against the database. The report therefore reads in the order of
		// the person's own spreadsheet whatever happens below.
		results = append(results, ImportRowResult{Line: line, TradeName: parsed.tradeName})
		toApply = append(toApply, accepted{row: parsed, at: len(results) - 1})
	}

	out := ImportResult{Filename: in.Filename, Mode: mode}

	err = s.inTransaction(ctx, func(ctx context.Context, q *dbgen.Queries) error {
		created, err := q.InsertImport(ctx, dbgen.InsertImportParams{
			FacilityID: in.FacilityID, Filename: in.Filename, Mode: mode,
			ImportedBy: in.ActorID,
			ActorID:    uuid.NullUUID{UUID: in.ActorID, Valid: true},
		})
		if err != nil {
			return err
		}
		out.ID, out.ImportedAt = created.ID, created.ImportedAt

		for _, item := range toApply {
			if err := s.applyRow(ctx, q, in, mode, item.row, &results[item.at]); err != nil {
				return err
			}
		}

		for i := range results {
			switch results[i].Outcome {
			case OutcomeRejected:
				out.Rejected++
			case OutcomeCreated:
				out.Created++
				out.Accepted++
			case OutcomeUpdated:
				out.Updated++
				out.Accepted++
			default:
				out.Accepted++
			}
			if err := q.InsertImportRow(ctx, dbgen.InsertImportRowParams{
				ImportID: out.ID, FacilityID: in.FacilityID,
				LineNumber: int32(results[i].Line), //nolint:gosec // a line number
				Outcome:    results[i].Outcome,
				Field:      nilIfBlank(results[i].Field),
				MessageEn:  nilIfBlank(results[i].MessageEN),
				MessageBn:  nilIfBlank(results[i].MessageBN),
				RawLine:    nilIfBlank(results[i].TradeName),
				ProductID:  nullUUID(results[i].ProductID),
			}); err != nil {
				return err
			}
		}
		out.Total = len(results)
		// A price is written for exactly the rows that were created or updated, and for none
		// of them on a dry run. Counted here rather than incremented inside applyRow so that
		// the number on the report cannot drift from the outcomes it is supposed to summarise.
		if mode == ModeApply {
			out.Prices = out.Created + out.Updated
		}

		return q.FinishImport(ctx, dbgen.FinishImportParams{
			ID: out.ID, RowsTotal: int32(out.Total), //nolint:gosec // bounded by the file
			RowsAccepted: int32(out.Accepted), RowsRejected: int32(out.Rejected), //nolint:gosec
			ProductsCreated: int32(out.Created), ProductsUpdated: int32(out.Updated), //nolint:gosec
			PricesRecorded: int32(out.Prices), //nolint:gosec
			ActorID:        uuid.NullUUID{UUID: in.ActorID, Valid: true},
		})
	})
	if err != nil {
		return ImportResult{}, err
	}
	out.Rows = results
	return out, nil
}

// applyRow resolves one validated row against the database and records what happened.
//
// A dry run does everything except write: it still resolves the generic and the natural key, so
// the report says "this would be created" or "this would be updated" rather than "this parsed".
func (s *Store) applyRow(ctx context.Context, q *dbgen.Queries, in ImportRequest,
	mode string, row parsedRow, result *ImportRowResult) error {

	generic, err := q.GenericByName(ctx, row.generic)
	if errors.Is(err, pgx.ErrNoRows) {
		*result = reject(row.line, "generic", nil,
			fmt.Sprintf("%q is not a generic this clinic holds. Add the generic first, or correct the spelling.", row.generic),
			fmt.Sprintf("%q নামে কোনও জেনেরিক এই ক্লিনিকে নেই। আগে জেনেরিকটি যোগ করুন, নয়তো বানান ঠিক করুন।", row.generic))
		result.TradeName = row.tradeName
		return nil
	}
	if err != nil {
		return err
	}

	existing, err := q.ProductIdentity(ctx, dbgen.ProductIdentityParams{
		FacilityID: in.FacilityID, TradeName: row.tradeName, Strength: row.strength,
		FormCode: row.form, Manufacturer: row.manufacturer, DispenseUnit: row.unit,
	})
	known := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	result.Outcome = OutcomeCreated
	if known {
		result.Outcome = OutcomeUpdated
		id := existing.ID
		result.ProductID = &id
	}
	if mode != ModeApply {
		return nil
	}

	productID := uuid.Nil
	if known {
		productID = existing.ID
		if _, err := q.UpdateProductDetails(ctx, dbgen.UpdateProductDetailsParams{
			ID: productID, FacilityID: in.FacilityID, GenericID: generic.ID,
			DgdaRegistration: nilIfBlank(row.dgda), Notes: nilIfBlank(row.notes),
			SourceUrl: nilIfBlank(row.sourceURL),
			ActorID:   uuid.NullUUID{UUID: in.ActorID, Valid: true},
		}); err != nil {
			return err
		}
	} else {
		productID, err = q.InsertProduct(ctx, dbgen.InsertProductParams{
			FacilityID: in.FacilityID, GenericID: generic.ID,
			TradeName: row.tradeName, Strength: row.strength, FormCode: row.form,
			Manufacturer: row.manufacturer, DispenseUnit: row.unit,
			DgdaRegistration: nilIfBlank(row.dgda), Notes: nilIfBlank(row.notes),
			SourceUrl: nilIfBlank(row.sourceURL),
			ActorID:   uuid.NullUUID{UUID: in.ActorID, Valid: true},
		})
		if err != nil {
			return err
		}
		result.ProductID = &productID
	}

	// The price. Unchanged rather than a new row when the number and the day are what is
	// already there — an import run twice should not write 250 identical price rows and make
	// the history unreadable.
	covering, err := q.PriceAsOf(ctx, dbgen.PriceAsOfParams{
		ProductID: productID, FacilityID: in.FacilityID, OnDate: row.effectiveFrom,
	})
	if err != nil {
		return err
	}
	if len(covering) > 1 {
		// See PriceOn: two prices for one day means the date filter is wrong, and picking one
		// would price an import against whichever row came back first.
		return fmt.Errorf("%w: %d prices cover %s for product %s",
			ErrAmbiguousPrice, len(covering), day(row.effectiveFrom), productID)
	}
	if len(covering) == 1 {
		current := covering[0]
		sameAmount := current.UnitPricePoisha == int64(row.price)
		sameState := (current.Verification == VerificationVerified) == in.Verified
		if sameAmount && sameState {
			result.Outcome = OutcomeUnchanged
			return nil
		}
		if err := q.ClosePriceAt(ctx, dbgen.ClosePriceAtParams{
			ProductID: productID, EffectiveTo: row.effectiveFrom,
		}); err != nil {
			return err
		}
	}

	verification := VerificationProvisional
	if in.Verified {
		verification = VerificationVerified
	}
	note := "Bulk import: " + in.Filename
	if _, err := q.InsertPrice(ctx, dbgen.InsertPriceParams{
		FacilityID: in.FacilityID, ProductID: productID,
		UnitPricePoisha: int64(row.price), EffectiveFrom: row.effectiveFrom,
		Verification: verification, Origin: OriginImport,
		SourceNote: &note, SourceUrl: nilIfBlank(row.sourceURL),
		RecordedBy: uuid.NullUUID{UUID: in.ActorID, Valid: true},
	}); err != nil {
		if isExclusionViolation(err) {
			*result = reject(row.line, "effective_from", nil,
				"Another price already covers that date for this product.",
				"এই পণ্যের জন্য ওই তারিখে আগে থেকেই আরেকটি দাম চালু আছে।")
			result.TradeName = row.tradeName
			return nil
		}
		return err
	}
	return nil
}

// parsedRow is one validated line.
type parsedRow struct {
	line          int
	generic       string
	tradeName     string
	strength      string
	form          string
	manufacturer  string
	unit          string
	price         Money
	dgda          string
	notes         string
	sourceURL     string
	effectiveFrom time.Time
	key           string
}

// parseRow validates one line and returns either the row or the rejection naming what is wrong.
func parseRow(line int, record []string, index map[string]int,
	forms, units map[string]bool, defaultFrom time.Time) (parsedRow, *ImportRowResult) {

	value := func(column string) string {
		at, ok := index[column]
		if !ok || at >= len(record) {
			return ""
		}
		return strings.TrimSpace(record[at])
	}

	row := parsedRow{
		line: line, generic: value("generic"), tradeName: value("trade_name"),
		strength: value("strength"), manufacturer: value("manufacturer"),
		dgda: value("dgda_reg"), notes: value("notes"), sourceURL: value("source_url"),
		effectiveFrom: defaultFrom,
	}

	// Required fields, in the order they appear in the file, so a row missing three of them
	// names the leftmost — which is the one the person will fix first.
	for _, column := range requiredColumns {
		if value(column) == "" {
			bad := reject(line, column, record,
				fmt.Sprintf("%s is required and this row leaves it empty.", columnLabelEN[column]),
				fmt.Sprintf("%s লাগবেই, এই সারিতে সেটি ফাঁকা আছে।", columnLabelBN[column]))
			bad.TradeName = row.tradeName
			return parsedRow{}, &bad
		}
	}

	price, err := ParseMoney(value("unit_price_bdt"))
	if err != nil {
		var en, bn string
		switch {
		case errors.Is(err, ErrMoneyPrecision):
			en = fmt.Sprintf("%q has more precision than one poisha. Prices are in taka and poisha, at most two decimal places.", value("unit_price_bdt"))
			bn = fmt.Sprintf("%q-তে পয়সার চেয়ে বেশি ভগ্নাংশ আছে। দাম টাকা ও পয়সায়, দশমিকের পরে সর্বোচ্চ দুই ঘর।", value("unit_price_bdt"))
		case errors.Is(err, ErrMoneyRange):
			en = fmt.Sprintf("%q is not a plausible price for one unit.", value("unit_price_bdt"))
			bn = fmt.Sprintf("একক প্রতি %q দামটি বিশ্বাসযোগ্য নয়।", value("unit_price_bdt"))
		default:
			en = fmt.Sprintf("%q is not an amount. Write the price in taka, like 12.50.", value("unit_price_bdt"))
			bn = fmt.Sprintf("%q কোনও অঙ্ক নয়। দামটি টাকায় লিখুন, যেমন 12.50।", value("unit_price_bdt"))
		}
		bad := reject(line, "unit_price_bdt", record, en, bn)
		bad.TradeName = row.tradeName
		return parsedRow{}, &bad
	}
	row.price = price

	row.form = normaliseCode(value("form"))
	if !forms[row.form] {
		bad := reject(line, "form", record,
			fmt.Sprintf("%q is not a dosage form this clinic lists.", value("form")),
			fmt.Sprintf("%q এই ক্লিনিকের তালিকাভুক্ত কোনও ওষুধের ধরন নয়।", value("form")))
		bad.TradeName = row.tradeName
		return parsedRow{}, &bad
	}
	row.unit = strings.ToLower(value("unit"))
	if !units[row.unit] {
		bad := reject(line, "unit", record,
			fmt.Sprintf("%q is not a dispensing unit this clinic lists.", value("unit")),
			fmt.Sprintf("%q এই ক্লিনিকের তালিকাভুক্ত কোনও সরবরাহ একক নয়।", value("unit")))
		bad.TradeName = row.tradeName
		return parsedRow{}, &bad
	}

	if raw := value("effective_from"); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			bad := reject(line, "effective_from", record,
				fmt.Sprintf("%q is not a date. Write it as 2026-03-04.", raw),
				fmt.Sprintf("%q কোনও তারিখ নয়। 2026-03-04 এভাবে লিখুন।", raw))
			bad.TradeName = row.tradeName
			return parsedRow{}, &bad
		}
		row.effectiveFrom = parsed.UTC()
	}

	row.key = strings.ToLower(row.tradeName) + "|" + strings.ToLower(row.strength) + "|" +
		row.form + "|" + strings.ToLower(row.manufacturer) + "|" + row.unit
	return row, nil
}

func reject(line int, field string, _ []string, en, bn string) ImportRowResult {
	return ImportRowResult{
		Line: line, Outcome: OutcomeRejected, Field: field, MessageEN: en, MessageBN: bn,
	}
}

// columnLabelEN and columnLabelBN name a column the way the person's spreadsheet does.
var columnLabelEN = map[string]string{
	"generic": "Generic name", "trade_name": "Trade name", "strength": "Strength",
	"form": "Dosage form", "manufacturer": "Manufacturer",
	"unit_price_bdt": "Unit price (BDT)", "unit": "Dispensing unit",
}

var columnLabelBN = map[string]string{
	"generic": "জেনেরিক নাম", "trade_name": "ব্র্যান্ডের নাম", "strength": "মাত্রা",
	"form": "ওষুধের ধরন", "manufacturer": "উৎপাদক",
	"unit_price_bdt": "এককপ্রতি দাম (টাকা)", "unit": "সরবরাহ একক",
}

// bom is the byte-order mark Excel writes at the start of a UTF-8 CSV. Trimmed rather than
// tolerated: it is invisible, it is on the first header cell only, and a "generic" column that
// does not match because of three bytes nobody can see is the most confusing possible rejection.
const bom = "\ufeff"

// headerIndex maps each expected column to its position, tolerating order and case.
func headerIndex(header []string) (map[string]int, error) {
	index := map[string]int{}
	for at, raw := range header {
		name := strings.ToLower(strings.TrimSpace(strings.Trim(raw, bom)))
		name = strings.ReplaceAll(name, " ", "_")
		switch name {
		// The seed workbook's own spellings, so the file a pharmacist is handed back is the
		// file they can edit and upload again.
		case "generic_name":
			name = "generic"
		case "class":
			continue // read for reference; the class comes from the generic
		case "price", "unit_price", "unit_price_(bdt)":
			name = "unit_price_bdt"
		case "dgda", "dgda_registration", "dgda_reg_no", "dgda_reg._no.":
			name = "dgda_reg"
		case "active":
			continue // withdrawal is an act with a reason, not a column in a price list
		}
		index[name] = at
	}
	for _, column := range requiredColumns {
		if _, ok := index[column]; !ok {
			return nil, fmt.Errorf("%w: it has no %q column", ErrBadHeader, column)
		}
	}
	return index, nil
}

// normaliseCode turns "Tablet (Extended Release)" into TABLET_EXTENDED_RELEASE, matching the
// codes the migration generated. A person editing the workbook writes the label, not the code.
func normaliseCode(text string) string {
	var b strings.Builder
	lastUnderscore := true
	for _, r := range strings.ToUpper(strings.TrimSpace(text)) {
		switch {
		case (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastUnderscore = false
		case !lastUnderscore:
			b.WriteRune('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func codeSet(items []Named) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, item := range items {
		out[item.Code] = true
	}
	return out
}

func nullUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

// Imports lists past imports, newest first.
func (s *Store) Imports(ctx context.Context, facility uuid.UUID, limit int) ([]ImportResult, error) {
	if limit <= 0 || limit > MaxPageSize {
		limit = 20
	}
	rows, err := s.q.Imports(ctx, dbgen.ImportsParams{
		FacilityID: facility, PLimit: int32(limit), //nolint:gosec // bounded above
	})
	if err != nil {
		return nil, err
	}
	out := make([]ImportResult, 0, len(rows))
	for _, row := range rows {
		out = append(out, ImportResult{
			ID: row.ID, Filename: row.Filename, Mode: row.Mode,
			Total: int(row.RowsTotal), Accepted: int(row.RowsAccepted),
			Rejected: int(row.RowsRejected), Created: int(row.ProductsCreated),
			Updated: int(row.ProductsUpdated), Prices: int(row.PricesRecorded),
			ImportedAt: row.ImportedAt, Rows: []ImportRowResult{},
		})
	}
	return out, nil
}

// ImportReport reads one import back with its per-row outcomes.
func (s *Store) ImportReport(ctx context.Context, facility, id uuid.UUID, rejectedOnly bool) (ImportResult, error) {
	row, err := s.q.ImportByID(ctx, dbgen.ImportByIDParams{ID: id, FacilityID: facility})
	if errors.Is(err, pgx.ErrNoRows) {
		return ImportResult{}, ErrNotFound
	}
	if err != nil {
		return ImportResult{}, err
	}
	out := ImportResult{
		ID: row.ID, Filename: row.Filename, Mode: row.Mode,
		Total: int(row.RowsTotal), Accepted: int(row.RowsAccepted),
		Rejected: int(row.RowsRejected), Created: int(row.ProductsCreated),
		Updated: int(row.ProductsUpdated), Prices: int(row.PricesRecorded),
		ImportedAt: row.ImportedAt, Rows: []ImportRowResult{},
	}
	rows, err := s.q.ImportRows(ctx, dbgen.ImportRowsParams{
		ImportID: id, FacilityID: facility, PRejectedOnly: rejectedOnly,
		PLimit: 1000,
	})
	if err != nil {
		return ImportResult{}, err
	}
	for _, r := range rows {
		entry := ImportRowResult{Line: int(r.LineNumber), Outcome: r.Outcome}
		if r.Field != nil {
			entry.Field = *r.Field
		}
		if r.MessageEn != nil {
			entry.MessageEN = *r.MessageEn
		}
		if r.MessageBn != nil {
			entry.MessageBN = *r.MessageBn
		}
		if r.RawLine != nil {
			entry.TradeName = *r.RawLine
		}
		if r.ProductID.Valid {
			id := r.ProductID.UUID
			entry.ProductID = &id
		}
		out.Rows = append(out.Rows, entry)
	}
	return out, nil
}
