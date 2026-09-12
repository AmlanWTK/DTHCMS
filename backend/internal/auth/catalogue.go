package auth

// The permission catalogue, as Go constants.
//
// A permission checked as a bare string is a permission that silently never passes when the
// string has a typo — and an authorisation check that never passes fails safe, so nobody
// notices until a clinician cannot do their job. Constants make it a compile error instead.
//
// This list and core.permission are two representations of one catalogue, which is a thing
// that can drift. TestPermissionConstantsMatchTheDatabase compares them exactly, in both
// directions: a permission added to the migration and not here fails, and so does one added
// here and not to the migration. Same pattern as the PHI key list, and for the same reason.
const (
	PermPatientReadDemographics  = "patient.read.demographics"
	PermPatientWriteDemographics = "patient.write.demographics"
	PermPatientReadAllergies     = "patient.read.allergies"
	PermPatientReadClinical      = "patient.read.clinical" // sensitive
	PermPatientMerge             = "patient.merge"
	PermPatientConsentRecord     = "patient.consent.record"
	PermPatientConsentRevoke     = "patient.consent.revoke"

	PermObservationWriteAnthro    = "observation.write.anthro"
	PermObservationWriteVitals    = "observation.write.vitals"
	PermObservationWriteLifestyle = "observation.write.lifestyle"
	PermObservationWriteHistory   = "observation.write.history"
	PermObservationWriteNutrition = "observation.write.nutrition"
	PermObservationWriteExercise  = "observation.write.exercise"
	// Station 5's structured examination (CP51): foot, neuropathy, retinopathy,
	// cardiovascular. Separate from the vitals permission it sits beside, because a foot
	// examination and a blood pressure are different acts by different people on different
	// days — and separate from history, which is where CP42 parked the four placeholder EXAM
	// codes before there was an examination screen to write them from.
	PermObservationWriteExam      = "observation.write.exam"
	PermObservationReadValues     = "observation.read.values"
	PermObservationCorrectRequest = "observation.correct.request"
	PermObservationCorrectApprove = "observation.correct.approve"

	// CP38. A visit is not a demographic record and not an observation: reusing
	// patient.write.demographics to open one would mean a physician closing a visit needs
	// the permission to rewrite a name, which is the over-grant §4.4 exists to stop.
	PermVisitOpen   = "visit.open"
	PermVisitClose  = "visit.close"
	PermVisitRead   = "visit.read"
	PermVisitAttend = "visit.attend"
	// CP40. `board.read` is the wall display's own permission rather than `visit.read`: the
	// screen in the waiting area needs an account, and that account should be able to do
	// exactly one thing. `visit.reroute` is a floor supervisor's — rerouting is deciding
	// somebody else's queue is wrong, which is not a station operator's call.
	PermBoardRead    = "board.read"
	PermVisitReroute = "visit.reroute"

	PermCounselingTick          = "counseling.tick"
	PermCounselingTemplateWrite = "counseling.template.write"
	// CP55. Reading a template is not clinical — it is the list of things a counsellor is
	// about to be asked to cover, and every station that touches counselling needs it.
	// Publishing is separate from writing because saving a draft is cheap and reversible,
	// while publishing puts a checklist on every phone on the floor and freezes it forever.
	PermCounselingTemplateRead    = "counseling.template.read"
	PermCounselingTemplatePublish = "counseling.template.publish"
	// CP56. Reading a session is not ticking one: the physician's panel and the traffic board
	// read what was covered and never write, and a panel that needed `counseling.tick` would be
	// a physician's screen carrying the right to write on somebody else's checklist.
	PermCounselingSessionRead = "counseling.session.read"
	// CP57. The valve on the counselling gate, and sensitive: it is the one act in that
	// checkpoint somebody has to answer for, and it is held by nobody who merely works at a
	// station.
	PermCounselingGateOverride = "counseling.gate.override" // sensitive

	PermRecordsUpload = "records.upload"
	PermRecordsRead   = "records.read" // sensitive
	PermRecordsVerify = "records.verify"

	PermLabOrder       = "lab.order"
	PermLabResultEnter = "lab.result.enter"
	PermLabRead        = "lab.read"

	PermDiagnosisRead  = "diagnosis.read"  // sensitive
	PermDiagnosisWrite = "diagnosis.write" // sensitive

	// CP52. Reading the classification is not reading a patient: there is no person in the
	// terminology tables, only the WHO's list of diseases and the clinic's own list of
	// complaints. Guarding the picker with diagnosis.read would mean a history officer who
	// is allowed to type a complaint needs the permission to read somebody's diagnoses,
	// which is exactly the over-grant §4.4 exists to stop.
	PermTerminologyRead = "terminology.read"

	// CP53. Reading a history is reading clinical detail about a person, and §4.4 blinds
	// registration and the pharmacist to exactly that. Writing and confirming are separate
	// from reading and from each other: the physician who reads a history at station 8 does
	// not edit it there — an amendment made in the consulting room, with no officer present
	// to ask, is how a record acquires a fact nobody heard the patient say — and confirming
	// that a carried-forward item is still true is answering a question rather than
	// asserting a new one.
	PermHistoryRead    = "history.read" // sensitive
	PermHistoryWrite   = "history.write"
	PermHistoryConfirm = "history.confirm"

	// CP54. Deliberately *not* sensitive, and the asymmetry with history.read is the point:
	// `patient.read.allergies` already reaches the pharmacist and the prescription educator,
	// roles §4.4 blinds to diagnoses, because an allergy has to reach the person handing over
	// the medicine. Blinding them to it would mean the last person who could catch the
	// mistake is the one person who cannot see the warning.
	PermAllergyWrite = "allergy.write"

	// The medication safety rule library (CP77, D-22). None is sensitive: a rule is a
	// statement about a medicine, not about a patient, and there is no patient identifier in
	// any of its tables. Writing and publishing are the physician's alone — that is D-22 as a
	// grant, and the migration explains why the administrator is not on the list either.
	PermMedicationRuleRead    = "medication.rule.read"
	PermMedicationRuleWrite   = "medication.rule.write"
	PermMedicationRulePublish = "medication.rule.publish"

	// CP78's deterministic safety engine. **Sensitive, unlike the three above, and the
	// asymmetry is the argument.** Reading the rule library is reading a drug label:
	// "pioglitazone is contraindicated in heart failure" names a medicine and nobody else.
	// Running a check is reading *this patient's* kidney function, coded diagnoses and
	// allergies, and multiplying them by what is about to be prescribed — which is precisely
	// the clinical picture §4.4 blinds registration and the pharmacist to. Granted to the two
	// prescribing roles and to QA, which re-runs the interaction and duplicate checks as part
	// of CP83's clearance.
	PermMedicationSafetyCheck = "medication.safety.check" // sensitive

	PermPrescriptionDraft    = "prescription.draft"
	PermPrescriptionSign     = "prescription.sign"
	PermPrescriptionRead     = "prescription.read"
	PermPrescriptionDispense = "prescription.dispense"

	PermAiSynthesisRead = "ai.synthesis.read" // sensitive
	// CP71. Asking for the pre-consultation summary is a **narrower** act than reading one, and
	// not sensitive: it exposes no clinical content, it costs money and queue time, and the
	// person who presses the button is the last assistant in the flow rather than the physician.
	// One permission for both would have meant the exercise specialist reading every diagnosis in
	// the clinic in order to press a button.
	PermAiSynthesisRequest  = "ai.synthesis.request"
	PermAiSuggestionApprove = "ai.suggestion.approve"
	// CP70. Not the synthesis a physician reads: the *gateway's* record of what was sent to a
	// model, what it cost, and which prompt and model version produced it. Its own permission
	// because it answers a different question for a different person — the plan's mitigation for
	// its own headline risk is "a human-reviewable outbound log", and a log nobody can open is not
	// reviewable. Sensitive: the payload names no person, by construction and by three separate
	// checks, but it carries that person's clinical picture in full, which is exactly what §4.4
	// blinds registration and the pharmacist to.
	PermAiGatewayRead = "ai.gateway.read" // sensitive
	// CP72. Recording a verdict on a grounding violation: the model really did invent something,
	// or the check was wrong about it. Its own permission because it is the *only* source of
	// acceptance criterion 2's false-positive rate once the system is running against real prose,
	// and because reading a log and pronouncing on it are different acts a clinic may want to
	// grant separately. Sensitive: a defect carries an excerpt of what the model wrote about a
	// patient, and nobody should be able to classify what they may not read.
	PermAiQualityReview = "ai.quality.review" // sensitive

	PermQaReview = "qa.review"
	PermQaClear  = "qa.clear"
	PermQaBounce = "qa.bounce"

	PermEducationRecord = "education.record"

	PermCrmRead     = "crm.read"
	PermCrmContact  = "crm.contact"
	PermCrmSchedule = "crm.schedule"

	PermResearchQuery  = "research.query"
	PermResearchExport = "research.export"

	PermOutreachCapture = "outreach.capture"
	PermOutreachRead    = "outreach.read"

	// CP75, and the names were reserved here at CP06 before the module existed. Three rather
	// than one, and the splits are §16.1 rather than tidiness: **reading** is wide (a physician
	// prescribing, a pharmacist dispensing, the education officer explaining a cost), **writing**
	// is adding a product and recording a price, and **owning the price review** is narrower
	// again — the person who fixes a typo in a manufacturer's name is not necessarily the person
	// answerable for whether the month's prices were checked, and §16.1 asks the clinic to name
	// somebody for the second.
	//
	// **None of them is sensitive**, and that is a decision rather than an omission. §4.4 blinds
	// registration and the pharmacist from diagnoses and clinical interpretations; a formulary
	// holds trade names, strengths and prices, and the pharmacist is the person §16.1 puts in
	// charge of it. Marking these sensitive would be the access model contradicting itself.
	PermFormularyRead        = "formulary.read"
	PermFormularyWrite       = "formulary.write"
	PermFormularyPriceReview = "formulary.price.review"

	PermStockMovementRecord = "stock.movement.record"

	PermUserInvite     = "user.invite"
	PermUserRead       = "user.read"
	PermUserSuspend    = "user.suspend"
	PermUserDeactivate = "user.deactivate"
	// PermUserCredentialReset: set a password in person, reset an authenticator, end
	// sessions (CP21). Separate from invite and suspend so it can be revoked precisely.
	PermUserCredentialReset = "user.credential.reset"

	PermRoleGrant  = "role.grant"
	PermRoleRevoke = "role.revoke"

	PermDeviceEnroll = "device.enroll"
	PermDeviceRevoke = "device.revoke"

	PermAuditRead = "audit.read"

	// Critical values (CP50). Reading the board and acknowledging an alert are separate on
	// purpose: the officer who typed the value already knows about it, and a clinic where
	// they can close their own alert is one that can clear its board without a clinician
	// ever seeing one.
	PermAlertRead        = "alert.read"
	PermAlertAcknowledge = "alert.acknowledge"

	PermStationConfigure = "station.configure"

	PermFacilityConfigure = "facility.configure"

	PermReportReadOperational = "report.read.operational"
	PermReportReadFinancial   = "report.read.financial"

	PermHrAttendanceRead  = "hr.attendance.read"
	PermHrPerformanceRead = "hr.performance.read"

	// CP63. The operator quality record, and deliberately **not** `hr.performance.read` above
	// it, which HR holds. The plan puts performance-linked pay and discipline out of scope, and a
	// permission that hands an operator's correction history to the department that sets pay puts
	// it back in whatever anybody intends by it — the mechanism decides what happens under
	// pressure, not the intention behind it. ADR-0029 has the argument.
	//
	// Sensitive, because a correction record is a claim about a named colleague's work; an access
	// review should have to explain who holds it. There is no `quality.read.own`: an operator
	// reads their own record with a session and nothing else, since somebody who has to be
	// granted something before they may see their own count will assume it is being kept
	// from them.
	PermQualityReadTeam    = "quality.read.team" // sensitive
	PermQualityFlagResolve = "quality.flag.resolve"

	// CP69. Two rather than one, and the split is the one CP50 made between reading the alert
	// board and acknowledging an alert. Reading queue health is looking at a graph; retrying a
	// dead-lettered job runs code against a patient's record, and pausing a kind stops the
	// synthesis §7.1 promises will be ready before the consultation. The floor supervisor who
	// needs to know whether the queue is healthy should not thereby be able to turn it off.
	//
	// Neither is sensitive: there is no patient in the queue by construction (invariant 90),
	// and an access review that had to justify "can look at a graph of background work" would
	// be one line longer and no more meaningful.
	PermOpsJobsRead   = "ops.jobs.read"
	PermOpsJobsManage = "ops.jobs.manage"

	// CP65. Reading the quarantine is **sensitive** and is the only permission in this system
	// that shows a clinical value from outside the ledger: a held event carries its whole
	// envelope, so this is a blood pressure on a screen and an access review should have to
	// justify it in those terms.
	//
	// Releasing is separate and narrower, and the asymmetry with the administrator is the point.
	// The administrator revoked the device; somebody who can both refuse a device and then admit
	// its data has undone their own control. Deciding that a measurement belongs in a patient's
	// record is the physician's, because they are the one answerable for that record.
	PermSyncQuarantineRead    = "sync.quarantine.read"    // sensitive
	PermSyncQuarantineRelease = "sync.quarantine.release" // sensitive
)

