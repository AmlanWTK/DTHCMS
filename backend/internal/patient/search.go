package patient

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/textmatch"
)

// Patient search (CP31).
//
// Used dozens of times an hour by every station, which sets the whole design: it has to be
// fast, it has to accept whatever handle the person in front of the operator happens to
// have, and it has to be forgiving about spelling — because the operator is typing a name
// they heard, in a script the patient may not use.
//
// One query with four ways in. A clinical id, a telephone number, a name in either script,
// or a name the operator has romanised differently from whoever registered it. Which route
// found the row does not change the result's shape; the rank does.
//
// **Searches are audited.** A bulk-search pattern — one operator running fifty name
// searches in a minute — is what data exfiltration looks like from the inside, and it is
// invisible unless somebody writes it down. The caller records it; this module returns the
// facts the record needs.

// A clinical id as typed: DTHC-FRD-2026-000137, or just the number at the end, which is
// what somebody reading it off a card usually types.
var (
	clinicalIDLike = regexp.MustCompile(`^[A-Za-z]{2,}[A-Za-z0-9-]*-[0-9]{4}-[0-9]{1,6}$`)
	digitsOnly     = regexp.MustCompile(`^[0-9\s+-]+$`)
)

// SearchQuery is one search.
type SearchQuery struct {
	Term string
	// IncludeMerged brings records that now redirect into the results. Off by default:
	// a station looking for today's patient does not want yesterday's duplicate. On for
	// the administrative screens, where "where did this record go" is the question.
	IncludeMerged bool
	Page          int
	PageSize      int
}

// SearchResult is one row of the answer.
type SearchResult struct {
	PatientID    uuid.UUID  `json:"patient_id"`
	ClinicalID   string     `json:"clinical_id"`
	NameEN       string     `json:"name_en"`
	NameBN       string     `json:"name_bn"`
	Sex          string     `json:"sex"`
	BirthDate    string     `json:"birth_date"`
	DOBPrecision string     `json:"dob_precision"`
	Age          int        `json:"age"`
	PhoneMasked  string     `json:"phone_masked"`
	District     string     `json:"district"`
	Upazila      string     `json:"upazila"`
	Status       string     `json:"status"`
	MergedIntoID *uuid.UUID `json:"merged_into_id,omitempty"`
	RegisteredAt time.Time  `json:"registered_at"`
	// Rank is how good the match is, 0..1. Returned so a screen can say "exact" against a
	// clinical id and stay quiet against a fuzzy name.
	Rank float64 `json:"rank"`
}

// DefaultPageSize is what a station screen shows without scrolling. Twenty is about two
// screenfuls on a tablet and comfortably under the point where the query stops being fast.
const DefaultPageSize = 20

// MaxPageSize caps what a caller may ask for. Not a performance guard so much as an
// exfiltration one: a search endpoint that will return two thousand rows is a bulk export
// with a different name.
const MaxPageSize = 100

