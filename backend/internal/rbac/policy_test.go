package rbac_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
)

var (
	facility = uuid.MustParse("0190a8f2-0000-7000-8000-000000000001")
	other    = uuid.MustParse("0190a8f2-0000-7000-8000-000000000002")
	station  = uuid.MustParse("0190a8f2-0000-7000-8000-0000000000a1")
	station2 = uuid.MustParse("0190a8f2-0000-7000-8000-0000000000a2")
	me       = uuid.MustParse("0190a8f2-0000-7000-8000-0000000000b1")
	someone  = uuid.MustParse("0190a8f2-0000-7000-8000-0000000000b2")
)

func subject(roles ...auth.RoleCode) rbac.Subject {
	return rbac.Subject{
		UserID: me, FacilityID: facility, Roles: roles,
		StationID: &station, Permissions: rbac.UnionFor(roles),
	}
}

func wearing(role auth.RoleCode, roles ...auth.RoleCode) rbac.Subject {
	s := subject(append([]auth.RoleCode{role}, roles...)...)
	s.ActiveRole = role
	return s
}

// atMyStation is a resource in reach of every scope: same facility, same station, mine.
func atMyStation() rbac.Resource {
	return rbac.Resource{Kind: "patient", FacilityID: facility, StationID: &station, OwnerID: &me}
}

// --- the acceptance criteria, by name ---

func TestNutritionistIsDeniedPrescriptionRead(t *testing.T) {
	d := rbac.Can(wearing(auth.RoleNutritionist), auth.PermPrescriptionRead, atMyStation())
	if d.Allowed {
		t.Fatal("criterion 1: a nutritionist must not read prescriptions")
	}
	// And even if some future migration handed the role the permission, the explicit rule
	// still says no — explicit deny beats allow.
	s := wearing(auth.RoleNutritionist)
	s.Permissions.Union(auth.NewPermissionSet(auth.PermPrescriptionRead))
	s.ActiveRole = ""
	d = rbac.Can(s, auth.PermPrescriptionRead, atMyStation())
	if d.Allowed || d.Rule != "nutritionist_no_prescriptions" {
		t.Fatalf("explicit deny must beat a held permission: %+v", d)
	}
}

func TestPharmacistIsDeniedDiagnosisRead(t *testing.T) {
	d := rbac.Can(wearing(auth.RolePharmacist), auth.PermDiagnosisRead, atMyStation())
	if d.Allowed {
		t.Fatal("criterion 2: a pharmacist must not read diagnoses")
	}
	// A prescription the pharmacist may read, but one that carries a diagnosis is refused
	// as a resource — the serialiser-level blinding CP20 enforces has a policy behind it.
	sensitive := atMyStation()
	sensitive.Kind, sensitive.Sensitive = "prescription", true
	d = rbac.Can(wearing(auth.RolePharmacist), auth.PermPrescriptionRead, sensitive)
	if d.Allowed || d.Reason != rbac.ReasonBlinded {
		t.Fatalf("a sensitive resource must be refused to a blinded role: %+v", d)
	}
	plain := atMyStation()
	plain.Kind = "prescription"
	if d := rbac.Can(wearing(auth.RolePharmacist), auth.PermPrescriptionRead, plain); !d.Allowed {
		t.Fatalf("a redacted prescription must be readable: %s", d.Explain(auth.PermPrescriptionRead))
	}
}

func TestRegistrationIsDeniedSensitiveDiagnosisRead(t *testing.T) {
	for _, action := range []string{auth.PermDiagnosisRead, auth.PermPatientReadClinical, auth.PermRecordsRead} {
		if d := rbac.Can(wearing(auth.RoleRegistration), action, atMyStation()); d.Allowed {
			t.Fatalf("criterion 3: registration must not %s", action)
		}
	}
	sensitive := atMyStation()
	sensitive.Sensitive = true
	d := rbac.Can(wearing(auth.RoleRegistration), auth.PermPatientReadDemographics, sensitive)
	if d.Allowed || d.Reason != rbac.ReasonBlinded {
		t.Fatalf("registration reading a record that carries a diagnosis: %+v", d)
	}
}

func TestUnknownActionIsDenied(t *testing.T) {
	for _, action := range []string{"", "patient.delete", "prescription.read ", "PRESCRIPTION.READ", "*"} {
		d := rbac.Can(wearing(auth.RolePhysician), action, atMyStation())
		if d.Allowed || d.Reason != rbac.ReasonUnknownAction {
			t.Fatalf("criterion 4: %q must be denied as unknown: %+v", action, d)
		}
	}
}

