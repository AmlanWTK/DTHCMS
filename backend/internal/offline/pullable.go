package offline

// What a device may be sent, and what it may not.
//
// # Why this is a declared map and not a filter
//
// The pull hands whole clinical events to a phone. Getting the scope wrong here is not a bug that
// shows up as an error — it is a bug that shows up as a nutritionist's tablet holding every
// diagnosis in the clinic, working correctly, for months.
//
// So the rule is the one the route registry uses: **an event type nobody has decided about is not
// pullable at all.** A denylist would put a type added next month on every phone in the building
// before anybody noticed; an allowlist makes adding one a line in this file that a reviewer reads.
// `TestEveryEventTypeIsDecidedAbout` refuses a build where the two lists do not cover the registry.
//
// # Why the permission is the one the API already uses
//
// Each entry names the permission a caller must hold to receive that type, and they are the same
// permissions that guard the corresponding HTTP routes. That is deliberate: if a nutritionist may
// read a diet entry over HTTP, they may receive one on their phone, and if they may not, they may
// not. Two different answers to "may this person see this" is how §4.4's blinding quietly stops
// meaning anything — and the mapping is here rather than on the event registry so that
// `internal/eventstore` keeps knowing nothing about permissions.

// Pullable is the event types a device may receive, and the permission each needs.
//
// Deliberately narrow. This is what a station app needs to render a patient's record offline —
// demographics, the visit it is working within, the values other stations have recorded today, and
// the allergy that has to reach everyone who meets the patient. Everything else waits until a
// checkpoint needs it on a device and somebody argues for it here.
func Pullable() map[string]string {
	return map[string]string{
		// Who the patient is. Without these a station app cannot put a name on a screen, and
		// every other event it holds is about an identifier.
		"PATIENT_REGISTERED":             "patient.read.demographics",
		"PATIENT_DEMOGRAPHICS_CORRECTED": "patient.read.demographics",
		"PATIENT_MERGED":                 "patient.read.demographics",

		// The visit the station is working within, and the queue it is part of.
		"VISIT_OPENED":       "visit.read",
		"VISIT_CLOSED":       "visit.read",
		"VISIT_ABANDONED":    "visit.read",
		"VISIT_REOPENED":     "visit.read",
		"ENCOUNTER_STARTED":  "visit.read",
		"ENCOUNTER_FINISHED": "visit.read",
		"QUEUE_ENTERED":      "visit.read",
		"QUEUE_CALLED":       "visit.read",
		"QUEUE_LEFT":         "visit.read",

		// What other stations have recorded. A station that could not see the weight taken ten
		// minutes ago at the next desk would ask the patient to be weighed again.
		"OBSERVATION_RECORDED": "observation.read.values",
		"HEIGHT_RECORDED":      "observation.read.values",
		"HEIGHT_CORRECTED":     "observation.read.values",
		"WEIGHT_RECORDED":      "observation.read.values",
		"WAIST_RECORDED":       "observation.read.values",
		"HIP_RECORDED":         "observation.read.values",
		"BP_RECORDED":          "observation.read.values",
		"BP_CORRECTED":         "observation.read.values",
		"WEIGHT_CORRECTED":     "observation.read.values",
		"PULSE_RECORDED":       "observation.read.values",
		"SPO2_RECORDED":        "observation.read.values",
		"TEMP_RECORDED":        "observation.read.values",

		// CP54 criterion 3: an allergy has to reach everyone who meets the patient, and a phone
		// that had not been told is a phone at the one station where it matters most.
		"ALLERGY_RECORDED":        "patient.read.allergies",
		"ALLERGY_WITHDRAWN":       "patient.read.allergies",
		"ALLERGY_STATUS_ASSERTED": "patient.read.allergies",

		// The checklist a counsellor is about to work through, and what has been ticked.
		"COUNSELING_SESSION_STARTED":   "counseling.session.read",
		"COUNSELING_SESSION_COMPLETED": "counseling.session.read",
		"COUNSELING_ITEM_TICKED":       "counseling.session.read",
		"COUNSELING_ITEM_UNTICKED":     "counseling.session.read",

		// A critical value that somebody has to act on. On the phone because the person who has
		// to acknowledge it may be walking between rooms with no signal.
		"CRITICAL_VALUE_ALERTED":      "alert.read",
		"CRITICAL_VALUE_ACKNOWLEDGED": "alert.read",
		"CRITICAL_VALUE_ESCALATED":    "alert.read",
		// The delivery attempt too: an acknowledgement screen that could not say "we tried to
		// reach you twice and your phone was off" would be asking somebody to explain a delay
		// they have no record of.
		"CRITICAL_VALUE_DELIVERY_ATTEMPTED": "alert.read",
	}
}