// AllPermissions is every code above, in catalogue order.
//
// Exists so the drift test has something to compare and so an administrative screen can list
// the catalogue without a database round trip.
var AllPermissions = []string{
	PermPatientReadDemographics,
	PermPatientWriteDemographics,
	PermPatientReadAllergies,
	PermPatientReadClinical,
	PermPatientMerge,
	PermPatientConsentRecord,
	PermPatientConsentRevoke,
	PermObservationWriteAnthro,
	PermObservationWriteVitals,
	PermObservationWriteLifestyle,
	PermObservationWriteHistory,
	PermObservationWriteNutrition,
	PermObservationWriteExercise,
	PermObservationWriteExam,
	PermObservationReadValues,
	PermObservationCorrectRequest,
	PermObservationCorrectApprove,
	PermVisitOpen,
	PermVisitClose,
	PermVisitRead,
	PermVisitAttend,
	PermBoardRead,
	PermVisitReroute,
	PermCounselingTick,
	PermCounselingTemplateWrite,
	PermCounselingTemplateRead,
	PermCounselingTemplatePublish,
	PermCounselingSessionRead,
	PermCounselingGateOverride,
	PermRecordsUpload,
	PermRecordsRead,
	PermRecordsVerify,
	PermLabOrder,
	PermLabResultEnter,
	PermLabRead,
	PermDiagnosisRead,
	PermDiagnosisWrite,
	PermTerminologyRead,
	PermHistoryRead,
	PermHistoryWrite,
	PermHistoryConfirm,
	PermAllergyWrite,
	PermMedicationRuleRead,
	PermMedicationRuleWrite,
	PermMedicationRulePublish,
	PermMedicationSafetyCheck,
	PermPrescriptionDraft,
	PermPrescriptionSign,
	PermPrescriptionRead,
	PermPrescriptionDispense,
	PermAiSynthesisRead,
	PermAiSynthesisRequest,
	PermAiSuggestionApprove,
	PermAiGatewayRead,
	PermAiQualityReview,
	PermQaReview,
	PermQaClear,
	PermQaBounce,
	PermEducationRecord,
	PermCrmRead,
	PermCrmContact,
	PermCrmSchedule,
	PermResearchQuery,
	PermResearchExport,
	PermOutreachCapture,
	PermOutreachRead,
	PermFormularyRead,
	PermFormularyWrite,
	PermFormularyPriceReview,
	PermStockMovementRecord,
	PermUserInvite,
	PermUserRead,
	PermUserSuspend,
	PermUserDeactivate,
	PermUserCredentialReset,
	PermRoleGrant,
	PermRoleRevoke,
	PermDeviceEnroll,
	PermDeviceRevoke,
	PermAuditRead,
	PermAlertRead,
	PermAlertAcknowledge,
	PermStationConfigure,
	PermFacilityConfigure,
	PermReportReadOperational,
	PermReportReadFinancial,
	PermHrAttendanceRead,
	PermHrPerformanceRead,
	PermQualityReadTeam,
	PermQualityFlagResolve,
	PermOpsJobsRead,
	PermOpsJobsManage,
	PermSyncQuarantineRead,
	PermSyncQuarantineRelease,
}

