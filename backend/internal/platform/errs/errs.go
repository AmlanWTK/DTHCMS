// Package errs defines the single error type DTHCMS returns to clients.
//
// Two properties matter more than elegance here:
//
//   - A client must be able to branch on a stable machine code, never on a message.
//   - Internal detail is logged and never returned. An error message that leaks a query,
//     a path or a patient's existence is an information disclosure.
//
// Clinical errors are distinguished from technical ones because the interface must treat
// them differently: a blocked safety rule needs an override path, a database timeout
// needs a retry button.
package errs

import (
	"errors"
	"fmt"
	"net/http"
)

// Kind separates errors the user can act on from errors the system must recover from.
type Kind string

const (
	// KindValidation — the request was malformed or failed a rule. The user can fix it.
	KindValidation Kind = "validation"
	// KindAuth — not authenticated, or not permitted. Never reveals which.
	KindAuth Kind = "auth"
	// KindNotFound — the resource does not exist, or the caller may not know that it does.
	KindNotFound Kind = "not_found"
	// KindConflict — the request contradicts current state.
	KindConflict Kind = "conflict"
	// KindClinical — a clinical rule blocked the action. Needs a clinical response,
	// which may include a recorded override.
	KindClinical Kind = "clinical"
	// KindTechnical — the system failed. Not the user's fault, and retryable.
	KindTechnical Kind = "technical"
)

// Error is the only error type crossing the API boundary.
type Error struct {
	// Code is stable and machine-readable. Clients branch on this.
	Code string
	// Kind drives how the interface responds.
	Kind Kind
	// Status is the HTTP status to return.
	Status int
	// MessageEN and MessageBN are shown to the user. Both are required for anything a
	// clinic operator can trigger: half the staff work in Bangla.
	MessageEN string
	MessageBN string
	// Detail is logged, never returned. It may contain anything useful for debugging
	// except patient identity.
	Detail error
	// Fields carries per-field validation messages, keyed by field name.
	Fields map[string]string
}

func (e *Error) Error() string {
	if e.Detail != nil {
		return fmt.Sprintf("%s: %v", e.Code, e.Detail)
	}
	return e.Code
}

// Unwrap exposes the internal cause to errors.Is and errors.As.
func (e *Error) Unwrap() error { return e.Detail }

// WithDetail attaches an internal cause. The returned error is a copy, so the shared
// package-level errors below are never mutated.
func (e *Error) WithDetail(err error) *Error {
	clone := *e
	clone.Detail = err
	return &clone
}

// WithField attaches a per-field validation message.
func (e *Error) WithField(field, message string) *Error {
	clone := *e
	clone.Fields = make(map[string]string, len(e.Fields)+1)
	for k, v := range e.Fields {
		clone.Fields[k] = v
	}
	clone.Fields[field] = message
	return &clone
}

// New builds an error.
func New(code string, kind Kind, status int, messageEN, messageBN string) *Error {
	return &Error{Code: code, Kind: kind, Status: status, MessageEN: messageEN, MessageBN: messageBN}
}

// The standard catalogue. Domain modules add their own; these are the ones the platform
// itself needs. Bangla wording is provisional and will be reviewed by a native speaker
// before any of it reaches a clinic screen.
var (
	ErrInternal = New("INTERNAL", KindTechnical, http.StatusInternalServerError,
		"Something went wrong. The team has been notified.",
		"কিছু একটা সমস্যা হয়েছে। বিষয়টি জানানো হয়েছে।")

	ErrUnavailable = New("UNAVAILABLE", KindTechnical, http.StatusServiceUnavailable,
		"The service is temporarily unavailable. Please try again.",
		"সেবাটি সাময়িকভাবে বন্ধ আছে। আবার চেষ্টা করুন।")

	ErrTimeout = New("TIMEOUT", KindTechnical, http.StatusGatewayTimeout,
		"The request took too long. Please try again.",
		"অনুরোধটি সম্পন্ন হতে বেশি সময় নিচ্ছে। আবার চেষ্টা করুন।")

	ErrBadRequest = New("BAD_REQUEST", KindValidation, http.StatusBadRequest,
		"The request could not be understood.",
		"অনুরোধটি বোঝা যায়নি।")

	ErrValidation = New("VALIDATION_FAILED", KindValidation, http.StatusUnprocessableEntity,
		"Some values need correcting.",
		"কিছু তথ্য সংশোধন করতে হবে।")

	ErrPayloadTooLarge = New("PAYLOAD_TOO_LARGE", KindValidation, http.StatusRequestEntityTooLarge,
		"The request is too large.",
		"অনুরোধটি অনেক বড়।")

	ErrUnauthenticated = New("UNAUTHENTICATED", KindAuth, http.StatusUnauthorized,
		"Please sign in again.",
		"অনুগ্রহ করে আবার সাইন ইন করুন।")

	// ErrForbidden is deliberately identical whether the resource is missing or merely
	// forbidden. Telling a caller that a patient exists but is out of reach is itself a
	// disclosure.
	ErrForbidden = New("FORBIDDEN", KindAuth, http.StatusForbidden,
		"You do not have permission to do that.",
		"এই কাজটি করার অনুমতি আপনার নেই।")

	ErrNotFound = New("NOT_FOUND", KindNotFound, http.StatusNotFound,
		"Not found.",
		"পাওয়া যায়নি।")

	ErrConflict = New("CONFLICT", KindConflict, http.StatusConflict,
		"This conflicts with a change someone else made.",
		"অন্য কেউ পরিবর্তন করায় দ্বন্দ্ব তৈরি হয়েছে।")

	ErrRateLimited = New("RATE_LIMITED", KindValidation, http.StatusTooManyRequests,
		"Too many requests. Please slow down.",
		"অনেক বেশি অনুরোধ। একটু ধীরে চেষ্টা করুন।")
)

// From maps any error to an *Error. Unknown errors become ErrInternal with the original
// preserved as detail, so nothing internal is ever exposed by accident.
func From(err error) *Error {
	if err == nil {
		return nil
	}
	var typed *Error
	if errors.As(err, &typed) {
		return typed
	}
	return ErrInternal.WithDetail(err)
}
