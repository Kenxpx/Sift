package core

import (
	"errors"
	"fmt"
)

// Sentinel errors that callers match with errors.Is.
var (
	// ErrNotFound reports a missing corpus, generation or document.
	ErrNotFound = errors.New("sift: not found")
	// ErrIntegrity reports an index whose files disagree with its manifest.
	ErrIntegrity = errors.New("sift: index integrity")
	// ErrConfig reports an unusable configuration.
	ErrConfig = errors.New("sift: configuration")
	// ErrQuery reports a query that cannot be parsed.
	ErrQuery = errors.New("sift: query")
	// ErrUsage reports a malformed command invocation.
	ErrUsage = errors.New("sift: usage")
)

// IntegrityError explains why a published index cannot be trusted. It always
// names the offending path so operators can act on the message alone.
type IntegrityError struct {
	// Path is the file the problem was found in, relative to the output dir.
	Path string
	// Reason is a short lower-case explanation, for example "missing",
	// "digest mismatch" or "belongs to generation 3".
	Reason string
}

// Error implements error.
func (e *IntegrityError) Error() string {
	return fmt.Sprintf("sift: index integrity: %s: %s", e.Path, e.Reason)
}

// Is lets errors.Is(err, ErrIntegrity) match every IntegrityError.
func (e *IntegrityError) Is(target error) bool { return target == ErrIntegrity }

// ConfigError explains why a configuration was rejected.
type ConfigError struct {
	Field  string
	Reason string
}

// Error implements error.
func (e *ConfigError) Error() string {
	return fmt.Sprintf("sift: configuration: %s: %s", e.Field, e.Reason)
}

// Is lets errors.Is(err, ErrConfig) match every ConfigError.
func (e *ConfigError) Is(target error) bool { return target == ErrConfig }

// QueryError explains why a query could not be parsed.
type QueryError struct {
	// Position is the zero-based offset in the query text.
	Position int
	Reason   string
}

// Error implements error.
func (e *QueryError) Error() string {
	return fmt.Sprintf("sift: query: at %d: %s", e.Position, e.Reason)
}

// Is lets errors.Is(err, ErrQuery) match every QueryError.
func (e *QueryError) Is(target error) bool { return target == ErrQuery }

// CorpusError attaches a corpus name to an error raised while working on one
// corpus of a workspace, so partial failures stay attributable.
type CorpusError struct {
	Corpus string
	Path   string
	Err    error
}

// Error implements error.
func (e *CorpusError) Error() string {
	return fmt.Sprintf("sift: corpus %s: %s: %v", e.Corpus, e.Path, e.Err)
}

// Unwrap exposes the wrapped error.
func (e *CorpusError) Unwrap() error { return e.Err }
