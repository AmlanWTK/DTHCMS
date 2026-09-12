package rbac

import "github.com/AmlanWTK/DTHCMS/backend/internal/auth"

// RolePermissions is the grant table of migration 00006, as Go.
//
// Two copies of one table, on purpose and with a guard. The database's copy is what
// PermissionsForUser reads on every request; this copy is what the engine narrows to when
// an active role is chosen [R-02], and what the decision matrix test walks without a
// database. TestRolePermissionsMatchTheDatabase compares them exactly, both ways.
//
// Read it as a paragraph per role — "what can a nutritionist do" — because that is the
// question asked of it in review.
var RolePermissions = map[auth.RoleCode]auth.PermissionSet{
	auth.RoleRegistration: auth.NewPermissionSet(
		auth.PermPatientReadDemographics,
		auth.PermPatientWriteDemographics,
		auth.PermPatientConsentRecord,
		auth.PermPatientConsentRevoke,
		auth.PermVisitOpen,
		auth.PermVisitRead,
		auth.PermVisitAttend,
		auth.PermBoardRead,
		auth.PermVisitReroute,
	),
	auth.RoleAnthropometry: auth.NewPermissionSet(
		auth.PermPatientReadDemographics,
		auth.PermObservationWriteAnthro,
		auth.PermObservationReadValues,
		auth.PermObservationCorrectRequest,
		auth.PermVisitRead,
		auth.PermVisitAttend,
		auth.PermBoardRead,
	),
	auth.RoleCounselor: auth.NewPermissionSet(
		auth.PermPatientReadDemographics,
		auth.PermObservationWriteLifestyle,
		auth.PermObservationReadValues,
		auth.PermCounselingTick,
		auth.PermObservationCorrectRequest,
		auth.PermVisitRead,
		auth.PermVisitAttend,
		auth.PermBoardRead,
		// The checklist a counsellor is about to work through (CP55).
		auth.PermCounselingTemplateRead,
		auth.PermCounselingSessionRead,
	),
	auth.RoleHistory: auth.NewPermissionSet(
		auth.PermPatientReadDemographics,
		auth.PermPatientReadAllergies,
		auth.PermPatientReadClinical,
		auth.PermObservationWriteHistory,
		auth.PermObservationReadValues,
		// The complaint and comorbidity pickers at station 4 (CP52, used by CP53).
		auth.PermTerminologyRead,
		auth.PermHistoryRead,
		auth.PermHistoryWrite,
		auth.PermHistoryConfirm,
		// The hard stop is station 4's to clear (CP54).
		auth.PermAllergyWrite,
		auth.PermRecordsRead,
		auth.PermObservationCorrectRequest,
		auth.PermVisitRead,
		auth.PermVisitAttend,
		auth.PermBoardRead,
	),
	auth.RoleClinicalAssistant: auth.NewPermissionSet(
		auth.PermPatientReadDemographics,
		auth.PermPatientReadAllergies,
		auth.PermObservationWriteVitals,
		auth.PermObservationReadValues,
		// Station 5's structured examination (CP51).
		auth.PermObservationWriteExam,
		auth.PermTerminologyRead,
		// Reads and confirms, but does not write. At a follow-up the patient often reaches
		// station 5 without seeing the history officer, and an unconfirmed list is worse
		// than a confirmed one — but taking a history is station 4's job.
		auth.PermHistoryRead,
		auth.PermHistoryConfirm,
		auth.PermAllergyWrite,
		auth.PermLabRead,
		auth.PermLabResultEnter,
		auth.PermObservationCorrectRequest,
		auth.PermVisitRead,
		auth.PermVisitAttend,
		auth.PermBoardRead,
		// The checklist a counsellor is about to work through (CP55).
		auth.PermCounselingTemplateRead,
		auth.PermCounselingSessionRead,
		// §7.1's button. The last assistant in the flow presses it, and this role is one of the
		// four that may be that assistant. Asking for a summary is not reading one — CP71 keeps
		// them as separate permissions precisely so this grant does not carry a diagnosis with it.
		auth.PermAiSynthesisRequest,
	),
	auth.RoleJuniorDoctor: auth.NewPermissionSet(
		// Reads the medication safety rules he prescribes under (CP77). Not write, not publish:
		// D-22 makes those the chief consultant's alone.
		auth.PermMedicationRuleRead,
		// CP78's safety engine, on every prescription he writes. Sensitive — it reads this
		// patient's kidney function, diagnoses and allergies — and he already holds all three
		// of those, so it grants no reach he did not have; what it adds is the act.
		auth.PermMedicationSafetyCheck,
		// The medicine formulary and its prices (CP75, §16.1). Read-only: what a medicine is,
		// what form it comes in and what it costs. No patient in it, and nothing sensitive —
		// which is the whole reason the pharmacist, a role §4.4 blinds from diagnoses, is the
		// one §16.1 puts in charge of it.
		auth.PermFormularyRead,
		auth.PermPatientReadDemographics,
		auth.PermPatientReadAllergies,
		auth.PermPatientReadClinical,
		auth.PermObservationWriteVitals,
		auth.PermObservationReadValues,
		// Station 5's structured examination (CP51).
		auth.PermObservationWriteExam,
		auth.PermObservationCorrectRequest,
		auth.PermObservationCorrectApprove,
		auth.PermRecordsRead,
		auth.PermLabRead,
		auth.PermLabOrder,
		auth.PermLabResultEnter,
		auth.PermDiagnosisRead,
		auth.PermTerminologyRead,
		auth.PermHistoryRead,
		auth.PermHistoryWrite,
		auth.PermHistoryConfirm,
		auth.PermAllergyWrite,
		auth.PermPrescriptionDraft,
		auth.PermAiSynthesisRead,
		auth.PermVisitRead,
		auth.PermVisitAttend,
		auth.PermBoardRead,
		// Critical values (CP50). The junior doctor is at the examination station, which is
		// where most of them are raised, and is the person who can act in the seconds before
		// the consultant is free.
		auth.PermAlertRead,
		auth.PermAlertAcknowledge,
		// The checklist a counsellor is about to work through (CP55).
		auth.PermCounselingTemplateRead,
		auth.PermCounselingSessionRead,
		// §7.1's button. The last assistant in the flow presses it, and this role is one of the
		// four that may be that assistant. Asking for a summary is not reading one — CP71 keeps
		// them as separate permissions precisely so this grant does not carry a diagnosis with it.
		auth.PermAiSynthesisRequest,
	),
	auth.RoleRecords: auth.NewPermissionSet(
		auth.PermPatientReadDemographics,
		auth.PermPatientMerge,
		auth.PermRecordsUpload,
		auth.PermRecordsRead,
		auth.PermRecordsVerify,
		auth.PermVisitRead,
		auth.PermVisitAttend,
		auth.PermBoardRead,
	),
	auth.RoleNutritionist: auth.NewPermissionSet(
		// The medicine formulary and its prices (CP75, §16.1). Read-only: what a medicine is,
		// what form it comes in and what it costs. No patient in it, and nothing sensitive —
		// which is the whole reason the pharmacist, a role §4.4 blinds from diagnoses, is the
		// one §16.1 puts in charge of it.
		auth.PermFormularyRead,
		auth.PermPatientReadDemographics,
		auth.PermPatientReadClinical,
		auth.PermObservationWriteNutrition,
		auth.PermObservationReadValues,
		auth.PermLabRead,
		auth.PermVisitRead,
		auth.PermVisitAttend,
		auth.PermBoardRead,
		// The nutritionist needs the comorbidities and the medication list: a diet plan
		// written without knowing somebody is on insulin is a plan that is wrong.
		auth.PermHistoryRead,
		// An allergy has to reach everyone who meets the patient (CP54 criterion 3).
		auth.PermPatientReadAllergies,
		// The checklist a counsellor is about to work through (CP55).
		auth.PermCounselingTemplateRead,
		auth.PermCounselingSessionRead,
		// §5.2 walks three rooms and the nutrition room is this one's. Before CP56 the diet
		// item could only be ticked by somebody who was not in the room, which is the precise
		// failure §5.4's spot-questioning exists to catch.
		auth.PermCounselingTick,
		// §7.1's button. The last assistant in the flow presses it, and this role is one of the
		// four that may be that assistant. Asking for a summary is not reading one — CP71 keeps
		// them as separate permissions precisely so this grant does not carry a diagnosis with it.
		auth.PermAiSynthesisRequest,
	),
	auth.RoleExercise: auth.NewPermissionSet(
		auth.PermPatientReadDemographics,
		auth.PermPatientReadClinical,
		auth.PermObservationWriteExercise,
		auth.PermObservationReadValues,
		auth.PermVisitRead,
		auth.PermVisitAttend,
		auth.PermBoardRead,
		// An allergy has to reach everyone who meets the patient (CP54 criterion 3).
		auth.PermPatientReadAllergies,
		// The checklist a counsellor is about to work through (CP55).
		auth.PermCounselingTemplateRead,
		auth.PermCounselingSessionRead,
		// §7.1's button. The last assistant in the flow presses it, and this role is one of the
		// four that may be that assistant. Asking for a summary is not reading one — CP71 keeps
		// them as separate permissions precisely so this grant does not carry a diagnosis with it.
		auth.PermAiSynthesisRequest,
	),
	auth.RolePhysician: auth.NewPermissionSet(
		// The medicine formulary (CP75, D-56). Read, write and the monthly price review: D-56
		// made the formulary's *content* a clinical decision, and the physician is the person
		// who makes it. The review is his as well as the pharmacist's so that the month it
		// matters and the pharmacist is away, it still gets done.
		auth.PermFormularyRead,
		auth.PermFormularyWrite,
		auth.PermFormularyPriceReview,
		// The medication safety rule library (CP77, D-22). **All three, and only here.** D-22
		// made the physician the author of every clinical rule; writing and publishing are
		// granted to no other role, which is that decision stated as a grant rather than as a
		// convention.
		auth.PermMedicationRuleRead,
		auth.PermMedicationRuleWrite,
		auth.PermMedicationRulePublish,
		// CP78's safety engine, on every prescription (CP81 runs it as items are added).
		auth.PermMedicationSafetyCheck,
		auth.PermPatientReadDemographics,
		auth.PermPatientReadAllergies,
		auth.PermPatientReadClinical,
		auth.PermObservationReadValues,
		// The offline quarantine (CP65). Held events are clinical measurements on this
		// physician's patients, and "should this be in the record" is a clinical question —
		// which is also why releasing is theirs alone.
		auth.PermSyncQuarantineRead,
		auth.PermSyncQuarantineRelease,
		// Queue health, read only (CP69). Here for a reason particular to §7.1: the five
		// minutes this queue promises is the wait before *their* consultation. A physician who
		// can see that synthesis is eight minutes behind starts without it rather than waiting
		// for something that is not coming — a clinical decision, not an operational one.
		auth.PermOpsJobsRead,
		// The AI gateway's outbound log (CP70), for the reason §10.6 exists at all: the
		// physician is accountable for what an AI draft said, and being able to open the exact
		// payload, prompt version and model behind a summary they were shown is the difference
		// between a tool they can defend and one they must simply trust.
		auth.PermAiGatewayRead,
		// And the verdict on a grounding violation (CP72). Deciding whether a claim the check
		// refused was really unsupported is a clinical judgement about a clinical sentence, and
		// the physician is the person who can make it.
		auth.PermAiQualityReview,
		// §4.3's protagonist. CP15's seed gave flagging to the stations that record values
		// and not to the consultant, so the person the scenario is written about could not
		// raise the request (CP62).
		auth.PermObservationCorrectRequest,
		// Station 5's structured examination (CP51).
		auth.PermObservationWriteExam,
		auth.PermObservationCorrectApprove,
		auth.PermRecordsRead,
		auth.PermLabRead,
		auth.PermLabOrder,
		auth.PermLabResultEnter,
		auth.PermDiagnosisRead,
		auth.PermDiagnosisWrite,
		auth.PermTerminologyRead,
		auth.PermHistoryRead,
		auth.PermHistoryWrite,
		auth.PermHistoryConfirm,
		auth.PermAllergyWrite,
		auth.PermPrescriptionDraft,
		auth.PermPrescriptionSign,
		auth.PermPrescriptionRead,
		auth.PermAiSynthesisRead,
		// §7.1 says the physician *can* trigger the summary while promising they never need to.
		// Without the grant they would be stuck on the morning the automatic trigger did not
		// fire, which is exactly the moment the button exists for.
		auth.PermAiSynthesisRequest,
		auth.PermAiSuggestionApprove,
		auth.PermCounselingTemplateWrite,
		auth.PermQaClear,
		auth.PermCrmRead,
		auth.PermReportReadOperational,
		auth.PermAuditRead,
		auth.PermVisitClose,
		auth.PermVisitRead,
		auth.PermVisitAttend,
		auth.PermBoardRead,
		auth.PermVisitReroute,
		// The consultant is step 1 of the escalation chain, immediately (CP50).
		auth.PermAlertRead,
		auth.PermAlertAcknowledge,
		// The checklist a counsellor is about to work through (CP55).
		auth.PermCounselingTemplateRead,
		auth.PermCounselingSessionRead,
		// Publishing is separate from authoring: it puts a checklist on every phone
		// on the floor and freezes it forever (CP55).
		auth.PermCounselingTemplatePublish,
		// The valve on the counselling gate (CP57). Held by nobody who merely works at a
		// station: the gate is acceptable because the way past it is answerable.
		auth.PermCounselingGateOverride,
		// The operator quality record (CP63). Deliberately not `hr.performance.read`, which HR
		// holds: the plan puts performance-linked pay and discipline out of scope, and a
		// permission handing an operator's correction history to the department that sets pay
		// puts it back in. ADR-0029 has the argument.
		auth.PermQualityReadTeam,
		auth.PermQualityFlagResolve,
	),
	auth.RoleQa: auth.NewPermissionSet(
		// Reads the medication safety rules, because CP83's clearance runs the interaction and
		// duplicate checks and a QA officer who cannot see the rule a finding cites cannot
		// judge the finding (CP77).
		auth.PermMedicationRuleRead,
		// CP78's safety engine. CP83's clearance re-runs the interaction and duplicate checks
		// before a prescription may be printed, which is what a fail-closed gate means: the
		// officer's screen shows the same findings the prescriber saw, computed again rather
		// than carried forward.
		auth.PermMedicationSafetyCheck,
		// The medicine formulary and its prices (CP75, §16.1). Read-only: what a medicine is,
		// what form it comes in and what it costs. No patient in it, and nothing sensitive —
		// which is the whole reason the pharmacist, a role §4.4 blinds from diagnoses, is the
		// one §16.1 puts in charge of it.
		auth.PermFormularyRead,
		auth.PermPatientReadDemographics,
		auth.PermPatientReadClinical,
		auth.PermObservationReadValues,
		// A queue that has silently stopped is exactly what an audit exists to notice (CP69).
		auth.PermOpsJobsRead,
		// AI safety governance is QA's (D-68), and a scrubber nobody audits is a scrubber
		// nobody knows is working. CP70's own stated mitigation for its headline risk is a
		// human-reviewable outbound log; this is the human.
		auth.PermAiGatewayRead,
		// And the queue of grounding violations (CP72). QA owns it in practice: §14.1's clinical
		// governance is where "the AI said something the record does not support" belongs, and the
		// false-positive rate this produces is the only honest input to any future argument for
		// making the check less strict.
		auth.PermAiQualityReview,
		// §4.3's protagonist. CP15's seed gave flagging to the stations that record values
		// and not to the consultant, so the person the scenario is written about could not
		// raise the request (CP62).
		auth.PermObservationCorrectRequest,
		auth.PermDiagnosisRead,
		auth.PermTerminologyRead,
		auth.PermHistoryRead,
		auth.PermPrescriptionRead,
		auth.PermLabRead,
		auth.PermRecordsRead,
		auth.PermQaReview,
		// The operator quality record (CP63). Deliberately not `hr.performance.read`, which HR
		// holds: the plan puts performance-linked pay and discipline out of scope, and a
		// permission handing an operator's correction history to the department that sets pay
		// puts it back in. ADR-0029 has the argument.
		auth.PermQualityReadTeam,
		auth.PermQualityFlagResolve,
		auth.PermQaClear,
		auth.PermQaBounce,
		auth.PermAuditRead,
		auth.PermHrPerformanceRead,
		auth.PermVisitClose,
		auth.PermVisitRead,
		auth.PermVisitAttend,
		auth.PermBoardRead,
		auth.PermVisitReroute,
		// An allergy has to reach everyone who meets the patient (CP54 criterion 3).
		auth.PermPatientReadAllergies,
		// The checklist a counsellor is about to work through (CP55).
		auth.PermCounselingTemplateRead,
		auth.PermCounselingSessionRead,
	),
	auth.RoleRxEducator: auth.NewPermissionSet(
		// What a medicine costs, so the prescription-education officer can answer the question
		// a patient asks first (CP75, §12.3). Read only.
		auth.PermFormularyRead,
		auth.PermPatientReadDemographics,
		auth.PermPrescriptionRead,
		auth.PermEducationRecord,
		auth.PermVisitRead,
		auth.PermVisitAttend,
		auth.PermBoardRead,
		// An allergy has to reach everyone who meets the patient (CP54 criterion 3).
		auth.PermPatientReadAllergies,
		// The checklist a counsellor is about to work through (CP55).
		auth.PermCounselingTemplateRead,
		auth.PermCounselingSessionRead,
	),
	auth.RolePharmacist: auth.NewPermissionSet(
		auth.PermPatientReadDemographics,
		auth.PermPatientReadAllergies,
		auth.PermPrescriptionRead,
		auth.PermPrescriptionDispense,
		auth.PermFormularyRead,
		auth.PermFormularyWrite,
		auth.PermFormularyPriceReview,
		auth.PermStockMovementRecord,
	),
	auth.RoleCrm: auth.NewPermissionSet(
		auth.PermPatientReadDemographics,
		auth.PermCrmRead,
		auth.PermCrmContact,
		auth.PermCrmSchedule,
		auth.PermVisitRead,
		auth.PermVisitAttend,
		auth.PermBoardRead,
	),
	auth.RoleResearcher: auth.NewPermissionSet(
		auth.PermResearchQuery,
		auth.PermResearchExport,
	),
	auth.RoleHr: auth.NewPermissionSet(
		auth.PermUserRead,
		auth.PermHrAttendanceRead,
		auth.PermHrPerformanceRead,
		auth.PermReportReadOperational,
	),
	auth.RoleAdmin: auth.NewPermissionSet(
		// Reads the medication safety rule library (CP77). Read only, deliberately: an
		// administrator can grant himself any role, so the grant is not what stops him — what
		// it does is make the ordinary path, an administrator tidying up a clinical rule at
		// the end of a day, not exist.
		auth.PermMedicationRuleRead,
		// The medicine formulary (CP75, §16.1). The administrator holds all three so that the
		// month the pharmacist is away the prices still get checked — the same reason they
		// hold the counselling gate's valve beside the physician.
		auth.PermFormularyRead,
		auth.PermFormularyWrite,
		auth.PermFormularyPriceReview,
		auth.PermUserInvite,
		auth.PermUserRead,
		auth.PermUserSuspend,
		auth.PermUserDeactivate,
		auth.PermUserCredentialReset,
		auth.PermRoleGrant,
		auth.PermRoleRevoke,
		auth.PermDeviceEnroll,
		auth.PermDeviceRevoke,
		auth.PermAuditRead,
		auth.PermStationConfigure,
		auth.PermFacilityConfigure,
		auth.PermPatientMerge,
		auth.PermReportReadOperational,
		auth.PermReportReadFinancial,
		auth.PermVisitClose,
		auth.PermVisitOpen,
		auth.PermVisitRead,
		auth.PermVisitAttend,
		auth.PermBoardRead,
		auth.PermVisitReroute,
		// The checklist a counsellor is about to work through (CP55).
		auth.PermCounselingTemplateRead,
		auth.PermCounselingSessionRead,
		// The valve on the counselling gate (CP57). The administrator holds it beside the
		// physician while the clinic decides who else should — an operational decision the
		// plan lists as open.
		auth.PermCounselingGateOverride,
		// The operator quality record (CP63). Deliberately not `hr.performance.read`, which HR
		// holds: the plan puts performance-linked pay and discipline out of scope, and a
		// permission handing an operator's correction history to the department that sets pay
		// puts it back in. ADR-0029 has the argument.
		auth.PermQualityReadTeam,
		auth.PermQualityFlagResolve,
		// The background work queue (CP69). Reading it and touching it are separate
		// permissions and the administrator is the only role that holds the second: pausing a
		// kind stops the synthesis §7.1 promises will be ready before the consultation, and
		// the person who did that has to be findable afterwards.
		auth.PermOpsJobsRead,
		auth.PermOpsJobsManage,
		// The AI gateway's outbound log and its spend (CP70). The budget alert lands on this
		// console, and an alert saying "the day's AI budget is spent" is only actionable by
		// somebody who can then see which agent spent it.
		auth.PermAiGatewayRead,
		// The grounding queue too (CP72), for the reason the administrator holds most of this
		// list: somebody has to be able to when neither the consultant nor QA is in.
		auth.PermAiQualityReview,
		// Reading what an offline device sent and was refused (CP65). Not releasing: the
		// administrator is who revoked the device, and somebody who can both refuse a device and
		// then admit its data has undone their own control.
		auth.PermSyncQuarantineRead,
		// §7.1's button (CP71). The administrator holds it for support rather than for care: a
		// summary that never arrived is an incident somebody has to be able to retry from a
		// console. Note what it does *not* carry — `ai.synthesis.read`, so the administrator can
		// ask for a summary and cannot read one, which is §4.4 holding for a non-clinical role.
		auth.PermAiSynthesisRequest,
	),
	auth.RoleFieldWorker: auth.NewPermissionSet(
		auth.PermPatientReadDemographics,
		auth.PermPatientWriteDemographics,
		auth.PermObservationWriteAnthro,
		auth.PermObservationWriteVitals,
		auth.PermOutreachCapture,
		auth.PermOutreachRead,
		auth.PermVisitOpen,
		auth.PermVisitRead,
		auth.PermVisitAttend,
	),
}

// UnionFor is the permissions a set of roles confers together — what /v1/auth/me reports
// for a person holding them all.
func UnionFor(roles []auth.RoleCode) auth.PermissionSet {
	set := auth.NewPermissionSet()
	for _, role := range roles {
		set.Union(RolePermissions[role])
	}
	return set
}
