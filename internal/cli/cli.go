// Package cli is the sift command line: it parses arguments, calls the
// application layer and renders the result.
//
// Run is the whole surface, and it returns an exit code instead of calling
// os.Exit, so the entire command line is testable with two buffers. The codes
// separate the two ways a command fails: an invocation the command line itself
// rejects, such as an unknown flag or an unparsable query, exits 2 and always
// names the command on standard error, while a command that ran and failed,
// such as indexing a corpus that does not exist, exits 1. Output is
// deterministic: nothing that varies between runs, such as a publication
// timestamp, is ever printed.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"sift/internal/app"
	"sift/internal/core"
)

// Version is the released version of the command line.
const Version = "1.0.0"

// Exit codes returned by Run.
const (
	// ExitOK reports a command that succeeded.
	ExitOK = 0
	// ExitError reports a command that ran and failed.
	ExitError = 1
	// ExitUsage reports an invocation the command line rejected.
	ExitUsage = 2
)

// Run executes one command line and returns the process exit code. Normal
// output is written to stdout and every diagnostic to stderr; a nil writer
// discards what would have been written to it.
func Run(args []string, stdout, stderr io.Writer) int {
	r := &runner{
		app:       app.New(),
		out:       writerOr(stdout),
		err:       writerOr(stderr),
		listen:    net.Listen,
		serveHTTP: http.Serve,
	}
	return r.run(args)
}

// runner holds everything one invocation needs. The listener and serving hooks
// are fields rather than package-level functions so a test can drive the serve
// command without binding a real port.
type runner struct {
	app       app.App
	out       io.Writer
	err       io.Writer
	listen    func(network, address string) (net.Listener, error)
	serveHTTP func(net.Listener, http.Handler) error
}

// run dispatches one command line and turns its outcome into an exit code.
func (r *runner) run(args []string) int {
	err := r.dispatch(args)
	if err == nil {
		return ExitOK
	}
	var help *helpRequest
	if errors.As(err, &help) {
		fmt.Fprint(r.out, usageFor(help.command))
		return ExitOK
	}
	fmt.Fprintln(r.err, err)
	if !errors.Is(err, core.ErrUsage) {
		return ExitError
	}
	var bad *usageError
	if errors.As(err, &bad) {
		fmt.Fprint(r.err, usageFor(bad.command))
	}
	return ExitUsage
}

// dispatch routes to the named command.
func (r *runner) dispatch(args []string) error {
	if len(args) == 0 {
		return &usageError{command: "", reason: "no command given"}
	}
	name, rest := args[0], args[1:]
	switch name {
	case "index":
		return r.index(rest)
	case "search":
		return r.search(rest)
	case "stats":
		return r.stats(rest)
	case "validate":
		return r.validate(rest)
	case "config":
		return r.config(rest)
	case "workspace":
		return r.workspace(rest)
	case "serve":
		return r.serve(rest)
	case "watch":
		return r.watch(rest)
	case "version", "-version", "--version":
		return r.version(rest)
	case "help", "-h", "-help", "--help":
		return r.help(rest)
	default:
		return &usageError{command: name, reason: "unknown command"}
	}
}

// usageError reports an invocation the command line rejected. It matches
// core.ErrUsage and always names the command, so the message on standard error
// is actionable on its own.
type usageError struct {
	// command is the command that was invoked, or "" when none was.
	command string
	// reason is a short lower-case explanation.
	reason string
}

// Error implements error.
func (e *usageError) Error() string {
	if e.command == "" {
		return "sift: " + e.reason
	}
	return "sift " + e.command + ": " + e.reason
}

// Is lets errors.Is(err, core.ErrUsage) match every usageError.
func (e *usageError) Is(target error) bool { return target == core.ErrUsage }

// helpRequest reports that the user asked for the usage of a command with -h
// rather than making a mistake. It is a successful outcome.
type helpRequest struct {
	command string
}

// Error implements error.
func (e *helpRequest) Error() string { return "sift " + e.command + ": help requested" }

// commandError wraps a failure from a command that ran, so the message on
// standard error names the command that produced it.
func commandError(command string, err error) error {
	return fmt.Errorf("sift %s: %w", command, err)
}

// newFlags returns the flag set of one command. Errors are returned rather
// than printed, so every diagnostic goes through the same path.
func newFlags(command string) *flag.FlagSet {
	fs := flag.NewFlagSet("sift "+command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// parse reads the flags of one command, turning a rejected flag into a usage
// error and an explicit -h into a help request.
func parse(command string, fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return &helpRequest{command: command}
		}
		return &usageError{command: command, reason: err.Error()}
	}
	return nil
}

// noArgs rejects positional arguments a command does not take.
func noArgs(command string, fs *flag.FlagSet) error {
	if fs.NArg() > 0 {
		return &usageError{command: command, reason: "unexpected argument: " + fs.Arg(0)}
	}
	return nil
}

// stringList collects a flag that may be repeated.
type stringList []string

// String implements flag.Value.
func (l *stringList) String() string { return strings.Join(*l, ",") }

// Set implements flag.Value by appending, so every occurrence is kept.
func (l *stringList) Set(v string) error {
	*l = append(*l, v)
	return nil
}

// writerOr replaces a nil writer with one that discards.
func writerOr(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}
