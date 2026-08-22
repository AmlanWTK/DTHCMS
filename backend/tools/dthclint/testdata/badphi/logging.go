package badphi

import "log/slog"

func offenders(log *slog.Logger, patientName, nid, phone string) {
	log.Info("patient registered", "name", patientName)
	log.Warn("duplicate suspected", "national_id", nid)
	log.Error("sms failed", "phone", phone)
	log.Info("login failed", "password", "hunter2")
}

func permitted(log *slog.Logger, patientID string, ageMonths int) {
	log.Info("patient registered", "patient_id", patientID, "age_months", ageMonths)

	// A deliberate, reviewed exception must say why.
	log.Info("support ticket", "name", "clinic-printer-01") // phicheck:ignore device label, not a person
}
