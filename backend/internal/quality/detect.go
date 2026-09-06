package quality

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Pattern detection (CP63, criterion 2).
//
// # When it runs
//
// After a correction is applied or rejected, on the operator whose record it lands on, in the
// request that answered it. Not on a schedule: a supervisor who is told about a pattern the
// morning after it completed can still act on it, and a nightly job is one more thing that can
// stop without anybody noticing (ADR-0029).
//
// It is deliberately cheap — three counts over an indexed window — and deliberately idempotent:
// a unique index refuses a second open flag for the same operator and threshold, so running it
// twice, or on every correction all day, raises one flag.
//
// # What it does not do
//
// It does not decide anything about anybody. It raises a row that says "these three corrections
// have the same shape; here is the suggested first question". The threshold behind it is
// unapproved until a clinician approves it, and the flag says so.

// Detector notices patterns and raises flags.
type Detector struct {
	store  *Store
	clock  interface{ Now() time.Time }
	audit  AuditSink
	notify Notifier
}

// AuditSink records that a flag was raised, on the security audit trail.
//
// An interface rather than an import: `quality` may not import `audit` (architecture.json), and
// the translation lives in `cmd/api` — the same bridge pattern as the critical-value alert.
type AuditSink interface {
	QualityFlagRaised(ctx context.Context, flag Flag) (int64, error)
	QualityFlagResolved(ctx context.Context, flag Flag) (int64, error)
}

// Notifier puts a raised flag in front of a supervisor, and in front of the operator it is
// about. Both, and the second is not optional: a flag somebody learns about from their
// supervisor first is a flag that felt like an ambush.
type Notifier interface {
	// PublishQualityFlag is named apart from the audit sink's method because one bridge
	// implements both, and Go has no room for two methods of the same name on one type. The
	// distinction is real anyway: one is a record, the other is a screen.
	PublishQualityFlag(ctx context.Context, flag Flag)
}

// NewDetector builds one.
func NewDetector(store *Store, clk interface{ Now() time.Time }, sink AuditSink, notify Notifier) *Detector {
	return &Detector{store: store, clock: clk, audit: sink, notify: notify}
}

// Review looks at one operator's recent record and raises whatever it finds.
//
// Returns the flags it raised, which is usually none. An error from the audit sink or the
// notifier is **not** returned: the flag is in the database, and losing it because a downstream
// notification failed would be the wrong trade — a supervisor who has to look at a list is a
// smaller failure than a pattern that was noticed and then dropped.
func (d *Detector) Review(ctx context.Context, facility, operator uuid.UUID) ([]Flag, error) {
	thresholds, err := d.store.Thresholds(ctx)
	if err != nil {
		return nil, err
	}
	now := d.clock.Now().UTC()

	// The same three corrections can satisfy more than one threshold — three mistyped heights
	// are both "repeated transcription errors" and "the same measurement going wrong", and both
	// are true. Raising both would hand a supervisor two flags about one conversation, and a
	// supervisor asked to have three conversations about three corrections will have none of
	// them. So a correction is evidence for **one** flag per pass, and the thresholds are
	// consulted in their own `ordering` — which is what that column is for, and why the
	// transcription pattern sorts first: "she is mistyping" is the more actionable reading, and
	// the instrument question is the second thing to check rather than a separate conversation.
	//
	// A partial overlap still raises. Three corrections that share one with an already-flagged
	// pattern are two different patterns with a coincidence in them.
	//
	// The set starts from the flags **already open** on this operator, not empty. Without that,
	// the second correction of the day would raise the same conversation under a different
	// threshold: the first pattern is blocked by its own open flag, its evidence is therefore
	// never marked spent, and the next threshold along picks up the identical corrections.
	spent, err := d.alreadyFlagged(ctx, facility, operator)
	if err != nil {
		return nil, err
	}

	var raised []Flag
	for _, threshold := range thresholds {
		flag, ok, err := d.check(ctx, facility, operator, threshold, now, spent)
		if err != nil {
			return raised, err
		}
		if ok {
			for _, item := range flag.Evidence {
				spent[item.RequestID] = true
			}
			raised = append(raised, flag)
		}
	}
	return raised, nil
}

