package install

import "fmt"

// Admission rejection reasons. Stable strings: they are published as
// gym.skill.install.rejected reasons and printed to the operator.
const (
	ReasonManifestInvalid        = "manifest-invalid"
	ReasonManifestMissing        = "manifest-missing"
	ReasonDigestMismatch         = "digest-mismatch"
	ReasonDigestUnsupportedEntry = "digest-unsupported-entry"
	ReasonToolsUndeclared        = "tools-undeclared"
	ReasonDataClassExceeds       = "data-class-exceeds-ceiling"
	ReasonApprovalRequired       = "approval-required"
	ReasonApprovalRevoked        = "approval-revoked"
	// ReasonValidatorFailed is not an admission check: the configured
	// validator command failed or could not run.
	ReasonValidatorFailed = "validator-failed"
)

// AdmissionError is a hard admission failure raised in Go. It is not
// relaxable by --insecure and does not depend on the event bus.
type AdmissionError struct {
	Reason string
	Detail string
}

func (e *AdmissionError) Error() string {
	if e.Detail == "" {
		return "admission: " + e.Reason
	}
	return fmt.Sprintf("admission: %s: %s", e.Reason, e.Detail)
}

func admissionErrorf(reason, format string, args ...any) *AdmissionError {
	return &AdmissionError{Reason: reason, Detail: fmt.Sprintf(format, args...)}
}

// ValidatorError is a validator command failure (non-zero exit, timeout,
// or the command could not run). It is a rejection, unlike I/O or
// source-fetch errors.
type ValidatorError struct {
	Err    error
	Output string
}

func (e *ValidatorError) Error() string {
	if e.Output == "" {
		return "validation failed: " + e.Err.Error()
	}
	return fmt.Sprintf("validation failed: %v\n%s", e.Err, e.Output)
}

func (e *ValidatorError) Unwrap() error { return e.Err }
