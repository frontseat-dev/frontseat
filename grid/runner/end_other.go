//go:build !linux

package runner

import "errors"

// subreaper is Linux's alone: the runner runs in a Linux sandbox.
func subreaper() error {
	return errors.New("the runner runs on Linux alone")
}

// end is Linux's alone, as subreaper is.
func end() {}
