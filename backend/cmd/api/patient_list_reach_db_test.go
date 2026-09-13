package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
)

// The two multi-patient lists, on the wire (CP85, ADR-0036 §1).
//
// # Why these two were refused, and why a guard was never the answer
//
// `GET /v1/patients` and `GET /v1/patients/today` return many patients. ADR-0036 defines
// reach as a relationship between a station and *one* patient, so there is no "the resource"
// for a handler to judge — and the route was therefore left on a plain requirement, which
// the guard refuses for the nine station roles. Safe, and the wrong answer for a clinic: the
// anthropometry officer who has to call the next patient in cannot find them.
//
// The fix is the same relationship applied as a `WHERE` clause. A patient this station has
// not had in the current visit is a patient this station cannot open individually, and is
// therefore a row that does not appear in the list.
//
// # The escalation this test is here for
//
// Two patients and two stations. The nutritionist has one of them on their queue and has
// never seen the other. The other must not appear — not in the search, not in the day list,
// not in the count. That is the whole of what a filtered list is for, and it is the one
// thing in CP85 that must not regress.
//
// The shape is deliberately the one a curious colleague would use: a real session at their
// own workstation, wearing a hat they really hold, typing a name into the search box. No
// forged header and no stolen token.

// listFixture registers a patient through the real route and returns their id and name.
func (s *registrationStack) listFixture(t *testing.T, token, role, surname string) (uuid.UUID, string) {
	t.Helper()
	form := registrationForm()
	name := "Listtest " + surname
	form["name_en"] = name
	form["name_bn"] = "তালিকা " + surname
	form["identifiers"] = map[string]string{}
	form["phone_primary"] = "0171" + fmt.Sprintf("%07d", len(surname)*1111111%9999999)
	status, body, raw := s.post(t, token, role, "/v1/patients", form)
	if status != http.StatusCreated {
		t.Fatalf("registering %s answered %d: %s", name, status, raw)
	}
	registered, _ := body["patient"].(map[string]any)
	id, err := uuid.Parse(fmt.Sprint(registered["id"]))
	if err != nil {
		t.Fatalf("the registration returned no usable id: %s", raw)
	}
	return id, name
}

// queueAt puts a registered patient on one station's queue, in an open visit.
func (s *registrationStack) queueAt(t *testing.T, patient uuid.UUID, station string) {
	t.Helper()
	// The allergy gate (CP54) refuses a queue entry past station 4 without a status. Not
	// what this test is about.
	if _, err := s.db.SQL.Exec(`
		INSERT INTO read.allergy_assertion
		  (id, event_id, facility_id, patient_id, kind, asserted_at, asserted_by, global_seq)
		VALUES (gen_random_uuid(), gen_random_uuid(), $1, $2, 'NO_KNOWN_ALLERGY', now(), $3,
		        (SELECT coalesce(max(global_seq), 0) + 1 FROM read.allergy_assertion))`,
		s.facility, patient, s.user); err != nil {
		t.Fatalf("clearing the allergy gate: %v", err)
	}
	var visit uuid.UUID
	if err := s.db.SQL.QueryRow(`
		INSERT INTO core.visit (facility_id, patient_id, visit_code, visit_type, clinic_day, opened_by)
		VALUES ($1, $2, $3, 'new', current_date, $4) RETURNING id`,
		s.facility, patient, "V-LIST-"+uuid.NewString()[:8], s.user).Scan(&visit); err != nil {
		t.Fatalf("opening the visit: %v", err)
	}
	if _, err := s.db.SQL.Exec(`
		INSERT INTO core.queue_entry
		  (facility_id, visit_id, patient_id, station_code, status, clinic_day, called_at, called_by)
		VALUES ($1, $2, $3, $4, 'in_service', current_date, now(), $5)`,
		s.facility, visit, patient, station, s.user); err != nil {
		t.Fatalf("queueing at %s: %v", station, err)
	}
}

// patientIDsIn pulls the ids out of a `{"patients": [...]}` body.
func patientIDsIn(t *testing.T, body string) map[string]bool {
	t.Helper()
	var decoded struct {
		Patients []struct {
			PatientID string `json:"patient_id"`
		} `json:"patients"`
	}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("the list did not decode: %v\n%s", err, body)
	}
	out := map[string]bool{}
	for _, p := range decoded.Patients {
		out[p.PatientID] = true
	}
	return out
}

func totalIn(t *testing.T, body string) float64 {
	t.Helper()
	var decoded struct {
		Total float64 `json:"total"`
	}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("the day list did not decode: %v\n%s", err, body)
	}
	return decoded.Total
}

