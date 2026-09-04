// Package terminology is the coded vocabulary a diagnosis or a complaint is recorded in
// (CP52, §8, §9.1, D-24).
//
// # What D-24 leaves open, and what it does not
//
// D-24 is about SNOMED CT: its use requires an Affiliate licence, and whether Bangladesh
// confers free use has to be verified before any SNOMED content is embedded. Nothing here is
// SNOMED-derived; `core.terminology_map` exists so that it can be layered on the day the
// answer comes back, and a standing invariant refuses any content from a system marked
// unusable — because "we remembered not to" is not a control.
//
// What is *not* open is ICD. WHO publishes it under permissive terms, and the plan's own
// recommendation is ICD as the backbone with an internal dictionary for complaints. The one
// thing still undecided is ICD-10 or ICD-11, and this package does not care: a system is a
// row, a version is a row, and every coding carries both.
//
// # Why a coding carries its version
//
// Criterion 2, and it is not bookkeeping. ICD-10's E11.9 and ICD-11's 5A11 are not the same
// concept, and a diagnosis recorded in 2026 has to still mean in 2032 what the person who
// recorded it meant. A code with no system and no version is a string.
package terminology

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Store reads the terminology. There is no write path: a code set is loaded by migration and
// a clinic does not edit the WHO's classification.
type Store struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: dbgen.New(pool)}
}

// System is one terminology and what may be done with it.
type System struct {
	Code           string `json:"code"`
	TitleEN        string `json:"title_en"`
	TitleBN        string `json:"title_bn"`
	Publisher      string `json:"publisher"`
	LicenceNote    string `json:"licence_note,omitempty"`
	Usable         bool   `json:"usable"`
	DefaultVersion string `json:"default_version,omitempty"`
}

// Concept is one coded thing: a diagnosis, or a complaint in the clinic's own dictionary.
type Concept struct {
	System  string `json:"system"`
	Version string `json:"version"`
	Code    string `json:"code"`

	DisplayEN string `json:"display_en"`
	DisplayBN string `json:"display_bn,omitempty"`

	// The grouping a picker files this under, in both languages. Bengali diagnoses filed
	// under English chapter names is what half-bilingual looks like on a screen, and it
	// reads as an interface somebody translated the easy parts of.
	Heading   string `json:"heading,omitempty"`
	HeadingBN string `json:"heading_bn,omitempty"`

	// FavouriteRank is the clinic's own ordering, where it has one. Criterion 1 — the twenty
	// most common diagnoses in three keystrokes — is reached by knowing which twenty, not by
	// a cleverer search.
	FavouriteRank *int `json:"favourite_rank,omitempty"`

	// Tier and Score are why this result came where it did. Returned rather than hidden:
	// "why is that third" is the question every search gets asked, and a ranking nobody can
	// inspect is a ranking nobody can tune.
	Tier  int     `json:"tier,omitempty"`
	Score float64 `json:"score,omitempty"`
}

// Mapping is one concept's equivalent in another system.
type Mapping struct {
	ToSystem    string `json:"to_system"`
	ToVersion   string `json:"to_version"`
	ToCode      string `json:"to_code"`
	Equivalence string `json:"equivalence"`
}

// ErrUnknownConcept is a code that is not in the system and version named.
var ErrUnknownConcept = errors.New("terminology: no such concept")

// ErrUnusableSystem is a search against a terminology this deployment may not use.
//
// Refused rather than returning nothing, because the two mean different things: "no results"
// sends a clinician looking for a better spelling, and "we may not use SNOMED here" sends
// somebody to D-24.
var ErrUnusableSystem = errors.New("terminology: that terminology may not be used here")

// ErrUnknownSystem is a terminology nobody has registered. Distinct from ErrUnusableSystem,
// because "we have never heard of that" and "we are not licensed for that" send the person
// reading the message to two different places.
var ErrUnknownSystem = errors.New("terminology: no such terminology")

// ErrUnknownVersion is a version that system has never published here.
//
// Refusing rather than falling back to the default is the whole of criterion 2: a caller who
// asked for ICD-10 2019 and silently got 2016 has recorded a coding whose version is a lie,
// and the lie is only discovered years later by somebody trying to read it back.
var ErrUnknownVersion = errors.New("terminology: no such version of that terminology")

// ErrNoDefaultVersion is a system that is registered but carries no content yet — ICD-11,
// today. A caller may name a version explicitly the moment one is loaded.
var ErrNoDefaultVersion = errors.New("terminology: that terminology has no default version here")

// MaxResults bounds a search. A picker showing more than this is a picker nobody reads to the
// bottom of, and the answer to a query with three hundred matches is a better query.
const MaxResults = 25

// Systems lists the terminologies and what may be done with each.
func (s *Store) Systems(ctx context.Context) ([]System, error) {
	rows, err := s.q.CodeSystems(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]System, 0, len(rows))
	for _, row := range rows {
		system := System{
			Code: row.Code, TitleEN: row.TitleEn, TitleBN: row.TitleBn,
			Publisher: row.Publisher, LicenceNote: row.LicenceNote, Usable: row.Usable,
		}
		if row.DefaultVersion != nil {
			system.DefaultVersion = *row.DefaultVersion
		}
		out = append(out, system)
	}
	return out, nil
}

