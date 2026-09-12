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

	// --- in-session role switching (CP41, [R-02]) ---
	//
	// Recorded although it needs no re-authentication, which is the point of recording it.
	// One operator wearing several hats in a morning is the staffing reality the blueprint
	// describes; "which hat were they wearing when they wrote that" has to be answerable
	// years later, and the events answer it one at a time. This row answers the other
	// question — when they changed, and to what — which is the one an investigator asks
	// when a whole run of entries looks wrong.
	"role.switched": {
		LabelEN: "Role switched", LabelBN: "ভূমিকা বদল",
		EN: "{actor} switched from {before} to {after}",
		BN: "{actor} {before} থেকে {after} ভূমিকায় গেছেন",
	},

	// --- break-glass (CP22) ---
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
	// --- counselling templates (CP55) ---
	//
	// Configuration rather than a patient's record, so it lives here with the role grants and
	// credential resets — the log somebody reads when asking "who changed what". Publishing is
	// the act that changes what every counsellor asks every patient from that second onwards.
	"counseling.template_published": {
		LabelEN: "Counselling template published", LabelBN: "কাউন্সেলিং টেমপ্লেট প্রকাশিত",
		EN: "{actor} published version {version} of the {template} counselling template ({items} items)",
		BN: "{actor} {template} কাউন্সেলিং টেমপ্লেটের {version} নম্বর সংস্করণ প্রকাশ করেছেন ({items}টি বিষয়)",
	},

	// --- the counselling gate's valve (CP57) ---
	//
	// The one act in the counselling checkpoint somebody has to answer for. Here rather than
	// only in the clinical ledger because the question it answers is "who decided this", which
	// is what this trail is for — and because the person reviewing how often the valve is used
	// reads it beside the role grants and the break-glass entries, not in a patient's record.
	"counseling.gate_overridden": {
		LabelEN: "Counselling gate overridden", LabelBN: "কাউন্সেলিং গেট উপেক্ষা",
		EN: "{actor} sent a patient past the counselling gate with {missing} items uncovered: {reason}",
		BN: "{actor} {missing}টি বিষয় বাকি রেখে একজন রোগীকে কাউন্সেলিং গেট পার করিয়েছেন: {reason}",
	},

	// --- the operator quality record (CP63) ---
	//
	// Here rather than only in a table because the question a reviewer asks about a retraining
	// flag is "who raised this and who decided what to do about it", which is what this trail is
	// for. It reads beside the role grants rather than in a patient's record, which is also the
	// honest place for it: there is no patient in a quality flag, and there must not be.
	//
	// The sentence names the count **and the denominator**. A trail entry reading "three
	// corrections" without "out of four hundred entries" is the accusation this whole checkpoint
	// is arranged to avoid, and an audit trail is exactly where such a sentence would outlive
	// everybody's good intentions.
	"quality.flag_raised": {
		LabelEN: "Quality flag raised", LabelBN: "মান সংক্রান্ত ফ্ল্যাগ উত্থাপিত",
		EN: "{target} was flagged for {threshold}: {observed} corrections out of {entries} entries in {days} days",
		BN: "{target}-এর ক্ষেত্রে {threshold} লক্ষ করা হয়েছে: {days} দিনে {entries}টি এন্ট্রির মধ্যে {observed}টি সংশোধন",
	},
	"quality.flag_resolved": {
		LabelEN: "Quality flag answered", LabelBN: "মান সংক্রান্ত ফ্ল্যাগের নিষ্পত্তি",
		EN: "{actor} marked {target}'s {threshold} flag {status}: {reason}",
		BN: "{actor} {target}-এর {threshold} ফ্ল্যাগটি {status} হিসাবে চিহ্নিত করেছেন: {reason}",
	},

	// --- the medicine formulary and its prices (CP75, §16.1) ---
	//
	// Configuration and commerce rather than a patient's record, so it lives here with the role
	// grants — the log somebody reads when asking "who changed what". A price change is the act
	// this checkpoint's security criterion names, and the sentence carries the old price as well
	// as the new one: "who raised the price of insulin" is a question about a difference, and a
	// trail that only had the new number could not answer it without a second lookup per row.
	"formulary.price_set": {
		LabelEN: "Medicine price set", LabelBN: "ওষুধের দাম নির্ধারিত",
		EN: "{actor} set the price of {medicine} at {price} BDT from {from} ({verification})",
		BN: "{actor} {from} থেকে {medicine}-এর দাম {price} টাকা নির্ধারণ করেছেন ({verification})",
	},
	"formulary.price_changed": {
		LabelEN: "Medicine price changed", LabelBN: "ওষুধের দাম পরিবর্তিত",
		EN: "{actor} changed the price of {medicine} from {previous} to {price} BDT from {from} ({verification})",
		BN: "{actor} {from} থেকে {medicine}-এর দাম {previous} টাকা থেকে {price} টাকা করেছেন ({verification})",
	},
	// Opened by a clock rather than by a person, so the sentence has no actor in it. The
	// registry renders an empty placeholder as "—"; an entry reading "— opened the review"
	// would be worse than one written without the name, so this one is written without it.
	"formulary.review_opened": {
		LabelEN: "Price review opened", LabelBN: "দাম পর্যালোচনা শুরু",
		EN: "the {month} medicine price review was opened for {owner_role}: {unverified} of {products} prices unchecked",
		BN: "{owner_role}-এর জন্য {month} মাসের ওষুধের দাম পর্যালোচনা শুরু হয়েছে: {products}টির মধ্যে {unverified}টি দাম অযাচাইকৃত",
	},
	"formulary.review_completed": {
		LabelEN: "Price review completed", LabelBN: "দাম পর্যালোচনা সম্পন্ন",
		EN: "{actor} completed the {month} medicine price review with {unverified} prices still unchecked",
		BN: "{actor} {month} মাসের ওষুধের দাম পর্যালোচনা শেষ করেছেন, {unverified}টি দাম তখনও অযাচাইকৃত",
	},

	// --- the medication safety rule library (CP77, §6.3, D-22) ---
	//
	// **The full rule content is in the entry's details**, which is what the checkpoint asks
	// for and is unusual for this trail — most entries name a thing and not its contents. The
	// reason is the defect it prevents: a rule that changed with no record of what it said is
	// one nobody can reconstruct months later, when a prescription written under it is
	// questioned and the only thing left is a version number.
	//
	// The sentence carries the rule's plain-language form rather than its condition document,
	// because the person reading this trail is answering "what did this rule do", and a JSON
	// object is not an answer to that. The condition is in the details beside it.
	"medication_rule.published": {
		LabelEN: "Medication rule published", LabelBN: "ওষুধের নিয়ম প্রকাশিত",
		EN: "{actor} published {rule} v{version} ({severity}): {plain}",
		BN: "{actor} {rule} v{version} প্রকাশ করেছেন ({severity}): {plain}",
	},
	"medication_rule.withdrawn": {
		LabelEN: "Medication rule withdrawn", LabelBN: "ওষুধের নিয়ম তুলে নেওয়া",
		EN: "{actor} withdrew {rule} (was v{version}): {reason}",
		BN: "{actor} {rule} তুলে নিয়েছেন (ছিল v{version}): {reason}",
	},

	// cmd/api/medsafety_check_bridge.go — CP78's `SAFETY_CHECK_RUN`, and **criterion 5 lives
	// here**: historical checks must be reproducible against the rule versions used at the
	// time. The rule table answers that only until somebody publishes a v3 or withdraws the
	// rule, and both are ordinary things to have happened — so the exact versions go into the
	// hash-chained trail, in `details.versions`, where they cannot be quietly edited.
	//
	// **The entry carries no clinical content.** Not a drug, not a diagnosis, not an allergen,
	// not the eGFR. The verdict and the counts say that a check happened and what it concluded;
	// what it concluded *about* is in the prescription, which is where a patient's record
	// belongs. The patient is named in the entry's subject, like every other clinical entry,
	// and nowhere in its details.
	"medication_safety.check_run": {
		LabelEN: "Prescription safety check", LabelBN: "ব্যবস্থাপত্রের নিরাপত্তা যাচাই",
		EN: "{actor} ran a safety check on {items} medicine(s): {verdict} " +
			"({findings} finding(s) from {rules} live rule(s), {uncovered} medicine(s) covered by none)",
		BN: "{actor} {items}টি ওষুধের নিরাপত্তা যাচাই করেছেন: {verdict} " +
			"({rules}টি চালু নিয়ম থেকে {findings}টি বিষয়, {uncovered}টি ওষুধ কোনো নিয়মের আওতায় নেই)",
	},

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