// --- deny by default, and the shape of a decision ---

func TestDenyByDefault(t *testing.T) {
	// No subject at all.
	if d := rbac.Can(rbac.Subject{}, auth.PermPatientReadDemographics, atMyStation()); d.Allowed || d.Reason != rbac.ReasonNoSubject {
		t.Fatalf("no subject: %+v", d)
	}
	// A subject with no roles.
	if d := rbac.Can(subject(), auth.PermPatientReadDemographics, atMyStation()); d.Allowed || d.Reason != rbac.ReasonPermissionNotHeld {
		t.Fatalf("no roles: %+v", d)
	}
	// A hat the person does not own.
	s := subject(auth.RoleAnthropometry)
	s.ActiveRole = auth.RolePhysician
	if d := rbac.Can(s, auth.PermPatientReadDemographics, atMyStation()); d.Allowed || d.Reason != rbac.ReasonRoleNotHeld {
		t.Fatalf("active role not held: %+v", d)
	}
	// Another facility.
	elsewhere := atMyStation()
	elsewhere.FacilityID = other
	if d := rbac.Can(wearing(auth.RolePhysician), auth.PermPatientReadDemographics, elsewhere); d.Allowed || d.Reason != rbac.ReasonOtherFacility {
		t.Fatalf("other facility: %+v", d)
	}
}

func TestActiveRoleNarrowsToThatHat(t *testing.T) {
	// A physician covering anthropometry [R-02]. Wearing the anthropometry hat, the
	// physician's reach is not carried along: no diagnosis, and only this station.
	s := wearing(auth.RoleAnthropometry, auth.RolePhysician)
	if d := rbac.Can(s, auth.PermDiagnosisRead, atMyStation()); d.Allowed {
		t.Fatal("the anthropometry hat must not read diagnoses, whoever wears it")
	}
	away := atMyStation()
	away.StationID = &station2
	if d := rbac.Can(s, auth.PermObservationWriteAnthro, away); d.Allowed || d.Reason != rbac.ReasonOutOfScope {
		t.Fatalf("station scope must bind the hat: %+v", d)
	}
	// Wearing the physician hat, both are allowed, anywhere in the clinic.
	s.ActiveRole = auth.RolePhysician
	if d := rbac.Can(s, auth.PermDiagnosisRead, away); !d.Allowed || d.Scope != rbac.ScopeAny {
		t.Fatalf("the physician hat: %+v", d)
	}
}

func TestNoHatMeansEveryRuleApplies(t *testing.T) {
	// Holding physician and nutritionist with no hat chosen: the physician's permissions
	// are held, but the nutritionist's rule still binds — choose a hat.
	s := subject(auth.RolePhysician, auth.RoleNutritionist)
	d := rbac.Can(s, auth.PermPrescriptionRead, atMyStation())
	if d.Allowed || d.Rule != "nutritionist_no_prescriptions" {
		t.Fatalf("no hat, conflicting roles: %+v", d)
	}
	// Whereas physician and QA together, no hat, read a diagnosis with the widest scope.
	s = subject(auth.RolePhysician, auth.RoleQa)
	if d := rbac.Can(s, auth.PermDiagnosisRead, atMyStation()); !d.Allowed || d.Scope != rbac.ScopeAny {
		t.Fatalf("compatible roles, no hat: %+v", d)
	}
}

