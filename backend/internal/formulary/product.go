package formulary

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Adding, correcting and withdrawing a product (CP75).
//
// # Why withdrawal is its own act rather than a field on the edit form
//
// "Nothing is deleted. A product that is withdrawn is deactivated, and its price history stays."
// A general update that carried `is_active` would let a screen that posted the whole form back
// withdraw a product nobody meant to withdraw, with nobody's reason attached — and the database
// requires a reason and a name, so the screen would then have to invent one. Withdrawal is
// therefore a POST of its own with a reason in the body, and reinstatement is the mirror of it.

// NewProduct is a product being added by hand.
type NewProduct struct {
	FacilityID uuid.UUID

	// GenericName rather than an id, because the person adding a product knows the molecule's
	// name and not its uuid, and because this is the same resolution the CSV import does — one
	// path, one set of errors.
	GenericName  string
	TradeName    string
	Strength     string
	FormCode     string
	Manufacturer string
	DispenseUnit string

	DGDARegistration string
	Notes            string
	SourceURL        string

	ActorID uuid.UUID
}

// AddProduct registers one, refusing a duplicate of the five-part natural key.
func (s *Store) AddProduct(ctx context.Context, in NewProduct) (uuid.UUID, error) {
	in.TradeName = strings.TrimSpace(in.TradeName)
	in.Strength = strings.TrimSpace(in.Strength)
	in.Manufacturer = strings.TrimSpace(in.Manufacturer)
	in.FormCode = strings.TrimSpace(in.FormCode)
	in.DispenseUnit = strings.TrimSpace(in.DispenseUnit)

	generic, err := s.GenericByName(ctx, in.GenericName)
	if err != nil {
		return uuid.Nil, err
	}
	if err := s.checkVocabulary(ctx, in.FormCode, in.DispenseUnit); err != nil {
		return uuid.Nil, err
	}

	existing, err := s.q.ProductIdentity(ctx, dbgen.ProductIdentityParams{
		FacilityID: in.FacilityID, TradeName: in.TradeName, Strength: in.Strength,
		FormCode: in.FormCode, Manufacturer: in.Manufacturer, DispenseUnit: in.DispenseUnit,
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, err
	}
	if err == nil {
		return existing.ID, ErrDuplicateProduct
	}

	id, err := s.q.InsertProduct(ctx, dbgen.InsertProductParams{
		FacilityID: in.FacilityID, GenericID: generic.ID,
		TradeName: in.TradeName, Strength: in.Strength, FormCode: in.FormCode,
		Manufacturer: in.Manufacturer, DispenseUnit: in.DispenseUnit,
		DgdaRegistration: nilIfBlank(in.DGDARegistration),
		Notes:            nilIfBlank(in.Notes), SourceUrl: nilIfBlank(in.SourceURL),
		ActorID: uuid.NullUUID{UUID: in.ActorID, Valid: in.ActorID != uuid.Nil},
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// ProductEdit is the descriptive half of a product. The identity columns are not here: changing
// a trade name or a strength does not correct a row, it names a different medicine, and the
// honest act is to withdraw this one and add that one so that the price history stays attached
// to the thing it was the price of.
type ProductEdit struct {
	FacilityID       uuid.UUID
	ProductID        uuid.UUID
	GenericName      string
	DGDARegistration string
	Notes            string
	SourceURL        string
	ActorID          uuid.UUID
}

// EditProduct updates the descriptive columns.
func (s *Store) EditProduct(ctx context.Context, in ProductEdit) error {
	generic, err := s.GenericByName(ctx, in.GenericName)
	if err != nil {
		return err
	}
	_, err = s.q.UpdateProductDetails(ctx, dbgen.UpdateProductDetailsParams{
		ID: in.ProductID, FacilityID: in.FacilityID, GenericID: generic.ID,
		DgdaRegistration: nilIfBlank(in.DGDARegistration),
		Notes:            nilIfBlank(in.Notes), SourceUrl: nilIfBlank(in.SourceURL),
		ActorID: uuid.NullUUID{UUID: in.ActorID, Valid: in.ActorID != uuid.Nil},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// Withdraw deactivates a product with a reason. Its prices stay exactly where they are: a
// prescription written last year was written at a price, and that price is still the answer to
// what it cost even though nobody stocks the medicine now.
func (s *Store) Withdraw(ctx context.Context, facility, product uuid.UUID, reason string, actor uuid.UUID) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return errors.New("formulary: withdrawing a product needs a reason")
	}
	_, err := s.q.WithdrawProduct(ctx, dbgen.WithdrawProductParams{
		ID: product, FacilityID: facility, Reason: &reason,
		ActorID: uuid.NullUUID{UUID: actor, Valid: actor != uuid.Nil},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Either it is not here, or it was already withdrawn. Both answer "there is nothing
		// to withdraw", and distinguishing them would tell a caller which products this
		// facility holds.
		return ErrNotFound
	}
	return err
}

// Reinstate puts a withdrawn product back in the list.
func (s *Store) Reinstate(ctx context.Context, facility, product uuid.UUID, actor uuid.UUID) error {
	_, err := s.q.ReinstateProduct(ctx, dbgen.ReinstateProductParams{
		ID: product, FacilityID: facility,
		ActorID: uuid.NullUUID{UUID: actor, Valid: actor != uuid.Nil},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// checkVocabulary refuses a form or a unit that is not in the catalogue.
//
// Checked here rather than left to the foreign key, because a foreign-key violation arrives as
// an internal error with a constraint name in it, and what the person who typed "Tablte" needs
// is a message naming the column and the fact that the value is not one of the listed forms.
func (s *Store) checkVocabulary(ctx context.Context, form, unit string) error {
	catalogue, err := s.Catalogue(ctx)
	if err != nil {
		return err
	}
	knownForm, knownUnit := false, false
	for _, f := range catalogue.Forms {
		if f.Code == form {
			knownForm = true
		}
	}
	for _, u := range catalogue.Units {
		if u.Code == unit {
			knownUnit = true
		}
	}
	if !knownForm || !knownUnit {
		return ErrUnknownVocabulary
	}
	return nil
}
