package consent_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/consent"
)

// The gate, without a database (CP36).
//
// What is worth asserting here is the part a database test would hide: that the cache is a
// performance decision and not a correctness one, and that a gate whose read fails refuses
// rather than allows.

type fakeReader struct {
	record consent.Record
	err    error
	reads  int
}

func (f *fakeReader) One(context.Context, uuid.UUID, uuid.UUID, consent.Type) (consent.Record, error) {
	f.reads++
	if f.err != nil {
		return consent.Record{}, f.err
	}
	return f.record, nil
}

func TestTheGateCachesForSecondsAndNotForLonger(t *testing.T) {
	now := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	reader := &fakeReader{record: consent.Record{Status: consent.Granted}}
	gate := consent.NewGate(reader, func() time.Time { return now })
	patient, facility := uuid.New(), uuid.New()

	for range 5 {
		if err := gate.Check(context.Background(), patient, facility, consent.Communication); err != nil {
			t.Fatal(err)
		}
	}
	if reader.reads != 1 {
		t.Errorf("an outreach run asking five times made %d database calls", reader.reads)
	}

	// Past the window, it asks again. The window is the whole exposure of the revocation
	// criterion, which is why it is seconds and not minutes.
	now = now.Add(consent.CacheTTL + time.Second)
	if err := gate.Check(context.Background(), patient, facility, consent.Communication); err != nil {
		t.Fatal(err)
	}
	if reader.reads != 2 {
		t.Errorf("reads = %d; the cache did not expire", reader.reads)
	}
	if consent.CacheTTL > 30*time.Second {
		t.Errorf("CacheTTL is %s, which is most of §15.1's one-minute budget", consent.CacheTTL)
	}
}

func TestForgettingAPatientTakesEffectAtOnce(t *testing.T) {
	now := time.Now()
	reader := &fakeReader{record: consent.Record{Status: consent.Granted}}
	gate := consent.NewGate(reader, func() time.Time { return now })
	patient, facility := uuid.New(), uuid.New()

	if err := gate.Check(context.Background(), patient, facility, consent.Communication); err != nil {
		t.Fatal(err)
	}
	reader.record = consent.Record{Status: consent.Revoked}
	gate.Forget(patient)

	if err := gate.Check(context.Background(), patient, facility, consent.Communication); !errors.Is(err, consent.ErrDenied) {
		t.Fatalf("a revocation did not take effect after Forget: %v", err)
	}
}

func TestAGateThatCannotReadRefusesRatherThanAllows(t *testing.T) {
	// A gate that fails open is a gate that sends messages during a database incident.
	reader := &fakeReader{err: errors.New("the database is unreachable")}
	gate := consent.NewGate(reader, time.Now)

	if err := gate.Check(context.Background(), uuid.New(), uuid.New(), consent.Communication); err == nil {
		t.Fatal("a failed consent read was treated as consent")
	}
	if gate.Allows(context.Background(), uuid.New(), uuid.New(), consent.Communication) {
		t.Fatal("Allows returned true when the read failed")
	}
}

func TestEveryPurposeNamesTheConsentItNeeds(t *testing.T) {
	// The mapping is the one place a new kind of outbound action gets a consent attached to
	// it, and a purpose with no consent must not silently be allowed.
	want := map[consent.Purpose]consent.Type{
		consent.Treat:     consent.Care,
		consent.Remind:    consent.Communication,
		consent.Invite:    consent.Outreach,
		consent.Analyse:   consent.Research,
		consent.Interpret: consent.AIProcessing,
	}
	for purpose, kind := range want {
		got, ok := consent.Requires(purpose)
		if !ok || got != kind {
			t.Errorf("%s requires %q, want %q", purpose, got, kind)
		}
	}
	if _, ok := consent.Requires(consent.Purpose("anything-else")); ok {
		t.Error("an unknown purpose claims a consent")
	}
}
