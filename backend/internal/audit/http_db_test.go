package audit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/audit"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
)

// The HTTP surface, with a stand-in for the session layer: a token is a person's employee
// code, and the caller it resolves to holds whatever the test says. The session service is
// tested where it lives; what is real here is the audit module, its database and the
// route guards in front of it.

type person struct {
	id          uuid.UUID
	code        string
	permissions []string
}

type fakeIdentifier struct {
	facility uuid.UUID
	people   map[string]person
}

func (f *fakeIdentifier) Identify(_ context.Context, token string) (httpx.Caller, error) {
	p, ok := f.people[token]
	if !ok {
		return httpx.Caller{}, errors.New("nobody")
	}
	return httpx.Caller{
		UserID: p.id.String(), FacilityID: f.facility.String(), SessionID: uuid.NewSHA1(p.id, []byte("session")).String(),
		Code: p.code, Permissions: p.permissions, Roles: []string{"X"},
	}, nil
}

type anyPermission struct{}

func (anyPermission) Authorize(ctx context.Context, caller httpx.Caller, anyOf []string) (context.Context, httpx.AuthzDecision) {
	for _, p := range anyOf {
		for _, held := range caller.Permissions {
			if p == held {
				return ctx, httpx.AuthzDecision{Allowed: true, Reason: "allowed"}
			}
		}
	}
	return ctx, httpx.AuthzDecision{Reason: "permission_not_held"}
}

// fakeStepUp accepts exactly one token, once, for one purpose.
type fakeStepUp struct {
	token, purpose string
	spent          bool
}

func (f *fakeStepUp) ConsumeStepUp(_ context.Context, token, _ string, purpose string) error {
	if f.spent || token != f.token || purpose != f.purpose {
		return errors.New("refused")
	}
	f.spent = true
	return nil
}

type codes struct{ people map[string]person }

func (c codes) EmployeeCode(_ context.Context, id uuid.UUID) (string, error) {
	for _, p := range c.people {
		if p.id == id {
			return p.code, nil
		}
	}
	return "", errors.New("unknown")
}

type httpHarness struct {
	*chainHarness
	server   *httptest.Server
	stepUp   *fakeStepUp
	people   map[string]person
	bg       *audit.BreakGlass
	handlers *audit.Handlers
}

