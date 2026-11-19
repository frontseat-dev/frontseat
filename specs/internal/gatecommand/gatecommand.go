// Package gatecommand reads what a command line asks of the gate program
// (specs/cmd/gate): its verb, the gate's own options and
// what it passes to go. It is the one reading of it, for the gate and for
// the architecture test that holds the tasks which run it.
package gatecommand

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
)

// Usage is how the gate is asked.
const Usage = "usage: gate fmt | vet | generated | test [-no-stack] [-except DIR... | -only DIR...] [-- go test flags] | budget | bench | go-test [go test arguments]"

// Command is what a command line asks of the gate.
type Command struct {
	// Verb is what the gate is asked to do.
	Verb string
	// NoStack tells the tests the gate runs with no local stack.
	NoStack bool
	// Except are directories whose packages a test command leaves to
	// another; Only those whose packages alone it runs. Each is clean, with
	// slashes.
	Except, Only []string
	// Go is what the gate passes to go test after its verb: for test, what
	// follows its own options; for go-test, everything.
	Go []string
}

// Parse reads the arguments the gate is given, after the program's name,
// as the gate reads them: its own options by Go's flag package, which
// takes one dash or two, and stops at "--" or at the first argument that
// is not an option.
func Parse(arguments []string) (Command, error) {
	if len(arguments) == 0 {
		return Command{}, errors.New(Usage)
	}
	command := Command{Verb: arguments[0]}
	rest := arguments[1:]
	switch command.Verb {
	case "fmt", "vet", "generated", "budget", "bench":
		if len(rest) > 0 {
			return Command{}, fmt.Errorf("gate %s takes nothing more: %s", command.Verb, Usage)
		}
	case "go-test":
		command.Go = rest
	case "test":
		options := flag.NewFlagSet("gate test", flag.ContinueOnError)
		options.SetOutput(io.Discard)
		options.BoolVar(&command.NoStack, "no-stack", false, "tell the tests the gate runs with no local stack")
		options.Func("except", "a directory beneath a module whose packages are left to another command", func(dir string) error {
			command.Except = append(command.Except, filepath.ToSlash(filepath.Clean(dir)))
			return nil
		})
		options.Func("only", "a directory beneath a module whose packages alone are run", func(dir string) error {
			command.Only = append(command.Only, filepath.ToSlash(filepath.Clean(dir)))
			return nil
		})
		if err := options.Parse(rest); err != nil {
			return Command{}, fmt.Errorf("gate test: %w: %s", err, Usage)
		}
		if len(command.Except) > 0 && len(command.Only) > 0 {
			return Command{}, errors.New("gate test: -except and -only are two commands' work: give one")
		}
		command.Go = options.Args()
	default:
		return Command{}, fmt.Errorf("gate does not %s: %s", command.Verb, Usage)
	}
	return command, nil
}
