package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/frontseat-dev/frontseat/stack"
	"github.com/frontseat-dev/frontseat/stack/box"
	"github.com/frontseat-dev/frontseat/stack/local"
)

// words are what this program does but serve, each by its word: what
// brings the local stack up and down from a checkout, and what builds
// the box. Each runs at the checkout's root, as mise runs it.
var words = map[string]func(arguments []string) int{
	"ready": func(arguments []string) int {
		options := flag.NewFlagSet("frontseat-local ready", flag.ContinueOnError)
		return checkout(options, arguments, func(ctx context.Context, root string) error {
			return stack.Stack{Root: root}.Ready(ctx)
		})
	},
	"up": func(arguments []string) int {
		options := flag.NewFlagSet("frontseat-local up", flag.ContinueOnError)
		plugins := options.String("plugins", "dist/plugins", "the directory holding the plugins the box holds, each a <name>.wasm (mise run build makes them)")
		return checkout(options, arguments, func(ctx context.Context, root string) error {
			return stack.Stack{Root: root, Plugins: *plugins, Log: os.Stderr}.Up(ctx)
		})
	},
	// Down asks nothing of a checkout: it stops the stack by its name,
	// from anywhere.
	"down": func(arguments []string) int {
		options := flag.NewFlagSet("frontseat-local down", flag.ContinueOnError)
		if err := options.Parse(arguments); err != nil || options.NArg() > 0 {
			fmt.Fprintln(os.Stderr, "usage: frontseat-local down")
			return 2
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := (stack.Stack{Log: os.Stderr}).Down(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "frontseat-local down:", err)
			return 1
		}
		return 0
	},
	"image": func(arguments []string) int {
		options := flag.NewFlagSet("frontseat-local image", flag.ContinueOnError)
		on := options.String("box", "", "the box to build on, by its tag of its content; without it, the box is built first")
		tag := options.String("tag", "", "a name to tag the image with beside its tag of its content, for a person to ask for it by (frontseat-local:dev)")
		plugins := options.String("plugins", "dist/plugins", "the directory holding the plugins the box holds, each a <name>.wasm, where the box is built")
		return checkout(options, arguments, func(ctx context.Context, root string) error {
			if *on == "" {
				modules, err := box.PluginsIn(*plugins)
				if err != nil {
					return err
				}
				built, err := box.Build{Root: root, Plugins: modules, Log: os.Stderr}.Run(ctx)
				if err != nil {
					return err
				}
				*on = built
			}
			built, err := box.Stack{Root: root, Box: *on, Tag: *tag, Log: os.Stderr}.Run(ctx)
			if err != nil {
				return err
			}
			fmt.Println(built)
			return nil
		})
	},
	// Init is what the stack's container starts with: it readies the
	// container while it is its only process and becomes the program it
	// is told (s6-overlay's /init), returning only where it could not.
	"init": func(arguments []string) int {
		if len(arguments) == 0 {
			fmt.Fprintln(os.Stderr, "usage: frontseat-local init PROGRAM [ARGUMENTS...]")
			return 2
		}
		fmt.Fprintln(os.Stderr, "frontseat-local init:", stack.ContainerInit().Run(arguments))
		return 1
	},
	"box": func(arguments []string) int {
		options := flag.NewFlagSet("frontseat-local box", flag.ContinueOnError)
		tag := options.String("tag", "", "a name to tag the image with beside its tag of its content, for a person to ask for it by (frontseat-box:local)")
		plugins := options.String("plugins", "dist/plugins", "the directory holding the plugins, each a <name>.wasm")
		return checkout(options, arguments, func(ctx context.Context, root string) error {
			modules, err := box.PluginsIn(*plugins)
			if err != nil {
				return err
			}
			built, err := box.Build{Root: root, Tag: *tag, Plugins: modules, Log: os.Stderr}.Run(ctx)
			if err != nil {
				return err
			}
			fmt.Println(built)
			return nil
		})
	},
}

// checkout reads a word's flags and does it at the checkout the working
// directory is the root of, until it is done or interrupted, and returns
// its exit status.
func checkout(options *flag.FlagSet, arguments []string, do func(ctx context.Context, root string) error) int {
	if err := options.Parse(arguments); err != nil {
		return 2
	}
	if options.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "%s takes no arguments, and was given %q\n", options.Name(), options.Args())
		return 2
	}
	root, err := local.Checkout()
	if err == nil {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		err = do(ctx, root)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, options.Name()+":", err)
		return 1
	}
	return 0
}
