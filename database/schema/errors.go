package schema

import "github.com/gradii/framework/errors"

// schemaError retains the public operation message and the underlying cause so
// callers can inspect a dialect error without parsing a formatted SQL message.
type schemaError struct{ context, cause error }

func (e schemaError) Error() string        { return e.context.Error() }
func (e schemaError) Unwrap() error        { return e.cause }
func (e schemaError) Is(target error) bool { return errors.Is(e.context, target) }

func withSchemaCause(context, cause error) error { return schemaError{context, cause} }
