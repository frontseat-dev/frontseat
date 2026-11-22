package vcs

// Fetching is what a system says of a repository fetched over HTTP by
// what cannot be trusted with its credential: what a fetch asks of the
// forge, which programs ask it, and how the system's program is told to
// present, as its password, what a variable of its environment holds.
// Whatever stands between a sandbox and a forge asks this, and so lets a
// repository be fetched without knowing which system fetches it.
type Fetching interface {
	// Cloned returns the address a sandbox clones a remote from, over
	// http or https, as the system reads the remote: one the system
	// converts, as one written for another transport, is returned as it
	// is converted. A remote no sandbox clones is an error, which never
	// repeats the remote: it may hold a password or a token.
	Cloned(remote string) (string, error)
	// NameOf returns the name a repository is brought under when it is
	// given none, as System.NameOf does.
	NameOf(remote string) string
	// Requests are the requests a fetch of a repository makes and no
	// others: the repository is its path at the forge, as it is spelled,
	// with no slash at its end. What a push asks is not among them.
	Requests(repository string) []Request
	// Pushes are the requests a push to a repository makes beside those
	// of a fetch, and no others: asked for only of a repository a
	// sandbox is let push to.
	Pushes(repository string) []Request
	// Programs are the system's programs that make those requests, each
	// by the path it is really at where the system is installed as its
	// packages install it; what one of them starts asks for it.
	Programs() []string
	// Tells is what every variable the system's program is told by
	// Presenting begins with, capitals and an underscore at its end: the
	// part of a sandbox's environment that is the system's own to set,
	// and all of it that is.
	Tells() string
	// Presenting returns what the system's program is told, through its
	// environment, so that it presents a username to an origin
	// (scheme://host:port) with, as the password, what the named variable
	// holds when it asks. It is told beside what it was told already,
	// which is given, and what is returned is only what is added or
	// changed.
	Presenting(told map[string]string, origin, username, variable string) (map[string]string, error)
	// Authoring returns what the system's program is told, through its
	// environment, so that what is committed in a sandbox is by a name
	// and an address; a part that is empty, or would be read as more than
	// itself, is told as nothing.
	Authoring(name, email string) map[string]string
}

// Request is one request to a forge.
type Request struct {
	// Method is the HTTP method.
	Method string
	// Path is the path asked for, in full.
	Path string
	// Query holds, for a parameter that must be there, the one value it
	// has; a request with none is asked with no parameter held to.
	Query map[string]string
}
