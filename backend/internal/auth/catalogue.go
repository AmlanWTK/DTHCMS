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

	PermRecordsUpload = "records.upload"
	PermRecordsRead   = "records.read" // sensitive
	PermRecordsVerify = "records.verify"

	PermLabOrder       = "lab.order"
	PermLabResultEnter = "lab.result.enter"
	PermLabRead        = "lab.read"

	PermDiagnosisRead  = "diagnosis.read"  // sensitive
	PermDiagnosisWrite = "diagnosis.write" // sensitive

	PermPrescriptionDraft    = "prescription.draft"
	PermPrescriptionSign     = "prescription.sign"
	PermPrescriptionRead     = "prescription.read"
	PermPrescriptionDispense = "prescription.dispense"

	PermAiSynthesisRead     = "ai.synthesis.read" // sensitive
	PermAiSuggestionApprove = "ai.suggestion.approve"

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

	PermStationConfigure = "station.configure"

	PermFacilityConfigure = "facility.configure"

	PermReportReadOperational = "report.read.operational"
	PermReportReadFinancial   = "report.read.financial"

	PermHrAttendanceRead  = "hr.attendance.read"
	PermHrPerformanceRead = "hr.performance.read"
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
	PermRecordsUpload,
	PermRecordsRead,
	PermRecordsVerify,
	PermLabOrder,
	PermLabResultEnter,
	PermLabRead,
	PermDiagnosisRead,
	PermDiagnosisWrite,
	PermPrescriptionDraft,
	PermPrescriptionSign,
	PermPrescriptionRead,
	PermPrescriptionDispense,
	PermAiSynthesisRead,
	PermAiSuggestionApprove,
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
	PermStationConfigure,
	PermFacilityConfigure,
	PermReportReadOperational,
	PermReportReadFinancial,
	PermHrAttendanceRead,
	PermHrPerformanceRead,
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
