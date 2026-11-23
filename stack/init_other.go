//go:build !linux

package stack

import "errors"

// ContainerInit is Init as the stack's container runs it, which is Linux
// alone: elsewhere it refuses.
func ContainerInit() Init { return Init{sys: elsewhere{}} }

// elsewhere is no Linux: Init has nothing to ask of it.
type elsewhere struct{}

var errNotLinux = errors.New("the stack's container is Linux: init runs there alone")

func (elsewhere) pid() int                     { return 0 }
func (elsewhere) chown(string, Owner) error    { return errNotLinux }
func (elsewhere) remountWritable(string) error { return errNotLinux }
func (elsewhere) shareSubtree(string) error    { return errNotLinux }
func (elsewhere) exec([]string) error          { return errNotLinux }
