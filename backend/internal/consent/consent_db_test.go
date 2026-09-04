package consent_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/consent"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
)

// Consent, end to end and against a real database (CP36, §15.1, D-02).
//
// Everything here is an acceptance criterion or a rule that would fail silently: a consent
// with no version behind it, a revocation that does not reach the research cohort, a message
// that goes out after somebody said stop.

type api struct {
	*testsupport.DB
	pool     *pgxpool.Pool
	store    *consent.Store
	service  *consent.Service
	gate     *consent.Gate
	server   *httptest.Server
	clock    *clock.Fixed
	facility uuid.UUID
	user     uuid.UUID
	device   uuid.UUID
	patient  uuid.UUID
	research string
}

type officer struct {
	facility, user, device uuid.UUID
	permissions            []string
}

func (o officer) Identify(context.Context, string) (httpx.Caller, error) {
	return httpx.Caller{
		UserID: o.user.String(), FacilityID: o.facility.String(),
		SessionID: uuid.NewSHA1(o.user, []byte("session")).String(),
		Code:      "R001", Permissions: o.permissions, Roles: []string{"REGISTRATION"},
	}, nil
}

func (o officer) Authorize(ctx context.Context, caller httpx.Caller, anyOf []string) (context.Context, httpx.AuthzDecision) {
	for _, want := range anyOf {
		for _, held := range caller.Permissions {
			if want == held {
				return httpx.WithPrincipal(ctx, httpx.Principal{
					UserID: caller.UserID, FacilityID: caller.FacilityID, SessionID: caller.SessionID,
					Code: caller.Code, DeviceID: o.device.String(),
					Role: "REGISTRATION", Station: "REGISTRATION",
				}), httpx.AuthzDecision{Allowed: true, Reason: "allowed"}
			}
		}
	}
	return ctx, httpx.AuthzDecision{Reason: "permission_not_held"}
}

