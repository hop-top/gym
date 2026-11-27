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