// check tests one threshold and raises a flag if it is crossed.
func (d *Detector) check(ctx context.Context, facility, operator uuid.UUID,
	threshold Threshold, now time.Time, spent map[uuid.UUID]bool) (Flag, bool, error) {

	// An open flag for this pattern means somebody has already been asked to look. A second row
	// would be the same conversation twice — see the unique index, which is the real guard; this
	// is the cheap check that avoids doing the counting at all.
	if _, err := d.store.q.OpenQualityFlagFor(ctx, openFlagParams(operator, threshold.Code)); err == nil {
		return Flag{}, false, nil
	} else if !isNoRows(err) {
		return Flag{}, false, err
	}

	from := now.AddDate(0, 0, -threshold.WindowDays)
	evidence, err := d.store.supporting(ctx, facility, operator, threshold, from, now, spent)
	if err != nil {
		return Flag{}, false, err
	}
	if len(evidence) < threshold.MinCount {
		return Flag{}, false, nil
	}

	// The denominator, and the floor under it. Three corrections out of five entries is a new
	// operator on their first morning; a system that flags them for retraining on their first
	// morning teaches a clinic's staff to stop asking for help.
	entries, err := d.store.entries(ctx, facility, operator, from, now)
	if err != nil {
		return Flag{}, false, err
	}
	if entries < threshold.MinEntries {
		return Flag{}, false, nil
	}

	sortEvidence(evidence)
	payload, err := json.Marshal(evidence)
	if err != nil {
		return Flag{}, false, err
	}

	flag, raised, err := d.store.raise(ctx, facility, operator, threshold, from, now,
		len(evidence), entries, payload)
	if err != nil || !raised {
		return Flag{}, false, err
	}

	// The record on the security audit trail, and the two notifications. Neither failure loses
	// the flag — see the note on Review.
	if d.audit != nil {
		if seq, err := d.audit.QualityFlagRaised(ctx, flag); err == nil && seq > 0 {
			_ = d.store.attachAudit(ctx, flag.ID, seq)
		}
	}
	if d.notify != nil {
		d.notify.PublishQualityFlag(ctx, flag)
	}
	return flag, true, nil
}

// alreadyFlagged is every correction that is already evidence for an open flag on this operator.
func (d *Detector) alreadyFlagged(ctx context.Context, facility, operator uuid.UUID) (map[uuid.UUID]bool, error) {
	open, err := d.store.Flags(ctx, facility, &operator, true, 50, nil)
	if err != nil {
		return nil, err
	}
	spent := map[uuid.UUID]bool{}
	for _, flag := range open {
		for _, item := range flag.Evidence {
			spent[item.RequestID] = true
		}
	}
	return spent, nil
}

// supporting is the corrections a threshold counts, narrowed by its own shape.
//
// The narrowing is the whole of the pattern detection, and each arm is one of §4.3's three:
//
//   - TRANSCRIPTION counts only reasons flagged as transcription in the vocabulary, which is why
//     that flag is on `core.correction_reason` rather than being inferred from free text.
//   - SAME_CODE counts per measurement code and needs the threshold met **within one code**, so
//     it re-counts rather than filtering: three different measurements corrected once each is
//     not a pattern.
//   - END_OF_SHIFT counts corrections on values recorded after a wall-clock hour in Faridpur.
//
// `spent` is the corrections that already belong to a flag. A shape whose every correction is
// already spoken for produces nothing, and — for SAME_CODE, which has one candidate per
// measurement — the search moves on to the next measurement rather than giving up. That
// distinction is the difference between "three mistyped heights and three misread weights are
// two conversations" and "the heights were flagged, so the weights are invisible".
func (s *Store) supporting(ctx context.Context, facility, operator uuid.UUID,
	threshold Threshold, from, to time.Time, spent map[uuid.UUID]bool) ([]Evidence, error) {

	switch threshold.Pattern {
	case Transcription:
		return s.unspent(ctx, facility, operator, from, to, true, nil, nil, threshold, spent)

	case EndOfShift:
		return s.unspent(ctx, facility, operator, from, to, false, nil, threshold.AfterHour,
			threshold, spent)

	case SameCode:
		// Every code the operator was corrected on, then the first one that on its own meets the
		// threshold with corrections nobody has flagged yet. "Three corrections across three
		// different measurements" is a busy month; "three corrections on the same measurement"
		// is a question about an instrument.
		codes, err := s.q.CorrectionsByCode(ctx, byCodeParams(facility, operator, from, to))
		if err != nil {
			return nil, err
		}
		for _, row := range codes {
			if int(row.Corrections) < threshold.MinCount {
				continue
			}
			code := row.Code
			found, err := s.unspent(ctx, facility, operator, from, to, false, &code, nil,
				threshold, spent)
			if err != nil {
				return nil, err
			}
			if len(found) >= threshold.MinCount {
				return found, nil
			}
		}
		return nil, nil
	}
	return nil, fmt.Errorf("%w: %s", ErrUnknownThreshold, threshold.Pattern)
}

