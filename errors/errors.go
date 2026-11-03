package errors

import (
	"errors"
	"fmt"

	contractserrors "github.com/gradii/framework/contracts/errors"
)

type errorString struct {
	text   string
	module string
	args   []any
}

// New creates a new error with the provided text and optional module
func New(text string, module ...string) contractserrors.Error {
	err := &errorString{
		text: text,
	}

	if len(module) > 0 {
		err.module = module[0]
	}

	return err
}

func (e *errorString) Args(args ...any) contractserrors.Error {
	return &errorString{
		text:   e.text,
		module: e.module,
		args:   args,
	}
}

func (e *errorString) Error() string {
	formattedText := e.text

	if len(e.args) > 0 {
		formattedText = fmt.Sprintf(e.text, e.args...)
	}

	if e.module != "" {
		formattedText = fmt.Sprintf("[%s] %s", e.module, formattedText)
	}

	return formattedText
}

func (e *errorString) SetModule(module string) contractserrors.Error {
	return &errorString{
		text:   e.text,
		module: module,
		args:   e.args,
	}
}

func (e *errorString) Is(target error) bool {
	t, ok := target.(*errorString)
	if !ok {
		return false
	}
	return e.text == t.text
}

func Is(err, target error) bool {
	return errors.Is(err, target)
}

func As(err error, target any) bool {
	return errors.As(err, target)
}

func Unwrap(err error) error {
	return errors.Unwrap(err)
}

func Ignore(fn func() error) {
	_ = fn()
}

func Join(errs ...error) error {
	return errors.Join(errs...)
}

// wrapError is an error whose message is its own formatted text while errors.Is and errors.As see
// through to the error it wraps. It is how a formatted message keeps matching a declared sentinel:
// list.go builds such a sentinel with wrap, so errors.Is(err, sentinel) still reports true.
type wrapError struct {
	text   string
	module string
	args   []any
	err    error
}

func (e *wrapError) Args(args ...any) contractserrors.Error {
	return &wrapError{text: e.text, module: e.module, args: args, err: e.err}
}

func (e *wrapError) Error() string {
	formattedText := e.text

	if len(e.args) > 0 {
		formattedText = fmt.Sprintf(e.text, e.args...)
	}

	if e.module != "" {
		formattedText = fmt.Sprintf("[%s] %s", e.module, formattedText)
	}

	return formattedText
}

func (e *wrapError) SetModule(module string) contractserrors.Error {
	return &wrapError{text: e.text, module: module, args: e.args, err: e.err}
}

// Is matches another wrapped or plain error with the same text and otherwise asks whether the
// wrapped error matches, so a formatted message answers both to its own template and to its cause.
func (e *wrapError) Is(target error) bool {
	switch t := target.(type) {
	case *wrapError:
		if e.text == t.text {
			return true
		}
	case *errorString:
		if e.text == t.text {
			return true
		}
	}

	return errors.Is(e.err, target)
}

func (e *wrapError) Unwrap() error { return e.err }

// wrap returns an error whose message is text formatted with Args and whose underlying error is
// err, so errors.Is and errors.As keep matching err.
func wrap(err error, text string) contractserrors.Error {
	return &wrapError{text: text, err: err}
}
