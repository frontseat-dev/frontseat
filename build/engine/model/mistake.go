package model

import (
	"path"
	"strconv"
)

// Mistake is something wrong in what somebody wrote: what a repository
// or an entity says of how it is built, what an action needs, what a
// build was pointed at. Whoever wrote it is to mend it, and the daemon
// is not at fault: a mistake fails a build, as a mistake in the source
// does, and is said in words its author can act on.
//
// It is the engine's one diagnostic: where it is, the rule that found
// it, what is wrong and what to do, each in a field of its own and none
// folded into the message. It is said in words once, by Error, a place
// written as Bazel's Location writes one (file:line); and sent once, as
// a Diagnostic among a failure's details (wire.Failure).
type Mistake struct {
	// Repository is the workspace's name for the repository the
	// mistake is in; empty for a lone repository, or where it is in
	// none.
	Repository string
	// File is the file the mistake is in, relative to Repository;
	// empty where it is in no one file.
	File string
	// Line is the line of File it is at, counted from one; zero where
	// no line is meant.
	Line int
	// Rule names the rule that found it, "<area>/<rule>"; empty where
	// no rule of its own names it.
	Rule string
	// Reason is what the mistake is refused by, as the API names it
	// (the build service's ErrorInfo reason), where it is a refusal of
	// its own: "DUPLICATE_ENTITY"; empty where whoever refuses it says.
	Reason string
	// Message says what is wrong, and only that.
	Message string
	// Help says what to do about it, as a clause that follows the
	// message: "did you mean test?"; empty where the message says enough.
	Help string
	// Cause is a failure the mistake wraps whose own details are what
	// is sent (a plugin's), or nil.
	Cause error
}

func (m *Mistake) Unwrap() error { return m.Cause }

// Where is where the mistake is, as it is written: its file from the
// workspace's root and its line, "lab/catalog-info.yaml:3"; empty where
// it is in no file.
func (m *Mistake) Where() string {
	where := path.Join(m.Repository, m.File)
	switch {
	case m.Line <= 0:
		return where
	case where == "":
		return "line " + strconv.Itoa(m.Line)
	}
	return where + ":" + strconv.Itoa(m.Line)
}

// Error is the mistake in words: where it is, what is wrong, and what
// to do.
func (m *Mistake) Error() string {
	said := m.Message
	if m.Help != "" {
		said += "; " + m.Help
	}
	if where := m.Where(); where != "" {
		return where + ": " + said
	}
	return said
}