// NotPullable is every registered type that stays on the server, with the reason.
//
// Listing them is the point. A type that is simply absent from both maps is a decision nobody
// made, and `TestEveryEventTypeIsDecidedAbout` fails the build rather than letting the default be
// whatever the pull query happens to do.
func NotPullable() map[string]string {
	return map[string]string{
		// Consent is a legal record with an evidence file behind it. A phone holding the consent
		// events but not the evidence would show a status nobody could support, and consent is
		// checked server-side on every write anyway — the check that matters is not the one a
		// device could make offline.
		"CONSENT_GRANTED": "consent is checked on the server; a device holding the status could not act on it",
		"CONSENT_REVOKED": "same",

		// Medical history and diagnoses are what §4.4 blinds most roles from. They belong on the
		// physician's screen, which is not offline-first, and putting them on every station
		// tablet to save a request would undo the blinding for the sake of a convenience nobody
		// asked for.
		"HISTORY_ITEM_RECORDED":  "§4.4 blinds most stations from clinical history",
		"HISTORY_ITEM_CONFIRMED": "same",
		"HISTORY_ITEM_AMENDED":   "same",
		"HISTORY_ITEM_REMOVED":   "same",

		// Station 3 and station 7 write these; nothing else reads them on a device today. They
		// become pullable the day a station needs to see another's questionnaire offline, and
		// that is a line here with a reason.
		"LIFESTYLE_ASSESSMENT_RECORDED": "written offline, not yet read offline by another station",
		"DIET_ENTRY_RECORDED":           "same",
		"DIET_ENTRY_WITHDRAWN":          "same",
		"EXERCISE_ASSESSMENT_RECORDED":  "same",
		"EXERCISE_PLAN_ISSUED":          "same",

		// The correction workflow is a supervisor's screen. A device that pulled corrections
		// would be holding one operator's mistakes on another operator's phone, which is the
		// thing CP63's invariant 79 exists to prevent one table over.
		"CORRECTION_REQUESTED":        "an operator's corrections do not belong on another operator's device",
		"CORRECTION_APPLIED":          "same",
		"CORRECTION_REJECTED":         "same",
		"SUPERVISOR_OVERRIDE_APPLIED": "same",

		// Gates are evaluated on the server by a trigger on the queue (CP57). A device holding
		// the gate events could show a state, and the state that matters is the one the database
		// enforces at the moment of the write.
		"VISIT_GATE_BLOCKED":         "the gate is a database trigger; a device's copy could only be stale",
		"VISIT_GATE_SATISFIED":       "same",
		"COUNSELING_GATE_OVERRIDDEN": "same",

		// Photographs are bytes behind a signed URL, and the URL expires in fifteen minutes.
		"PATIENT_PHOTO_CAPTURED": "the bytes are not in the event and the signed URL would expire",

		// The pre-consultation synthesis (CP71). The events say a summary was asked for and how it
		// ended; the summary itself is on the physician's screen, which is not offline-first. A
		// station tablet holding these would be holding the timing of somebody else's AI job with
		// nothing to do about it, and §4.4 blinds most stations from the clinical content anyway.
		"AI_SYNTHESIS_REQUESTED": "the summary is read on the physician's screen, which is not offline-first",
		"AI_SUGGESTION_DECIDED":  "same — and a station tablet has no panel to render a physician's answer on",
		"AI_SYNTHESIS_COMPLETED": "same",
		"AI_SYNTHESIS_FAILED":    "same",
	}
}
