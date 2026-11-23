package testfiles

import (
	"fmt"
	"os"
)

// Suite runs a suite of tests in a directory of its own: it makes the
// directory, named from prefix, has run run the suite in it and say how
// it ended, removes the directory, and exits with what run said, or as
// failed where the directory could not be made or removed.
func Suite(prefix string, run func(directory string) int) {
	root, err := os.MkdirTemp("", prefix)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	exit := run(root)
	if err := os.RemoveAll(root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		exit = 1
	}
	os.Exit(exit)
}
