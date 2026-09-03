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
		auth.PermObservationCorrectRequest,
	),
	auth.RoleAnthropometry: auth.NewPermissionSet(
		auth.PermPatientReadDemographics,
		auth.PermObservationWriteAnthro,
		auth.PermObservationReadValues,
		auth.PermObservationCorrectRequest,
	),
	auth.RoleCounselor: auth.NewPermissionSet(
		auth.PermPatientReadDemographics,
		auth.PermObservationWriteLifestyle,
		auth.PermObservationReadValues,
		auth.PermCounselingTick,
		auth.PermObservationCorrectRequest,
	),
	auth.RoleHistory: auth.NewPermissionSet(
		auth.PermPatientReadDemographics,
		auth.PermPatientReadAllergies,
		auth.PermPatientReadClinical,
		auth.PermObservationWriteHistory,
		auth.PermObservationReadValues,
		auth.PermRecordsRead,
		auth.PermObservationCorrectRequest,
	),
	auth.RoleClinicalAssistant: auth.NewPermissionSet(
		auth.PermPatientReadDemographics,
		auth.PermPatientReadAllergies,
		auth.PermObservationWriteVitals,
		auth.PermObservationReadValues,
		auth.PermLabRead,
		auth.PermLabResultEnter,
		auth.PermObservationCorrectRequest,
	),
	auth.RoleJuniorDoctor: auth.NewPermissionSet(
		auth.PermPatientReadDemographics,
		auth.PermPatientReadAllergies,
		auth.PermPatientReadClinical,
		auth.PermObservationWriteVitals,
		auth.PermObservationReadValues,
		auth.PermObservationCorrectRequest,
		auth.PermObservationCorrectApprove,
		auth.PermRecordsRead,
		auth.PermLabRead,
		auth.PermLabOrder,
		auth.PermLabResultEnter,
		auth.PermDiagnosisRead,
		auth.PermPrescriptionDraft,
		auth.PermAiSynthesisRead,
	),
	auth.RoleRecords: auth.NewPermissionSet(
		auth.PermPatientReadDemographics,
		auth.PermPatientMerge,
		auth.PermRecordsUpload,
		auth.PermRecordsRead,
		auth.PermRecordsVerify,
	),
	auth.RoleNutritionist: auth.NewPermissionSet(
		auth.PermPatientReadDemographics,
		auth.PermPatientReadClinical,
		auth.PermObservationWriteNutrition,
		auth.PermObservationReadValues,
		auth.PermLabRead,
	),
	auth.RoleExercise: auth.NewPermissionSet(
		auth.PermPatientReadDemographics,
		auth.PermPatientReadClinical,
		auth.PermObservationWriteExercise,
		auth.PermObservationReadValues,
	),
	auth.RolePhysician: auth.NewPermissionSet(
		auth.PermPatientReadDemographics,
		auth.PermPatientReadAllergies,
		auth.PermPatientReadClinical,
		auth.PermObservationReadValues,
		auth.PermObservationCorrectApprove,
		auth.PermRecordsRead,
		auth.PermLabRead,
		auth.PermLabOrder,
		auth.PermLabResultEnter,
		auth.PermDiagnosisRead,
		auth.PermDiagnosisWrite,
		auth.PermPrescriptionDraft,
		auth.PermPrescriptionSign,
		auth.PermPrescriptionRead,
		auth.PermAiSynthesisRead,
		auth.PermAiSuggestionApprove,
		auth.PermCounselingTemplateWrite,
		auth.PermQaClear,
		auth.PermCrmRead,
		auth.PermReportReadOperational,
		auth.PermAuditRead,
	),
	auth.RoleQa: auth.NewPermissionSet(
		auth.PermPatientReadDemographics,
		auth.PermPatientReadClinical,
		auth.PermObservationReadValues,
		auth.PermDiagnosisRead,
		auth.PermPrescriptionRead,
		auth.PermLabRead,
		auth.PermRecordsRead,
		auth.PermQaReview,
		auth.PermQaClear,
		auth.PermQaBounce,
		auth.PermAuditRead,
		auth.PermHrPerformanceRead,
	),
	auth.RoleRxEducator: auth.NewPermissionSet(
		auth.PermPatientReadDemographics,
		auth.PermPrescriptionRead,
		auth.PermEducationRecord,
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
	),
	auth.RoleFieldWorker: auth.NewPermissionSet(
		auth.PermPatientReadDemographics,
		auth.PermPatientWriteDemographics,
		auth.PermObservationWriteAnthro,
		auth.PermObservationWriteVitals,
		auth.PermOutreachCapture,
		auth.PermOutreachRead,
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