// Search finds concepts. The ranking is the query's, in one statement; this only bounds the
// request and refuses a terminology that may not be used.
func (s *Store) Search(ctx context.Context, system, version, query string, limit int) ([]Concept, error) {
	version, err := s.Resolve(ctx, system, version)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > MaxResults {
		limit = MaxResults
	}
	// An empty query is the favourites, not an error and not everything. A picker opens
	// before anybody has typed, and what it should show then is the clinic's own list.
	if strings.TrimSpace(query) == "" {
		return s.Favourites(ctx, system, version)
	}
	rows, err := s.q.SearchTerminology(ctx, dbgen.SearchTerminologyParams{
		PQuery: query, PSystem: system, PVersion: version, PLimit: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Concept, 0, len(rows))
	for _, row := range rows {
		concept := Concept{
			System: row.System, Version: row.Version, Code: row.Code,
			DisplayEN: row.DisplayEn, DisplayBN: row.DisplayBn,
			Heading: row.Heading, HeadingBN: row.HeadingBn,
			Tier: int(row.Tier), Score: row.Score,
		}
		if row.FavouriteRank != nil {
			rank := int(*row.FavouriteRank)
			concept.FavouriteRank = &rank
		}
		out = append(out, concept)
	}
	return out, nil
}

// Favourites is the clinic's own list, in the order it was ranked.
func (s *Store) Favourites(ctx context.Context, system, version string) ([]Concept, error) {
	version, err := s.Resolve(ctx, system, version)
	if err != nil {
		return nil, err
	}
	rows, err := s.q.TerminologyFavourites(ctx, dbgen.TerminologyFavouritesParams{
		System: system, Version: version,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Concept, 0, len(rows))
	for _, row := range rows {
		concept := Concept{
			System: row.System, Version: row.Version, Code: row.Code,
			DisplayEN: row.DisplayEn, DisplayBN: row.DisplayBn,
			Heading: row.Heading, HeadingBN: row.HeadingBn,
		}
		if row.FavouriteRank != nil {
			rank := int(*row.FavouriteRank)
			concept.FavouriteRank = &rank
		}
		out = append(out, concept)
	}
	return out, nil
}

// Concept reads one, so a screen can render a coding recorded years ago under a version
// nobody uses any more.
func (s *Store) Concept(ctx context.Context, system, version, code string) (Concept, error) {
	version, err := s.Resolve(ctx, system, version)
	if err != nil {
		return Concept{}, err
	}
	row, err := s.q.TerminologyConcept(ctx, dbgen.TerminologyConceptParams{
		System: system, Version: version, Code: code,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Concept{}, ErrUnknownConcept
	}
	if err != nil {
		return Concept{}, err
	}
	concept := Concept{
		System: row.System, Version: row.Version, Code: row.Code,
		DisplayEN: row.DisplayEn, DisplayBN: row.DisplayBn,
		Heading: row.Heading, HeadingBN: row.HeadingBn,
	}
	if row.FavouriteRank != nil {
		rank := int(*row.FavouriteRank)
		concept.FavouriteRank = &rank
	}
	return concept, nil
}

// Mappings is where a concept maps in another system. Empty until D-24 answers; the method
// exists so that the day it does, nothing above this line changes.
func (s *Store) Mappings(ctx context.Context, system, version, code string) ([]Mapping, error) {
	version, err := s.Resolve(ctx, system, version)
	if err != nil {
		return nil, err
	}
	rows, err := s.q.TerminologyMappings(ctx, dbgen.TerminologyMappingsParams{
		FromSystem: system, FromVersion: version, FromCode: code,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Mapping, 0, len(rows))
	for _, row := range rows {
		out = append(out, Mapping{
			ToSystem: row.ToSystem, ToVersion: row.ToVersion, ToCode: row.ToCode,
			Equivalence: row.Equivalence,
		})
	}
	return out, nil
}

// Resolve turns a request's system and optional version into the pair every coding must
// carry, and refuses a terminology this deployment may not use.
//
// It is one round trip and it happens before every read, which is deliberate: the licence
// state and the version set are properties of the deployment, not of the caller, and a check
// the caller can skip is not a check. The version it returns is the one the rows will carry,
// so a client that never names a version still receives one with every result — which is how
// criterion 2 survives a picker whose author did not read it.
func (s *Store) Resolve(ctx context.Context, system, version string) (string, error) {
	var (
		usable       bool
		defaultVer   string
		versionKnown bool
	)
	err := s.pool.QueryRow(ctx, `
		SELECT s.usable,
		       coalesce((SELECT v.version FROM core.code_system_version v
		                  WHERE v.system = s.code AND v.is_default), ''),
		       EXISTS (SELECT 1 FROM core.code_system_version v
		                WHERE v.system = s.code AND v.version = $2)
		  FROM core.code_system s
		 WHERE s.code = $1`, system, version).Scan(&usable, &defaultVer, &versionKnown)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrUnknownSystem
	}
	if err != nil {
		return "", err
	}
	// Licence before existence of content: a system nobody may use should say so even when
	// it happens to be empty, or the operator fixes the wrong problem.
	if !usable {
		return "", ErrUnusableSystem
	}
	if strings.TrimSpace(version) == "" {
		if defaultVer == "" {
			return "", ErrNoDefaultVersion
		}
		return defaultVer, nil
	}
	if !versionKnown {
		return "", ErrUnknownVersion
	}
	return version, nil
}