func newHTTP(t *testing.T) *httpHarness {
	t.Helper()
	ch := newChain(t)
	h := &httpHarness{chainHarness: ch, stepUp: &fakeStepUp{token: "su-ok", purpose: audit.PurposeBreakGlass}}

	// Three people: an administrator, a junior doctor, and a pharmacist with no clinical
	// read at all.
	h.people = map[string]person{
		"admin":  {id: ch.admin, code: "A001", permissions: []string{"audit.read", "user.read"}},
		"doctor": {id: uuid.New(), code: "JD01", permissions: []string{"patient.read.clinical", "patient.read.demographics"}},
		"pharm":  {id: uuid.New(), code: "P001", permissions: []string{"prescription.read"}},
	}
	for _, key := range []string{"doctor", "pharm"} {
		p := h.people[key]
		if _, err := ch.db.SQL.Exec(`INSERT INTO core.app_user (id, facility_id, employee_code, name_en, name_bn, status)
			VALUES ($1, $2, $3, $3, $3, 'active')`, p.id, ch.facility, p.code); err != nil {
			t.Fatal(err)
		}
	}

	signer, err := audit.NewSigner("test-key-1", bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	h.bg = audit.NewBreakGlass(ch.store, ch.recorder, ch.clock, codes{h.people})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h.handlers = audit.NewHandlers(audit.HandlersConfig{
		Recorder: ch.recorder, Store: ch.store, BreakGlass: h.bg, Signer: signer,
		FacilityName: func(uuid.UUID) string { return "DTHC Faridpur" },
		StepUp:       h.stepUp, Clock: ch.clock, Logger: logger,
	})
	router, err := httpx.NewRouter(httpx.RouterOptions{
		Logger: logger, IDs: &ids.Sequential{Prefix: "req"}, MaxBodyBytes: 1 << 16, RequestTimeout: 5 * time.Second,
		Health:        &httpx.Health{Service: "api", Version: "test", Logger: logger},
		Authenticator: &fakeIdentifier{facility: ch.facility, people: h.people},
		Authorizer:    anyPermission{},
		Routes:        func(r chi.Router) { h.handlers.Mount(r) },
	})
	if err != nil {
		t.Fatal(err)
	}
	h.server = httptest.NewServer(router)
	t.Cleanup(h.server.Close)
	return h
}

func (h *httpHarness) call(t *testing.T, as, method, path string, body any, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if as != "" {
		req.Header.Set("Authorization", "Bearer "+as)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		req.Header.Set("X-Requested-With", "DTHCMS")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}

func code(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	return env.Error.Code
}

// Criterion 3: break-glass requires a typed justification and notifies an administrator
// within one minute.
func TestBreakGlassNeedsAJustificationAndRaisesTheAlarm(t *testing.T) {
	h := newHTTP(t)
	patient := uuid.New()

	t.Run("a pharmacist cannot reach the door at all", func(t *testing.T) {
		resp, body := h.call(t, "pharm", "POST", "/v1/audit/break-glass",
			map[string]any{"scope_kind": "patient", "scope_ref": patient.String(), "justification": "a perfectly long justification"},
			map[string]string{"X-Step-Up-Token": "su-ok"})
		if resp.StatusCode != 403 || code(t, body) != "FORBIDDEN" {
			t.Fatalf("status %d %s; want a plain FORBIDDEN, not a step-up hint", resp.StatusCode, body)
		}
	})

	t.Run("without a step-up the door stays shut", func(t *testing.T) {
		resp, body := h.call(t, "doctor", "POST", "/v1/audit/break-glass",
			map[string]any{"scope_kind": "patient", "scope_ref": patient.String(), "justification": "a perfectly long justification"}, nil)
		if resp.StatusCode != 403 || code(t, body) != "STEP_UP_REQUIRED" {
			t.Fatalf("status %d %s", resp.StatusCode, body)
		}
	})

	t.Run("a justification that is too short is refused, and no alert is raised", func(t *testing.T) {
		resp, body := h.call(t, "doctor", "POST", "/v1/audit/break-glass",
			map[string]any{"scope_kind": "patient", "scope_ref": patient.String(), "justification": "urgent"},
			map[string]string{"X-Step-Up-Token": "su-ok"})
		if resp.StatusCode != 422 || !strings.Contains(string(body), "justification") {
			t.Fatalf("status %d %s", resp.StatusCode, body)
		}
		// The step-up was spent by the attempt; the fake gives a fresh one for the next.
		h.stepUp.spent = false
		var n int
		if err := h.db.SQL.QueryRow(`SELECT count(*) FROM core.admin_alert`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%d alerts raised for a refused request", n)
		}
	})

	var accessID string
	t.Run("with one, the door opens and the record and the alert exist at once", func(t *testing.T) {
		before := h.clock.Now()
		resp, body := h.call(t, "doctor", "POST", "/v1/audit/break-glass",
			map[string]any{"scope_kind": "patient", "scope_ref": patient.String(),
				"justification": "Unconscious patient in room 2; regular physician unreachable."},
			map[string]string{"X-Step-Up-Token": "su-ok", "X-Active-Role": "JUNIOR_DOCTOR"})
		if resp.StatusCode != 201 {
			t.Fatalf("status %d %s", resp.StatusCode, body)
		}
		var access struct {
			ID        string `json:"id"`
			ExpiresAt string `json:"expires_at"`
			AuditSeq  int64  `json:"audit_seq"`
		}
		_ = json.Unmarshal(body, &access)
		accessID = access.ID
		if access.AuditSeq != 1 {
			t.Errorf("audit_seq = %d, want the first row", access.AuditSeq)
		}

		// The administrator's console polls this; the alert is already there.
		resp, body = h.call(t, "admin", "GET", "/v1/audit/alerts", nil, nil)
		var alerts struct {
			Alerts []struct {
				ID        string `json:"id"`
				Kind      string `json:"kind"`
				MessageEN string `json:"message_en"`
				MessageBN string `json:"message_bn"`
				CreatedAt string `json:"created_at"`
			} `json:"alerts"`
		}
		_ = json.Unmarshal(body, &alerts)
		if resp.StatusCode != 200 || len(alerts.Alerts) != 1 {
			t.Fatalf("status %d %s", resp.StatusCode, body)
		}
		a := alerts.Alerts[0]
		if a.Kind != "break_glass" || !strings.Contains(a.MessageEN, "JD01 broke the glass") || !strings.Contains(a.MessageBN, "জরুরি প্রবেশাধিকার") {
			t.Errorf("alert = %+v", a)
		}
		created, _ := time.Parse(time.RFC3339Nano, a.CreatedAt)
		if created.Sub(before) > time.Minute {
			t.Errorf("the alert was raised %s after the access; the criterion is one minute", created.Sub(before))
		}

		// And the trail reads correctly in both languages.
		_, body = h.call(t, "admin", "GET", "/v1/audit/events?kind=break_glass.opened", nil, nil)
		var page struct {
			Events []struct {
				SentenceEN string `json:"sentence_en"`
				SentenceBN string `json:"sentence_bn"`
				ActorRole  string `json:"actor_role"`
			} `json:"events"`
		}
		_ = json.Unmarshal(body, &page)
		if len(page.Events) != 1 {
			t.Fatalf("events: %s", body)
		}
		if !strings.HasPrefix(page.Events[0].SentenceEN, "JD01 broke the glass for patient "+patient.String()) {
			t.Errorf("EN: %s", page.Events[0].SentenceEN)
		}
		if !strings.Contains(page.Events[0].SentenceBN, "JD01") || !strings.Contains(page.Events[0].SentenceBN, "জরুরি প্রবেশাধিকার নিয়েছেন") {
			t.Errorf("BN: %s", page.Events[0].SentenceBN)
		}
		if page.Events[0].ActorRole != "JUNIOR_DOCTOR" {
			t.Errorf("the hat worn was not recorded: %q", page.Events[0].ActorRole)
		}

		// The doctor sees their own open door; the pharmacist sees nothing.
		_, body = h.call(t, "doctor", "GET", "/v1/audit/break-glass/mine", nil, nil)
		if !strings.Contains(string(body), accessID) {
			t.Errorf("mine: %s", body)
		}
		resp, _ = h.call(t, "pharm", "GET", "/v1/audit/break-glass", nil, nil)
		if resp.StatusCode != 403 {
			t.Errorf("a pharmacist listed the open doors: %d", resp.StatusCode)
		}
	})

	t.Run("acknowledging the alert acknowledges the access and is recorded", func(t *testing.T) {
		_, body := h.call(t, "admin", "GET", "/v1/audit/alerts", nil, nil)
		var alerts struct {
			Alerts []struct {
				ID string `json:"id"`
			} `json:"alerts"`
		}
		_ = json.Unmarshal(body, &alerts)
		resp, body := h.call(t, "admin", "POST", "/v1/audit/alerts/"+alerts.Alerts[0].ID+"/acknowledge", nil, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("status %d %s", resp.StatusCode, body)
		}
		resp, body = h.call(t, "admin", "POST", "/v1/audit/alerts/"+alerts.Alerts[0].ID+"/acknowledge", nil, nil)
		if resp.StatusCode != 409 {
			t.Errorf("a second acknowledgement: %d %s", resp.StatusCode, body)
		}
		_, body = h.call(t, "admin", "GET", "/v1/audit/alerts", nil, nil)
		if strings.Contains(string(body), alerts.Alerts[0].ID) {
			t.Error("the acknowledged alert is still open")
		}
		var ack *time.Time
		if err := h.db.SQL.QueryRow(`SELECT acknowledged_at FROM core.break_glass_access WHERE id = $1`, accessID).Scan(&ack); err != nil {
			t.Fatal(err)
		}
		if ack == nil {
			t.Error("the access itself was not acknowledged")
		}
		_, body = h.call(t, "admin", "GET", "/v1/audit/events?kind=break_glass.acknowledged", nil, nil)
		if !strings.Contains(string(body), "A001 acknowledged the break-glass access of JD01") {
			t.Errorf("trail: %s", body)
		}
	})

	t.Run("the doctor closes the door early", func(t *testing.T) {
		resp, body := h.call(t, "doctor", "POST", "/v1/audit/break-glass/"+accessID+"/end", map[string]any{"reason": "patient handed over"}, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("status %d %s", resp.StatusCode, body)
		}
		resp, _ = h.call(t, "doctor", "POST", "/v1/audit/break-glass/"+accessID+"/end", map[string]any{"reason": "again"}, nil)
		if resp.StatusCode != 409 {
			t.Errorf("closing twice: %d", resp.StatusCode)
		}
		_, body = h.call(t, "doctor", "GET", "/v1/audit/break-glass/mine", nil, nil)
		if strings.Contains(string(body), accessID) {
			t.Error("a closed door is still listed as held")
		}
	})

	// The chain survived all of it.
	resp, body := h.call(t, "admin", "GET", "/v1/audit/chain", nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("chain: %d %s", resp.StatusCode, body)
	}
}

func TestOnlyAuditReadersSeeTheTrail(t *testing.T) {
	h := newHTTP(t)
	for _, route := range []string{"/v1/audit/events", "/v1/audit/chain", "/v1/audit/export", "/v1/audit/alerts", "/v1/audit/break-glass"} {
		resp, body := h.call(t, "doctor", "GET", route, nil, nil)
		if resp.StatusCode != 403 || code(t, body) != "FORBIDDEN" {
			t.Errorf("%s as a doctor: %d %s", route, resp.StatusCode, code(t, body))
		}
		resp, _ = h.call(t, "", "GET", route, nil, nil)
		if resp.StatusCode != 401 {
			t.Errorf("%s anonymously: %d", route, resp.StatusCode)
		}
	}
	// The kind registry and the public key are for any signed-in person.
	for _, route := range []string{"/v1/audit/kinds", "/v1/audit/signing-key"} {
		resp, _ := h.call(t, "doctor", "GET", route, nil, nil)
		if resp.StatusCode != 200 {
			t.Errorf("%s as a doctor: %d", route, resp.StatusCode)
		}
	}
}

// Criterion 4, end to end: the PDF the API hands out verifies against the key the API
// publishes, and the export itself is in the trail.
func TestTheExportedPDFVerifiesAgainstThePublishedKey(t *testing.T) {
	h := newHTTP(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		h.clock.Advance(time.Minute)
		if _, err := h.recorder.Record(ctx, h.entry("role.granted", map[string]any{"role": "COUNSELOR"})); err != nil {
			t.Fatal(err)
		}
	}

	resp, pdf := h.call(t, "admin", "GET", "/v1/audit/export?person=N002", nil, nil)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/pdf" {
		t.Fatalf("status %d, type %s: %s", resp.StatusCode, resp.Header.Get("Content-Type"), pdf)
	}
	sig := audit.Signature{
		KeyID: resp.Header.Get(audit.HeaderKeyID), Algorithm: audit.Algorithm,
		Digest: resp.Header.Get(audit.HeaderDigest), Value: resp.Header.Get(audit.HeaderSignature),
	}
	if sig.KeyID != "test-key-1" || sig.Value == "" {
		t.Fatalf("signature headers: %+v", sig)
	}

	_, keyBody := h.call(t, "doctor", "GET", "/v1/audit/signing-key", nil, nil)
	var key struct {
		PublicKey string `json:"public_key"`
	}
	_ = json.Unmarshal(keyBody, &key)
	if err := audit.Verify(key.PublicKey, pdf, sig); err != nil {
		t.Fatalf("the served PDF does not verify against the served key: %v", err)
	}
	tampered := append([]byte(nil), pdf...)
	tampered[len(tampered)/2] ^= 0x01
	if err := audit.Verify(key.PublicKey, tampered, sig); err == nil {
		t.Fatal("a tampered PDF verified")
	}
	if !bytes.Contains(pdf, []byte("A001 granted COUNSELOR to N002")) {
		t.Error("the sentences are not in the file")
	}

	// The export is the newest row in the trail — and it says how many entries left.
	_, body := h.call(t, "admin", "GET", "/v1/audit/events?kind=audit.exported", nil, nil)
	if !strings.Contains(string(body), "A001 exported 3 audit entries as a signed PDF") {
		t.Errorf("the export was not recorded: %s", body)
	}
}
