// Package compare measures frontseat beside the build tools it is held
// against, on the same work. It writes one workspace for each tool: the
// same source, and the tool's own files saying how the source is built,
// each saying the same thing. Then it builds each workspace with its
// tool, the same way, and says how long each took.
//
// Nothing it writes is kept in the repository. A tool that is not on
// the machine is left out, and said to be.
//
// What is built is the least that has the shape of a build: every
// module has three actions that concatenate its files, and the modules
// come in groups whose first is needed by the rest, so that a change to
// it reaches them. An action costs next to nothing to run, so what is
// measured is what the tool itself costs.
package compare