func newAPI(t *testing.T, permissions ...string) *api {
	t.Helper()
	if len(permissions) == 0 {
		permissions = []string{
			"patient.read.demographics", "patient.consent.record", "patient.consent.revoke",
		}
	}
	base := testsupport.Postgres(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, base.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	h := &api{DB: base, pool: pool, user: uuid.New(), device: uuid.New()}
	h.clock = clock.NewFixed(time.Date(2026, 9, 14, 4, 42, 0, 0, time.UTC))
	if err := base.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&h.facility); err != nil {
		t.Fatal(err)
	}

	events := eventstore.New(eventstore.Config{
		Pool: pool, Clock: h.clock, Synchronous: projection.NewSyncSet(projection.Default),
	})
	if err := projection.NewEngineWithEvents(pool, projection.Default, events).Register(ctx); err != nil {
		t.Fatal(err)
	}

	h.store = consent.NewStore(pool)
	h.service = consent.NewService(h.store, events, h.clock)
	h.gate = consent.NewGate(h.store, h.clock.Now)
	h.service.Watching(h.gate)

	h.seedStaff(t)
	h.seedPatient(t)
	h.publish(t)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handlers := consent.NewHandlers(consent.HandlersConfig{
		Service: h.service, Store: h.store, Clock: h.clock, Logger: logger,
	})
	who := officer{facility: h.facility, user: h.user, device: h.device, permissions: permissions}
	router, err := httpx.NewRouter(httpx.RouterOptions{
		Logger: logger, IDs: &ids.Sequential{Prefix: "req"},
		MaxBodyBytes: 1 << 16, RequestTimeout: 10 * time.Second,
		Health:        &httpx.Health{Service: "api", Version: "test", Logger: logger},
		Authenticator: who, Authorizer: who,
		Routes: func(r chi.Router) {
			r.Route("/patients", handlers.Mount)
			handlers.MountTemplates(r)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.server = httptest.NewServer(router)
	t.Cleanup(h.server.Close)
	return h
}

func (h *api) seedStaff(t *testing.T) {
	t.Helper()
	if _, err := h.SQL.Exec(`
		INSERT INTO core.app_user (id, facility_id, employee_code, name_en, name_bn, status)
		VALUES ($1, $2, 'R001', 'Registration Officer', 'নিবন্ধন কর্মকর্তা', 'active')`,
		h.user, h.facility); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO core.device (id, facility_id, name, kind, status, enrolled_at)
		VALUES ($1, $2, 'Tablet 1', 'tablet', 'active', now())`,
		h.device, h.facility); err != nil {
		t.Fatal(err)
	}
}

// seedPatient writes a patient and their anonymised research row the way registration does,
// including the link — because the whole research half of this checkpoint is about that
// link being crossed by exactly one SECURITY DEFINER function and nothing else.
func (h *api) seedPatient(t *testing.T) {
	t.Helper()
	h.patient = uuid.New()
	h.research = "RS-" + "ABCDEFGHJKMNPQRSTVWXYZ2345"[:26]
	var code string
	if err := h.SQL.QueryRow(`SELECT code FROM core.facility WHERE id = $1`, h.facility).Scan(&code); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO core.patient (id, facility_id, clinical_id, name_en, sex, birth_date,
		                          dob_precision, dob_verified_by, phone_primary,
		                          status, registered_by, registered_at)
		VALUES ($1, $2, 'DTHC-FRD-2026-000137', 'Md Rahim Uddin', 'male', DATE '1985-06-14',
		        'day', 'national_id', '+8801711111101', 'active', $3, now())`,
		h.patient, h.facility, h.user); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO research.research_subject (research_id, facility_code, enrolled_month, birth_year, sex)
		VALUES ($1, $2, date_trunc('month', now())::date, 1985, 'male')`, h.research, code); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO identity_link.research_subject (patient_id, research_id, facility_id)
		VALUES ($1, $2, $3)`, h.patient, h.research, h.facility); err != nil {
		t.Fatal(err)
	}
}

// publish loads a template for every consent, in both languages.
//
// Standing in for D-02's approved wording, which is legal text nobody has written yet. The
// point of the checkpoint is that when that text arrives, this is what happens to it: an
// INSERT, and everything else already works.
func (h *api) publish(t *testing.T) {
	t.Helper()
	for _, kind := range eventstore.ConsentTypes {
		for _, language := range []string{"en", "bn"} {
			body := "Placeholder wording for " + kind + " in " + language + ". Pending D-02."
			sum := sha256.Sum256([]byte(body))
			if _, err := h.SQL.Exec(`
				INSERT INTO core.consent_template
				  (consent_type, version, language, title, body, body_digest, status, effective_from)
				VALUES ($1, 1, $2, $3, $4, $5, 'active', now())`,
				kind, language, kind+" consent", body, hex.EncodeToString(sum[:])); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// --- requests ---

func (h *api) call(t *testing.T, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test")
	req.Header.Set("X-Requested-With", "DTHCMS")
	req.Header.Set("X-Active-Role", "REGISTRATION")

	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	decoded := map[string]any{}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &decoded)
	}
	return resp, decoded
}

func (h *api) grant(t *testing.T, kind string, edit func(map[string]any)) (*http.Response, map[string]any) {
	t.Helper()
	body := map[string]any{
		"event_id":       uuid.Must(uuid.NewV7()).String(),
		"consent_type":   kind,
		"language":       "bn",
		"capture_method": "verbal_attested",
		"witnessed_by":   h.user.String(),
	}
	if edit != nil {
		edit(body)
	}
	return h.call(t, http.MethodPost, "/v1/patients/"+h.patient.String()+"/consents", body)
}

func (h *api) revoke(t *testing.T, kind string, edit func(map[string]any)) (*http.Response, map[string]any) {
	t.Helper()
	body := map[string]any{"event_id": uuid.Must(uuid.NewV7()).String(), "requested_by": "patient"}
	if edit != nil {
		edit(body)
	}
	return h.call(t, http.MethodPost,
		"/v1/patients/"+h.patient.String()+"/consents/"+kind+"/revoke", body)
}

func (h *api) ctx() context.Context {
	return httpx.WithPrincipal(context.Background(), httpx.Principal{
		UserID: h.user.String(), FacilityID: h.facility.String(),
		Code: "R001", DeviceID: h.device.String(),
		Role: "REGISTRATION", Station: "REGISTRATION",
	})
}
