package audit_test

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/audit"
)

// Criterion 2: every audited action renders as a correct human sentence in both languages.

var placeholder = regexp.MustCompile(`\{([a-z_]+)\}`)

// recordedKinds is every kind the code base records, listed by hand so that adding a
// Record call somewhere without a sentence fails here rather than at runtime. The
// registry test below walks the other direction: every sentence has both languages.
var recordedKinds = []string{
	// auth/sessions.go, auth/http.go, auth/secondfactor.go
	"session.login", "session.login_failed", "session.logout", "session.step_up",
	// auth/admin.go
	"user.invited", "user.status_changed", "role.granted", "role.revoked",
	"sessions.ended", "password.set", "second_factor.reset",
	// audit
	"break_glass.opened", "break_glass.acknowledged", "break_glass.ended",
	"audit.exported", "audit.verified", "audit.chain_broken",
}

func TestEveryRecordedKindHasASentenceInBothLanguages(t *testing.T) {
	for _, kind := range recordedKinds {
		s, ok := audit.Kinds[kind]
		if !ok {
			t.Errorf("%s is recorded but has no sentence", kind)
			continue
		}
		if strings.TrimSpace(s.EN) == "" || strings.TrimSpace(s.BN) == "" || s.LabelEN == "" || s.LabelBN == "" {
			t.Errorf("%s: a template or label is empty", kind)
		}
		// The two languages must name the same facts: a placeholder in one and not the
		// other is a sentence that tells one reader less than the other.
		en := placeholders(s.EN)
		bn := placeholders(s.BN)
		if en != bn {
			t.Errorf("%s: EN uses %s, BN uses %s", kind, en, bn)
		}
		if strings.ContainsAny(s.BN, "abcdefghijklmnopqrstuvwxyz") && !placeholder.MatchString(s.BN) {
			t.Errorf("%s: the Bengali template contains Latin text outside a placeholder", kind)
		}
	}
	for kind := range audit.Kinds {
		if !contains(recordedKinds, kind) {
			t.Errorf("%s has a sentence but nothing records it; drop it or record it", kind)
		}
	}
}

func placeholders(tpl string) string {
	set := map[string]bool{}
	for _, m := range placeholder.FindAllStringSubmatch(tpl, -1) {
		set[m[1]] = true
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	// Sorted for a stable comparison.
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return strings.Join(keys, ",")
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestSentencesReadAsTheBlueprintAsks(t *testing.T) {
	at := time.Date(2026, 9, 3, 4, 42, 0, 0, time.UTC) // 10:42 in Dhaka
	target := uuid.New()
	cases := []struct {
		ev     audit.Event
		en, bn string
	}{
		{
			ev: audit.Event{RecordedAt: at, Entry: audit.Entry{
				Kind: "role.granted", ActorCode: "A001", TargetUserID: &target, TargetCode: "N006",
				Details: map[string]any{"role": "NUTRITIONIST"},
			}},
			en: "10:42 — A001 granted NUTRITIONIST to N006",
			bn: "10:42 — A001 N006-কে NUTRITIONIST ভূমিকা দিয়েছেন",
		},
		{
			ev: audit.Event{RecordedAt: at, Entry: audit.Entry{
				Kind: "user.status_changed", ActorCode: "A001", TargetCode: "P001", Reason: "on leave until October",
				Details: map[string]any{"before": "active", "after": "suspended"},
			}},
			en: "10:42 — A001 changed P001 from active to suspended: on leave until October",
			bn: "10:42 — A001 P001-কে active থেকে suspended করেছেন: on leave until October",
		},
		{
			ev: audit.Event{RecordedAt: at, Entry: audit.Entry{
				Kind: "sessions.ended", ActorCode: "A001", TargetCode: "N002", Reason: "lost tablet",
				Details: map[string]any{"count": float64(2)}, // as it comes back from JSON
			}},
			en: "10:42 — A001 signed N002 out of 2 session(s): lost tablet",
			bn: "10:42 — A001 N002-কে 2টি সেশন থেকে সাইন আউট করেছেন: lost tablet",
		},
		{
			ev: audit.Event{RecordedAt: at, Entry: audit.Entry{
				Kind: "break_glass.opened", ActorCode: "JD01", Reason: "unconscious patient, regular physician away",
				Details: map[string]any{"scope": "patient 0190a8f2-0000-7000-8000-0000000000b1", "until": "2026-09-03T08:42:00Z"},
			}},
			en: "10:42 — JD01 broke the glass for patient 0190a8f2-0000-7000-8000-0000000000b1 until 14:42: unconscious patient, regular physician away",
			bn: "10:42 — JD01 14:42 পর্যন্ত patient 0190a8f2-0000-7000-8000-0000000000b1-এর জন্য জরুরি প্রবেশাধিকার নিয়েছেন: unconscious patient, regular physician away",
		},
		{
			// A failed sign-in for a code nobody holds: no actor, and the sentence says so
			// with a dash rather than a blank.
			ev: audit.Event{RecordedAt: at, Entry: audit.Entry{
				Kind: "session.login_failed", Details: map[string]any{"failure": "no_such_user"},
			}},
			en: "10:42 — a sign-in for — was refused (no_such_user)",
			bn: "10:42 — —-এর সাইন ইন প্রত্যাখ্যাত হয়েছে (no_such_user)",
		},
		{
			ev: audit.Event{RecordedAt: at, Entry: audit.Entry{
				Kind: "user.invited", ActorCode: "A001", TargetCode: "N006",
				Details: map[string]any{"roles": []any{"NUTRITIONIST", "COUNSELOR"}, "password_set": true},
			}},
			en: "10:42 — A001 created an account for N006 with roles NUTRITIONIST, COUNSELOR",
			bn: "10:42 — A001 N006-এর জন্য NUTRITIONIST, COUNSELOR ভূমিকাসহ অ্যাকাউন্ট তৈরি করেছেন",
		},
	}
	for _, c := range cases {
		if got := audit.Render(c.ev, audit.English); got != c.en {
			t.Errorf("%s EN:\n got %q\nwant %q", c.ev.Kind, got, c.en)
		}
		if got := audit.Render(c.ev, audit.Bangla); got != c.bn {
			t.Errorf("%s BN:\n got %q\nwant %q", c.ev.Kind, got, c.bn)
		}
	}
}

func TestAnUnknownKindStillRenders(t *testing.T) {
	ev := audit.Event{RecordedAt: time.Now(), Entry: audit.Entry{Kind: "future.thing"}}
	if got := audit.Describe(ev, audit.English); got != "future.thing" {
		t.Errorf("got %q", got)
	}
	if audit.Known("future.thing") {
		t.Error("an unregistered kind must not be Known")
	}
}
