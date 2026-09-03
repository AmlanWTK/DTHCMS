package idempotency_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/idempotency"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
)

// The idempotency store against a real database (CP24 criterion 2). The interesting
// properties are all properties of the database — that two concurrent claims cannot both
// win, that a claim survives a process restart, that expiry actually removes rows — so
// there is nothing here a fake could tell us.

type harness struct {
	db       *testsupport.DB
	pool     *pgxpool.Pool
	store    *idempotency.Store
	clock    *clock.Fixed
	facility uuid.UUID
	user     uuid.UUID
	other    uuid.UUID
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db := testsupport.Postgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, db.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	h := &harness{db: db, pool: pool, store: idempotency.New(pool),
		clock: clock.NewFixed(time.Date(2026, 9, 3, 4, 42, 0, 0, time.UTC))}
	if err := db.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&h.facility); err != nil {
		t.Fatal(err)
	}
	for code, into := range map[string]*uuid.UUID{"S001": &h.user, "S002": &h.other} {
		if err := db.SQL.QueryRow(`INSERT INTO core.app_user (facility_id, employee_code, name_en, name_bn, status)
			VALUES ($1, $2, 'Station', 'স্টেশন', 'active') RETURNING id`, h.facility, code).Scan(into); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

// chain is the middleware over the real store, with the caller the chain would have set.
func (h *harness) chain(user uuid.UUID, handler http.HandlerFunc) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	inner := httpx.Idempotent(httpx.IdempotencyConfig{Store: h.store, Clock: h.clock, Logger: logger})(handler)
	return httpx.CallerForTest(httpx.Caller{UserID: user.String(), FacilityID: h.facility.String()}, inner)
}

func (h *harness) post(t *testing.T, chain http.Handler, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/visits/1/measurements", strings.NewReader(body))
	r.Header.Set(httpx.IdempotencyHeader, key)
	w := httptest.NewRecorder()
	chain.ServeHTTP(w, r)
	return w
}

const key = "0190a8f2-0000-7000-8000-0000000000aa"

// Criterion 2, through the real store: the retry gets the identical body.
func TestARetriedRequestGetsTheIdenticalResponse(t *testing.T) {
	h := newHarness(t)
	var runs atomic.Int64
	chain := h.chain(h.user, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(w, `{"event_id":"0190a8f2-0000-7000-8000-0000000000e1","run":%d}`, runs.Add(1))
	})

	first := h.post(t, chain, key, `{"value":150}`)
	second := h.post(t, chain, key, `{"value":150}`)
	third := h.post(t, chain, key, `{"value":150}`)

	if runs.Load() != 1 {
		t.Fatalf("the handler ran %d times", runs.Load())
	}
	if first.Body.String() != second.Body.String() || second.Body.String() != third.Body.String() {
		t.Errorf("bodies differ:\n1 %s\n2 %s\n3 %s", first.Body, second.Body, third.Body)
	}
	if second.Code != http.StatusCreated || second.Header().Get("Content-Type") != "application/json" {
		t.Errorf("the replay: %d %q", second.Code, second.Header().Get("Content-Type"))
	}
	if second.Header().Get(httpx.IdempotencyReplayedHeader) != "true" {
		t.Error("the replay is not marked")
	}
}

// Concurrency, at the database: a hundred simultaneous retries, one handler run, and no
// caller left without an answer.
func TestConcurrentRetriesClaimTheKeyOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("concurrency test")
	}
	h := newHarness(t)
	var runs atomic.Int64
	chain := h.chain(h.user, func(w http.ResponseWriter, _ *http.Request) {
		runs.Add(1)
		time.Sleep(20 * time.Millisecond) // long enough that the others really do overlap
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	const attempts = 100
	var wg sync.WaitGroup
	codes := make([]int, attempts)
	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = h.post(t, chain, key, `{"value":150}`).Code
		}()
	}
	wg.Wait()

	if runs.Load() != 1 {
		t.Fatalf("the handler ran %d times for %d concurrent retries", runs.Load(), attempts)
	}
	var created, conflicts int
	for _, code := range codes {
		switch code {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicts++
		default:
			t.Errorf("unexpected status %d", code)
		}
	}
	if created < 1 {
		t.Error("nobody got the created response")
	}
	if created+conflicts != attempts {
		t.Errorf("%d created + %d conflicts != %d", created, conflicts, attempts)
	}
	// Every caller that was told to wait gets the answer when it retries.
	if got := h.post(t, chain, key, `{"value":150}`); got.Code != http.StatusCreated || got.Body.String() != `{"ok":true}` {
		t.Errorf("the settled retry: %d %s", got.Code, got.Body)
	}

	var rows int
	if err := h.db.SQL.QueryRow(`SELECT count(*) FROM ops.idempotency_record`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("%d rows for one key", rows)
	}
}

