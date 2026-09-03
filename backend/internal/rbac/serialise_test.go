package rbac_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
)

// The serialiser layer, and CP20's golden tests: the exact bytes a pharmacist and a
// registration officer receive, with no diagnosis key in them.
//
// The prescription and patient types below stand in for the ones CP84 and CP29 will
// define; they are shaped the way those will be — drugs and dosing beside a diagnosis
// and a clinical note — so that the redaction is proven on the shape before the real
// endpoint exists, and the real endpoint inherits a test rather than writes one.

var update = flag.Bool("update", false, "rewrite the golden files")

type drugLine struct {
	Name      string `json:"name"`
	Dose      string `json:"dose"`
	Frequency string `json:"frequency"`
	Days      int    `json:"days"`
}

type patientLine struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	AgeYears int       `json:"age_years"`
	// A patient's clinical summary: what registration must not see.
	ClinicalSummary string   `json:"clinical_summary" visible:"patient.read.clinical"`
	Allergies       []string `json:"allergies" visible:"patient.read.allergies"`
}

type prescriptionView struct {
	ID           uuid.UUID   `json:"id"`
	Patient      patientLine `json:"patient"`
	Drugs        []drugLine  `json:"drugs"`
	Diagnosis    string      `json:"diagnosis" visible:"diagnosis.read"`
	ICDCodes     []string    `json:"icd_codes" visible:"diagnosis.read"`
	ClinicalNote string      `json:"clinical_note" visible:"patient.read.clinical"`
	SignedBy     string      `json:"signed_by"`
	SignedAt     time.Time   `json:"signed_at"`
	// Nested and repeated: the AI's reasoning travels with each suggestion.
	Suggestions []suggestion `json:"suggestions"`
}

type suggestion struct {
	Drug      string `json:"drug"`
	Synthesis string `json:"synthesis" visible:"ai.synthesis.read"`
}

func sample() prescriptionView {
	return prescriptionView{
		ID: uuid.MustParse("0190a8f2-0000-7000-8000-0000000000e1"),
		Patient: patientLine{
			ID: uuid.MustParse("0190a8f2-0000-7000-8000-0000000000e2"), Name: "Rahima Khatun", AgeYears: 54,
			ClinicalSummary: "T2DM with diabetic nephropathy, eGFR 48", Allergies: []string{"sulfonamides"},
		},
		Drugs: []drugLine{
			{Name: "Metformin", Dose: "500 mg", Frequency: "twice daily", Days: 30},
			{Name: "Linagliptin", Dose: "5 mg", Frequency: "once daily", Days: 30},
		},
		Diagnosis:    "Type 2 diabetes mellitus with nephropathy",
		ICDCodes:     []string{"E11.21"},
		ClinicalNote: "Avoid metformin escalation while eGFR < 45.",
		SignedBy:     "Dr K. M. Nahid-Ul-Haque",
		SignedAt:     time.Date(2026, 9, 3, 11, 30, 0, 0, time.UTC),
		Suggestions: []suggestion{
			{Drug: "Linagliptin", Synthesis: "Renal-safe DPP-4 inhibitor given eGFR trend."},
		},
	}
}

func wearingOnly(role auth.RoleCode) rbac.Subject {
	return rbac.Subject{
		UserID: me, FacilityID: facility, Roles: []auth.RoleCode{role}, ActiveRole: role,
		Permissions: rbac.UnionFor([]auth.RoleCode{role}),
	}
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, got, "", "  "); err != nil {
		t.Fatal(err)
	}
	pretty.WriteByte('\n')
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, pretty.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v (run with -update to create it)", path, err)
	}
	if !bytes.Equal(want, pretty.Bytes()) {
		t.Fatalf("%s differs from the golden file.\n got:\n%s\nwant:\n%s", name, pretty.String(), want)
	}
}

