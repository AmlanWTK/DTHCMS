package eventstore

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// Genesis is the prev_hash of an aggregate's first event: 32 zero bytes.
var Genesis = make([]byte, sha256.Size)

// hashOf is an event's hash: SHA-256 over the previous hash and the canonical form of
// every field the row holds — sequence and recorded time included — so that changing any
// of them, or lifting a row out of the middle, leaves the chain disagreeing with itself.
//
// The form is a fixed field order with lengths in front of variable parts. JSON fields
// are canonicalised (sorted keys, no whitespace, numbers as Go formats a float64) after a
// round trip through the same decoder the verifier uses, so what the database stores and
// what the writer hashed agree even though JSONB normalises the text.
func hashOf(prev []byte, seq, globalSeq int64, recordedAt time.Time, e Envelope) []byte {
	h := sha256.New()
	h.Write(prev)
	write := func(s string) {
		h.Write([]byte(strconv.Itoa(len(s))))
		h.Write([]byte{':'})
		h.Write([]byte(s))
	}
	write(strconv.FormatInt(seq, 10))
	write(strconv.FormatInt(globalSeq, 10))
	write(e.EventID.String())
	write(e.AggregateType)
	write(e.AggregateID.String())
	write(uuidString(e.PatientID))
	write(uuidString(e.VisitID))
	write(e.EventType)
	write(strconv.Itoa(e.EventVersion))
	write(e.OccurredAt.UTC().Format(time.RFC3339Nano))
	write(recordedAt.UTC().Format(time.RFC3339Nano))
	write(e.Actor.userID.String())
	write(e.Actor.deviceID.String())
	write(e.Actor.role)
	write(e.Actor.station)
	write(e.Actor.facilityID.String())
	write(string(e.Source))
	write(string(canonicalJSON(e.Payload)))
	write(string(canonicalJSON(e.Previous)))
	write(string(correctionJSON(e.Correction)))
	write(string(canonicalMap(e.Metadata)))
	return h.Sum(nil)
}

func uuidString(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

// canonicalJSON re-encodes any JSON document with sorted object keys and no whitespace.
// Numbers become float64 and back: "150.0" and "150" both hash as 150, which is what the
// database's numeric normalisation would have done to them anyway.
func canonicalJSON(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	return canonicalValue(v)
}

func canonicalMap(m map[string]any) []byte {
	if m == nil {
		return []byte("{}")
	}
	return canonicalValue(m)
}

func correctionJSON(c *Correction) []byte {
	if c == nil {
		return nil
	}
	return canonicalValue(map[string]any{
		"corrects_event_id": c.CorrectsEventID.String(), "reason_code": c.ReasonCode, "reason_text": c.ReasonText,
	})
}

// canonicalValue is encoding/json's output for a decoded value — which already sorts map
// keys — written by hand so the rule is visible: objects sorted by key, arrays in order,
// no whitespace.
func canonicalValue(v any) []byte {
	var buf bytes.Buffer
	writeCanonical(&buf, v)
	return buf.Bytes()
}

func writeCanonical(buf *bytes.Buffer, v any) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, _ := json.Marshal(k)
			buf.Write(kb)
			buf.WriteByte(':')
			writeCanonical(buf, t[k])
		}
		buf.WriteByte('}')
	case []any:
		buf.WriteByte('[')
		for i, item := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeCanonical(buf, item)
		}
		buf.WriteByte(']')
	default:
		b, err := json.Marshal(t)
		if err != nil {
			b, _ = json.Marshal(fmt.Sprint(t))
		}
		buf.Write(b)
	}
}

// anchorOf folds a day's event hashes, in global order, onto the previous day's anchor.
func anchorOf(prev []byte, day string, hashes [][]byte) []byte {
	h := sha256.New()
	h.Write(prev)
	h.Write([]byte(day))
	h.Write([]byte(strconv.Itoa(len(hashes))))
	for _, hash := range hashes {
		h.Write(hash)
	}
	return h.Sum(nil)
}
