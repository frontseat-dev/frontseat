// Package place is how a fetch places what the asset service brought
// into its root: the program a fetch's command runs, frontseat-place, in
// place of a shell. It unpacks a gzipped tar into a directory, or cuts
// parts out of a download and keeps each that is what its checksum says.
// It is the one implementation of that, for the daemon that writes a
// fetch's command (exec/download) and for the program on the worker that
// does it (cmd/frontseat-place).
//
// What it is told is data, never a script: a verb and its arguments,
// paths relative to where it runs. It is started as every action's
// command is, by the confiner (server/internal/confine), which confines
// it to the action's root by Landlock in the runner sandbox the fetch
// runs in, with no network: the paths it is told are the daemon's, and
// may climb from a project's directory to the downloads beside its home;
// what is in an archive is from outside, and is held to the directory it
// is unpacked into by the program itself (unpack).
//
// # Form
//
// Form is the digest of place.go, which holds all the program does
// (server/cmd/frontseat-place only calls Run, and place imports only the
// standard library, which the architecture test holds), and of the Go
// release that built it, as the confiner's is of its sources: a fetch's
// command names the program by its form (Placer), so a placer that does
// anything else gives every fetch another key, and only fetches. This
// file is not in it. Each form is installed under its own name beside
// the confiner (Install), where a worker finds it, by the stack's
// installer and by the gate.
package place
