//go:build !linux

package runner

import "errors"

// mountOf is Linux's alone: the pool runs beside the worker, in the
// stack's Linux container.
func mountOf(string) (string, bool, error) {
	return "", false, errors.New("the runner pool reads the worker's mounts on Linux alone")
}

// handOver is Linux's alone, as mountOf is.
func handOver(string, string) error {
	return errors.New("the runner pool hands its runners over on Linux alone")
}