// SensitivePermissions reveal a diagnosis or a clinical interpretation.
//
// Blueprint §4.4 blinds registration and the pharmacist to exactly these. The database
// asserts the same thing from core.permission.is_sensitive; this is the list the application
// reasons about before it asks.
var SensitivePermissions = []string{
	PermPatientReadClinical,
	PermRecordsRead,
	PermDiagnosisRead,
	PermDiagnosisWrite,
	PermAiSynthesisRead,
	// The gateway's outbound log (CP70). It contains no identifier — that is the whole point of
	// the module — and it contains every diagnosis, every medication and every clinical narrative
	// the system has ever sent to a model. Blinding §4.4's roles from a patient's record and then
	// handing them the same record with the name removed would be the rule defeated by a
	// technicality.
	PermAiGatewayRead,
	// The grounding defect queue (CP72). A defect is a sentence the model wrote about a patient
	// with the offending number in it, which is a clinical interpretation in miniature — and the
	// act it grants is a judgement about that sentence. Both halves belong where §4.4's blinded
	// roles are not.
	PermAiQualityReview,
	// A history is what the patient brought with them: their conditions, their operations,
	// what their mother has. §4.4's blinded roles do not receive that either.
	PermHistoryRead,
	// A critical value is an interpretation of a measurement — this number means somebody is
	// in danger — and §4.4's blinded roles do not receive interpretations. The measurement
	// itself is not blinded: the officer who took it sees the number they typed.
	PermAlertRead,
	PermAlertAcknowledge,
	// Sending a patient past the counselling gate is a clinical decision about that patient,
	// and the roles §4.4 blinds are not the ones who make it.
	PermCounselingGateOverride,
	// CP78's safety check. The object of this permission is a patient's kidney function,
	// coded diagnoses and allergy list, multiplied by a draft prescription — a clinical
	// interpretation in the fullest sense, and one of the few acts in the system that reads
	// all three at once. The rule library beside it is not sensitive, and the difference
	// between the two is the difference between a drug label and a patient.
	PermMedicationSafetyCheck,
	// A quality record is a claim about a named colleague's work (CP63). No patient in it — an
	// invariant refuses one — but an access review should still have to explain who reads it.
	PermQualityReadTeam,
	// The offline quarantine (CP65), and the strongest case on this list. A held event carries
	// its whole envelope, so reading it is the only way in this system to see a clinical value
	// from outside the ledger — and releasing one puts that value into a patient's permanent
	// record on the reader's authority. Both belong exactly where §4.4's blinded roles are not.
	PermSyncQuarantineRead,
	PermSyncQuarantineRelease,
}

