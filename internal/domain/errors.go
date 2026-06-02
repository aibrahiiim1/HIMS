package domain

import "errors"

// Sentinel errors the rest of the app inspects via errors.Is. Storage
// implementations map driver/db errors onto these.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
	ErrInvalid  = errors.New("invalid input")
)

// NotFoundError carries which resource was missing.
type NotFoundError struct {
	Resource string
	ID       string
}

func (e *NotFoundError) Error() string {
	if e.ID != "" {
		return e.Resource + " not found: " + e.ID
	}
	return e.Resource + " not found"
}
func (e *NotFoundError) Is(target error) bool { return target == ErrNotFound }

// ConflictError is a uniqueness / FK violation surfaced to callers.
type ConflictError struct{ Message string }

func (e *ConflictError) Error() string        { return e.Message }
func (e *ConflictError) Is(target error) bool { return target == ErrConflict }

// InvalidInputError is a domain-rule / check-constraint violation.
type InvalidInputError struct{ Message string }

func (e *InvalidInputError) Error() string        { return e.Message }
func (e *InvalidInputError) Is(target error) bool { return target == ErrInvalid }
