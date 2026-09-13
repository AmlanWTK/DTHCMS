package rbac_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
)

// The two questions, and the layer each belongs to (CP83).
//
// The defect these tests exist against: the route guard asked Can — a question about a
// resource — with a resource that was not one. Every permission whose reach is narrower
// than the facility was therefore refused at every route that declared it, which is every
// clinical write in the clinic, and the refusal said "out_of_scope" so the search went to
// the scope table rather than to the caller.

// The regression, at the smallest scale it can be shown: the two roles that hold
// `patient.write.demographics` used to be refused at the door, because both were scoped
// narrower than the facility and the guard measured that scope against no resource.
//
// ADR-0036 §2 then corrected one of the two. REGISTRATION is facility-wide for this
// permission — a registration creates the patient, so there is no patient to be at a
// station — and FIELD_WORKER still reaches only its own captures. So the assertion here is
// not "both defer" but "both are admitted, and each reports honestly whether anything is
// still owed": that is the property the defect broke, and it is the one that has to hold
// through a scope change rather than being restated by one.
func TestReachesAdmitsTheOnlyRolesThatRegisterPatients(t *testing.T) {
	deferred := map[auth.RoleCode]rbac.Scope{
		auth.RoleRegistration: "",            // facility-wide: the decision is complete at the door
		auth.RoleFieldWorker:  rbac.ScopeOwn, // their own captures: a resource is still owed
	}
	for role, want := range deferred {
		d := rbac.Reaches(wearing(role), auth.PermPatientWriteDemographics, facility)
		if !d.Allowed {
			t.Fatalf("%s may not reach POST /v1/patients: %s", role, d.Explain(auth.PermPatientWriteDemographics))
		}
		if d.Deferred != want {
			t.Errorf("%s deferred %q, want %q; a debt reported wrongly in either direction is a "+
				"resource check that either nobody makes or nobody can satisfy", role, d.Deferred, want)
		}
	}
}

// A route that would have been an outright allow owes nothing, and a caller that reads
// Deferred must be able to tell the two apart.
func TestReachesDefersNothingForAFacilityWideReach(t *testing.T) {
	d := rbac.Reaches(wearing(auth.RolePhysician), auth.PermPatientReadDemographics, facility)
	if !d.Allowed {
		t.Fatalf("a physician may not read demographics: %s", d.Explain(auth.PermPatientReadDemographics))
	}
	if d.Deferred != "" {
		t.Errorf("a facility-wide reach deferred %q; nothing is owed and saying otherwise would make "+
			"every such route refuse until somebody wrote a check that cannot fail", d.Deferred)
	}
}

// Reaches must not apply resource scope. The subject below stands at a station and the
// question names no resource at all; that is the shape the route guard always has.
func TestReachesAppliesNoResourceScope(t *testing.T) {
	s := wearing(auth.RoleAnthropometry)
	s.StationCode = "" // a route guard knows no station either; the session does not carry one
	d := rbac.Reaches(s, auth.PermObservationWriteAnthro, facility)
	if !d.Allowed {
		t.Fatalf("an anthropometry officer may not reach the observation route: %s",
			d.Explain(auth.PermObservationWriteAnthro))
	}
	if d.Deferred != rbac.ScopeOwnStation {
		t.Errorf("deferred %q, want %q", d.Deferred, rbac.ScopeOwnStation)
	}
}

// One test per reason Reaches refuses, because a closed list of reasons is only useful if
// each one is actually reachable.
func TestReachesRefusesForEachReason(t *testing.T) {
	registration := wearing(auth.RoleRegistration)

	cases := []struct {
		name    string
		subject rbac.Subject
		action  rbac.Action
		// facility the request is addressed to.
		at   uuid.UUID
		want rbac.Reason
	}{
		{
			name:    "a permission the role does not hold",
			subject: registration,
			action:  auth.PermPrescriptionSign,
			at:      facility,
			want:    rbac.ReasonPermissionNotHeld,
		},
		{
			name: "a hat the person does not own",
			// ActiveRole PHYSICIAN, Roles REGISTRATION: the header named a role the
			// resolver did not find. The engine refuses the hat, not the permission.
			subject: func() rbac.Subject {
				s := subject(auth.RoleRegistration)
				s.ActiveRole = auth.RolePhysician
				return s
			}(),
			action: auth.PermPatientReadDemographics,
			at:     facility,
			want:   rbac.ReasonRoleNotHeld,
		},
		{
			name:    "a rule in the blueprint that refuses outright",
			subject: wearing(auth.RoleNutritionist),
			action:  auth.PermPrescriptionRead,
			at:      facility,
			want:    rbac.ReasonExplicitDeny,
		},
		{
			name:    "a request addressed to another facility",
			subject: registration,
			action:  auth.PermPatientWriteDemographics,
			at:      other,
			want:    rbac.ReasonOtherFacility,
		},
		{
			name:    "a permission that is not in the catalogue",
			subject: registration,
			action:  "patient.invent.something",
			at:      facility,
			want:    rbac.ReasonUnknownAction,
		},
		{
			name:    "nobody at all",
			subject: rbac.Subject{},
			action:  auth.PermPatientReadDemographics,
			at:      facility,
			want:    rbac.ReasonNoSubject,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := rbac.Reaches(tc.subject, tc.action, tc.at)
			if d.Allowed {
				t.Fatalf("allowed; want a refusal for %s", tc.want)
			}
			if d.Reason != tc.want {
				t.Errorf("refused for %s; want %s (%s)", d.Reason, tc.want, d.Explain(tc.action))
			}
		})
	}
}

