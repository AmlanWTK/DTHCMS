package logging

// PHIKeys are the structured-logging keys that must never carry a value.
//
// This is the single source of truth for the rule, used twice:
//
//   - at build time by tools/dthclint, which fails the build when one appears as a
//     literal key in a log call;
//   - at run time by the redaction handler below, which catches keys built dynamically
//     and therefore invisible to static analysis.
//
// The two layers exist because either alone is insufficient. Static analysis cannot see
// a key assembled from a variable; a runtime handler cannot stop a developer shipping
// the mistake. Together they make it very hard to log a patient's identity by accident.
//
// The value is the guidance shown to whoever trips the rule. A rule that does not say
// what to do instead gets worked around.
var PHIKeys = map[string]string{
	"name":            "log patient_id instead",
	"patient_name":    "log patient_id instead",
	"full_name":       "log patient_id instead",
	"name_bn":         "log patient_id instead",
	"name_en":         "log patient_id instead",
	"nid":             "national IDs must never be logged, not even masked",
	"national_id":     "national IDs must never be logged, not even masked",
	"national_id_raw": "national IDs must never be logged",
	"phone":           "log patient_id instead",
	"mobile":          "log patient_id instead",
	"address":         "log patient_id instead",
	"dob":             "log age_years or age_months if you need it",
	"date_of_birth":   "log age_years or age_months if you need it",
	"email":           "log user_id instead",
	"photo":           "never log image data or its location",
	"diagnosis":       "clinical detail belongs in the event ledger, not in logs",
	"prescription":    "clinical detail belongs in the event ledger, not in logs",
	"password":        "never log credentials, even hashed",
	"token":           "never log credentials",
	"secret":          "never log credentials",
	"otp":             "never log authentication codes",
	"totp_secret":     "never log authentication secrets",
}

// Redacted replaces any value logged under a PHI key.
const Redacted = "[REDACTED]"