// The escalation test that matters.
func TestTheListsDoNotShowAPatientTheStationHasNotHad(t *testing.T) {
	s := newRegistrationStack(t)

	// Two patients, registered for real, one on each of two stations' queues.
	s.seedClerk(t, "REG-CP85L", auth.RoleRegistration)
	desk := s.enrol(t, "Registration desk 85", "STN_REGISTRATION")
	regToken := s.signIn(t, "REG-CP85L", desk)
	mine, mineName := s.listFixture(t, regToken, string(auth.RoleRegistration), "Mine")
	theirs, theirsName := s.listFixture(t, regToken, string(auth.RoleRegistration), "Theirs")
	s.queueAt(t, mine, string(auth.StationNutrition))
	s.queueAt(t, theirs, string(auth.StationAnthropometry))

	// The nutritionist, at their own desk, wearing a hat they hold.
	s.seedClerk(t, "NUT-CP85L", auth.RoleNutritionist)
	nutritionDesk := s.enrol(t, "Nutrition desk 85", "STN_NUTRITION")
	token := s.signIn(t, "NUT-CP85L", nutritionDesk)
	hat := string(auth.RoleNutritionist)

	// Their own patient is findable.
	status, body := s.get(t, token, hat, "/v1/patients?q="+url.QueryEscape(mineName))
	if status != http.StatusOK {
		t.Fatalf("the nutritionist cannot search at all: %d %s\n"+
			"A 403 here means the list is back on a guard, and the station cannot find the "+
			"patient standing in front of it.", status, body)
	}
	if !patientIDsIn(t, body)[mine.String()] {
		t.Fatalf("the nutritionist cannot find the patient on their own queue: %s", body)
	}

	// The patient at the next desk is not.
	status, body = s.get(t, token, hat, "/v1/patients?q="+url.QueryEscape(theirsName))
	if status != http.StatusOK {
		t.Fatalf("searching for somebody else's patient answered %d, not 200: %s\n\n"+
			"A filtered list must not change its status code. A 403 on one name and a 200 on "+
			"another is an existence oracle over the whole register — worse than the leak it "+
			"is trying to prevent, because it answers for names that are not in the clinic at "+
			"all.", status, body)
	}
	if patientIDsIn(t, body)[theirs.String()] {
		t.Fatalf("the nutritionist found a patient queued to anthropometry: %s\n\n"+
			"This is the escalation CP85 exists to prevent. `GET /v1/patients/{id}` refuses "+
			"this patient for this role; a search that returns them makes that refusal "+
			"decorative, because the name, the clinical id, the district and the age are the "+
			"row.", body)
	}

	// The day list, the same way — and its `total` must be the count of what was shown,
	// not of what exists. A count is a number of withheld rows said out loud.
	status, body = s.get(t, token, hat, "/v1/patients/today")
	if status != http.StatusOK {
		t.Fatalf("the day list answered %d: %s", status, body)
	}
	shown := patientIDsIn(t, body)
	if !shown[mine.String()] {
		t.Errorf("the day list omits the patient on this station's queue: %s", body)
	}
	if shown[theirs.String()] {
		t.Fatalf("the day list shows a patient queued to anthropometry: %s", body)
	}
	if total := totalIn(t, body); int(total) != len(shown) {
		t.Errorf("the day list says total %v and shows %d rows; the difference is the number "+
			"of patients this station was not allowed to see, reported to them as a number",
			total, len(shown))
	}

	// And the search does not leak through the other two handles either. A clinical id and a
	// phone number are separate statements in the store, and a restriction applied to the
	// name route alone would be a restriction somebody could type their way around.
	var clinicalID, phone string
	if err := s.db.SQL.QueryRow(`SELECT clinical_id, phone_primary FROM read.patient WHERE patient_id = $1`,
		theirs).Scan(&clinicalID, &phone); err != nil {
		t.Fatal(err)
	}
	for _, handle := range []string{clinicalID, phone} {
		status, body = s.get(t, token, hat, "/v1/patients?q="+url.QueryEscape(handle))
		if status != http.StatusOK {
			t.Fatalf("searching by an exact handle answered %d: %s", status, body)
		}
		if patientIDsIn(t, body)[theirs.String()] {
			t.Errorf("the exact-handle route returned a patient this station has not had "+
				"(searched %q): %s\n\nThe name route is not the only way in. CP31 routes a "+
				"clinical id and a phone number to their own statements, and each of them "+
				"needs the same predicate.", handle, body)
		}
	}
}

// The other half: a facility-wide role still sees the whole register.
//
// Without this, the test above passes on a change that simply broke the lists for everybody
// — which is a thing that has happened in this codebase and is why every restriction in
// CP83's family is paired with the read it must not break.
func TestAFacilityWideRoleStillSeesEveryPatientInBothLists(t *testing.T) {
	s := newRegistrationStack(t)

	s.seedClerk(t, "REG-CP85W", auth.RoleRegistration)
	desk := s.enrol(t, "Registration desk 86", "STN_REGISTRATION")
	regToken := s.signIn(t, "REG-CP85W", desk)
	first, firstName := s.listFixture(t, regToken, string(auth.RoleRegistration), "Widefirst")
	second, secondName := s.listFixture(t, regToken, string(auth.RoleRegistration), "Widesecond")
	// One of them is on a queue and the other has never been routed anywhere. Both must
	// appear: the facility-wide roles do not run the predicate at all, and a patient
	// registered but not yet routed is precisely the case ADR-0036 warns will feel wrong.
	s.queueAt(t, first, string(auth.StationNutrition))

	s.seedClerk(t, "DOC-CP85W", auth.RolePhysician)
	room := s.enrol(t, "Consulting room 86", "STN_CONSULTATION")
	token := s.signIn(t, "DOC-CP85W", room)
	hat := string(auth.RolePhysician)

	for name, want := range map[string]uuid.UUID{firstName: first, secondName: second} {
		status, body := s.get(t, token, hat, "/v1/patients?q="+url.QueryEscape(name))
		if status != http.StatusOK {
			t.Fatalf("the physician's search answered %d: %s", status, body)
		}
		if !patientIDsIn(t, body)[want.String()] {
			t.Errorf("the physician cannot find %s: %s\n\n"+
				"`patient.read.demographics` reaches the whole facility for this role, so the "+
				"restriction must not be applied to it at all.", name, body)
		}
	}

	status, body := s.get(t, token, hat, "/v1/patients/today")
	if status != http.StatusOK {
		t.Fatalf("the physician's day list answered %d: %s", status, body)
	}
	shown := patientIDsIn(t, body)
	for name, want := range map[string]uuid.UUID{firstName: first, secondName: second} {
		if !shown[want.String()] {
			t.Errorf("the physician's day list omits %s: %s", name, body)
		}
	}
}