func TestPharmacistPrescriptionHasNoDiagnosisKey(t *testing.T) {
	// Acceptance criterion 2, half one: the exact JSON a pharmacist receives.
	got, err := rbac.Marshal(wearingOnly(auth.RolePharmacist), sample())
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"diagnosis"`, `"icd_codes"`, `"clinical_note"`, `"clinical_summary"`, `"synthesis"`, "nephropathy", "E11.21"} {
		if strings.Contains(string(got), forbidden) {
			t.Errorf("pharmacist payload contains %s:\n%s", forbidden, got)
		}
	}
	for _, kept := range []string{`"drugs"`, "Metformin", "500 mg", `"allergies"`, "sulfonamides", `"signed_by"`} {
		if !strings.Contains(string(got), kept) {
			t.Errorf("pharmacist payload lacks %s:\n%s", kept, got)
		}
	}
	golden(t, "pharmacist_prescription.golden.json", got)
}

func TestRegistrationPatientHasNoClinicalKey(t *testing.T) {
	// Acceptance criterion 2, half two: what registration receives of a patient.
	got, err := rbac.Marshal(wearingOnly(auth.RoleRegistration), sample().Patient)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"clinical_summary"`, "nephropathy", `"allergies"`} {
		if strings.Contains(string(got), forbidden) {
			t.Errorf("registration payload contains %s:\n%s", forbidden, got)
		}
	}
	golden(t, "registration_patient.golden.json", got)
}

func TestPhysicianSeesEverything(t *testing.T) {
	got, err := rbac.Marshal(wearingOnly(auth.RolePhysician), sample())
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := json.Marshal(sample())
	if !bytes.Equal(got, plain) {
		t.Fatalf("the physician's payload must be the unredacted one:\n%s\n%s", got, plain)
	}
	golden(t, "physician_prescription.golden.json", got)
}

func TestRedactionFollowsTheHat(t *testing.T) {
	// A physician wearing the pharmacist's hat sees what a pharmacist sees.
	s := rbac.Subject{
		UserID: me, FacilityID: facility,
		Roles:       []auth.RoleCode{auth.RolePhysician, auth.RolePharmacist},
		ActiveRole:  auth.RolePharmacist,
		Permissions: rbac.UnionFor([]auth.RoleCode{auth.RolePhysician, auth.RolePharmacist}),
	}
	got, err := rbac.Marshal(s, sample())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), `"diagnosis"`) {
		t.Fatalf("the pharmacist hat must hide the diagnosis whoever wears it:\n%s", got)
	}
	// With no hat, the pharmacist's rule still binds the person — choose a hat.
	s.ActiveRole = ""
	got, err = rbac.Marshal(s, sample())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), `"diagnosis"`) {
		t.Fatalf("no hat: every rule applies:\n%s", got)
	}
}

func TestRedactionReachesNestedAndRepeatedFields(t *testing.T) {
	type wrapper struct {
		Items  []prescriptionView          `json:"items"`
		ByID   map[string]prescriptionView `json:"by_id"`
		Latest *prescriptionView           `json:"latest"`
	}
	v := wrapper{Items: []prescriptionView{sample(), sample()}, ByID: map[string]prescriptionView{"a": sample()}}
	latest := sample()
	v.Latest = &latest
	got, err := rbac.Marshal(wearingOnly(auth.RoleRxEducator), v)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), `"diagnosis"`) || strings.Contains(string(got), `"synthesis"`) {
		t.Fatalf("a nested or repeated diagnosis leaked:\n%s", got)
	}
	if strings.Count(string(got), `"drugs"`) != 4 {
		t.Fatalf("the four prescriptions must all be present:\n%s", got)
	}
}

func TestDefaultRestrictive(t *testing.T) {
	// A field that looks clinical and says nothing about who may see it is refused
	// outright — the type cannot be served at all.
	type careless struct {
		ID        string `json:"id"`
		Diagnosis string `json:"diagnosis"`
	}
	if _, err := rbac.Marshal(wearingOnly(auth.RolePhysician), careless{}); err == nil ||
		!strings.Contains(err.Error(), "looks clinical") {
		t.Fatalf("an undeclared clinical field must be refused; got %v", err)
	}
	type misspelt struct {
		Diagnosis string `json:"diagnosis" visible:"diagnosis.reed"`
	}
	if _, err := rbac.Marshal(wearingOnly(auth.RolePhysician), misspelt{}); err == nil ||
		!strings.Contains(err.Error(), "not a permission") {
		t.Fatalf("an unknown permission in a tag must be refused; got %v", err)
	}
	type opaque struct {
		Payload any `json:"payload"`
	}
	if _, err := rbac.Marshal(wearingOnly(auth.RolePhysician), opaque{}); err == nil ||
		!strings.Contains(err.Error(), "interface") {
		t.Fatalf("an interface-typed field must be refused; got %v", err)
	}
	// And a type with nothing to guard is passed through untouched, at no cost.
	type plain struct {
		Name string `json:"name"`
	}
	got, err := rbac.Marshal(rbac.Subject{}, plain{Name: "x"})
	if err != nil || string(got) != `{"name":"x"}` {
		t.Fatalf("plain: %s %v", got, err)
	}
}
