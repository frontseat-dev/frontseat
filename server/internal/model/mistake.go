package model

// Mistake is something wrong in what somebody wrote: what a repository
// or an entity says of how it is built, what an action needs, what a
// build was pointed at. Whoever wrote it is to mend it, and the daemon
// is not at fault: a mistake fails a build, as a mistake in the source
// does, and is said in words its author can act on.
type Mistake struct {
	// File is the file the mistake is in, relative to the workspace;
	// empty where it is in no one file.
	File string
	// Message says what is wrong.
	Message string
	// Cause retains structured plugin failure data for API and run adapters.
	Cause error
}

func (m *Mistake) Unwrap() error { return m.Cause }

func (m *Mistake) Error() string {
	if m.File == "" {
		return m.Message
	}
	return m.File + ": " + m.Message
}
