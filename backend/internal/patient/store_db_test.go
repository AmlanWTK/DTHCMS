package patient_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/patient"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
)

// The patient schema against a real database (CP28).
//
// Nothing here is mocked, because everything worth asserting is a property of the real
// thing: that two registrations racing each other cannot draw the same clinical id, that a
// birth date in the future is refused by the database and not only by the Go that usually
// runs first, and that the link from a research row back to a person is unreachable by the
// roles that must not have it.

type db struct {
	*testsupport.DB
	pool     *pgxpool.Pool
	store    *patient.Store
	sealer   *patient.IdentifierSealer
	facility uuid.UUID
	code     string
}

func open(t *testing.T) *db {
	t.Helper()
	base := testsupport.Postgres(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, base.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	h := &db{DB: base, pool: pool, store: patient.NewStore(pool), sealer: sealer(t)}
	if err := base.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&h.facility); err != nil {
		t.Fatal(err)
	}
	if err := base.SQL.QueryRow(`SELECT code FROM core.facility WHERE id = $1`, h.facility).Scan(&h.code); err != nil {
		t.Fatal(err)
	}
	return h
}

// poolAs opens a second pool as one of the deployment's real roles, so that a test can
// run the production write path with the production privileges.
func (h *db) poolAs(t *testing.T, role string) *pgxpool.Pool {
	t.Helper()
	dsn, err := url.Parse(h.DSN)
	if err != nil {
		t.Fatal(err)
	}
	dsn.User = url.UserPassword(role, "dthcms_local_only")
	pool, err := pgxpool.New(context.Background(), dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// registration is a complete, valid patient. `at` fixes the clinic year the clinical id is
// drawn from.
func (h *db) registration(name string, at time.Time) patient.NewPatient {
	researchID, err := patient.NewResearchID()
	if err != nil {
		panic(err)
	}
	phone, _ := patient.NormalisePhone("01712345678")
	return patient.NewPatient{
		Patient: patient.Patient{
			FacilityID: h.facility,
			NameEN:     name,
			NameBN:     "রোগী",
			Sex:        patient.SexFemale,
			Birth: patient.BirthDate{
				Date:      time.Date(1979, 4, 12, 0, 0, 0, 0, patient.Dhaka),
				Precision: patient.PrecisionDay,
				Source:    patient.SourceNationalID,
			},
			PhonePrimary: phone,
			Socio: patient.Socioeconomic{
				Education: "secondary", Occupation: "homemaker", IncomeBand: "10k_25k",
				HouseholdSize: 5, Residence: "rural", MedicinePayer: "family",
			},
			Status:       patient.StatusActive,
			RegisteredAt: at,
		},
		ResearchID:   researchID,
		FacilityCode: h.code,
	}
}

func (h *db) withNationalID(t *testing.T, in patient.NewPatient, nid string) patient.NewPatient {
	t.Helper()
	id, err := h.sealer.Seal(patient.NationalID, nid)
	if err != nil {
		t.Fatal(err)
	}
	in.Identifiers = append(in.Identifiers, id)
	return in
}

var registeredAt = time.Date(2026, 9, 3, 10, 0, 0, 0, patient.Dhaka)

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return c
}

// --- one registration ---

func TestARegistrationWritesEverythingOrNothing(t *testing.T) {
	h := open(t)
	in := h.withNationalID(t, h.registration("Rahima Begum", registeredAt), "1990 1234 5678")

	created, err := h.store.Create(ctx(t), in)
	if err != nil {
		t.Fatal(err)
	}

	if created.ClinicalID != h.code+"-2026-000001" {
		t.Errorf("clinical id = %q, want %s-2026-000001", created.ClinicalID, h.code)
	}
	if created.Socio.HouseholdSize != 5 || created.Socio.MedicinePayer != "family" {
		t.Errorf("the socio-economic baseline did not survive the round trip: %+v", created.Socio)
	}

	identifiers, err := h.store.Identifiers(ctx(t), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(identifiers) != 1 || !strings.Contains(identifiers[0].Masked, "*") {
		t.Fatalf("identifiers = %+v", identifiers)
	}

	// The anonymised row and the link exist, in the same breath as the patient: a patient
	// with no research id is a gap in every cohort they should have been in, and nobody
	// would notice for a year.
	var subjects int
	if err := h.SQL.QueryRow(
		`SELECT count(*) FROM research.research_subject WHERE research_id = $1`, in.ResearchID,
	).Scan(&subjects); err != nil || subjects != 1 {
		t.Errorf("research subjects = %d (%v)", subjects, err)
	}
	var linked uuid.UUID
	if err := h.SQL.QueryRow(
		`SELECT patient_id FROM identity_link.research_subject WHERE research_id = $1`, in.ResearchID,
	).Scan(&linked); err != nil {
		t.Fatalf("reading the link as the owner: %v", err)
	}
	if linked != created.ID {
		t.Errorf("the link points at %s, not the patient %s", linked, created.ID)
	}
}

func TestAFailedRegistrationLeavesNothingBehind(t *testing.T) {
	// CP29 appends the clinical event inside this transaction. If the event is refused —
	// an unknown type, a payload that does not validate — the patient must not exist
	// either, or the ledger and the record disagree from the first row.
	h := open(t)
	in := h.registration("Never Registered", registeredAt)
	in.InTx = func(context.Context, pgx.Tx, patient.Patient) error {
		return fmt.Errorf("the event was refused")
	}

	if _, err := h.store.Create(ctx(t), in); err == nil {
		t.Fatal("a registration whose event failed was committed")
	}

	for table, query := range map[string]string{
		"core.patient":                   `SELECT count(*) FROM core.patient WHERE name_en = 'Never Registered'`,
		"research.research_subject":      `SELECT count(*) FROM research.research_subject WHERE research_id = '` + in.ResearchID + `'`,
		"identity_link.research_subject": `SELECT count(*) FROM identity_link.research_subject WHERE research_id = '` + in.ResearchID + `'`,
	} {
		var rows int
		if err := h.SQL.QueryRow(query).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows != 0 {
			t.Errorf("%s kept %d row(s) from a rolled-back registration", table, rows)
		}
	}

	// And the clinical id the failed attempt drew is *not* reused, because the counter
	// rolled back with it: the next patient is still 000001.
	next, err := h.store.Create(ctx(t), h.registration("Rahima Begum", registeredAt))
	if err != nil {
		t.Fatal(err)
	}
	if next.ClinicalID != h.code+"-2026-000001" {
		t.Errorf("after a rollback the next clinical id was %q; the series has a gap", next.ClinicalID)
	}
}

// --- the clinical id ---

func TestClinicalIDsAreUniqueAndGaplessUnderConcurrency(t *testing.T) {
	// Acceptance criterion 2. A sequence would be simpler and would leave gaps when a
	// transaction rolls back; a clinic that finds DTHC-FRD-2026-000138 with no 000137
	// spends an afternoon looking for the missing person.
	h := open(t)
	const registrations = 40

	var wg sync.WaitGroup
	ids := make([]string, registrations)
	errs := make([]error, registrations)
	start := make(chan struct{})
	for i := range registrations {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // everybody contends for the counter at once
			created, err := h.store.Create(ctx(t), h.registration(fmt.Sprintf("Patient %02d", i), registeredAt))
			ids[i], errs[i] = created.ClinicalID, err
		}()
	}
	close(start)
	wg.Wait()

	seen := map[string]bool{}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("registration %d failed: %v", i, err)
		}
		if seen[ids[i]] {
			t.Fatalf("%s was issued twice", ids[i])
		}
		seen[ids[i]] = true
	}
	for n := 1; n <= registrations; n++ {
		want := fmt.Sprintf("%s-2026-%06d", h.code, n)
		if !seen[want] {
			t.Errorf("%s was never issued; the series has a gap", want)
		}
	}
}

