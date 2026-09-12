package formulary

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// The price history (CP75 criterion 1, §12.3).
//
// # The shape, and the one thing to keep hold of
//
// A price covers a **half-open range of days**: `[effective_from, effective_to)`. The successor's
// first day is the predecessor's last day plus one, and there is no gap and no overlap. Which
// means the answer to "what did this cost on day D" is:
//
//	effective_from <= D  AND  (effective_to IS NULL OR effective_to > D)
//
// and the `EXCLUDE` constraint on the table guarantees at most one row satisfies it. That is why
// [Store.PriceOn] can be a `:one` honestly rather than a `LIMIT 1` that hides a second answer.
//
// The `effective_from <= D` half is the one that carries the criterion. The obvious query — "the
// most recent price for this product" — returns a price recorded last week for a prescription
// written last year, and looks perfectly correct while doing it. `PriceHistoryDB_Test` asserts
// five dates around three prices for exactly this reason.
//
// # Recording is two statements in one transaction
//
// Closing the open range and opening the new one have to commit together. Half of that is a
// product with no price at all (closed, not reopened) or a product with two (opened, not closed)
// — and the second would be refused by the EXCLUDE constraint, which is the safety net rather
// than the plan.

// Price is one row of the history.
type Price struct {
	ID        uuid.UUID `json:"id"`
	ProductID uuid.UUID `json:"product_id"`

	// Amount is poisha; AmountBDT is the same number as "1234.50" for a screen to draw. Both,
	// because a client that formats the integer itself is a client that will divide by 100 in
	// floating point somewhere.
	Amount    Money  `json:"amount_poisha"`
	AmountBDT string `json:"amount_bdt"`

	// From is inclusive, To is exclusive and empty while this is the current price.
	From string `json:"effective_from"`
	To   string `json:"effective_to,omitempty"`

	Verification string `json:"verification"`
	Origin       string `json:"origin"`
	SourceNote   string `json:"source_note,omitempty"`
	SourceURL    string `json:"source_url,omitempty"`

	RecordedAt time.Time `json:"recorded_at"`
	// RecordedBy is empty on, and only on, the 250 seeded prices — the ones nobody has
	// checked. Every other price names a person, which is both a database constraint and a
	// registered invariant.
	RecordedBy     string `json:"recorded_by,omitempty"`
	RecordedByName string `json:"recorded_by_name_en,omitempty"`
	RecordedByBN   string `json:"recorded_by_name_bn,omitempty"`
}

// Verified reports whether a person has confirmed this is what the clinic charges.
func (p Price) Verified() bool { return p.Verification == VerificationVerified }

// PriceOn is **criterion 1**: what this product cost on that day, and nothing else.
//
// Returns ErrPriceNotYetSet when no price covered the day — which is a real and common answer
// (every product's history starts somewhere) and is deliberately not the zero Price. A caller
// that got Money(0) back and drew it would be telling a patient a medicine is free.
func (s *Store) PriceOn(ctx context.Context, facility, product uuid.UUID, on time.Time) (Price, error) {
	rows, err := s.q.PriceAsOf(ctx, dbgen.PriceAsOfParams{
		ProductID: product, FacilityID: facility, OnDate: on,
	})
	if err != nil {
		return Price{}, err
	}
	if len(rows) == 0 {
		return Price{}, ErrPriceNotYetSet
	}
	// Two prices for one day is not a tie to break. The EXCLUDE constraint makes it impossible
	// for two rows to overlap, so more than one answer here means the query's date filter is
	// wrong — and quietly returning the first would price every prescription at whichever row
	// the planner happened to hand back. Refused loudly instead.
	if len(rows) > 1 {
		return Price{}, fmt.Errorf("%w: %d prices cover %s for product %s",
			ErrAmbiguousPrice, len(rows), day(on), product)
	}
	row := rows[0]
	return priceOf(priceRow{
		ID: row.ID, ProductID: row.ProductID, UnitPricePoisha: row.UnitPricePoisha,
		EffectiveFrom: row.EffectiveFrom, EffectiveTo: row.EffectiveTo,
		Verification: row.Verification, Origin: row.Origin,
		SourceNote: row.SourceNote, SourceURL: row.SourceUrl,
		RecordedAt: row.RecordedAt, RecordedBy: row.RecordedBy,
		NameEN: row.RecordedByNameEn, NameBN: row.RecordedByNameBn,
	}), nil
}