func TestScopes(t *testing.T) {
	// Station role: only at its station.
	mine, theirs := atMyStation(), atMyStation()
	theirs.StationID = &station2
	if d := rbac.Can(wearing(auth.RoleCounselor), auth.PermCounselingTick, mine); !d.Allowed || d.Scope != rbac.ScopeOwnStation {
		t.Fatalf("counselor at own station: %+v", d)
	}
	if d := rbac.Can(wearing(auth.RoleCounselor), auth.PermCounselingTick, theirs); d.Allowed {
		t.Fatal("counselor at another station must be out of scope")
	}
	nowhere := atMyStation()
	nowhere.StationID = nil
	if d := rbac.Can(wearing(auth.RoleCounselor), auth.PermPatientReadDemographics, nowhere); d.Allowed {
		t.Fatal("a resource at no station is out of a station role's reach")
	}
	// Field worker: only their own captures.
	own, others := atMyStation(), atMyStation()
	others.OwnerID = &someone
	if d := rbac.Can(wearing(auth.RoleFieldWorker), auth.PermObservationWriteAnthro, own); !d.Allowed || d.Scope != rbac.ScopeOwn {
		t.Fatalf("field worker, own capture: %+v", d)
	}
	if d := rbac.Can(wearing(auth.RoleFieldWorker), auth.PermObservationWriteAnthro, others); d.Allowed {
		t.Fatal("field worker must not touch another's capture")
	}
	// Administrative actions have no station; the admin reaches them from anywhere.
	admin := wearing(auth.RoleAdmin)
	admin.StationID = nil
	if d := rbac.Can(admin, auth.PermDeviceRevoke, rbac.Resource{Kind: "device", FacilityID: facility}); !d.Allowed {
		t.Fatalf("admin revoking a device: %+v", d)
	}
}

func TestExplainNamesTheRule(t *testing.T) {
	d := rbac.Can(wearing(auth.RolePharmacist), auth.PermDiagnosisRead, atMyStation())
	got := d.Explain(auth.PermDiagnosisRead)
	for _, want := range []string{"denied diagnosis.read", "explicit_deny", "pharmacist_no_diagnoses", "§4.4"} {
		if !strings.Contains(got, want) {
			t.Errorf("explanation %q lacks %q", got, want)
		}
	}
	if got := rbac.Can(wearing(auth.RolePhysician), auth.PermDiagnosisRead, atMyStation()).Explain(auth.PermDiagnosisRead); got != "allowed diagnosis.read (scope any)" {
		t.Errorf("allow explanation = %q", got)
	}
}

// --- the decision matrix ---

// TestDecisionMatrix is the living specification: every role × every permission, against
// the table derived from blueprint §4.4 and the CP15 catalogue. A cell that changes —
// because a migration handed a role a permission, or a rule was edited — fails here and
// has to be explained in the matrix document (docs/access-matrix.md), which is generated
// from the same engine and checked for drift by TestMatrixDocumentIsCurrent.
func TestDecisionMatrix(t *testing.T) {
	for _, role := range auth.AllRoles {
		for _, action := range auth.AllPermissions {
			want := rbac.RolePermissions[role].Has(action)
			// The three named constraints, stated independently of the catalogue so that
			// the test does not merely agree with itself.
			switch {
			case role == auth.RoleNutritionist && strings.HasPrefix(action, "prescription."):
				want = false
			case role == auth.RolePharmacist && (strings.HasPrefix(action, "diagnosis.") || isSensitive(action)):
				want = false
			case role == auth.RoleRegistration && isSensitive(action):
				want = false
			}
			got := rbac.Can(wearing(role), action, atMyStation())
			if got.Allowed != want {
				t.Errorf("%s × %s: got %v (%s), want %v", role, action, got.Allowed, got.Reason, want)
			}
		}
	}
}

func isSensitive(action string) bool {
	for _, s := range auth.SensitivePermissions {
		if s == action {
			return true
		}
	}
	return false
}

func TestMatrixDocumentIsCurrent(t *testing.T) {
	// The document is what Dr. Nahid reads against the blueprint (CP19's manual
	// verification). It must be the engine's output, not a hand-maintained table.
	want := rbac.RenderMatrix()
	got, err := readDoc("access-matrix.md")
	if err != nil {
		t.Fatalf("reading docs/access-matrix.md: %v", err)
	}
	if got != want {
		t.Fatalf("docs/access-matrix.md is out of date. Regenerate it:\n\n  go run ./tools/accessmatrix > ../docs/access-matrix.md\n")
	}
}

func ExampleCan() {
	s := rbac.Subject{
		UserID: me, FacilityID: facility,
		Roles: []auth.RoleCode{auth.RolePharmacist}, ActiveRole: auth.RolePharmacist,
		Permissions: rbac.UnionFor([]auth.RoleCode{auth.RolePharmacist}),
	}
	d := rbac.Can(s, auth.PermDiagnosisRead, rbac.Resource{Kind: "diagnosis", FacilityID: facility})
	fmt.Println(d.Explain(auth.PermDiagnosisRead))
	// Output: denied diagnosis.read: blueprint §4.4: the pharmacist sees drugs and dosing only; diagnoses are hidden [explicit_deny, rule pharmacist_no_diagnoses]
}
