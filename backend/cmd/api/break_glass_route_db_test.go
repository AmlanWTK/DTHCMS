package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/audit"
	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/auth/totp"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/secretbox"
)

// The emergency door, through the real thing (ADR-0036 §2(b)).
//
// # Why this is end-to-end and not a unit test of the guard
//
// internal/audit's own HTTP tests drive the same handler behind a fake authorizer that says
// yes to every permission, so they were green for the whole time the route answered 403 to
// the eight roles that most needed it. That is not a criticism of them — they are testing the
// door, not the lock — but it is exactly the gap ADR-0036 was written out of, and the same
// gap that let `POST /v1/patients` refuse every role in the catalogue for a checkpoint and a
// half. A rule that holds in the engine and not on the wire is a rule the clinic does not
// have.
//
// So everything between the request and the row is real: a real sign-in at a real enrolled
// workstation, a real session, the real middleware chain, the real route guard, the real RBAC
// engine with the real reach store behind it, the real step-up middleware, and the real
// break-glass service writing a real row, a real chained audit event and a real alert.
//
// The one thing minted rather than typed is the step-up token, because typing it would mean
// generating a TOTP code and posting it to `/v1/auth/step-up`, and the auth handlers this
// harness builds carry no second factor. It is minted through the real `IssueStepUp` against
// a real confirmed enrolment, and it is spent by the real `ConsumeStepUp` inside the real
// middleware — so what is skipped is the person's thumb, not a check.

// breakGlassStack adds the audit module to the registration stack and keeps hold of the
// second factor, which the test needs in order to mint a token the middleware will accept.
type breakGlassStack struct {
	*registrationStack
	secondFactor *auth.SecondFactor
}

func newBreakGlassStack(t *testing.T) *breakGlassStack {
	t.Helper()
	bg := &breakGlassStack{}
	bg.registrationStack = newRegistrationStack(t, func(pool *pgxpool.Pool, logger *slog.Logger, r chi.Router) {
		store := auth.NewPostgresStore(pool)
		ring, err := secretbox.NewRing(secretbox.Key{ID: "k1", Material: bytes.Repeat([]byte{5}, secretbox.KeySize)})
		if err != nil {
			t.Fatalf("building the seal ring: %v", err)
		}
		bg.secondFactor = auth.NewSecondFactor(auth.SecondFactorConfig{
			Store: store, Users: store, Ring: ring, Clock: clock.Real{},
		})

		auditStore := audit.NewPostgresStore(pool)
		recorder := audit.NewRecorder(auditStore, clock.Real{}, logger)
		signer, err := audit.NewSigner("test-key-1", bytes.Repeat([]byte{9}, 32))
		if err != nil {
			t.Fatalf("building the audit signer: %v", err)
		}
		audit.NewHandlers(audit.HandlersConfig{
			Recorder: recorder, Store: auditStore, Signer: signer,
			BreakGlass:   audit.NewBreakGlass(auditStore, recorder, clock.Real{}, store),
			FacilityName: func(uuid.UUID) string { return "DTHC Faridpur" },
			StepUp:       &auth.StepUpAdapter{SecondFactor: bg.secondFactor},
			Clock:        clock.Real{}, Logger: logger,
		}).Mount(r)
	})
	return bg
}

