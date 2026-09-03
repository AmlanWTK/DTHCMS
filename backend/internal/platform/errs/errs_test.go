package errs

import (
	"errors"
	"net/http"
	"testing"
)

func TestFromWrapsUnknownErrorsAsInternal(t *testing.T) {
	// An unexpected error must never reach a client as itself.
	original := errors.New("pq: password authentication failed for user \"dthcms\"")

	got := From(original)

	if got.Code != "INTERNAL" {
		t.Errorf("Code = %q, want INTERNAL", got.Code)
	}
	if got.Status != http.StatusInternalServerError {
		t.Errorf("Status = %d, want 500", got.Status)
	}
	if !errors.Is(got, original) {
		t.Error("the original error must remain retrievable for logging")
	}
}

func TestWithDetailDoesNotMutateTheShared(t *testing.T) {
	// The catalogue values are package-level. Attaching detail to one request must not
	// contaminate every other request that uses the same error.
	first := ErrValidation.WithDetail(errors.New("first request"))
	second := ErrValidation.WithDetail(errors.New("second request"))

	if ErrValidation.Detail != nil {
		t.Error("the shared error was mutated")
	}
	if first.Detail == second.Detail {
		t.Error("two requests share one detail")
	}
}

func TestWithFieldAccumulates(t *testing.T) {
	err := ErrValidation.
		WithField("date_of_birth", "must be a real date").
		WithField("phone", "must be a Bangladeshi mobile number")

	if len(err.Fields) != 2 {
		t.Fatalf("Fields = %v, want two entries", err.Fields)
	}
	if ErrValidation.Fields != nil {
		t.Error("the shared error accumulated fields")
	}
}

func TestEveryCatalogueErrorIsComplete(t *testing.T) {
	// A missing Bangla message reaches a clinic operator as a blank space.
	catalogue := map[string]*Error{
		"ErrInternal": ErrInternal, "ErrUnavailable": ErrUnavailable, "ErrTimeout": ErrTimeout,
		"ErrBadRequest": ErrBadRequest, "ErrValidation": ErrValidation,
		"ErrPayloadTooLarge": ErrPayloadTooLarge, "ErrUnauthenticated": ErrUnauthenticated,
		"ErrForbidden": ErrForbidden, "ErrNotFound": ErrNotFound, "ErrConflict": ErrConflict,
		"ErrRateLimited": ErrRateLimited, "ErrStepUpRequired": ErrStepUpRequired,
	}

	for name, e := range catalogue {
		if e.Code == "" {
			t.Errorf("%s has no machine code; clients would have to match on text", name)
		}
		if e.MessageEN == "" {
			t.Errorf("%s has no English message", name)
		}
		if e.MessageBN == "" {
			t.Errorf("%s has no Bangla message", name)
		}
		if e.Status < 400 || e.Status > 599 {
			t.Errorf("%s has status %d, which is not an error status", name, e.Status)
		}
		if e.Kind == "" {
			t.Errorf("%s has no kind; the interface cannot tell clinical from technical", name)
		}
	}
}

func TestFromNilIsNil(t *testing.T) {
	if From(nil) != nil {
		t.Error("From(nil) must be nil")
	}
}
