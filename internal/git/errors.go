package git

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

type ErrorKind uint8

const (
	ErrorGitUnavailable ErrorKind = iota
	ErrorGitTooOld
	ErrorNotRepository
	ErrorBareRepository
	ErrorTimeout
	ErrorCancelled
	ErrorNoUpstream
	ErrorValidation
	ErrorBusy
	ErrorAuthentication
	ErrorNetwork
	ErrorDirtyCheckout
	ErrorWorktreeConflict
	ErrorRejectedPush
	ErrorParse
	ErrorHerdr
	ErrorCommand
)

type OperationError struct {
	Kind      ErrorKind
	Operation string
	Phase     string
	ExitCode  int
	Stderr    string
	Err       error
}

func (e *OperationError) Error() string {
	switch e.Kind {
	case ErrorGitUnavailable:
		return "Git is not installed or not in PATH."
	case ErrorGitTooOld:
		return "Git 2.31 or newer is required."
	case ErrorNotRepository:
		return "No Git repository found."
	case ErrorBareRepository:
		return "Bare Git repositories have no working tree."
	case ErrorTimeout:
		return "Git operation timed out."
	case ErrorCancelled:
		return "Git operation was cancelled."
	case ErrorNoUpstream:
		return "The current branch has no usable remote upstream."
	case ErrorBusy:
		return "Repository is busy. Finish the other Git operation and refresh."
	case ErrorAuthentication:
		return "Configure Git credentials outside Herdr, then retry."
	case ErrorNetwork:
		return "Could not reach the remote. Check your connection and retry."
	case ErrorDirtyCheckout:
		return "Local changes block branch checkout. Commit or stash them outside Herdr, then retry."
	case ErrorWorktreeConflict:
		if e.Stderr != "" {
			return "Branch is already checked out in another worktree: " + e.Stderr
		}
		return "Branch is already checked out in another worktree."
	case ErrorRejectedPush:
		return "The remote rejected the push. Refresh and resolve it outside Herdr; no force push was attempted."
	case ErrorValidation:
		if e.Stderr != "" {
			return e.Stderr
		}
		return "Invalid branch name."
	case ErrorParse:
		return "Git returned unsupported or malformed output."
	}

	message := "Git operation failed."
	if e.Operation != "" {
		message = fmt.Sprintf("Git %s failed.", e.Operation)
	}
	if e.Stderr != "" {
		message += " " + e.Stderr
	}
	return message
}

func (e *OperationError) Unwrap() error { return e.Err }

func IsKind(err error, kind ErrorKind) bool {
	var operationErr *OperationError
	return errors.As(err, &operationErr) && operationErr.Kind == kind
}

var credentialURL = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)([^/@\s]+)@`)

func sanitizeDetail(data []byte) string {
	var b strings.Builder
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			fmt.Fprintf(&b, `\x%02X`, data[0])
			data = data[1:]
			continue
		}
		if r == '\n' || r == '\r' || r == '\t' {
			b.WriteByte(' ')
		} else if r == '\\' {
			b.WriteString(`\\`)
		} else if r < 0x20 || r == 0x7f {
			for _, c := range data[:size] {
				fmt.Fprintf(&b, `\x%02X`, c)
			}
		} else {
			b.WriteRune(r)
		}
		data = data[size:]
	}
	detail := strings.Join(strings.Fields(b.String()), " ")
	detail = credentialURL.ReplaceAllString(detail, `${1}<redacted>@`)
	if len(detail) <= stderrLimit {
		return detail
	}
	detail = detail[:stderrLimit]
	for !utf8.ValidString(detail) {
		detail = detail[:len(detail)-1]
	}
	return detail
}

func commandError(operation, phase string, result commandResult) error {
	kind := ErrorCommand
	detail := sanitizeDetail(result.stderr)
	lower := strings.ToLower(detail + " " + sanitizeDetail(result.stdout))
	switch {
	case errors.Is(result.err, errTimedOut):
		kind = ErrorTimeout
	case errors.Is(result.err, errCancelled):
		kind = ErrorCancelled
	case strings.Contains(lower, "index.lock") || strings.Contains(lower, "another git process") || strings.Contains(lower, "unable to create") && strings.Contains(lower, ".lock"):
		kind = ErrorBusy
	case operation == "checkout" && (strings.Contains(lower, "would be overwritten by checkout") || strings.Contains(lower, "local changes")):
		kind = ErrorDirtyCheckout
	case strings.Contains(lower, "already checked out at") || strings.Contains(lower, "used by worktree"):
		kind = ErrorWorktreeConflict
	case strings.Contains(lower, "authentication failed") || strings.Contains(lower, "could not read username") || strings.Contains(lower, "permission denied (publickey"):
		kind = ErrorAuthentication
	case strings.Contains(lower, "could not resolve host") || strings.Contains(lower, "could not read from remote repository") || strings.Contains(lower, "connection timed out") || strings.Contains(lower, "connection refused"):
		kind = ErrorNetwork
	case operation == "push" && (strings.Contains(lower, "rejected") || strings.Contains(lower, "non-fast-forward")):
		kind = ErrorRejectedPush
	case operation == "create branch" && strings.Contains(lower, "already exists"):
		kind = ErrorValidation
	}
	return &OperationError{Kind: kind, Operation: operation, Phase: phase, ExitCode: result.exitCode, Stderr: detail, Err: result.err}
}

func parseError(err error) error {
	return &OperationError{Kind: ErrorParse, Operation: "parse", ExitCode: -1, Err: err}
}
