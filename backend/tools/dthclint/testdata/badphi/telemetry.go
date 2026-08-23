package badphi

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// A span attribute leaves the process exactly as a log line does, and reaches the same
// third-party backend. These four are the same mistake in a different vocabulary.
func spanOffenders(span trace.Span, patientName, nid string) {
	span.SetAttributes(
		attribute.String("patient_name", patientName),
		attribute.String("enduser.email", "someone@example.com"),
	)
	span.SetAttributes(attribute.String("national_id", nid))
}

// A metric label carries a second hazard: one time series per distinct value. Labelling
// by patient name breaks the metrics backend as surely as it breaks confidentiality.
func metricOffenders(ctx context.Context, counter metric.Int64Counter, phone string) {
	counter.Add(ctx, 1, metric.WithAttributes(attribute.String("phone", phone)))
}

func spanPermitted(span trace.Span, patientID string, ageMonths int) {
	span.SetAttributes(
		attribute.String("patient_id", patientID),
		attribute.Int("age_months", ageMonths),
		attribute.String("http.route", "/v1/patients/{patientID}"),
	)
}
