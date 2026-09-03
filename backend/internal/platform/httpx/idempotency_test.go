package httpx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
)

// The Idempotency-Key middleware's protocol (CP24). The Postgres store has its own tests;
// what is asserted here is what the middleware decides, against a store that does exactly
// what a correct store does and nothing more.

type memoryStore struct {
	mu      sync.Mutex
	records map[string]*IdempotencyRecord
	claims  int
	failOn  string
}

func newMemoryStore() *memoryStore {
	return &memoryStore{records: map[string]*IdempotencyRecord{}}
}

func (m *memoryStore) Claim(_ context.Context, userID, _, key string, fingerprint []byte, _, _ time.Time) (bool, IdempotencyRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failOn == "claim" {
		return false, IdempotencyRecord{}, errors.New("the store is down")
	}
	m.claims++
	id := userID + "/" + key
	if existing, held := m.records[id]; held {
		return false, *existing, nil
	}
	m.records[id] = &IdempotencyRecord{Fingerprint: fingerprint}
	return true, IdempotencyRecord{Fingerprint: fingerprint}, nil
}

func (m *memoryStore) Complete(_ context.Context, userID, key string, status int, headers map[string]string, body []byte, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	record, held := m.records[userID+"/"+key]
	if !held {
		return ErrNoClaim
	}
	record.Complete, record.Status, record.Headers, record.Body = true, status, headers, body
	return nil
}

func (m *memoryStore) Release(_ context.Context, userID, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.records, userID+"/"+key)
	return nil
}

func (m *memoryStore) held(userID, key string) (IdempotencyRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.records[userID+"/"+key]
	if !ok {
		return IdempotencyRecord{}, false
	}
	return *r, true
}

// idempotentChain is the middleware with a caller already on the context, which is where
// it sits in the real chain: after Authenticate, so a key is always scoped to a person.
func idempotentChain(store IdempotencyStore, handler http.HandlerFunc) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	withCaller := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), callerKey{}, Caller{
				UserID: "0190a8f2-0000-7000-8000-000000000001", FacilityID: "0190a8f2-0000-7000-8000-000000000003",
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	return withCaller(Idempotent(IdempotencyConfig{
		Store: store, Clock: clock.NewFixed(time.Date(2026, 9, 3, 4, 42, 0, 0, time.UTC)), Logger: logger,
	})(handler))
}

