package audit

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// The rendering layer (§4.5): every kind of audit event has one sentence in each language,
// and the sentence is made from the row — never stored, so a template corrected later
// corrects the whole history at once. One truth, two presentations.
//
// A template names its placeholders in braces. {actor} and {target} are the employee
// codes; {time} is the clock time in Dhaka; the rest come from Details by name. A
// placeholder with nothing to fill renders as "—", never as a blank, so a sentence with a
// hole in it is visibly incomplete rather than quietly misleading.

// Language is what the viewer asked for.
type Language string

const (
	English Language = "en"
	Bangla  Language = "bn"
)

// Sentence is a kind's two templates. Label is the kind's short name for a filter list.
type Sentence struct {
	LabelEN, LabelBN string
	EN, BN           string
}

// Kinds is the registry. A kind not in it cannot be recorded (Recorder.Record refuses), so
// adding an event to the system means adding its sentence here first, in both languages.
var Kinds = map[string]Sentence{
	// --- sessions ---
	"session.login": {
		LabelEN: "Signed in", LabelBN: "সাইন ইন",
		EN: "{actor} signed in",
		BN: "{actor} সাইন ইন করেছেন",
	},
	"session.login_failed": {
		LabelEN: "Sign-in refused", LabelBN: "সাইন ইন প্রত্যাখ্যাত",
		EN: "a sign-in for {actor} was refused ({failure})",
		BN: "{actor}-এর সাইন ইন প্রত্যাখ্যাত হয়েছে ({failure})",
	},
	"session.logout": {
		LabelEN: "Signed out", LabelBN: "সাইন আউট",
		EN: "{actor} signed out",
		BN: "{actor} সাইন আউট করেছেন",
	},
	"session.step_up": {
		LabelEN: "Confirmed with authenticator", LabelBN: "অথেনটিকেটর দিয়ে নিশ্চিত",
		EN: "{actor} confirmed with their authenticator for {purpose}",
		BN: "{actor} {purpose}-এর জন্য অথেনটিকেটর দিয়ে নিশ্চিত করেছেন",
	},

	// --- the console (CP21) ---
	"user.invited": {
		LabelEN: "Account created", LabelBN: "অ্যাকাউন্ট তৈরি",
		EN: "{actor} created an account for {target} with roles {roles}",
		BN: "{actor} {target}-এর জন্য {roles} ভূমিকাসহ অ্যাকাউন্ট তৈরি করেছেন",
	},
	"user.status_changed": {
		LabelEN: "Account status changed", LabelBN: "অ্যাকাউন্টের অবস্থা বদল",
		EN: "{actor} changed {target} from {before} to {after}: {reason}",
		BN: "{actor} {target}-কে {before} থেকে {after} করেছেন: {reason}",
	},
	"role.granted": {
		LabelEN: "Role granted", LabelBN: "ভূমিকা প্রদান",
		EN: "{actor} granted {role} to {target}",
		BN: "{actor} {target}-কে {role} ভূমিকা দিয়েছেন",
	},
	"role.revoked": {
		LabelEN: "Role revoked", LabelBN: "ভূমিকা প্রত্যাহার",
		EN: "{actor} revoked {role} from {target}: {reason}",
		BN: "{actor} {target}-এর {role} ভূমিকা প্রত্যাহার করেছেন: {reason}",
	},
	"sessions.ended": {
		LabelEN: "Signed out everywhere", LabelBN: "সব জায়গা থেকে সাইন আউট",
		EN: "{actor} signed {target} out of {count} session(s): {reason}",
		BN: "{actor} {target}-কে {count}টি সেশন থেকে সাইন আউট করেছেন: {reason}",
	},
	"password.set": {
		LabelEN: "Password set", LabelBN: "পাসওয়ার্ড নির্ধারণ",
		EN: "{actor} set a new password for {target}: {reason}",
		BN: "{actor} {target}-এর নতুন পাসওয়ার্ড দিয়েছেন: {reason}",
	},
	"second_factor.reset": {
		LabelEN: "Authenticator reset", LabelBN: "অথেনটিকেটর রিসেট",
		EN: "{actor} reset the authenticator of {target}: {reason}",
		BN: "{actor} {target}-এর অথেনটিকেটর রিসেট করেছেন: {reason}",
	},

	// --- this checkpoint ---
	"break_glass.opened": {
		LabelEN: "Break-glass access", LabelBN: "জরুরি প্রবেশাধিকার",
		EN: "{actor} broke the glass for {scope} until {until}: {reason}",
		BN: "{actor} {until} পর্যন্ত {scope}-এর জন্য জরুরি প্রবেশাধিকার নিয়েছেন: {reason}",
	},
	"break_glass.acknowledged": {
		LabelEN: "Break-glass acknowledged", LabelBN: "জরুরি প্রবেশাধিকার দেখা হয়েছে",
		EN: "{actor} acknowledged the break-glass access of {target}",
		BN: "{actor} {target}-এর জরুরি প্রবেশাধিকার দেখেছেন",
	},
	"break_glass.ended": {
		LabelEN: "Break-glass ended", LabelBN: "জরুরি প্রবেশাধিকার শেষ",
		EN: "{actor} ended the break-glass access of {target}: {reason}",
		BN: "{actor} {target}-এর জরুরি প্রবেশাধিকার শেষ করেছেন: {reason}",
	},
	// CP31. A search is recorded without the term: the term is the patient's name, and a
	// name in the audit trail is PHI in a table read by administrators who may have no
	// clinical access. What is recorded is that a search happened, how it was framed and
	// how many rows came back — which is what a bulk-search pattern looks like, and the
	// only thing an exfiltration review actually needs.
	"patient.searched": {
		LabelEN: "Patient search", LabelBN: "রোগী অনুসন্ধান",
		EN: "{actor} searched the patient register by {by} and saw {count} result(s)",
		BN: "{actor} {by} দিয়ে রোগী তালিকায় খুঁজেছেন এবং {count}টি ফলাফল দেখেছেন",
	},
	"patient.viewed": {
		LabelEN: "Patient record opened", LabelBN: "রোগীর রেকর্ড খোলা",
		EN: "{actor} opened the record of patient {target}",
		BN: "{actor} রোগী {target}-এর রেকর্ড খুলেছেন",
	},
	"audit.exported": {
		LabelEN: "Audit trail exported", LabelBN: "অডিট ট্রেইল রপ্তানি",
		EN: "{actor} exported {count} audit entries as a signed PDF",
		BN: "{actor} {count}টি অডিট এন্ট্রি স্বাক্ষরিত PDF হিসেবে রপ্তানি করেছেন",
	},
	"audit.verified": {
		LabelEN: "Chain verified", LabelBN: "চেইন যাচাই",
		EN: "{actor} verified the audit chain: {outcome}",
		BN: "{actor} অডিট চেইন যাচাই করেছেন: {outcome}",
	},
	"audit.chain_broken": {
		LabelEN: "Chain verification failed", LabelBN: "চেইন যাচাই ব্যর্থ",
		EN: "the audit chain failed verification at row {seq}: {problem}",
		BN: "অডিট চেইন {seq} নম্বর সারিতে যাচাইয়ে ব্যর্থ হয়েছে: {problem}",
	},

	// --- CP25: rebuilding a read model ---
	//
	// A rebuild is the only operation in DTHCMS that legitimately deletes derived clinical
	// data. The events it is derived from are untouched, so nothing is lost — but "nothing
	// was lost" is a claim somebody has to be able to check afterwards, which is what these
	// two rows are for.
	"projection.rebuilt": {
		LabelEN: "Read model rebuilt", LabelBN: "রিড মডেল পুনর্গঠিত",
		EN: "{actor} rebuilt {projection} v{version} from {events} events: {reason}",
		BN: "{actor} {events}টি ইভেন্ট থেকে {projection} v{version} পুনর্গঠন করেছেন: {reason}",
	},
	"projection.rebuild_failed": {
		LabelEN: "Read model rebuild failed", LabelBN: "রিড মডেল পুনর্গঠন ব্যর্থ",
		EN: "{actor} could not rebuild {projection}: {error}",
		BN: "{actor} {projection} পুনর্গঠন করতে পারেননি: {error}",
	},
}

