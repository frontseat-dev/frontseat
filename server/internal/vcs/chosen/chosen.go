// Package chosen is the version control system Frontseat's server
// programs use: git. It is the one package that names it; whatever
// brings a repository, reads a directory's history or says how a sandbox
// fetches asks through the port (server/internal/vcs) and is given this
// one by the program that composes it.
package chosen

import "github.com/frontseat-dev/frontseat/server/internal/vcs/git"

// System is the version control system: what brings a repository, tells
// its history and says how one is fetched from inside a sandbox.
func System() git.System { return git.System{} }