func TestEachFacilityAndYearHasItsOwnSeries(t *testing.T) {
	h := open(t)
	// A second clinic, as there will be.
	var second uuid.UUID
	if err := h.SQL.QueryRow(`
		INSERT INTO core.facility (code, name_en, name_bn, facility_type)
		VALUES ('DTHC-DHK', 'DTHC Dhaka', 'ডিটিএইচসি ঢাকা', 'clinic') RETURNING id`,
	).Scan(&second); err != nil {
		t.Fatal(err)
	}

	first, err := h.store.Create(ctx(t), h.registration("Faridpur One", registeredAt))
	if err != nil {
		t.Fatal(err)
	}
	other := h.registration("Dhaka One", registeredAt)
	other.Patient.FacilityID = second
	other.FacilityCode = "DTHC-DHK"
	elsewhere, err := h.store.Create(ctx(t), other)
	if err != nil {
		t.Fatal(err)
	}
	// Next year, at the same clinic.
	nextYear := h.registration("Faridpur Next Year", registeredAt.AddDate(1, 0, 0))
	later, err := h.store.Create(ctx(t), nextYear)
	if err != nil {
		t.Fatal(err)
	}

	if first.ClinicalID != h.code+"-2026-000001" ||
		elsewhere.ClinicalID != "DTHC-DHK-2026-000001" ||
		later.ClinicalID != h.code+"-2027-000001" {
		t.Errorf("the three series are not independent: %q, %q, %q",
			first.ClinicalID, elsewhere.ClinicalID, later.ClinicalID)
	}
	// And the id names the clinic and the year, which is the whole reason it is not a
	// UUID: it is read aloud at a desk.
	if !strings.HasPrefix(first.ClinicalID, h.code+"-2026-") {
		t.Errorf("%q does not name its clinic and year", first.ClinicalID)
	}
}