// Known reports whether a kind may be recorded.
func Known(kind string) bool {
	_, ok := Kinds[kind]
	return ok
}

// KindList is the registry in a stable order, for the viewer's filter.
func KindList() []string {
	out := make([]string, 0, len(Kinds))
	for k := range Kinds {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Dhaka is where the clock on the wall is. Storage is UTC (§9.2); sentences are not.
var Dhaka = mustLoad("Asia/Dhaka")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.FixedZone("BDT", 6*3600)
	}
	return loc
}

// Render turns an event into "10:42 — JD_04 signed in", in the language asked for. An
// unknown kind renders its name rather than nothing, so a row written by a newer server
// than the one rendering it is still visible.
func Render(ev Event, lang Language) string {
	return fmt.Sprintf("%s — %s", ev.RecordedAt.In(Dhaka).Format("15:04"), Describe(ev, lang))
}

// Describe is the sentence without the time, for a table that has a time column.
func Describe(ev Event, lang Language) string {
	s, ok := Kinds[ev.Kind]
	if !ok {
		return ev.Kind
	}
	tpl := s.EN
	if lang == Bangla {
		tpl = s.BN
	}
	return fill(tpl, ev, lang)
}

// Label is the kind's short name.
func Label(kind string, lang Language) string {
	s, ok := Kinds[kind]
	if !ok {
		return kind
	}
	if lang == Bangla {
		return s.LabelBN
	}
	return s.LabelEN
}

