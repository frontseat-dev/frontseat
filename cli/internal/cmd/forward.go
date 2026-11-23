package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/frontseat-dev/frontseat/core/plain"
	"github.com/frontseat-dev/frontseat/sdk/go"
)

var forwardListen string

// forwardCmd carries a port of this machine to a port of a workspace's
// sandbox, through the control plane that keeps the workspace.
var forwardCmd = &cobra.Command{
	Use:   "forward [NAME] PORT",
	Short: "Reach a port of a workspace's sandbox from this machine",
	Long: `Listen on this machine's loopback, and carry each connection made there
to a port of the sandbox of the workspace named, or of the one last
opened, until interrupted: what a program of the workspace serves
there, a development server or a debugger, is reached at this machine.

It listens on the same port of 127.0.0.1 unless --listen says another
address, which is one of this machine's loopback: a port of a sandbox
is led to no network by this command. Once it listens, it says on one
line what it listens on and where that leads.

The connections go through the control plane, to the workspace's owner
alone. A connection ends, both ways, when either end does. One that
cannot be carried is ended, and said on standard error; the command
goes on.`,
	Example: `  frontseat workspace forward 8080
  frontseat workspace forward services-main 8080
  frontseat workspace forward services-main 8080 --listen 127.0.0.1:9090
  frontseat workspace forward 5432 --listen 127.0.0.1:0`,
	Args: func(_ *cobra.Command, args []string) error {
		if len(args) < 1 || len(args) > 2 {
			return errors.New("a port is named, after the workspace where one is: frontseat workspace forward [NAME] PORT")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		named, written := args[:len(args)-1], args[len(args)-1]
		port, ok := plain.Port(written)
		if !ok {
			return fmt.Errorf("%q is no port: one is a number from 1 to 65535, in digits alone, after the workspace's name where one is given", written)
		}
		at := forwardListen
		if at == "" {
			at = net.JoinHostPort("127.0.0.1", written)
		}
		if !onLoopback(at) {
			return fmt.Errorf("--listen %q is no address of this machine's loopback: one is 127.0.0.1 or [::1] and a port, as 127.0.0.1:8080, or 127.0.0.1:0 for any port that is free", at)
		}
		w, err := subject(named)
		if err != nil {
			return err
		}
		return forward(cmd.Context(), w, port, at)
	},
}

// onLoopback is whether an address to listen on is of this machine's
// loopback, said by its number: an address of 127.0.0.0/8 or ::1, and a
// port, or 0 for any. A name is none, whatever it resolves to.
func onLoopback(at string) bool {
	host, port, err := net.SplitHostPort(at)
	if err != nil {
		return false
	}
	if _, ok := plain.Port(port); !ok && port != "0" {
		return false
	}
	address, err := netip.ParseAddr(host)
	return err == nil && address.IsLoopback()
}

// forward listens at an address of this machine and carries each
// connection made there to a port of a workspace's sandbox, until the
// context ends: then it returns why the context ended, with everything
// it started ended, nothing listening, and no connection left.
func forward(ctx context.Context, w *frontseat.Workspace, port int, at string) error {
	var listening net.ListenConfig
	listener, err := listening.Listen(ctx, "tcp", at)
	if err != nil {
		return err
	}
	said("%s leads to the port %d of the sandbox of the workspace %s; Ctrl-C ends it", listener.Addr(), port, w.Name())
	// The context of what is carried, ended when accepting ends, however
	// it does.
	carrying, end := context.WithCancel(ctx)
	// What wakes the accepting below once the context has ended: the
	// listener is closed only after both have stopped.
	woken := make(chan struct{})
	go func() {
		defer close(woken)
		<-carrying.Done()
		if deadlined, ok := listener.(interface{ SetDeadline(time.Time) error }); ok {
			_ = deadlined.SetDeadline(time.Now())
		}
	}()
	// What went wrong with one connection is said whole, one at a time.
	var saying sync.Mutex
	var carried sync.WaitGroup
	for {
		local, failed := listener.Accept()
		if failed != nil {
			if err = ctx.Err(); err == nil {
				err = failed
			}
			break
		}
		carried.Go(func() {
			if err := carry(carrying, w, port, local); err != nil && carrying.Err() == nil {
				saying.Lock()
				defer saying.Unlock()
				complained("a connection was not carried to the port %d: %v", port, err)
			}
		})
	}
	end()
	<-woken
	carried.Wait()
	_ = listener.Close()
	return err
}

// carry carries one connection made on this machine to a port of a
// workspace's sandbox, until either end ends it or the context does,
// and returns why it could not be carried, where it could not. It
// closes the connection, and what it opened, once nothing reads either.
func carry(ctx context.Context, w *frontseat.Workspace, port int, local net.Conn) error {
	defer local.Close()
	ctx, end := context.WithCancel(ctx)
	defer end()
	inside, err := w.Forward(ctx, port)
	if err != nil {
		return err
	}
	defer inside.Close()
	var copying sync.WaitGroup
	// What the sandbox's end says, until it ends or the context does:
	// then this machine's end is woken, and is read no longer.
	copying.Go(func() {
		_, _ = io.Copy(local, inside)
		_ = local.SetDeadline(time.Now())
	})
	// What this machine's end says, until it ends: then the tunnel does,
	// which carries no closing of one direction alone.
	_, _ = io.Copy(inside, local)
	end()
	copying.Wait()
	return nil
}

func init() {
	workspaceCmd.AddCommand(forwardCmd)
	forwardCmd.Flags().StringVar(&forwardListen, "listen", "", "the address of this machine's loopback to listen on, as 127.0.0.1:9090, or 127.0.0.1:0 for any free port; without it, 127.0.0.1 and the same port")
}
