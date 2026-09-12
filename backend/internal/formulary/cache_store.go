package formulary

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Loading the autocomplete's cache, and asking whether it is stale (CP76).

// CacheRow is one product as the cache loads it: the product, its vocabularies in both
// languages, and its current price if it has one.
//
// A type of this module's own rather than the generated row, because the cache is the thing a
// test builds an index from and a test should not have to name a sqlc struct to do it.
type CacheRow struct {
	ProductID uuid.UUID
	TradeName string
	Strength  string

	GenericID   uuid.UUID
	GenericName string

	ClassCode string
	ClassEN   string
	ClassBN   string

	FormCode string
	FormEN   string
	FormBN   string

	Manufacturer string
	DispenseUnit string
	UnitEN       string
	UnitBN       string

	IsActive bool

	// Price is nil for a product nobody has priced. See [SearchStrength.Price].
	Price *SearchPrice
}

// CacheRows loads one facility's whole formulary as of a day, and the watermark it was loaded at.
//
// `on` is the clinic's calendar day, and it is a parameter rather than `CURRENT_DATE` because the
// cache has to know which day it built for: the price in force changes at midnight with no row
// changing, so the watermark cannot see it and the snapshot's own day is what triggers the
// rebuild. See [Cache.snapshotFor].
//
// The watermark is read **after** the rows, not before. Read first, a change landing between the
// two statements would be baked into a snapshot stamped with the older mark, and the next
// refresh would compare the new watermark against the old one, see a difference, and reload —
// which is harmless. Read after, the same change produces a snapshot stamped with the *newer*
// mark that may not contain it, and the refresh would then see no difference and never reload.
//
// So it is read after and the two statements are **in one transaction**, which removes the
// question entirely: both see the same snapshot of the database.
func (s *Store) CacheRows(ctx context.Context, facility uuid.UUID, on time.Time) ([]CacheRow, watermark, error) {
	var rows []CacheRow
	var mark watermark

	err := s.inTransaction(ctx, func(ctx context.Context, q *dbgen.Queries) error {
		loaded, err := q.FormularyCacheRows(ctx, dbgen.FormularyCacheRowsParams{
			FacilityID: facility, AsOf: on,
		})
		if err != nil {
			return err
		}
		rows = make([]CacheRow, 0, len(loaded))
		for _, r := range loaded {
			row := CacheRow{
				ProductID: r.ID, TradeName: r.TradeName, Strength: r.Strength,
				GenericID: r.GenericID, GenericName: r.GenericName,
				ClassCode: r.ClassCode, ClassEN: r.ClassNameEn, ClassBN: r.ClassNameBn,
				FormCode: r.FormCode, FormEN: r.FormNameEn, FormBN: r.FormNameBn,
				Manufacturer: r.Manufacturer, DispenseUnit: r.DispenseUnit,
				UnitEN: r.UnitNameEn, UnitBN: r.UnitNameBn,
				IsActive: r.IsActive,
			}
			if r.UnitPricePoisha != nil && r.Verification != nil && r.PriceFrom != nil {
				amount := Money(*r.UnitPricePoisha)
				row.Price = &SearchPrice{
					AmountPoisha: amount,
					AmountBDT:    amount.String(),
					Verification: *r.Verification,
					From:         r.PriceFrom.Format("2006-01-02"),
				}
			}
			rows = append(rows, row)
		}

		wm, err := q.FormularyWatermark(ctx, facility)
		if err != nil {
			return err
		}
		mark = watermark{rows: wm.RowCount, changedAt: wm.ChangedAt}
		return nil
	})
	if err != nil {
		return nil, watermark{}, err
	}
	return rows, mark, nil
}

// Watermark asks whether anything has changed, in one cheap row.
func (s *Store) Watermark(ctx context.Context, facility uuid.UUID) (watermark, error) {
	wm, err := s.q.FormularyWatermark(ctx, facility)
	if err != nil {
		return watermark{}, err
	}
	return watermark{rows: wm.RowCount, changedAt: wm.ChangedAt}, nil
}