// PriceHistory is every price this product has ever carried, newest first.
func (s *Store) PriceHistory(ctx context.Context, facility, product uuid.UUID) ([]Price, error) {
	rows, err := s.q.PriceHistory(ctx, dbgen.PriceHistoryParams{
		ProductID: product, FacilityID: facility,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Price, 0, len(rows))
	for _, row := range rows {
		out = append(out, priceOf(priceRow{
			ID: row.ID, ProductID: row.ProductID, UnitPricePoisha: row.UnitPricePoisha,
			EffectiveFrom: row.EffectiveFrom, EffectiveTo: row.EffectiveTo,
			Verification: row.Verification, Origin: row.Origin,
			SourceNote: row.SourceNote, SourceURL: row.SourceUrl,
			RecordedAt: row.RecordedAt, RecordedBy: row.RecordedBy,
			NameEN: row.RecordedByNameEn, NameBN: row.RecordedByNameBn,
		}))
	}
	return out, nil
}

// Recording is a new price taking effect.
type Recording struct {
	FacilityID uuid.UUID
	ProductID  uuid.UUID

	Amount Money
	// From is the day it takes effect, on the clinic's calendar. Today, unless somebody is
	// recording a change they learned about late.
	From time.Time

	// Verified is whether the person recording it is saying "this is what we charge", as
	// opposed to "this is what the distributor's list says". Recording a price is not by
	// itself verifying it: an import of a supplier's list is a fact about the supplier.
	Verified bool

	Origin     string
	SourceNote string
	SourceURL  string

	// ActorID is required and there is no path that leaves it empty. `price_names_who_recorded_it`
	// refuses a row without one for any origin but SEED, and the seed is a migration.
	ActorID uuid.UUID
}

// PriceChange is what happened, for the audit bridge.
type PriceChange struct {
	FacilityID uuid.UUID
	ProductID  uuid.UUID
	PriceID    uuid.UUID

	TradeName string
	Strength  string

	// Previous is empty when this is the first price this product has ever had. An audit
	// sentence that read "changed the price from 0.00" would be a lie about a first price.
	Previous     Money
	HadPrevious  bool
	Amount       Money
	From         string
	Verification string
	Origin       string

	ActorID   uuid.UUID
	ActorCode string
	ActorRole string
}

// RecordPrice supersedes the current price from a date, and returns what changed.
//
// The rules, in the order they are checked:
//
//  1. The product must exist here, and must not be withdrawn. Pricing something the clinic has
//     stopped stocking is almost always a mistake, and reinstating is one click away.
//  2. `From` may not be before the day the current price began. Backdating *over* a period
//     somebody has already reported on would change what a past prescription cost, which is the
//     one thing §12.3 cannot survive. Backdating into a gap before any price exists is allowed.
//  3. The open period is closed at `From`, and the new one opens there. One transaction.
func (s *Store) RecordPrice(ctx context.Context, in Recording) (PriceChange, error) {
	if in.ActorID == uuid.Nil {
		return PriceChange{}, errors.New("formulary: a price change must name who made it")
	}
	if in.Amount <= 0 || in.Amount > MaxPrice {
		return PriceChange{}, ErrMoneyRange
	}
	origin := in.Origin
	if origin == "" {
		origin = OriginManual
	}
	verification := VerificationProvisional
	if in.Verified {
		verification = VerificationVerified
	}
	from := in.From.UTC().Truncate(24 * time.Hour)

	product, err := s.q.ProductByID(ctx, dbgen.ProductByIDParams{
		ID: in.ProductID, FacilityID: in.FacilityID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return PriceChange{}, ErrNotFound
	}
	if err != nil {
		return PriceChange{}, err
	}
	if !product.IsActive {
		return PriceChange{}, ErrWithdrawn
	}

	change := PriceChange{
		FacilityID: in.FacilityID, ProductID: in.ProductID,
		TradeName: product.TradeName, Strength: product.Strength,
		Amount: in.Amount, From: day(from), Verification: verification, Origin: origin,
		ActorID: in.ActorID,
	}

	current, err := s.PriceOn(ctx, in.FacilityID, in.ProductID, from)
	switch {
	case errors.Is(err, ErrPriceNotYetSet):
		// First price for this product, or a backdated one before the history begins.
	case err != nil:
		return PriceChange{}, err
	default:
		change.Previous, change.HadPrevious = current.Amount, true
		// The current price began on or after the day being recorded. Closing it would
		// leave it with an empty range, and rewriting it is exactly what this module
		// refuses to do — so this is an explicit error rather than a silent no-op.
		if current.From >= day(from) && current.To == "" {
			return PriceChange{}, ErrEffectiveBeforeCurrent
		}
	}

	err = s.inTransaction(ctx, func(ctx context.Context, q *dbgen.Queries) error {
		if err := q.ClosePriceAt(ctx, dbgen.ClosePriceAtParams{
			ProductID: in.ProductID, EffectiveTo: from,
		}); err != nil {
			return err
		}
		actor := uuid.NullUUID{UUID: in.ActorID, Valid: true}
		inserted, err := q.InsertPrice(ctx, dbgen.InsertPriceParams{
			FacilityID: in.FacilityID, ProductID: in.ProductID,
			UnitPricePoisha: int64(in.Amount), EffectiveFrom: from,
			Verification: verification, Origin: origin,
			SourceNote: nilIfBlank(in.SourceNote), SourceUrl: nilIfBlank(in.SourceURL),
			RecordedBy: actor,
		})
		if err != nil {
			return err
		}
		change.PriceID = inserted.ID
		return nil
	})
	if err != nil {
		// 23P01 is the exclusion violation: a backdated price landing inside a closed
		// period. Named rather than surfaced as an internal error, because the person who
		// typed the date can fix it and the message has to tell them what to fix.
		if isExclusionViolation(err) {
			return PriceChange{}, ErrPriceOverlaps
		}
		return PriceChange{}, err
	}
	return change, nil
}

// priceRow is the shape every price query returns, so priceOf is written once.
type priceRow struct {
	ID              uuid.UUID
	ProductID       uuid.UUID
	UnitPricePoisha int64
	EffectiveFrom   time.Time
	EffectiveTo     *time.Time
	Verification    string
	Origin          string
	SourceNote      *string
	SourceURL       *string
	RecordedAt      time.Time
	RecordedBy      uuid.NullUUID
	NameEN, NameBN  *string
}

func priceOf(row priceRow) Price {
	price := Price{
		ID: row.ID, ProductID: row.ProductID,
		Amount: Money(row.UnitPricePoisha), AmountBDT: Money(row.UnitPricePoisha).String(),
		From: day(row.EffectiveFrom), Verification: row.Verification, Origin: row.Origin,
		RecordedAt: row.RecordedAt,
	}
	if row.EffectiveTo != nil {
		price.To = day(*row.EffectiveTo)
	}
	if row.SourceNote != nil {
		price.SourceNote = *row.SourceNote
	}
	if row.SourceURL != nil {
		price.SourceURL = *row.SourceURL
	}
	if row.RecordedBy.Valid {
		price.RecordedBy = row.RecordedBy.UUID.String()
	}
	if row.NameEN != nil {
		price.RecordedByName = *row.NameEN
	}
	if row.NameBN != nil {
		price.RecordedByBN = *row.NameBN
	}
	return price
}

func nilIfBlank(s string) *string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// isExclusionViolation reports a 23P01 from PostgreSQL — two prices covering one day.
func isExclusionViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23P01"
	}
	return false
}

func (s *Store) inTransaction(ctx context.Context, fn func(context.Context, *dbgen.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(ctx, s.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
