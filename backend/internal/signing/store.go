package signing

import (
	"context"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Reading signatures, and writing the one thing in this system an anonymous caller writes
// (CP84, CP85).
//
// # Two kinds of statement in one file, and the asymmetry is the design
//
// Everything about a *signature* here is a SELECT. Signatures arrive through the projection,
// like every other row in `read`, because a signature is a fact derived from an event and the
// application role holds no INSERT on the table.
//
// The *attempt log* is the exception, and it is the only one: `ops.prescription_verification_
// attempt` is written directly by the application because an attempt is not derived from
// anything. Somebody with no account scanned a piece of paper; that is a thing that happened to
// this system rather than a clinical fact inside it. The alternative — appending to the ledger —
// would let an unauthenticated caller put rows into the clinical event stream, which is a far
// worse trade than one table with an INSERT grant and no UPDATE.
//
// # No PHI in any query in this file
//
// The public lookup joins `core.app_user` for the physician's name and `core.facility` for the
// clinic's, and touches no patient table at all. That is not an oversight to be corrected later:
// the query is the enforcement. A public response cannot contain a diagnosis that was never
// selected.

// Store reads and writes signature rows.
type Store struct{ pool *pgxpool.Pool }

// NewStore builds one.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const signatureColumns = `
	s.prescription_id, s.facility_id, s.canonical_version, s.canonical_sha256,
	s.algorithm, s.signer_kind, s.key_id, s.public_key, s.signature,
	s.signed_at, s.signed_by, s.device_assurance,
	-- The clearance by value, both columns. Verification recomputes the canonical bytes from
	-- these rather than asking station 10 what stands now; see [Signature].
	s.qa_review_id, s.qa_cleared_at,
	coalesce(u.employee_code, ''), coalesce(u.name_en, ''), coalesce(u.name_bn, ''),
	k.non_exportable`

// ByPrescription reads the signature on one prescription.
//
// Facility-scoped, and the scoping is in the WHERE clause rather than checked afterwards: a
// caller from another clinic gets [ErrNotSigned], which is the same answer they get for a
// prescription that was never signed, which is the same answer they get for one that does not
// exist. Three states, one response, no oracle.
func (s *Store) ByPrescription(ctx context.Context, facility, prescription uuid.UUID) (Signature, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+signatureColumns+`
		  FROM read.prescription_signature s
		  JOIN core.signer_kind k ON k.kind = s.signer_kind
		  LEFT JOIN core.app_user u ON u.id = s.signed_by
		 WHERE s.prescription_id = $1 AND s.facility_id = $2`, prescription, facility)
	return scanSignature(row)
}

// Resolved is what a verification token resolves to: the signature, and the handful of facts the
// public page is allowed to show.
//
// A separate type from [Signature] on purpose. The public response is built from this and only
// from this, so "does the public page leak anything" is answered by reading one struct rather
// than by auditing a handler — and a field added to [Signature] tomorrow does not appear on an
// unauthenticated page because somebody reused the wrong struct.
type Resolved struct {
	Signature Signature

	// PrescriptionID, IssuedOn, PhysicianNameEN/BN, ItemCount and FacilityName are the whole of
	// what CP85's "minimum necessary" allows. See public.go, where the allowlist is stated
	// again as an assertion rather than as a comment.

	// IssuedOn is the **date** the prescription was signed, in the clinic's own time zone, as a
	// date rather than an instant — see [PublicPrescription] for why the minute is withheld.
	//
	// Carried as a `time.Time` and rendered by the handler rather than by `to_char` in SQL,
	// because the public page says the date in two languages and Postgres has no Bengali. The
	// rendering is `clinicalterm`'s, which is the one place in this system that owns "what does
	// this look like to a person reading it".
	IssuedOn        time.Time
	PhysicianNameEN string
	PhysicianNameBN string
	ItemCount       int
	FacilityNameEN  string
	FacilityNameBN  string
}

// ByTokenDigest resolves a presented token to what it names.
//
// One indexed probe on a unique digest. The token itself never appears in this query, in a
// parameter, or in any log line this method can reach: what travels is the digest, so a database
// audit log, a slow-query log or a `pg_stat_statements` row cannot yield a working QR code.
func (s *Store) ByTokenDigest(ctx context.Context, digest []byte) (Resolved, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+signatureColumns+`,
		       -- The clinic's own calendar day, as a date. AT TIME ZONE first, because a
		       -- prescription signed at half past eleven at night in Faridpur is that day's
		       -- prescription and not the next one's, which is what a UTC truncation would
		       -- make it. Rendered for a reader in Go; see Resolved.IssuedOn.
		       (s.signed_at AT TIME ZONE 'Asia/Dhaka')::date,
		       (SELECT count(*) FROM read.prescription_item i
		         WHERE i.prescription_id = s.prescription_id AND i.removed_at IS NULL),
		       coalesce(f.name_en, ''), coalesce(f.name_bn, '')
		  FROM read.prescription_signature s
		  JOIN core.signer_kind k ON k.kind = s.signer_kind
		  LEFT JOIN core.app_user u ON u.id = s.signed_by
		  LEFT JOIN core.facility f ON f.id = s.facility_id
		 WHERE s.verification_token_digest = $1`, digest)

	var out Resolved
	var canonicalDigest, publicKey, signature []byte
	err := row.Scan(
		&out.Signature.PrescriptionID, &out.Signature.FacilityID,
		&out.Signature.CanonicalVersion, &canonicalDigest,
		&out.Signature.Algorithm, &out.Signature.SignerKind, &out.Signature.KeyID,
		&publicKey, &signature,
		&out.Signature.SignedAt, &out.Signature.SignedBy,
		&out.Signature.DeviceAssurance, &out.Signature.QAReviewID, &out.Signature.QAClearedAt,
		&out.Signature.SignedByCode, &out.Signature.SignedByNameEN, &out.Signature.SignedByNameBN,
		&out.Signature.NonExportableKey,
		&out.IssuedOn, &out.ItemCount, &out.FacilityNameEN, &out.FacilityNameBN)
	if errors.Is(err, pgx.ErrNoRows) {
		return Resolved{}, ErrNotSigned
	}
	if err != nil {
		return Resolved{}, err
	}
	out.Signature.CanonicalSHA256 = hex.EncodeToString(canonicalDigest)
	out.Signature.PublicKey = hex.EncodeToString(publicKey)
	out.Signature.Value = hex.EncodeToString(signature)
	out.PhysicianNameEN = out.Signature.SignedByNameEN
	out.PhysicianNameBN = out.Signature.SignedByNameBN
	return out, nil
}

// SealedToken is the sealed verification token and the key that sealed it.
//
// Facility-scoped like everything else here, and answering [ErrNotSigned] for a prescription in
// another clinic — the same answer it gives for one that was never signed, so that this method
// cannot be used to learn which prescriptions exist elsewhere.
func (s *Store) SealedToken(ctx context.Context, facility, prescription uuid.UUID) ([]byte, string, error) {
	var sealed []byte
	var keyID string
	err := s.pool.QueryRow(ctx, `
		SELECT verification_token_sealed, verification_token_key_id
		  FROM read.prescription_signature
		 WHERE prescription_id = $1 AND facility_id = $2`, prescription, facility).
		Scan(&sealed, &keyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrNotSigned
	}
	return sealed, keyID, err
}

type scannable interface {
	Scan(dest ...any) error
}

func scanSignature(row scannable) (Signature, error) {
	var out Signature
	var canonicalDigest, publicKey, signature []byte
	err := row.Scan(
		&out.PrescriptionID, &out.FacilityID, &out.CanonicalVersion, &canonicalDigest,
		&out.Algorithm, &out.SignerKind, &out.KeyID, &publicKey, &signature,
		&out.SignedAt, &out.SignedBy, &out.DeviceAssurance, &out.QAReviewID, &out.QAClearedAt,
		&out.SignedByCode, &out.SignedByNameEN, &out.SignedByNameBN, &out.NonExportableKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return Signature{}, ErrNotSigned
	}
	if err != nil {
		return Signature{}, err
	}
	out.CanonicalSHA256 = hex.EncodeToString(canonicalDigest)
	out.PublicKey = hex.EncodeToString(publicKey)
	out.Value = hex.EncodeToString(signature)
	return out, nil
}

// Attempt is one scan of a QR code, on its way to the log.
type Attempt struct {
	// FacilityID and PrescriptionID are set **only when the token resolved**, and are taken
	// from the resolved row rather than from anything the caller sent. That is the whole
	// defence against this table becoming a patient-linkage oracle: a caller cannot cause a
	// prescription id of their choosing to be written beside their own address.
	FacilityID     *uuid.UUID
	PrescriptionID *uuid.UUID

	PresentedDigest []byte
	Outcome         string
	ClientDigest    []byte
	At              time.Time
}

// RecordAttempt writes one verification attempt.
//
// # Why its error is swallowed by every caller
//
// A verification that succeeded must not be reported as failed because the log write did not
// land, and — more importantly for a public endpoint — a caller must not be able to learn
// anything from a log failure. The error is returned so that a test can assert the write
// happened; production callers log it and answer the question they were asked.
func (s *Store) RecordAttempt(ctx context.Context, in Attempt) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO ops.prescription_verification_attempt
		  (id, facility_id, prescription_id, presented_digest, outcome, client_digest, at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		uuid.New(), in.FacilityID, in.PrescriptionID, in.PresentedDigest,
		in.Outcome, in.ClientDigest, in.At)
	return err
}

// Attempts is the log, newest first. For the operations console; never public.
func (s *Store) Attempts(ctx context.Context, limit int) ([]Attempt, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT facility_id, prescription_id, presented_digest, outcome, client_digest, at
		  FROM ops.prescription_verification_attempt
		 ORDER BY at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Attempt{}
	for rows.Next() {
		var one Attempt
		if err := rows.Scan(&one.FacilityID, &one.PrescriptionID, &one.PresentedDigest,
			&one.Outcome, &one.ClientDigest, &one.At); err != nil {
			return nil, err
		}
		out = append(out, one)
	}
	return out, rows.Err()
}