// Search finds patients by any plausible handle.
//
// **Routed, not unioned.** An earlier version was one query with four ORed branches, and it
// cost 280 ms to look up an exact clinical id at fifty thousand patients: the row was found
// by an index lookup and then the query paid for three trigram scans that could not
// possibly match. So the route is decided here, in Go, from the shape of what was typed —
// and an exact handle costs one index lookup.
//
// The fall-through matters as much as the routing. A handle that finds nothing falls back
// to the name query, because somebody may have typed a name that looks like a number, and a
// search that returns nothing when it could have returned something is the failure staff
// remember.
func (s *Store) Search(ctx context.Context, facility uuid.UUID, q SearchQuery, now time.Time) ([]SearchResult, error) {
	term := strings.TrimSpace(q.Term)
	if term == "" {
		return []SearchResult{}, nil
	}
	size := q.PageSize
	if size <= 0 {
		size = DefaultPageSize
	}
	if size > MaxPageSize {
		size = MaxPageSize
	}
	offset := 0
	if q.Page > 1 {
		offset = (q.Page - 1) * size
	}
	//nolint:gosec // size is capped at MaxPageSize and offset is bounded by the page number
	limit, skip := int32(size), int32(offset)

	// A clinical id, whole or as the digits off a card.
	if whole, serial := clinicalHandles(term); whole != "" || serial != "" {
		rows, err := s.q.PatientsByClinicalID(ctx, dbgen.PatientsByClinicalIDParams{
			FacilityID: facility, ClinicalID: whole, Serial: serial,
			IncludeMerged: q.IncludeMerged, PageSize: limit,
		})
		if err != nil {
			return nil, err
		}
		if len(rows) > 0 {
			return plainResults(rows, now), nil
		}
	}

	// A telephone number.
	if phone := phonePattern(term); phone != "" {
		rows, err := s.q.PatientsByPhone(ctx, dbgen.PatientsByPhoneParams{
			FacilityID: facility, Phone: phone,
			IncludeMerged: q.IncludeMerged, PageSize: limit,
		})
		if err != nil {
			return nil, err
		}
		if len(rows) > 0 {
			return phoneResults(rows, now), nil
		}
	}

	// A name, in either script, however it was romanised.
	latin, bangla := scripts(term)
	rows, err := s.q.PatientsByName(ctx, dbgen.PatientsByNameParams{
		FacilityID: facility, Term: term,
		// The phonetic key is what makes "Muhammad Raheem" find "Mohammad Rahim" (CP30).
		NameKey: textmatch.Key(term),
		// Which scripts the term uses. Comparing a Latin term against the Bangla column
		// can never be above zero, and skipping it is a measurable share of the budget.
		Latin: latin, Bangla: bangla,
		IncludeMerged: q.IncludeMerged,
		PageSize:      limit, PageOffset: skip,
	})
	if err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(rows))
	for _, row := range rows {
		result := resultOf(row.PatientID, row.ClinicalID, row.NameEn, row.NameBn, row.Sex,
			row.BirthDate, row.DobPrecision, row.PhonePrimary, row.District, row.Upazila,
			row.Status, row.MergedIntoID, row.RegisteredAt, now)
		result.Rank = float64(row.Rank)
		out = append(out, result)
	}
	return out, nil
}

// plainResults and phoneResults exist because sqlc generates a distinct row type per query
// even when the columns are identical. The conversion is the same; only the type differs.
func plainResults(rows []dbgen.PatientsByClinicalIDRow, now time.Time) []SearchResult {
	out := make([]SearchResult, 0, len(rows))
	for _, row := range rows {
		out = append(out, resultOf(row.PatientID, row.ClinicalID, row.NameEn, row.NameBn,
			row.Sex, row.BirthDate, row.DobPrecision, row.PhonePrimary, row.District,
			row.Upazila, row.Status, row.MergedIntoID, row.RegisteredAt, now))
	}
	return out
}

func phoneResults(rows []dbgen.PatientsByPhoneRow, now time.Time) []SearchResult {
	out := make([]SearchResult, 0, len(rows))
	for _, row := range rows {
		out = append(out, resultOf(row.PatientID, row.ClinicalID, row.NameEn, row.NameBn,
			row.Sex, row.BirthDate, row.DobPrecision, row.PhonePrimary, row.District,
			row.Upazila, row.Status, row.MergedIntoID, row.RegisteredAt, now))
	}
	return out
}

//nolint:revive // one converter beats three copies of the same field list
func resultOf(id uuid.UUID, clinicalID, nameEN, nameBN, sex string, born time.Time,
	precision, phone, district, upazila, status string, merged uuid.NullUUID,
	registeredAt, now time.Time) SearchResult {
	return SearchResult{
		PatientID: id, ClinicalID: clinicalID,
		NameEN: nameEN, NameBN: nameBN, Sex: sex,
		BirthDate:    born.In(Dhaka).Format(time.DateOnly),
		DOBPrecision: precision,
		Age:          BirthDate{Date: born}.Age(now),
		// Masked, because a search result list is the screen most often read over an
		// operator's shoulder.
		PhoneMasked: maskPhone(phone),
		District:    district, Upazila: upazila,
		Status: status, MergedIntoID: nullableUUID(merged),
		RegisteredAt: registeredAt,
		// An exact handle is not a guess.
		Rank: 1,
	}
}