// unspent is `evidence` with the already-flagged corrections removed, or nothing when what is
// left no longer meets the threshold.
func (s *Store) unspent(ctx context.Context, facility, operator uuid.UUID, from, to time.Time,
	transcriptionOnly bool, code *string, afterHour *int,
	threshold Threshold, spent map[uuid.UUID]bool) ([]Evidence, error) {

	all, err := s.evidence(ctx, facility, operator, from, to, transcriptionOnly, code, afterHour)
	if err != nil || len(spent) == 0 {
		return all, err
	}
	fresh := make([]Evidence, 0, len(all))
	for _, item := range all {
		if !spent[item.RequestID] {
			fresh = append(fresh, item)
		}
	}
	if len(fresh) < threshold.MinCount {
		return nil, nil
	}
	return fresh, nil
}

// Describe renders what a threshold looks for, in both languages.
//
// One language was the first version, and on the one screen whose entire purpose is that an
// operator can read the rule that measures them, an English-only sentence in front of a Bangla
// reader is not a rendering choice. Every other display string in this API is a pair; so is this.
//
// It is rendered from the row rather than stored, so that numbers which change cannot leave a
// sentence describing the old ones behind them.
func Describe(threshold Threshold) (english, bangla string) {
	en := []string{fmt.Sprintf("%d in %d days", threshold.MinCount, threshold.WindowDays)}
	bn := []string{fmt.Sprintf("%s দিনে %s বার", bengali(threshold.WindowDays), bengali(threshold.MinCount))}

	switch threshold.Pattern {
	case Transcription:
		en = append(en, "counting only the reasons that mean a number was mistyped")
		bn = append(bn, "শুধু যেসব কারণ বলে সংখ্যাটি ভুল লেখা হয়েছিল")
	case SameCode:
		en = append(en, "on one and the same measurement")
		bn = append(bn, "একই মাপের ক্ষেত্রে")
	case EndOfShift:
		if threshold.AfterHour != nil {
			en = append(en, fmt.Sprintf("on values recorded after %02d:00", *threshold.AfterHour))
			bn = append(bn, fmt.Sprintf("%s টার পরে নেওয়া মানের ক্ষেত্রে", bengali(*threshold.AfterHour)))
		}
	}
	if threshold.MinEntries > 0 {
		en = append(en, fmt.Sprintf("and only once there are at least %d entries", threshold.MinEntries))
		bn = append(bn, fmt.Sprintf("এবং অন্তত %sটি এন্ট্রি থাকলে তবেই", bengali(threshold.MinEntries)))
	}
	return strings.Join(en, ", "), strings.Join(bn, ", ")
}

// bengali renders a number in Bengali digits.
//
// Not decoration: a Latin numeral inside a Bangla sentence is the thing that makes an interface
// read as translated rather than written, and the numbers are the part of this sentence a reader
// is actually looking for.
func bengali(n int) string {
	digits := []rune("০১২৩৪৫৬৭৮৯")
	out := []rune{}
	if n == 0 {
		return string(digits[0])
	}
	for _, r := range fmt.Sprintf("%d", n) {
		out = append(out, digits[r-'0'])
	}
	return string(out)
}

func isNoRows(err error) bool { return err != nil && err == pgx.ErrNoRows }