// stepUpToken enrols the signed-in user's authenticator and mints a token for one purpose.
func (s *breakGlassStack) stepUpToken(t *testing.T, purpose string) string {
	t.Helper()
	ctx := context.Background()

	user, err := s.store.UserByID(ctx, s.user)
	if err != nil {
		t.Fatalf("reading the user back: %v", err)
	}
	enrolment, err := s.secondFactor.BeginEnrolment(ctx, user, nil)
	if err != nil {
		t.Fatalf("beginning the enrolment: %v", err)
	}
	now := time.Now()
	code, err := totp.Code(enrolment.Secret, totp.Step(now))
	if err != nil {
		t.Fatalf("generating a code: %v", err)
	}
	if _, err := s.secondFactor.ConfirmEnrolment(ctx, user, code, nil); err != nil {
		t.Fatalf("confirming the enrolment: %v", err)
	}

	// The session the bearer token names. Read from the database rather than decoded from
	// the token, because the binding the middleware checks is the session id and this test
	// should fail if that ever stops being true.
	var session auth.Session
	if err := s.db.SQL.QueryRow(`
		SELECT id, facility_id, user_id FROM core.session
		 WHERE user_id = $1 AND revoked_at IS NULL
		 ORDER BY issued_at DESC LIMIT 1`, s.user).
		Scan(&session.ID, &session.FacilityID, &session.UserID); err != nil {
		t.Fatalf("finding the session: %v", err)
	}

	// A second code, one step on, because the first was spent confirming the enrolment and
	// the replay guard refuses it a second time — which is the guard working.
	next, err := totp.Code(enrolment.Secret, totp.Step(now.Add(31*time.Second)))
	if err != nil {
		t.Fatalf("generating the step-up code: %v", err)
	}
	minted, err := s.secondFactor.IssueStepUp(ctx, user, session, purpose, auth.Proof{Code: next}, nil)
	if err != nil {
		t.Fatalf("minting the step-up: %v", err)
	}
	return minted.Token
}

// postWithStepUp is the stack's post with the step-up header the door demands.
func (s *breakGlassStack) postWithStepUp(t *testing.T, token, role, path, stepUp string, body any) (int, map[string]any, string) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, s.URL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "DTHCMS")
	req.Header.Set("Idempotency-Key", uuid.NewString())
	req.Header.Set("Authorization", "Bearer "+token)
	if role != "" {
		req.Header.Set(httpx.ActiveRoleHeader, role)
	}
	if stepUp != "" {
		req.Header.Set(httpx.StepUpHeader, stepUp)
	}
	res, err := s.Client().Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = res.Body.Close() }()
	var decoded map[string]any
	payload := new(bytes.Buffer)
	_, _ = payload.ReadFrom(res.Body)
	_ = json.Unmarshal(payload.Bytes(), &decoded)
	return res.StatusCode, decoded, payload.String()
}

// The test this defect exists for.
//
// Before ADR-0036 §2(b) this answered 403 with `scope_not_enforced` in the log: the route
// declared `patient.read.clinical`, which is station-scoped for the nutritionist, and the
// route judges no resource — so the guard refused the one door whose entire purpose is to be
// used when a station reach refuses.
func TestAStationRoleCanBreakTheGlassThroughTheRealRouter(t *testing.T) {
	s := newBreakGlassStack(t)
	s.seedClerk(t, "NUT-CP86", auth.RoleNutritionist)
	workstation := s.enrol(t, "Nutrition desk 1", "STN_NUTRITION")
	token := s.signIn(t, "NUT-CP86", workstation)
	hat := string(auth.RoleNutritionist)

	// A patient on nobody's queue: precisely the person ADR-0036 §1 leaves unreachable, and
	// therefore precisely the person the door is for.
	patient := s.reachFixture(t, "", "")

	// The reach refusal the door exists to answer, asserted first so that the 201 below
	// cannot be green for the boring reason that everything is open.
	if status, _ := s.get(t, token, hat, "/v1/patients/"+patient.String()); status != http.StatusForbidden {
		t.Fatalf("the nutritionist can already open an unqueued patient: %d\n"+
			"If this is a 200 the reach rule is not being applied and the rest of this test proves nothing.",
			status)
	}

	stepUp := s.stepUpToken(t, auth.PurposeBreakGlass)
	status, decoded, body := s.postWithStepUp(t, token, hat, "/v1/audit/break-glass", stepUp, map[string]any{
		"scope_kind":    "patient",
		"scope_ref":     patient.String(),
		"justification": "Patient collapsed at the nutrition desk and is not on any queue today.",
	})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("a nutritionist cannot break the glass: %d %s\n"+
			"A 403 here is ADR-0036 §2(b) undone: the door has re-acquired a station scope, and the "+
			"eight roles that cannot reach past their own queue have nowhere left to go.",
			status, body)
	}

	accessID, _ := decoded["id"].(string)
	if accessID == "" {
		t.Fatalf("the door opened and named no access: %s", body)
	}

	// Time-boxed, audited, alarmed. Asserted here rather than left to the module's own tests
	// because these three are the whole of the argument for the door being facility-wide:
	// if any of them stops being true, §2(b)'s premise has gone and the scope decision has to
	// be revisited rather than quietly inherited.
	var granted, expires time.Time
	var auditSeq *int64
	if err := s.db.SQL.QueryRow(`
		SELECT granted_at, expires_at, audit_seq FROM core.break_glass_access WHERE id = $1`,
		accessID).Scan(&granted, &expires, &auditSeq); err != nil {
		t.Fatalf("reading the access back: %v", err)
	}
	if bounded := expires.Sub(granted); bounded <= 0 || bounded > 24*time.Hour {
		t.Errorf("the access is open for %s; it must be bounded and at most twenty-four hours", bounded)
	}
	if auditSeq == nil {
		t.Error("the access is not linked to an audit row; an emergency access nobody can review " +
			"afterwards is the one thing this must never produce")
	}
	var alerts int
	if err := s.db.SQL.QueryRow(`
		SELECT count(*) FROM core.admin_alert
		 WHERE kind = 'break_glass' AND severity = 'high' AND acknowledged_at IS NULL`).Scan(&alerts); err != nil {
		t.Fatalf("counting the alerts: %v", err)
	}
	if alerts == 0 {
		t.Error("no high-severity alert stands on the administrators' console; the door is quiet, " +
			"and loudness is what makes it safe to have")
	}
}