// RoleCode is a role in the catalogue. Roles are referenced by code rather than by id
// because the id differs between every database and the code does not.
type RoleCode string

const (
	RoleRegistration      RoleCode = "REGISTRATION"       // Registration Officer
	RoleAnthropometry     RoleCode = "ANTHROPOMETRY"      // Anthropometry Officer
	RoleCounselor         RoleCode = "COUNSELOR"          // Clinical Counselor
	RoleHistory           RoleCode = "HISTORY"            // Medical History Officer
	RoleClinicalAssistant RoleCode = "CLINICAL_ASSISTANT" // Clinical Assistant
	RoleJuniorDoctor      RoleCode = "JUNIOR_DOCTOR"      // Junior Doctor
	RoleRecords           RoleCode = "RECORDS"            // Medical Records Officer
	RoleNutritionist      RoleCode = "NUTRITIONIST"       // Clinical Nutritionist
	RoleExercise          RoleCode = "EXERCISE"           // Exercise Specialist
	RolePhysician         RoleCode = "PHYSICIAN"          // Chief Consultant
	RoleQa                RoleCode = "QA"                 // Quality Assurance Officer
	RoleRxEducator        RoleCode = "RX_EDUCATOR"        // Prescription Education Officer
	RolePharmacist        RoleCode = "PHARMACIST"         // Pharmacist
	RoleCrm               RoleCode = "CRM"                // Patient Relations Officer
	RoleResearcher        RoleCode = "RESEARCHER"         // Researcher
	RoleHr                RoleCode = "HR"                 // Human Resources Officer
	RoleAdmin             RoleCode = "ADMIN"              // System Administrator
	RoleFieldWorker       RoleCode = "FIELD_WORKER"       // Community Field Worker
)