// --- the date of birth, at the database ---

func TestTheDatabaseRefusesAnImplausibleBirthDate(t *testing.T) {
	// The Go validation usually runs first. This asserts the floor beneath it: a bulk
	// import, a migration or a future handler cannot write an age a percentile
	// calculation would silently believe. Acceptance criterion 1.
	h := open(t)
	for name, born := range map[string]string{
		"tomorrow":             time.Now().In(patient.Dhaka).AddDate(0, 0, 1).Format(time.DateOnly),
		"a year typed as 1093": "1093-04-12",
	} {
		_, err := h.SQL.Exec(`
			INSERT INTO core.patient (facility_id, clinical_id, name_en, sex, birth_date,
			                          dob_verified_by, phone_primary)
			VALUES ($1, $2, 'Impossible', 'female', $3, 'patient_stated', '+8801712345678')`,
			h.facility, "X-"+name, born)
		if err == nil {
			t.Errorf("a birth date %s was accepted", name)
			continue
		}
		if !strings.Contains(err.Error(), "birth_date") {
			t.Errorf("%s was refused for the wrong reason: %v", name, err)
		}
	}

	// And the precision travels with the date, so nothing downstream has to guess.
	var precision string
	created, err := h.store.Create(ctx(t), h.registration("Rahima Begum", registeredAt))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.SQL.QueryRow(
		`SELECT dob_precision FROM core.patient WHERE id = $1`, created.ID).Scan(&precision); err != nil {
		t.Fatal(err)
	}
	if precision != string(patient.PrecisionDay) {
		t.Errorf("dob_precision = %q", precision)
	}
}

func TestAYearPrecisionBirthDateIsStorable(t *testing.T) {
	// A patient who knows only their birth year is common, and the honest record is
	// 1 January with the precision beside it — not a refusal, and not a made-up day that
	// a percentile calculation treats as exact.
	h := open(t)
	in := h.registration("Abdul Karim", registeredAt)
	in.Patient.Birth = patient.BirthDate{
		Date:      time.Date(1958, 1, 1, 0, 0, 0, 0, patient.Dhaka),
		Precision: patient.PrecisionYear,
		Source:    patient.SourcePatientStated,
	}
	created, err := h.store.Create(ctx(t), in)
	if err != nil {
		t.Fatal(err)
	}
	if created.Birth.Precision != patient.PrecisionYear || created.Birth.Source != patient.SourcePatientStated {
		t.Errorf("the qualification was lost: %+v", created.Birth)
	}
}

// --- identifiers ---

