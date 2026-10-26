// Package core holds the business rules behind the API: evaluating request
// drafts (validation, placement, naming, approval, quota, lease), resolving
// dynamic form values, and submitting and cancelling requests.
package core

import (
	"errors"
	"fmt"

	"github.com/damhau/rvav/internal/api"
)

// Error kinds the HTTP layer maps to status codes.
var (
	ErrNotFound  = errors.New("not found")
	ErrForbidden = errors.New("forbidden")
)

// ValidationError lists input errors (HTTP 422).
type ValidationError struct {
	Errors []api.FieldError
}

func (e *ValidationError) Error() string {
	if len(e.Errors) == 1 {
		return e.Errors[0].Message
	}
	return fmt.Sprintf("%d inputs are not valid", len(e.Errors))
}

// ConflictError is a state that does not allow the action (HTTP 409).
type ConflictError struct{ Msg string }

func (e *ConflictError) Error() string { return e.Msg }

// UpstreamError is a failure of vCenter, EfficientIP or AWX (HTTP 502).
type UpstreamError struct {
	System string
	Err    error
}

func (e *UpstreamError) Error() string { return fmt.Sprintf("%s: %v", e.System, e.Err) }
func (e *UpstreamError) Unwrap() error { return e.Err }

// BadRequestError is a malformed request (HTTP 400).
type BadRequestError struct{ Msg string }

func (e *BadRequestError) Error() string { return e.Msg }

func fieldErr(field, code, msg string) api.FieldError {
	c := code
	return api.FieldError{Field: field, Code: &c, Message: msg}
}