// Today is the today's-patients fast path: who was registered on the clinic's current day.
//
// A separate query rather than a search with an empty term, because it is the single most
// frequent read in the building and it must never become a scan.
func (s *Store) Today(ctx context.Context, facility uuid.UUID, now time.Time, limit int) ([]SearchResult, int, error) {
	if limit <= 0 || limit > MaxPageSize {
		limit = MaxPageSize
	}
	start, end := clinicDay(now)

	total, err := s.q.CountTodaysPatients(ctx, dbgen.CountTodaysPatientsParams{
		FacilityID: facility, RegisteredAt: start, RegisteredAt_2: end,
	})
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.q.TodaysPatients(ctx, dbgen.TodaysPatientsParams{
		FacilityID: facility, RegisteredAt: start, RegisteredAt_2: end,
		Limit: int32(limit), //nolint:gosec // capped above
	})
	if err != nil {
		return nil, 0, err
	}
	out := make([]SearchResult, 0, len(rows))
	for _, row := range rows {
		out = append(out, SearchResult{
			PatientID: row.PatientID, ClinicalID: row.ClinicalID,
			NameEN: row.NameEn, NameBN: row.NameBn, Sex: row.Sex,
			BirthDate:    row.BirthDate.In(Dhaka).Format(time.DateOnly),
			DOBPrecision: row.DobPrecision,
			Age:          BirthDate{Date: row.BirthDate}.Age(now),
			PhoneMasked:  maskPhone(row.PhonePrimary),
			District:     row.District, Upazila: row.Upazila,
			Status: row.Status, MergedIntoID: nullableUUID(row.MergedIntoID),
			RegisteredAt: row.RegisteredAt, Rank: 1,
		})
	}
	return out, int(total), nil
}

// clinicDay is the day on the clinic's wall clock, not UTC's. A patient registered at 00:30
// in Dhaka belongs to that day, and a "today's patients" list that disagrees for six hours
// every morning is one nobody uses.
func clinicDay(now time.Time) (start, end time.Time) {
	local := now.In(Dhaka)
	start = time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, Dhaka)
	return start.UTC(), start.AddDate(0, 0, 1).UTC()
}

// clinicalHandles reads a term as a clinical id: the whole thing, or the six digits a
// person reads off a card. Either may be empty; both empty means this is not a clinical id.
func clinicalHandles(term string) (whole, serial string) {
	trimmed := strings.ToUpper(strings.TrimSpace(term))
	if clinicalIDLike.MatchString(trimmed) {
		return trimmed, ""
	}
	// "137", "000137" — the serial, padded to the six digits the column holds. Matched by
	// an expression index rather than a trailing LIKE, which would be a leading wildcard
	// and therefore a scan of the whole register.
	if isAllDigits(trimmed) && len(trimmed) <= 6 {
		return "", strings.Repeat("0", 6-len(trimmed)) + trimmed
	}
	return "", ""
}

// phonePattern normalises a term that looks like a telephone number, and returns empty
// otherwise. Empty rather than the raw term, because the column comparison is exact and a
// name would match nothing anyway — but a name that happens to be digits should not run it.
func phonePattern(term string) string {
	if !digitsOnly.MatchString(strings.TrimSpace(term)) {
		return ""
	}
	if normalised, ok := NormalisePhone(term); ok {
		return normalised
	}
	return ""
}

// scripts says which alphabets a search term is written in. Both can be true — a clinic's
// staff mix them, and "রহিম Begum" is a thing somebody types.
func scripts(term string) (latin, bangla bool) {
	for _, r := range term {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			latin = true
		case r >= 0x0980 && r <= 0x09FF:
			bangla = true
		}
	}
	// A term of pure digits or punctuation reached the name route only as a fall-through;
	// treat it as Latin so it is compared against something rather than nothing.
	if !latin && !bangla {
		latin = true
	}
	return latin, bangla
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
