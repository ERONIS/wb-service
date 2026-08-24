// Package core_errors contains stable error categories shared by application
// layers.
//
// Callers classify wrapped errors with errors.Is instead of depending on
// implementation-specific messages. Feature packages should keep their
// domain-specific errors close to the owning feature and wrap one of these
// categories when the error has an application-wide meaning.
package core_errors

import "errors"

var (
	// ErrNotFound means that the requested entity does not exist.
	ErrNotFound = errors.New("not found")

	// ErrInvalidArgument means that the caller supplied invalid input.
	ErrInvalidArgument = errors.New("invalid argument")

	// ErrConflict means that the operation conflicts with current state.
	ErrConflict = errors.New("conflict")

	// ErrForbidden means that the caller is not allowed to perform the
	// operation in the current authorization context.
	ErrForbidden = errors.New("forbidden")
)