func fill(tpl string, ev Event, lang Language) string {
	var out strings.Builder
	for {
		i := strings.IndexByte(tpl, '{')
		if i < 0 {
			out.WriteString(tpl)
			break
		}
		j := strings.IndexByte(tpl[i:], '}')
		if j < 0 {
			out.WriteString(tpl)
			break
		}
		out.WriteString(tpl[:i])
		out.WriteString(value(tpl[i+1:i+j], ev, lang))
		tpl = tpl[i+j+1:]
	}
	return out.String()
}

func value(name string, ev Event, lang Language) string {
	dash := "—"
	switch name {
	case "actor":
		return orDash(ev.ActorCode, dash)
	case "target":
		return orDash(ev.TargetCode, dash)
	case "time":
		return ev.RecordedAt.In(Dhaka).Format("15:04")
	case "reason":
		return orDash(ev.Reason, dash)
	}
	v, ok := ev.Details[name]
	if !ok || v == nil {
		return dash
	}
	switch t := v.(type) {
	case string:
		if t == "" {
			return dash
		}
		if name == "until" {
			if ts, err := time.Parse(time.RFC3339, t); err == nil {
				return ts.In(Dhaka).Format("15:04")
			}
		}
		return t
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	case int:
		return fmt.Sprintf("%d", t)
	case int64:
		return fmt.Sprintf("%d", t)
	case bool:
		if lang == Bangla {
			if t {
				return "হ্যাঁ"
			}
			return "না"
		}
		if t {
			return "yes"
		}
		return "no"
	case []any:
		parts := make([]string, 0, len(t))
		for _, p := range t {
			parts = append(parts, fmt.Sprint(p))
		}
		return orDash(strings.Join(parts, ", "), dash)
	case []string:
		return orDash(strings.Join(t, ", "), dash)
	default:
		return fmt.Sprint(t)
	}
}

func orDash(s, dash string) string {
	if strings.TrimSpace(s) == "" {
		return dash
	}
	return s
}