// The half that must not change. Facility-wide reach is not the same thing as an open door.
func TestBreakGlassIsStillRefusedWithoutAStepUp(t *testing.T) {
	s := newBreakGlassStack(t)
	s.seedClerk(t, "NUT-CP86B", auth.RoleNutritionist)
	workstation := s.enrol(t, "Nutrition desk 2", "STN_NUTRITION")
	token := s.signIn(t, "NUT-CP86B", workstation)
	hat := string(auth.RoleNutritionist)
	patient := s.reachFixture(t, "", "")

	form := map[string]any{
		"scope_kind":    "patient",
		"scope_ref":     patient.String(),
		"justification": "Patient collapsed at the nutrition desk and is not on any queue today.",
	}

	t.Run("no token at all", func(t *testing.T) {
		status, _, body := s.postWithStepUp(t, token, hat, "/v1/audit/break-glass", "", form)
		if status != http.StatusForbidden && status != http.StatusUnauthorized {
			t.Fatalf("the door opened without a second factor: %d %s", status, body)
		}
		if !strings.Contains(strings.ToUpper(body), "STEP_UP") {
			t.Errorf("the refusal does not say a step-up is what is missing: %s", body)
		}
	})

	t.Run("a token minted for another purpose", func(t *testing.T) {
		// A merge token. Same person, same session, same freshness — and good for nothing
		// here, which is the whole point of a purpose.
		other := s.stepUpToken(t, auth.PurposeMergePatients)
		status, _, body := s.postWithStepUp(t, token, hat, "/v1/audit/break-glass", other, form)
		if status != http.StatusForbidden && status != http.StatusUnauthorized {
			t.Fatalf("a token minted to merge two patients opened the emergency door: %d %s", status, body)
		}
	})

	t.Run("nothing was recorded", func(t *testing.T) {
		var opened int
		if err := s.db.SQL.QueryRow(`SELECT count(*) FROM core.break_glass_access`).Scan(&opened); err != nil {
			t.Fatalf("counting the accesses: %v", err)
		}
		if opened != 0 {
			t.Errorf("%d break-glass accesses exist after two refusals; a refused door must leave no grant", opened)
		}
	})
}
