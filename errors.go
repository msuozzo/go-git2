package git2

import (
	"errors"
	"fmt"
	"runtime/debug"
)

// Sentinel errors. They are returned unwrapped, so callers may compare with
// ==, except for the ErrMalformed errors, which wrap details and need
// errors.Is.
var (
	ErrRepositoryNotExists = errors.New("repository does not exist")
	ErrObjectNotFound      = errors.New("object not found")
	ErrReferenceNotFound   = errors.New("reference not found")
	ErrTagNotFound         = errors.New("tag not found")
	ErrFileNotFound        = errors.New("file not found")
	ErrDirectoryNotFound   = errors.New("directory not found")
	ErrEntryNotFound       = errors.New("entry not found")
	ErrInvalidType         = errors.New("invalid object type")
	ErrUnsupportedObject   = errors.New("unsupported object type")
	ErrClosed              = errors.New("repository is closed")
	ErrMalformedCommit     = errors.New("malformed commit")
	ErrMalformedTree       = errors.New("malformed tree")
	ErrMalformedTag        = errors.New("malformed tag")

	// ErrFault reports a bug hit while reading the repository, such as a
	// trap in the module or a panic while decoding its data. The error
	// returned is a *FaultError that wraps ErrFault. Afterwards the
	// Repository is unusable, and only Close succeeds.
	ErrFault = errors.New("git2: internal fault")

	// ErrStop stops an iteration early when returned from a ForEach
	// callback. ForEach then returns nil.
	ErrStop = errors.New("stop iter")
)

// errGitFile is returned when a working tree's .git is a file rather than a
// directory.
var errGitFile = errors.New("git2: .git is a gitdir file, as in submodule checkouts and " +
	"linked worktrees, which is not supported. Open the repository it belongs to")

// FaultError is the error returned for a fault. See ErrFault.
type FaultError struct {
	Value any    // the recovered panic value
	Stack []byte // the stack where the fault occurred, for bug reports
}

func (e *FaultError) Error() string { return fmt.Sprintf("%v: %v", ErrFault, e.Value) }

// Unwrap returns ErrFault.
func (e *FaultError) Unwrap() error { return ErrFault }

// newFault returns the error for panic value p. Call it from the deferred
// function that recovered p. Deferred calls run before the panicking frames
// unwind, so the stack still shows where the panic happened.
func newFault(p any) *FaultError { return &FaultError{Value: p, Stack: debug.Stack()} }

// Error is an error reported by libgit2.
type Error struct {
	Code    int    // git_error_code
	Class   int    // git_error_t
	Message string // libgit2's message
}

func (e *Error) Error() string {
	return fmt.Sprintf("libgit2: %s (code %d, class %d)", e.Message, e.Code, e.Class)
}
