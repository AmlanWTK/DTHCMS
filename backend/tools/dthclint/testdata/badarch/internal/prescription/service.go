package prescription

import (
	"example.com/backend/internal/patient"
	// Not allowed: prescription may not depend on records.
	"example.com/backend/internal/records"
)

var _ = patient.Lookup
var _ = records.Ingest