func post(t *testing.T, h http.Handler, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/visits/1/measurements", strings.NewReader(body))
	if key != "" {
		r.Header.Set(IdempotencyHeader, key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

const testKey = "0190a8f2-0000-7000-8000-0000000000aa"

// Criterion 2: a retried request with the same key returns the identical response body.
func TestARetryIsAnsweredFromTheStore(t *testing.T) {
	store := newMemoryStore()
	var runs int
	chain := idempotentChain(store, func(w http.ResponseWriter, r *http.Request) {
		runs++
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Location", "/v1/events/7")
		w.WriteHeader(http.StatusCreated)
		// The run count is in the body: if the handler ran twice the bodies differ, and
		// "identical" is then a claim the test can actually check.
		_, _ = fmt.Fprintf(w, `{"run":%d,"echo":%s}`, runs, body)
	})

	first := post(t, chain, testKey, `{"value":150}`)
	second := post(t, chain, testKey, `{"value":150}`)

	if runs != 1 {
		t.Fatalf("the handler ran %d times", runs)
	}
	if first.Body.String() != second.Body.String() {
		t.Errorf("bodies differ:\n first  %s\n second %s", first.Body, second.Body)
	}
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Errorf("statuses: %d then %d", first.Code, second.Code)
	}
	if got := second.Header().Get("Location"); got != "/v1/events/7" {
		t.Errorf("the replay lost its Location header: %q", got)
	}
	if got := second.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("the replay lost its Content-Type: %q", got)
	}
	if first.Header().Get(IdempotencyReplayedHeader) != "" {
		t.Error("the first response is marked as a replay")
	}
	if second.Header().Get(IdempotencyReplayedHeader) != "true" {
		t.Error("the replay is not marked as one")
	}
}

// Two retries in flight at once: exactly one runs the handler, and the other is told to
// wait rather than being handed a half-written answer.
func TestConcurrentRetriesRunTheHandlerOnce(t *testing.T) {
	store := newMemoryStore()
	release := make(chan struct{})
	var mu sync.Mutex
	var runs int
	chain := idempotentChain(store, func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		runs++
		mu.Unlock()
		<-release
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- post(t, chain, testKey, `{"value":150}`) }()

	// The second arrives while the first is still inside the handler.
	var inProgress *httptest.ResponseRecorder
	for i := 0; i < 200; i++ {
		mu.Lock()
		started := runs > 0
		mu.Unlock()
		if started {
			inProgress = post(t, chain, testKey, `{"value":150}`)
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	first := <-done

	if inProgress == nil {
		t.Fatal("the handler never started")
	}
	if inProgress.Code != http.StatusConflict || !strings.Contains(inProgress.Body.String(), "IDEMPOTENCY_IN_PROGRESS") {
		t.Errorf("the concurrent retry got %d: %s", inProgress.Code, inProgress.Body)
	}
	if first.Code != http.StatusCreated {
		t.Errorf("the first attempt got %d", first.Code)
	}
	if runs != 1 {
		t.Errorf("the handler ran %d times", runs)
	}

	// And once it has settled, the retry is answered.
	replayed := post(t, chain, testKey, `{"value":150}`)
	if replayed.Body.String() != first.Body.String() {
		t.Errorf("the settled replay differs: %s vs %s", replayed.Body, first.Body)
	}
}

// A key reused for a different request is refused. Answering it with the first request's
// response would be the worse failure: the client would believe a write happened that did
// not.
func TestAKeyReusedForADifferentRequestIsRefused(t *testing.T) {
	store := newMemoryStore()
	chain := idempotentChain(store, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	post(t, chain, testKey, `{"value":150}`)
	reused := post(t, chain, testKey, `{"value":151}`)

	if reused.Code != http.StatusConflict || !strings.Contains(reused.Body.String(), "IDEMPOTENCY_KEY_REUSED") {
		t.Fatalf("a reused key got %d: %s", reused.Code, reused.Body)
	}
	if strings.Contains(reused.Body.String(), `"ok":true`) {
		t.Error("the first request's response was handed to a different request")
	}
}

// A failure is not an answer. A 500 releases the claim so that the client's next retry
// runs the handler again rather than meeting a cached failure for a day.
func TestAFailedAttemptIsNotRemembered(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		cached bool
	}{
		"a server error":     {http.StatusInternalServerError, false},
		"a refusal":          {http.StatusForbidden, false},
		"an expired token":   {http.StatusUnauthorized, false},
		"a rate limit":       {http.StatusTooManyRequests, false},
		"a validation error": {http.StatusUnprocessableEntity, true},
		"a conflict":         {http.StatusConflict, true},
		"a created":          {http.StatusCreated, true},
	} {
		t.Run(name, func(t *testing.T) {
			store := newMemoryStore()
			chain := idempotentChain(store, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"body":"x"}`))
			})
			post(t, chain, testKey, `{"value":150}`)
			record, held := store.held("0190a8f2-0000-7000-8000-000000000001", testKey)
			switch {
			case tc.cached && (!held || !record.Complete):
				t.Errorf("a %d was not remembered", tc.status)
			case !tc.cached && held:
				t.Errorf("a %d was remembered and will be replayed for a day", tc.status)
			}
		})
	}
}

// A response too large to keep is served normally and simply not cached: the ledger's
// event_id guarantee is underneath it either way.
func TestAnOversizedResponseIsServedAndNotCached(t *testing.T) {
	store := newMemoryStore()
	big := strings.Repeat("x", MaxIdempotentResponse+1024)
	chain := idempotentChain(store, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(big))
	})
	got := post(t, chain, testKey, `{"value":150}`)
	if got.Code != http.StatusOK || got.Body.Len() != len(big) {
		t.Fatalf("the client got %d and %d bytes", got.Code, got.Body.Len())
	}
	if _, held := store.held("0190a8f2-0000-7000-8000-000000000001", testKey); held {
		t.Error("an oversized response was cached")
	}
}

func TestTheMiddlewareIsInertWithoutAKey(t *testing.T) {
	store := newMemoryStore()
	var runs int
	chain := idempotentChain(store, func(w http.ResponseWriter, _ *http.Request) {
		runs++
		w.WriteHeader(http.StatusCreated)
	})
	post(t, chain, "", `{"value":150}`)
	post(t, chain, "", `{"value":150}`)
	if runs != 2 {
		t.Errorf("the handler ran %d times without a key", runs)
	}
	if store.claims != 0 {
		t.Errorf("%d claims were made without a key", store.claims)
	}
}

func TestAKeyMustLookLikeAKey(t *testing.T) {
	store := newMemoryStore()
	chain := idempotentChain(store, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) })
	for _, key := range []string{"short", strings.Repeat("k", 201)} {
		got := post(t, chain, key, `{}`)
		if got.Code != http.StatusUnprocessableEntity {
			t.Errorf("key %q got %d, want a validation error", key[:min(len(key), 12)], got.Code)
		}
	}
}

// The handler must not run when the store cannot be reached: proceeding would mean a write
// nobody can replay, which is precisely what a client with no answer will retry.
func TestAStoreFailureRefusesRatherThanRunningTheHandler(t *testing.T) {
	store := newMemoryStore()
	store.failOn = "claim"
	var runs int
	chain := idempotentChain(store, func(w http.ResponseWriter, _ *http.Request) {
		runs++
		w.WriteHeader(http.StatusCreated)
	})
	got := post(t, chain, testKey, `{}`)
	if got.Code != http.StatusInternalServerError {
		t.Errorf("status = %d", got.Code)
	}
	if runs != 0 {
		t.Error("the handler ran despite the claim failing")
	}
}

// The fingerprint is method, path and body — and nothing else. A retry that carries a
// refreshed token or a new correlation id is still the same request.
func TestTheFingerprintIsTheRequestAndNotItsHeaders(t *testing.T) {
	body := []byte(`{"value":150}`)
	same := requestFingerprint(http.MethodPost, "/v1/x", body)
	if string(requestFingerprint(http.MethodPost, "/v1/x", body)) != string(same) {
		t.Error("the fingerprint is not stable")
	}
	for name, other := range map[string][]byte{
		"the method": requestFingerprint(http.MethodPut, "/v1/x", body),
		"the path":   requestFingerprint(http.MethodPost, "/v1/y", body),
		"the body":   requestFingerprint(http.MethodPost, "/v1/x", []byte(`{"value":151}`)),
	} {
		if string(other) == string(same) {
			t.Errorf("changing %s did not change the fingerprint", name)
		}
	}
}

// The middleware in the chain the service actually assembles (CP24).
//
// The unit tests above drive the middleware directly. This one goes through NewRouter,
// because that is where `Required` is set, and a contract the router does not enforce is a
// contract in name only.
func TestTheRouterRequiresAKeyOnEveryStateChangingRequest(t *testing.T) {
	store := newMemoryStore()
	var runs int
	router, err := NewRouter(RouterOptions{
		Logger: testLogger(), IDs: &ids.Sequential{Prefix: "req"}, MaxBodyBytes: 1 << 20,
		RequestTimeout: time.Second, Health: &Health{Logger: testLogger()},
		Authenticator: fakeAuthenticator{caller: Caller{
			UserID: "0190a8f2-0000-7000-8000-000000000001", FacilityID: "0190a8f2-0000-7000-8000-000000000003",
		}},
		Authorizer:  &fakeAuthorizer{allow: true},
		Idempotency: store,
		Clock:       clock.NewFixed(time.Date(2026, 9, 3, 4, 42, 0, 0, time.UTC)),
		Routes: func(r chi.Router) {
			r.Method(http.MethodPost, "/things", Declare(Public(), func(w http.ResponseWriter, _ *http.Request) {
				runs++
				w.WriteHeader(http.StatusCreated)
				_, _ = fmt.Fprintf(w, `{"run":%d}`, runs)
			}))
			r.Method(http.MethodGet, "/things", Declare(Public(), func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
		},
	})
	if err != nil {
		t.Fatalf("building the router: %v", err)
	}

	send := func(method, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/v1/things", strings.NewReader(`{"value":150}`))
		req.Header.Set("Authorization", "Bearer t")
		req.Header.Set(RequestedWithHeader, RequestedWithValue)
		if key != "" {
			req.Header.Set(IdempotencyHeader, key)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	// A read needs no key.
	if got := send(http.MethodGet, ""); got.Code != http.StatusOK {
		t.Errorf("a read without a key got %d: %s", got.Code, got.Body)
	}
	// A write without one is refused before the handler runs.
	if got := send(http.MethodPost, ""); got.Code != http.StatusUnprocessableEntity {
		t.Errorf("a write without a key got %d: %s", got.Code, got.Body)
	}
	if runs != 0 {
		t.Fatalf("the handler ran %d times for a request with no key", runs)
	}
	// With one, it runs once however many times it is sent.
	first := send(http.MethodPost, testKey)
	second := send(http.MethodPost, testKey)
	if runs != 1 {
		t.Errorf("the handler ran %d times", runs)
	}
	if first.Body.String() != second.Body.String() || second.Header().Get(IdempotencyReplayedHeader) != "true" {
		t.Errorf("the retry was not replayed: %s / %s", first.Body, second.Body)
	}
}