func TestAnIdentifierBelongsToOnePatient(t *testing.T) {
	// §3 Step 1's "strict duplicate-record prevention" as a property of the database
	// rather than of a check somebody remembered to run.
	h := open(t)
	const nid = "1990 1234 5678"

	first, err := h.store.Create(ctx(t), h.withNationalID(t, h.registration("Rahima Begum", registeredAt), nid))
	if err != nil {
		t.Fatal(err)
	}

	_, err = h.store.Create(ctx(t), h.withNationalID(t, h.registration("Rahima Begum Again", registeredAt), "1990-1234-5678"))
	if err == nil {
		t.Fatal("the same national ID was registered twice")
	}
	if !isDuplicate(err) {
		t.Fatalf("the refusal was not recognisable: %v", err)
	}

	// And the desk can be told politely first, which is what the duplicate check is for.
	found, err := h.store.ByIdentifier(ctx(t), h.facility, patient.NationalID,
		h.sealer.Digest(patient.NationalID, nid))
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != first.ID {
		t.Errorf("the duplicate check found %s, want %s", found.ID, first.ID)
	}
}

func TestAnIdentifierIsNeverStoredReadable(t *testing.T) {
	// D-47. The failure this catches is the quiet one: the schema says `sealed`, the dump
	// says 1990123456789, and every layer assumes another layer handled it.
	h := open(t)
	if _, err := h.store.Create(ctx(t),
		h.withNationalID(t, h.registration("Rahima Begum", registeredAt), "1990123456789")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`SELECT core.assert_no_plaintext_identifiers()`); err != nil {
		t.Fatalf("a properly sealed identifier failed the invariant: %v", err)
	}

	// And the invariant is not vacuous: plant what a careless import would write.
	var patientID uuid.UUID
	if err := h.SQL.QueryRow(`SELECT id FROM core.patient LIMIT 1`).Scan(&patientID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO core.patient_identifier (facility_id, patient_id, kind, digest, sealed, key_id, masked)
		VALUES ($1, $2, 'passport', repeat('a', 32)::bytea, '1990123456789'::bytea, 'k1', '**** 6789')`,
		h.facility, patientID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`SELECT core.assert_no_plaintext_identifiers()`); err == nil {
		t.Error("a readable number in `sealed` passed the invariant")
	}
}

// --- the research boundary ---

func TestTheProductionRoleCanRegisterAPatient(t *testing.T) {
	// Everything else in this file connects as the owner, which can do anything. This one
	// runs the real registration transaction as `dthcms_app`, because a missing GRANT is
	// invisible to a suite that never changes roles and then takes the whole registration
	// desk down on the first deployment.
	h := open(t)
	pool := h.poolAs(t, "dthcms_app_local")
	store := patient.NewStore(pool)

	created, err := store.Create(ctx(t), h.withNationalID(t, h.registration("Rahima Begum", registeredAt), "1990 1234 5678"))
	if err != nil {
		t.Fatalf("the application role cannot register a patient: %v", err)
	}
	if created.ClinicalID != h.code+"-2026-000001" {
		t.Errorf("clinical id = %q", created.ClinicalID)
	}
}

func TestTheApplicationMayWriteTheLinkAndMayNotReadIt(t *testing.T) {
	// §12. Going from a research finding back to a person is an IRB decision carried out
	// by the owner, not a query a handler can make — so it is a privilege, not a rule in
	// the code that a later handler could forget.
	h := open(t)
	pool := h.poolAs(t, "dthcms_app_local")
	if _, err := patient.NewStore(pool).Create(ctx(t), h.registration("Rahima Begum", registeredAt)); err != nil {
		t.Fatalf("the application wrote no link: %v", err)
	}

	app := h.OpenAs(t, "dthcms_app_local", "dthcms_local_only")
	for what, statement := range map[string]string{
		"read the link":    `SELECT research_id FROM identity_link.research_subject`,
		"rewrite the link": `UPDATE identity_link.research_subject SET research_id = 'RS-00000000000000000000000000'`,
		"erase the link":   `DELETE FROM identity_link.research_subject`,
	} {
		if _, err := app.Exec(statement); err == nil {
			t.Errorf("the application could %s", what)
		} else if !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("%s failed for the wrong reason: %v", what, err)
		}
	}
}

func TestAPatientHasExactlyOneResearchIdentity(t *testing.T) {
	// The link's primary key is the patient. A second research id for the same person
	// would put them in two cohorts, and a cohort analysis that double-counts is one whose
	// numbers nobody can reproduce.
	h := open(t)
	created, err := h.store.Create(ctx(t), h.registration("Rahima Begum", registeredAt))
	if err != nil {
		t.Fatal(err)
	}
	second, err := patient.NewResearchID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO research.research_subject (research_id, facility_code, enrolled_month, birth_year, sex)
		VALUES ($1, $2, '2026-09-01', 1979, 'female')`, second, h.code); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO identity_link.research_subject (patient_id, research_id, facility_id)
		VALUES ($1, $2, $3)`, created.ID, second, h.facility); err == nil {
		t.Error("a patient was given a second research identity")
	}
}

func TestResearchCannotReachAnythingIdentified(t *testing.T) {
	h := open(t)
	if _, err := h.store.Create(ctx(t), h.registration("Rahima Begum", registeredAt)); err != nil {
		t.Fatal(err)
	}

	research := h.OpenAs(t, "dthcms_research_local", "dthcms_local_only")

	// What it is for: the anonymised cohort. Since CP36 that is `research.cohort`, a view
	// filtered on live consent — and a freshly registered patient has consented to nothing,
	// so the honest answer is zero. Research being *opt-in* is the point (D-02): a subject
	// appears here when they agree and disappears when they withdraw.
	var subjects int
	if err := research.QueryRow(`SELECT count(*) FROM research.cohort`).Scan(&subjects); err != nil {
		t.Fatalf("research cannot read its own view: %v", err)
	}
	if subjects != 0 {
		t.Errorf("research sees %d subjects who have not consented", subjects)
	}

	// And the base table is out of reach entirely, so a researcher cannot get past the
	// filter by writing their own query.
	if _, err := research.Exec(`SELECT count(*) FROM research.research_subject`); err == nil {
		t.Error("research could read the subject table directly, filter and all")
	}

	// And nothing else. Anonymisation that depends on an analyst querying the right
	// schema is not anonymisation.
	for what, statement := range map[string]string{
		"the patients":    `SELECT name_en FROM core.patient`,
		"the identifiers": `SELECT masked FROM core.patient_identifier`,
		"the link":        `SELECT patient_id FROM identity_link.research_subject`,
		"the ledger":      `SELECT event_type FROM ledger.event`,
	} {
		if _, err := research.Exec(statement); err == nil {
			t.Errorf("research could read %s", what)
		} else if !strings.Contains(err.Error(), "permission denied") &&
			!strings.Contains(err.Error(), "does not exist") {
			t.Errorf("reading %s failed for the wrong reason: %v", what, err)
		}
	}

	// The invariants that say the same thing, and run at every start.
	for _, assertion := range []string{
		"core.assert_research_isolated()",
		"core.assert_research_link_sealed()",
	} {
		if _, err := h.SQL.Exec(`SELECT ` + assertion); err != nil {
			t.Errorf("%s: %v", assertion, err)
		}
	}
}

func TestTheAnonymisedRowCarriesNothingIdentifying(t *testing.T) {
	// The columns are the guarantee. A name, a phone number or an exact date of birth
	// appearing here later would be a re-identification path that no query has to be
	// careless to take.
	h := open(t)
	in := h.registration("Rahima Begum", registeredAt)
	if _, err := h.store.Create(ctx(t), in); err != nil {
		t.Fatal(err)
	}

	rows, err := h.SQL.Query(`
		SELECT column_name FROM information_schema.columns
		 WHERE table_schema = 'research' AND table_name = 'research_subject'`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	columns := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"name_en", "name_bn", "phone_primary", "phone_secondary", "birth_date",
		"patient_id", "clinical_id", "address_line", "upazila", "postcode",
		"emergency_name", "emergency_phone",
	} {
		if columns[forbidden] {
			t.Errorf("research.research_subject has a %s column", forbidden)
		}
	}
	// And it does carry what §12's cohorting needs (acceptance criterion 4).
	for _, needed := range []string{
		"education_level", "occupation_category", "income_band",
		"household_size", "residence_type", "medicine_payer", "birth_year", "sex",
	} {
		if !columns[needed] {
			t.Errorf("research.research_subject is missing %s; §12 cannot cohort without it", needed)
		}
	}

	// The birth *year*, not the date; the enrolment *month*, not the day. A registration
	// date to the day plus a birth year plus a sex narrows a small population further
	// than a cohort study needs.
	var birthYear int
	var enrolled time.Time
	if err := h.SQL.QueryRow(`
		SELECT birth_year, enrolled_month FROM research.research_subject WHERE research_id = $1`,
		in.ResearchID).Scan(&birthYear, &enrolled); err != nil {
		t.Fatal(err)
	}
	if birthYear != 1979 {
		t.Errorf("birth_year = %d", birthYear)
	}
	if enrolled.Day() != 1 || enrolled.Month() != time.September || enrolled.Year() != 2026 {
		t.Errorf("enrolled_month = %s; it should be the first of the month", enrolled.Format(time.DateOnly))
	}
}

func TestTheDatabaseRefusesAResearchIDThatIsNotOpaque(t *testing.T) {
	// Acceptance criterion 3, held at the column. A research id that carried the clinical
	// id would be a re-identification path visible to anyone who read one row.
	h := open(t)
	//
	// A column can judge shape and nothing else — `RS-000...001` is well-shaped, and no
	// regular expression can tell a counter from a coin toss. That the id is actually
	// random is the generator's promise, and it is asserted in
	// TestAResearchIDIsNotDerivedFromAnything.
	for name, id := range map[string]string{
		"a clinical id":      "DTHC-FRD-2026-000137",
		"the wrong prefix":   "PT-ABCDEFGHJKMNPQRSTVWXYZ234",
		"a forbidden letter": "RS-IBCDEFGHJKMNPQRSTVWXYZ234",
		"lower case":         "RS-abcdefghjkmnpqrstvwxyz234",
		"too short":          "RS-ABCDEFGHJKMNPQRSTVWXYZ23",
		"too long":           "RS-ABCDEFGHJKMNPQRSTVWXYZ23456",
	} {
		_, err := h.SQL.Exec(`
			INSERT INTO research.research_subject
				(research_id, facility_code, enrolled_month, birth_year, sex)
			VALUES ($1, 'DTHC-FRD', '2026-09-01', 1979, 'female')`, id)
		if err == nil {
			t.Errorf("%s (%q) was accepted as a research id", name, id)
		}
	}
	// And a generated one is accepted, so the check is not simply refusing everything.
	opaque, err := patient.NewResearchID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO research.research_subject
			(research_id, facility_code, enrolled_month, birth_year, sex)
		VALUES ($1, 'DTHC-FRD', '2026-09-01', 1979, 'female')`, opaque); err != nil {
		t.Fatalf("a generated research id was refused by its own column: %v", err)
	}
}

// --- reads ---

func TestAPatientIsInvisibleToAnotherFacility(t *testing.T) {
	// A 404 that distinguishes "no such patient" from "not yours" is a way to enumerate
	// patients one id at a time.
	h := open(t)
	created, err := h.store.Create(ctx(t), h.registration("Rahima Begum", registeredAt))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := h.store.ByID(ctx(t), created.ID, uuid.New()); err == nil {
		t.Fatal("another facility read the patient")
	} else if !isNotFound(err) {
		t.Errorf("the refusal was not ErrNotFound: %v", err)
	}
	if _, err := h.store.ByID(ctx(t), uuid.New(), h.facility); !isNotFound(err) {
		t.Errorf("an unknown id gave %v, want ErrNotFound", err)
	}

	found, err := h.store.ByClinicalID(ctx(t), created.ClinicalID)
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != created.ID {
		t.Errorf("ByClinicalID found %s", found.ID)
	}
}

// --- helpers ---

func isDuplicate(err error) bool { return errors.Is(err, patient.ErrDuplicateIdentifier) }
func isNotFound(err error) bool  { return errors.Is(err, patient.ErrNotFound) }