// AllRoles is the eighteen roles of blueprint §6.3.
var AllRoles = []RoleCode{
	RoleRegistration,
	RoleAnthropometry,
	RoleCounselor,
	RoleHistory,
	RoleClinicalAssistant,
	RoleJuniorDoctor,
	RoleRecords,
	RoleNutritionist,
	RoleExercise,
	RolePhysician,
	RoleQa,
	RoleRxEducator,
	RolePharmacist,
	RoleCrm,
	RoleResearcher,
	RoleHr,
	RoleAdmin,
	RoleFieldWorker,
}

// StationCode identifies one of the twelve stations of blueprint §3.
type StationCode string

const (
	StationRegistration  StationCode = "STN_REGISTRATION"  // step 1: Registration
	StationAnthropometry StationCode = "STN_ANTHROPOMETRY" // step 2: Anthropometry & Screening
	StationCounseling    StationCode = "STN_COUNSELING"    // step 3: Counseling & Lifestyle
	StationHistory       StationCode = "STN_HISTORY"       // step 4: Medical History
	StationExamination   StationCode = "STN_EXAMINATION"   // step 5: Clinical Examination & Vitals
	StationRecords       StationCode = "STN_RECORDS"       // step 6: Medical Records Import
	StationNutrition     StationCode = "STN_NUTRITION"     // step 7: Nutrition Assessment
	StationExercise      StationCode = "STN_EXERCISE"      // step 8: Exercise Assessment
	StationConsultation  StationCode = "STN_CONSULTATION"  // step 9: Physician Consultation
	StationQa            StationCode = "STN_QA"            // step 10: Quality Assurance Review
	StationRxEducation   StationCode = "STN_RX_EDUCATION"  // step 11: Prescription Education
	StationFollowup      StationCode = "STN_FOLLOWUP"      // step 12: Long-Term Monitoring & Follow-Up
)

// AllStations is the twelve stations in their default order.
var AllStations = []StationCode{
	StationRegistration,
	StationAnthropometry,
	StationCounseling,
	StationHistory,
	StationExamination,
	StationRecords,
	StationNutrition,
	StationExercise,
	StationConsultation,
	StationQa,
	StationRxEducation,
	StationFollowup,
}