// A key is one person's. Two operators who happen to choose the same key must not be
// handed each other's responses — which is why the primary key includes the user.
func TestAKeyBelongsToOnePerson(t *testing.T) {
	h := newHarness(t)
	handler := func(who string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"who":%q}`, who)
		}
	}
	mine := h.post(t, h.chain(h.user, handler("mine")), key, `{"value":150}`)
	theirs := h.post(t, h.chain(h.other, handler("theirs")), key, `{"value":150}`)

	if !strings.Contains(mine.Body.String(), "mine") || !strings.Contains(theirs.Body.String(), "theirs") {
		t.Fatalf("one operator saw another's response: %s / %s", mine.Body, theirs.Body)
	}
}

// The claim outlives the process: a restart mid-request leaves the key held, and the
// client's retry is answered by the record rather than running the handler again.
func TestTheClaimSurvivesTheProcess(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	fingerprint := make([]byte, 32)
	fingerprint[0] = 7

	won, _, err := h.store.Claim(ctx, h.user.String(), h.facility.String(), key, fingerprint, h.clock.Now(), h.clock.Now().Add(time.Hour))
	if err != nil || !won {
		t.Fatalf("first claim: won=%v %v", won, err)
	}
	if err := h.store.Complete(ctx, h.user.String(), key, http.StatusCreated,
		map[string]string{"Content-Type": "application/json"}, []byte(`{"ok":true}`), h.clock.Now()); err != nil {
		t.Fatal(err)
	}

	// A brand-new store over the same database — a restarted process.
	fresh := idempotency.New(h.pool)
	won, existing, err := fresh.Claim(ctx, h.user.String(), h.facility.String(), key, fingerprint, h.clock.Now(), h.clock.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if won {
		t.Fatal("a restarted process re-claimed a held key")
	}
	if !existing.Complete || existing.Status != http.StatusCreated || string(existing.Body) != `{"ok":true}` {
		t.Errorf("the record did not come back: %+v", existing)
	}
	if existing.Headers["Content-Type"] != "application/json" {
		t.Errorf("headers: %+v", existing.Headers)
	}
}

// Release is what makes a failure retryable rather than cached.
func TestAReleasedClaimCanBeClaimedAgain(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	fingerprint := make([]byte, 32)

	if won, _, err := h.store.Claim(ctx, h.user.String(), h.facility.String(), key, fingerprint, h.clock.Now(), h.clock.Now().Add(time.Hour)); err != nil || !won {
		t.Fatalf("claim: %v %v", won, err)
	}
	if err := h.store.Release(ctx, h.user.String(), key); err != nil {
		t.Fatal(err)
	}
	won, _, err := h.store.Claim(ctx, h.user.String(), h.facility.String(), key, fingerprint, h.clock.Now(), h.clock.Now().Add(time.Hour))
	if err != nil || !won {
		t.Fatalf("the released key could not be claimed again: won=%v %v", won, err)
	}

	// A completed record, on the other hand, is not released by a stray call: the response
	// must stay replayable for its whole TTL.
	if err := h.store.Complete(ctx, h.user.String(), key, http.StatusOK, nil, []byte(`{}`), h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if err := h.store.Release(ctx, h.user.String(), key); err != nil {
		t.Fatal(err)
	}
	if won, _, err := h.store.Claim(ctx, h.user.String(), h.facility.String(), key, fingerprint, h.clock.Now(), h.clock.Now().Add(time.Hour)); err != nil || won {
		t.Errorf("a completed record was released: won=%v %v", won, err)
	}
}

// The table has a TTL and something that enforces it — the risk the plan names for this
// checkpoint is exactly "idempotency table growth".
func TestExpiredRecordsArePurged(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	fingerprint := make([]byte, 32)
	now := h.clock.Now()

	for i, expiry := range []time.Time{now.Add(time.Minute), now.Add(2 * time.Minute), now.Add(3 * time.Hour)} {
		k := fmt.Sprintf("0190a8f2-0000-7000-8000-00000000%04d", i)
		if _, _, err := h.store.Claim(ctx, h.user.String(), h.facility.String(), k, fingerprint, now, expiry); err != nil {
			t.Fatal(err)
		}
	}

	// The cutoff is the cleanup job's "now", an hour later: two records have expired.
	removed, err := h.store.Purge(ctx, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Errorf("purged %d, want 2", removed)
	}
	var left int
	if err := h.db.SQL.QueryRow(`SELECT count(*) FROM ops.idempotency_record`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 1 {
		t.Errorf("%d rows left, want the unexpired one", left)
	}
}

// The application role owns this table — it must, since it writes and deletes here — but
// the shape rules are the database's, not the application's.
func TestTheTableRefusesAMalformedRecord(t *testing.T) {
	h := newHarness(t)
	for name, statement := range map[string]string{
		"a key that is too short": `INSERT INTO ops.idempotency_record (facility_id, user_id, key, fingerprint, expires_at)
			VALUES ($1, $2, 'short', repeat('x', 32)::bytea, now() + interval '1 hour')`,
		"a fingerprint of the wrong length": `INSERT INTO ops.idempotency_record (facility_id, user_id, key, fingerprint, expires_at)
			VALUES ($1, $2, '0190a8f2-0000-7000-8000-0000000000bb', 'short'::bytea, now() + interval '1 hour')`,
		"an unknown state": `INSERT INTO ops.idempotency_record (facility_id, user_id, key, fingerprint, state, expires_at)
			VALUES ($1, $2, '0190a8f2-0000-7000-8000-0000000000bb', repeat('x', 32)::bytea, 'maybe', now() + interval '1 hour')`,
		"complete with no response": `INSERT INTO ops.idempotency_record (facility_id, user_id, key, fingerprint, state, expires_at)
			VALUES ($1, $2, '0190a8f2-0000-7000-8000-0000000000bb', repeat('x', 32)::bytea, 'complete', now() + interval '1 hour')`,
		"an expiry before the claim": `INSERT INTO ops.idempotency_record (facility_id, user_id, key, fingerprint, claimed_at, expires_at)
			VALUES ($1, $2, '0190a8f2-0000-7000-8000-0000000000bb', repeat('x', 32)::bytea, now(), now() - interval '1 hour')`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := h.db.SQL.Exec(statement, h.facility, h.user); err == nil {
				t.Errorf("%s was accepted", name)
			}
		})
	}
}
