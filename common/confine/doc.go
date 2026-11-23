// Package confine is how a worker starts an action and confines it: the
// program every action's command begins with, frontseat-confine, and what
// the daemon tells it. It is the one implementation of that, for the
// daemon that writes an action's command (exec/tools) and for the
// program on the worker that reads it (cmd/frontseat-confine).
//
// What it is told is data, never a script: a Spec, the way from the
// directory the action runs in to its root, and the command. The program
// is the worker's own, at Confiner, beside the runner: nothing an action is
// handed starts it. Each form of it is installed under its own name, so
// that the daemons of several checkouts share one grid, each finding its
// own. It finds the root, makes the action's home and temporary directory
// in it, and its TMPDIR beside it on the runner's filesystem, whose files
// keep their modes as the worker's mount does not, sets the action's
// environment, confines it and runs the command as its child.
//
// An action is confined by the sandbox it runs in: one of the grid's
// runner sandboxes, an OpenShell sandbox that runs one action at a time,
// holds the action's build directory and no other's, and reaches the
// network only where it is one of those an action that fetches is sent
// to (Spec.Network, stage's network property). Within it, the program
// confines the action by Landlock before running it (confineTo), since
// one sandbox runs one action after another: an action writes its own
// root, the output files beside it and the devices that keep nothing,
// and nothing else of the slot. OpenShell refuses a sandbox made in one
// of its own; Landlock makes none. The program stays the command's
// parent, a subreaper, and kills whatever the command left running once
// it ends (supervise), so that nothing of one action runs into the next.
//
// Nothing of what an action is to be given is in the environment of what
// starts it: the action's environment is in the spec, set by the program
// just before the command runs. What it is started with is what the
// request gave, which any client of the grid may write, and which it
// passes on.
//
// # Form
//
// Form is the digest of confine.go and routes.go, which hold all the
// program does (cmd/frontseat-confine only calls
// Run, and confine imports only the standard library, which the
// architecture test holds), and of the Go release that built it. This
// file is not in it: it holds what is said of the program, not what it
// does. The box builds the program from the repository's root, where
// go.work's go and godebug lines set what the runtime does by default;
// they are not in the form, and change with the Go release they follow.
// A spec names the form it was written for, so it is in every action's
// key: a confiner whose source does anything else gives another key,
// never a result another confiner left in the grid's cache. How it was
// built is not in it: it is built one way, static and for the workers
// (the box), and the daemon reckons the same form on its own platform,
// so the platform the confiner was built for cannot be in it either.
// A confiner of each form is at a path of its own, so a daemon whose
// form a worker lacks finds none there (the grid says no such file); one
// put under another's name refuses the spec by name. The daemon and the
// program are built from this one package, by the repository's one pin
// of Go, and so agree. What it runs is the worker's, outside it: /bin/sh
// is of the worker's image, which the action's platform names.
package confine
