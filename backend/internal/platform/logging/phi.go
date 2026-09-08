package logging

// PHIKeys are the structured-logging keys that must never carry a value.
//
// This is the single source of truth for the rule, used four times:
//
//   - at build time by tools/dthclint, which fails the build when one appears as a
//     literal key in a log call;
//   - at run time by the redaction handler below, which catches keys built dynamically
//     and therefore invisible to static analysis;
//   - by the job queue (CP69), whose arguments must reference and never embed — checked
//     in Go so the refusal names the key, and again by a check constraint reading
//     `ops.phi_key`, which is this map's copy in the database;
//   - by the AI gateway (CP70), which is the reason each key now carries a **class**.
//
// # Why a class was added, and what breaks without it
//
// The first three uses want the same answer for every key on the list: never, anywhere.
// The fourth does not, and cannot. An AI payload's whole purpose is to carry clinical
// content — a diagnosis is exactly what the pre-consultation synthesis is summarising —
// so a gateway that refused every key here would refuse every payload it exists to send.
// What the gateway must refuse is narrower and sharper: anything that says *which person*
// this is.
//
// Splitting the list in two would have been the obvious move and the wrong one. Two lists
// drift, and the drift is silent in the direction that matters: a key added to the logging
// list and forgotten on the gateway's list is an identifier the gateway will happily
// forward. So there is one list, and each entry says which rule it belongs to. Adding a key
// without classifying it is a compile error, which is the property worth having.
//
// The value is also the guidance shown to whoever trips the rule. A rule that does not say
// what to do instead gets worked around.
var PHIKeys = map[string]PHIKey{
	"name":            {"log patient_id instead", ClassIdentifier},
	"patient_name":    {"log patient_id instead", ClassIdentifier},
	"full_name":       {"log patient_id instead", ClassIdentifier},
	"name_bn":         {"log patient_id instead", ClassIdentifier},
	"name_en":         {"log patient_id instead", ClassIdentifier},
	"nid":             {"national IDs must never be logged, not even masked", ClassIdentifier},
	"national_id":     {"national IDs must never be logged, not even masked", ClassIdentifier},
	"national_id_raw": {"national IDs must never be logged", ClassIdentifier},
	"phone":           {"log patient_id instead", ClassIdentifier},
	"mobile":          {"log patient_id instead", ClassIdentifier},
	"address":         {"log patient_id instead", ClassIdentifier},
	"dob":             {"log age_years or age_months if you need it", ClassIdentifier},
	"date_of_birth":   {"log age_years or age_months if you need it", ClassIdentifier},
	"email":           {"log user_id instead", ClassIdentifier},
	"photo":           {"never log image data or its location", ClassIdentifier},
	"diagnosis":       {"clinical detail belongs in the event ledger, not in logs", ClassClinical},
	"prescription":    {"clinical detail belongs in the event ledger, not in logs", ClassClinical},
	"password":        {"never log credentials, even hashed", ClassCredential},
	"token":           {"never log credentials", ClassCredential},
	"secret":          {"never log credentials", ClassCredential},
	"otp":             {"never log authentication codes", ClassCredential},
	"totp_secret":     {"never log authentication secrets", ClassCredential},
}

// PHIKey is one entry: what to do instead, and which rule the key belongs to.
type PHIKey struct {
	// Guidance is shown to whoever trips the rule.
	Guidance string
	// Class decides whether a rule narrower than the logging rule also refuses this key.
	Class PHIClass
}

// PHIClass says what kind of secret a key holds.
//
// The logging, telemetry and job-argument rules ignore this and refuse all three; only the
// AI gateway reads it, and only to decide the one question it has to answer differently.
type PHIClass string

const (
	// ClassIdentifier says which person this is: a name, a number issued to them, a way of
	// reaching them, the day they were born, their face. Nothing in this class may leave
	// the boundary, on any tier, for any reason. It is the class D-08's default deny is
	// about.
	ClassIdentifier PHIClass = "IDENTIFIER"
	// ClassClinical is what is wrong with them and what is being done about it. Banned from
	// logs and from job arguments because a log line is read by whoever has the log, and a
	// job argument sits in a queue for hours — but *permitted*, deliberately, in a
	// PHI-minimised AI payload, because summarising it is what the model is for. A gateway
	// that refused this class would refuse every payload the AI framework (§7) exists to
	// send, and the feature would be built around the check rather than through it.
	ClassClinical PHIClass = "CLINICAL"
	// ClassCredential is a secret that grants access. It belongs nowhere: not in a log, not
	// in a queue, and certainly not in a prompt, where it would be trained on under D-07's
	// free tier and read by a human reviewer under Google's own terms.
	ClassCredential PHIClass = "CREDENTIAL"
)

// IsPHIKey reports whether a key must never carry a value anywhere.
func IsPHIKey(key string) bool { _, banned := PHIKeys[key]; return banned }

// MustNotLeaveTheBoundary reports whether a key must never appear in a payload sent to an
// external model, whatever the tier.
//
// Everything except ClassClinical. Stated as an allow-of-one rather than a deny-of-two so
// that a class added later is refused by default: the failure mode of the opposite spelling
// is that a new class silently becomes sendable, and nobody reviewing the constant would
// see the consequence.
func MustNotLeaveTheBoundary(key string) bool {
	entry, known := PHIKeys[key]
	return known && entry.Class != ClassClinical
}

// Redacted replaces any value logged under a PHI key.
const Redacted = "[REDACTED]"
