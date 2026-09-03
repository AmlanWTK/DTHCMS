// Package audit is the security audit log (CP22): the append-only, hash-chained record of
// what happened to the system — sign-ins, role changes, credential resets, exports,
// break-glass — and the renderer that turns each row into a sentence a person can read
// in either language.
//
// It is the second of the two trails the blueprint asks for (§4.5). The clinical ledger
// (CP23) records what happened to patients; this records what happened to access. The two
// are separate tables in the same append-only schema, and the same rule holds for both:
// the application may add a row and read a row, and the database refuses everything else.
//
// The module depends on platform only. Callers that live in auth hand it entries through
// an interface auth declares (auth.AuditRecorder); the composition root joins the two.
package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// Entry is one thing that happened, as the caller describes it. The recorder adds the
// sequence, the time and the chain.
type Entry struct {
	// Kind names the event, dotted: "role.granted", "session.login". Every kind must be
	// in the sentence registry; Record refuses one that is not, because a row nobody can
	// read is a row nobody will review.
	Kind       string
	FacilityID uuid.UUID

	ActorID   *uuid.UUID
	ActorCode string
	ActorRole string

	TargetUserID *uuid.UUID
	TargetCode   string

	PatientID *uuid.UUID
	DeviceID  *uuid.UUID
	SessionID *uuid.UUID

	Reason string
	// Details are the small facts the sentence needs — the role code, the status before
	// and after, a count. Never a secret, never a clinical value.
	Details map[string]any

	ClientDigest []byte
	// At is when it happened; zero means "now" by the recorder's clock.
	At time.Time
}

// Event is an Entry once it is in the chain.
type Event struct {
	Entry
	Seq        int64
	RecordedAt time.Time
	PrevHash   []byte
	Hash       []byte
}

// ErrUnknownKind is returned for an entry whose kind has no sentence.
var ErrUnknownKind = errors.New("audit: unknown event kind")

// Genesis is the prev_hash of the first row: 32 zero bytes.
var Genesis = make([]byte, sha256.Size)

// hashOf computes a row's hash: SHA-256 over the previous hash and the canonical form of
// every field — sequence, time and all — so that a change to any of them, or a row lifted
// out of the middle, leaves the chain disagreeing with itself from that point on.
//
// Canonical form is a fixed field order with lengths in front of variable parts, not
// JSON, so that a JSON library's choices (key order, escaping, number formatting) can
// never make two verifiers disagree. Details are sorted by key.
func hashOf(prev []byte, seq int64, recordedAt time.Time, e Entry) []byte {
	h := sha256.New()
	h.Write(prev)
	write := func(s string) {
		h.Write([]byte(strconv.Itoa(len(s))))
		h.Write([]byte{':'})
		h.Write([]byte(s))
	}
	write(strconv.FormatInt(seq, 10))
	write(recordedAt.UTC().Format(time.RFC3339Nano))
	write(e.FacilityID.String())
	write(e.Kind)
	write(uuidString(e.ActorID))
	write(e.ActorCode)
	write(e.ActorRole)
	write(uuidString(e.TargetUserID))
	write(e.TargetCode)
	write(uuidString(e.PatientID))
	write(uuidString(e.DeviceID))
	write(uuidString(e.SessionID))
	write(e.Reason)
	write(string(canonicalDetails(e.Details)))
	write(string(e.ClientDigest))
	return h.Sum(nil)
}

func uuidString(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

// canonicalDetails is the details map as JSON with sorted keys and no whitespace — the
// one form both the recorder and the verifier produce for the same map.
func canonicalDetails(details map[string]any) []byte {
	if len(details) == 0 {
		return []byte("{}")
	}
	keys := make([]string, 0, len(details))
	for k := range details {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := json.Marshal(details[k])
		if err != nil {
			vb, _ = json.Marshal(fmt.Sprint(details[k]))
		}
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

// detailsFromJSON turns the stored form back into the map the hash was computed over.
// JSON numbers decode as float64 both here and when the recorder marshals a Go int, which
// is what keeps a re-read row hashing the same as it did when written.
func detailsFromJSON(raw []byte) map[string]any {
	out := map[string]any{}
	if len(raw) == 0 {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

// ChainFunc computes a row's hash from what precedes it and what it holds.
type ChainFunc func(prevHash []byte, seq int64, at time.Time, e Entry) []byte

// Store is what the recorder needs from the database.
type Store interface {
	// Append assigns the next sequence, chains and writes the row, all under a lock that
	// serialises appends so the sequence is gapless and the chain linear.
	Append(ctx context.Context, e Entry, chain ChainFunc, at time.Time) (Event, error)
	// Walk reads the chain forwards from a sequence, in slices.
	Walk(ctx context.Context, fromSeq int64, limit int) ([]Event, error)
	// Head is the last event, or ok=false for an empty chain.
	Head(ctx context.Context) (Event, bool, error)
	// Strays counts rows in the default partition — zero unless the monthly partitions
	// were not created.
	Strays(ctx context.Context) (int64, error)
	Query(ctx context.Context, q Query) ([]Event, error)
}

// Query is what the viewer narrows to. Zero values mean "any".
type Query struct {
	FacilityID  uuid.UUID
	Before      int64
	Kind        string
	ActorCode   string
	SubjectCode string
	PatientID   *uuid.UUID
	Since       time.Time
	Until       time.Time
	Limit       int
}