// The compiler cannot stop a resource-less Resource reaching Can, so Can says which layer
// got it wrong instead of inventing an answer about the caller. "out_of_scope" on this
// call is what hid the defect for months: it is a sentence about the person, and the
// person was not the problem.
func TestCanRefusesAQuestionThatNamesNoResource(t *testing.T) {
	// Exactly the resource the route guard used to build.
	routeShaped := rbac.Resource{Kind: "route", FacilityID: facility}

	// A field worker, not the registration desk: ADR-0036 §2 made the desk facility-wide
	// for this permission, and a facility-wide reach needs no resource — which is the next
	// test down, and would make this one pass for the wrong reason.
	d := rbac.Can(wearing(auth.RoleFieldWorker), auth.PermPatientWriteDemographics, routeShaped)
	if d.Allowed {
		t.Fatal("allowed a scope decision against a resource that is not one")
	}
	if d.Reason != rbac.ReasonNoResource {
		t.Errorf("refused for %s; want %s — a deny that says \"you asked the wrong layer\" is the "+
			"whole point, and %q is what sent this defect undiagnosed",
			d.Reason, rbac.ReasonNoResource, rbac.ReasonOutOfScope)
	}
}

// A facility-wide reach needs no resource, so the guard above must not fire for one: it is
// a guard against asking the wrong question, not a requirement to always have a resource.
func TestCanStillAnswersAFacilityWideQuestionWithoutOne(t *testing.T) {
	d := rbac.Can(wearing(auth.RolePhysician), auth.PermPatientReadDemographics,
		rbac.Resource{Kind: "patient", FacilityID: facility})
	if !d.Allowed {
		t.Fatalf("a physician's facility-wide read was refused: %s", d.Explain(auth.PermPatientReadDemographics))
	}
}

// The loosening must not have become a widening. Can still measures reach the moment a
// real resource is in hand, and this is the case that matters: the resource exists, the
// subject is standing somewhere else.
func TestCanStillRefusesAStationRoleTheNextStationsWork(t *testing.T) {
	s := wearing(auth.RoleAnthropometry)
	s.StationCode = station

	elsewhere := rbac.Resource{Kind: "observation", FacilityID: facility, StationCode: station2}
	d := rbac.Can(s, auth.PermObservationWriteAnthro, elsewhere)
	if d.Allowed {
		t.Fatal("an anthropometry officer reached an observation at another station")
	}
	if d.Reason != rbac.ReasonOutOfScope {
		t.Errorf("refused for %s; want %s — this refusal is the one that must keep working",
			d.Reason, rbac.ReasonOutOfScope)
	}

	// And the same subject, at the station they are actually working, is allowed: a rule
	// that refused everything would pass the test above and be useless.
	here := rbac.Resource{Kind: "observation", FacilityID: facility, StationCode: station}
	if d := rbac.Can(s, auth.PermObservationWriteAnthro, here); !d.Allowed {
		t.Fatalf("refused at their own station, so the test above proves nothing: %s",
			d.Explain(auth.PermObservationWriteAnthro))
	}
}

// The same, for the reach a field worker has.
func TestCanStillRefusesAFieldWorkerSomebodyElsesCapture(t *testing.T) {
	s := wearing(auth.RoleFieldWorker)
	theirs := rbac.Resource{Kind: "patient", FacilityID: facility, OwnerID: &someone}
	if d := rbac.Can(s, auth.PermPatientWriteDemographics, theirs); d.Allowed {
		t.Fatal("a field worker edited a capture they did not make")
	}
	mine := rbac.Resource{Kind: "patient", FacilityID: facility, OwnerID: &me}
	if d := rbac.Can(s, auth.PermPatientWriteDemographics, mine); !d.Allowed {
		t.Fatalf("refused their own capture: %s", d.Explain(auth.PermPatientWriteDemographics))
	}
}
