// Package errs is Sapien's structured error model (PLAN §29). Every surface
// (HTTP, CLI, MCP) serializes the same object.
package errs

import (
	"errors"
	"fmt"

	"github.com/gs-sinha/sapien/internal/domain"
)

// Code is a stable, machine-readable error code.
type Code string

const (
	WorkspaceNotFound Code = "E_WORKSPACE_NOT_FOUND"
	ServiceSource     Code = "E_SERVICE_SOURCE"
	ServiceNotFound   Code = "E_SERVICE_NOT_FOUND"
	ContractParse     Code = "E_CONTRACT_PARSE"
	OperationNotFound Code = "E_OPERATION_NOT_FOUND"
	FlowNotFound      Code = "E_FLOW_NOT_FOUND"
	FlowInvalid       Code = "E_FLOW_INVALID"
	Expr              Code = "E_EXPR"
	InputMissing      Code = "E_INPUT_MISSING"
	EnvNotFound       Code = "E_ENV_NOT_FOUND"
	SecretMissing     Code = "E_SECRET_MISSING"
	HTTPTransport     Code = "E_HTTP_TRANSPORT"
	AssertionFailed   Code = "E_ASSERTION_FAILED"
	UntilTimeout      Code = "E_UNTIL_TIMEOUT"
	Cancelled         Code = "E_CANCELLED"
	PermissionDenied  Code = "E_PERMISSION_DENIED"
	ProductionBlocked Code = "E_PRODUCTION_BLOCKED"
	RunNotFound       Code = "E_RUN_NOT_FOUND"
	MemoryNotFound    Code = "E_MEMORY_NOT_FOUND"
	ExampleNotFound   Code = "E_EXAMPLE_NOT_FOUND"
	DocNotFound       Code = "E_DOC_NOT_FOUND"
	FrictionNotFound  Code = "E_FRICTION_NOT_FOUND"
	Conflict          Code = "E_CONFLICT"
	Invalid           Code = "E_INVALID"
	NotImplemented    Code = "E_NOT_IMPLEMENTED"
	DaemonUnavailable Code = "E_DAEMON_UNAVAILABLE"
	Internal          Code = "E_INTERNAL"
)

// Error is the structured error.
type Error struct {
	Code    Code              `json:"code"`
	Message string            `json:"message"`
	Details map[string]any    `json:"details,omitempty"`
	Source  *domain.SourceLoc `json:"source,omitempty"`
	Hint    string            `json:"hint,omitempty"`
	Cause   error             `json:"-"`
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap exposes the cause.
func (e *Error) Unwrap() error { return e.Cause }

// New creates an error with a code and a formatted message.
func New(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Wrap attaches a cause.
func Wrap(code Code, cause error, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Cause: cause}
}

// WithDetail adds a detail key and returns the error for chaining.
func (e *Error) WithDetail(key string, value any) *Error {
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	e.Details[key] = value
	return e
}

// WithHint sets a hint and returns the error for chaining.
func (e *Error) WithHint(hint string) *Error { e.Hint = hint; return e }

// WithSource sets a source location and returns the error for chaining.
func (e *Error) WithSource(loc domain.SourceLoc) *Error { e.Source = &loc; return e }

// CodeOf returns the code of err, or Internal for unknown errors.
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return Internal
}

// As returns the *Error inside err, wrapping plain errors as Internal.
func As(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Code: Internal, Message: err.Error(), Cause: err}
}

// Is reports whether err carries the given code.
func Is(err error, code Code) bool { return CodeOf(err) == code }

// ToInfo converts an error to its persisted form.
func ToInfo(err error) *domain.ErrorInfo {
	if err == nil {
		return nil
	}
	e := As(err)
	return &domain.ErrorInfo{Code: string(e.Code), Message: e.Message, Details: e.Details}
}

// ExitCode maps an error to a CLI exit status (PLAN §21):
// 0 ok, 1 assertion failure, 2 error, 3 blocked (permission/production).
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	switch CodeOf(err) {
	case AssertionFailed, UntilTimeout:
		return 1
	case PermissionDenied, ProductionBlocked:
		return 3
	default:
		return 2
	}
}

// HTTPStatus maps an error to an HTTP status.
func HTTPStatus(err error) int {
	switch CodeOf(err) {
	case WorkspaceNotFound, ServiceNotFound, OperationNotFound, FlowNotFound, RunNotFound, MemoryNotFound, ExampleNotFound, DocNotFound, EnvNotFound, FrictionNotFound:
		return 404
	case FlowInvalid, Expr, InputMissing, Invalid, ContractParse:
		return 400
	case PermissionDenied, ProductionBlocked:
		return 403
	case Conflict:
		return 409
	case NotImplemented:
		return 501
	case AssertionFailed, UntilTimeout:
		return 200 // a failed run is a successful request; status lives in the run
	default:
		return 500
	}
}
