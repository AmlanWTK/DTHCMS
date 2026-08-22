// Package httpx holds the HTTP plumbing every module shares: the middleware chain, the
// response envelope, and the health endpoints.
package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/logging"
)

// errorEnvelope is the only shape an error ever takes on the wire.
//
// The client branches on Code. Message and MessageBN are shown to the user. Nothing
// internal appears: no query text, no file path, no indication of whether a resource
// the caller may not see happens to exist.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code          string            `json:"code"`
	Kind          string            `json:"kind"`
	Message       string            `json:"message"`
	MessageBN     string            `json:"message_bn"`
	Fields        map[string]string `json:"fields,omitempty"`
	CorrelationID string            `json:"correlation_id,omitempty"`
}

// WriteJSON writes a successful JSON response.
func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	// The header is already written, so an encoding failure cannot change the status.
	// It is logged by the caller's access log via the recovered panic, if any.
	_ = json.NewEncoder(w).Encode(body)
}

// WriteError converts any error into the standard envelope and logs the internal detail.
//
// The split is the point of this function: the client receives a stable code and a
// human message, while the cause — which may name a table, a constraint or a
// third-party failure — goes only to the log.
func WriteError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	appErr := errs.From(err)
	if appErr == nil {
		return
	}

	ctx := r.Context()
	correlationID := logging.CorrelationID(ctx)

	if appErr.Status >= http.StatusInternalServerError {
		logger.ErrorContext(ctx, "request failed",
			"code", appErr.Code,
			"kind", string(appErr.Kind),
			"status", appErr.Status,
			"detail", detailOf(appErr),
			"path", r.URL.Path,
			"method", r.Method)
	} else {
		logger.InfoContext(ctx, "request rejected",
			"code", appErr.Code,
			"kind", string(appErr.Kind),
			"status", appErr.Status,
			"detail", detailOf(appErr),
			"path", r.URL.Path,
			"method", r.Method)
	}

	WriteJSON(w, appErr.Status, errorEnvelope{Error: errorBody{
		Code:          appErr.Code,
		Kind:          string(appErr.Kind),
		Message:       appErr.MessageEN,
		MessageBN:     appErr.MessageBN,
		Fields:        appErr.Fields,
		CorrelationID: correlationID,
	}})
}

func detailOf(e *errs.Error) string {
	if e.Detail == nil {
		return ""
	}
	return e.Detail.Error()
}

// DecodeJSON reads a JSON request body, rejecting unknown fields.
//
// Unknown fields are an error rather than an ignored curiosity: silently dropping a
// field a client believed it sent is how a clinical value goes missing without anyone
// noticing.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		return errs.ErrBadRequest.WithDetail(err)
	}
	if decoder.More() {
		return errs.ErrBadRequest.WithDetail(errTrailingContent)
	}
	return nil
}

var errTrailingContent = &staticError{"request body contains more than one JSON value"}

type staticError struct{ msg string }

func (e *staticError) Error() string { return e.msg }
