// Command frontseat-local serves the control plane on this machine, over
// an OpenShell gateway that makes each workspace's sandbox with the
// machine's container runtime: what makes workspaces where there is no
// platform. It keeps everything of its own beneath one directory
// (--state). It answers its owner alone, by a token it mints the first
// time it starts and keeps there (owner/token), which the local stack
// lays out where the command line finds it. It resolves the entities a
// workspace is asked for through the Backstage its operator names
// (--backstage, with its token in a file, --backstage-token), and
// refuses them where it is given none. The sandbox API it
// makes sandboxes with is its own, and is served to no one.
//
// Its sandboxes build on the grid of the local stack (local/image), which they
// reach through the machine they run on.
//
// It also brings that stack up or down from a checkout, and builds the
// box, each by a word first (local.go):
//
//	frontseat-local ready                        whether this checkout may bring the stack up (mise start, before it builds)
//	frontseat-local up [--plugins DIR]           the stack, from this checkout, running the box built from it (mise start)
//	frontseat-local down                         the stack, whichever checkout started it, from anywhere (mise stop)
//	frontseat-local box [--tag T] [--plugins DIR] the box image, printing its tag of its content (mise run build)
//	frontseat-local image [--box B] [--tag T]       the stack's one image (local/image), on the box, printing its tag of its content
//	frontseat-local init PROGRAM...             the stack container's first process: readies it, then becomes PROGRAM
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/frontseat-dev/frontseat/local"
	"github.com/frontseat-dev/frontseat/local/ports"
	"github.com/frontseat-dev/frontseat/server/internal/backstage"
	"github.com/frontseat-dev/frontseat/server/internal/controlplane"
	"github.com/frontseat-dev/frontseat/server/internal/forge"
	"github.com/frontseat-dev/frontseat/server/internal/httpserver"
	"github.com/frontseat-dev/frontseat/server/internal/secretfile"
	"github.com/frontseat-dev/frontseat/server/internal/source"
	"github.com/frontseat-dev/frontseat/server/telemetry"
)

// kept is where a person's programs keep what must outlive them, by the
// XDG base directory specification, and this server's own place in it.
func kept() string {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "frontseat", "local")
}

func main() {
	if len(os.Args) > 1 {
		if word, known := words[os.Args[1]]; known {
			os.Exit(word(os.Args[2:]))
		}
	}
	listen := flag.String("listen", net.JoinHostPort("127.0.0.1", strconv.Itoa(ports.ControlPlane)), "the address to serve the control plane on")
	grid := flag.String("grid", net.JoinHostPort("host.docker.internal", strconv.Itoa(ports.Grid)), "the grid its sandboxes build on, as a sandbox reaches it")
	public := flag.String("public", "", "the address its clients reach it at, as a URL, where that is not the address it listens on: a sandbox's ports are said to be beneath it")
	box := flag.String("box", "", "the image a workspace's sandbox runs where whoever asks for one names none; without it, an image is always named")
	state := flag.String("state", kept(), "the directory it keeps everything of its own in: its sandboxes' and workspaces' records, so that it finds them when it starts again, and its owner's token (owner/token), minted there the first time it starts")
	openshell := flag.String("openshell", "https://"+net.JoinHostPort("127.0.0.1", strconv.Itoa(ports.OpenShell)), "the OpenShell gateway that makes its sandboxes, as a URL: one that makes them with this machine's container runtime, as the local stack's does")
	runtime := flag.String("runtime", "docker", "the command line of the container runtime the gateway makes its sandboxes with, asked what an image is started with: docker, or podman, as the local stack's gateway's is")
	identity := flag.String("openshell-identity", "", "the directory holding what the gateway is known by and knows this server by: ca.crt, tls.crt and tls.key (default: openshell beneath --state)")
	backstageURL := flag.String("backstage", "", "the Backstage its workspaces' entities are resolved through, as https://HOST[:PORT][/PATH]; without it, a workspace asked for by entity is refused")
	backstageToken := flag.String("backstage-token", "", "a file holding the token presented to Backstage: one line, the file 0600 in a directory only this server reaches; read when it starts, so a new one is taken by starting it again")
	backstageLoopback := flag.Bool("backstage-loopback", false, "let Backstage be at this machine, and asked over http there: for a local stack and tests")
	var forges addresses
	flag.Var(&forges, "github", "the address of a forge that answers as GitHub does, as https://code.acme.example, beside GitHub itself: a workspace made to push to a repository of it may publish that repository's releases; given once for each")
	flag.Parse()
	if *identity == "" {
		*identity = filepath.Join(*state, "openshell")
	}
	token := filepath.Join(*state, "owner", "token")

	catalog, err := catalogOf(*backstageURL, *backstageToken, *backstageLoopback)
	if err != nil {
		fmt.Fprintln(os.Stderr, "frontseat-local:", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := serve(ctx, told{listen: *listen, public: *public, grid: *grid, state: *state, openshell: *openshell, identity: *identity, runtime: *runtime, box: *box, token: token, catalog: catalog, forges: forges}); err != nil {
		fmt.Fprintln(os.Stderr, "frontseat-local:", err)
		os.Exit(1)
	}
}

// addresses are addresses a flag is given, once for each.
type addresses []string

func (a *addresses) String() string { return strings.Join(*a, ",") }

func (a *addresses) Set(written string) error {
	*a = append(*a, written)
	return nil
}

// reached is the address clients are told a sandbox's ports are beneath:
// the one given, or the one listened on where that is an address a
// client can ask. Listening on every address says nothing of where a
// client reaches the server, so there it must be given.
func reached(listen, public string) (string, error) {
	if public == "" {
		host, _, err := net.SplitHostPort(listen)
		if err != nil {
			return "", fmt.Errorf("--listen %q: %w", listen, err)
		}
		if address := net.ParseIP(host); host == "" || (address != nil && address.IsUnspecified()) {
			return "", fmt.Errorf("--listen %s is every address of this machine and none a client can be told: say where clients reach this server with --public", listen)
		}
		return "http://" + listen, nil
	}
	at, err := url.Parse(public)
	if err != nil || (at.Scheme != "http" && at.Scheme != "https") || at.Hostname() == "" || at.Path != "" || at.RawQuery != "" || at.Fragment != "" || at.Opaque != "" || at.User != nil {
		return "", fmt.Errorf("--public %q: where clients reach this server is written as http://HOST:PORT", public)
	}
	return at.String(), nil
}

// copied is where a person reads how the local stack's gateway is known.
const copied = "the local stack lays out its own in /var/lib/frontseat/controlplane/openshell, in its container: the oneshot identity of local/image"

// asked is the gateway's address as it is to be asked, or why it is not
// asked at all. A gateway is asked over https alone, where it knows its
// callers by certificate and they know it: one that answers anyone would
// make a sandbox for whoever reaches it. The directory holds what both
// are known by, and its key is this server's alone to read, since
// whoever holds it may have the gateway send a credential it keeps to a
// host of their choosing.
func asked(address, identity string) (string, error) {
	if address == "" {
		return "", errors.New("an OpenShell gateway makes its sandboxes, and nothing else does: name one with --openshell")
	}
	gateway, err := url.Parse(address)
	if err != nil || gateway.Scheme != "https" || gateway.Hostname() == "" || gateway.User != nil || gateway.Path != "" || gateway.RawQuery != "" || gateway.Fragment != "" || gateway.Opaque != "" {
		return "", fmt.Errorf("--openshell %q: a gateway is asked over https, as https://HOST:PORT, where it knows its callers by certificate", address)
	}
	if identity == "" {
		return "", errors.New("a gateway knows its callers by certificate: name the directory holding ca.crt, tls.crt and tls.key with --openshell-identity (" + copied + ")")
	}
	// What says whom the server trusts is as much its own as its key:
	// another who could write there could put an authority of their own,
	// and the key is worth every credential the gateway keeps. Each is
	// held as any secret this server keeps in a file is (secretfile): a
	// directory and files of its own, and no link.
	const what = "what the gateway is known by and knows this server by (--openshell-identity)"
	if err := secretfile.Directory(identity, what); err != nil {
		return "", err
	}
	for _, held := range []string{"ca.crt", "tls.crt", "tls.key"} {
		if _, err := secretfile.Read(filepath.Join(identity, held), what, identityLimit); errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("--openshell-identity %s holds no %s, which the gateway is known by or knows this server by (%s)", identity, held, copied)
		} else if err != nil {
			return "", err
		}
	}
	return gateway.String(), nil
}

// identityLimit is more than a certificate, a chain of them or a key
// holds.
const identityLimit = 1 << 20

// catalogOf is the Backstage entities are resolved through, as its flags
// give it: none without --backstage, and nothing else of it then. Its
// token is read now, once.
func catalogOf(base, tokenFile string, loopback bool) (*backstage.Catalog, error) {
	if base == "" {
		if tokenFile != "" || loopback {
			return nil, errors.New("--backstage-token and --backstage-loopback are said of a Backstage: name it with --backstage")
		}
		return nil, nil
	}
	var token string
	if tokenFile != "" {
		read, err := backstage.ReadToken(tokenFile)
		if err != nil {
			return nil, fmt.Errorf("--backstage-token: %w", err)
		}
		token = read
	}
	catalog, err := backstage.New(backstage.Config{Base: base, Token: token, Loopback: loopback})
	if err != nil {
		return nil, fmt.Errorf("--backstage: %w", err)
	}
	return catalog, nil
}

// told is what the server is told by its flags.
type told struct {
	// listen is the address it serves on, and public where its clients
	// reach it, where that is another.
	listen, public string
	// grid is the grid its sandboxes build on, as a sandbox reaches it.
	grid string
	// state is the directory it keeps everything of its own in, and
	// token the file its owner's token is in.
	state, token string
	// openshell is the gateway that makes its sandboxes, identity the
	// directory holding what they know each other by, and runtime the
	// command line of the container runtime the gateway makes them with.
	openshell, identity, runtime string
	// box is the image a workspace's sandbox runs where none is named.
	box string
	// catalog is the Backstage entities are resolved through; nil for
	// none.
	catalog *backstage.Catalog
	// forges are the forges that answer as GitHub does, beside it.
	forges []string
}

func serve(ctx context.Context, f told) error {
	signals, stop, err := telemetry.Setup(ctx, "frontseat-local")
	defer func() {
		if err := telemetry.Ended(stop); err != nil {
			fmt.Fprintln(os.Stderr, "frontseat-local: telemetry:", err)
		}
	}()
	if err != nil {
		return err
	}
	grid, err := local.Embedded.Grid(f.grid)
	if err != nil {
		return err
	}
	public, err := reached(f.listen, f.public)
	if err != nil {
		return err
	}
	gateway, err := asked(f.openshell, f.identity)
	if err != nil {
		return err
	}
	callers, err := controlplane.OwnerToken(f.token)
	if err != nil {
		return err
	}
	// A nil catalog is no resolver at all, and not one that is nil.
	var entities controlplane.Entities
	if f.catalog != nil {
		entities = f.catalog
	}
	releasing, err := forge.GitHub(f.forges...)
	if err != nil {
		return fmt.Errorf("--github: %w", err)
	}
	plane, err := controlplane.OverGateway(controlplane.Config{
		PublicURL: public, State: f.state, Box: f.box, Callers: callers,
		Grid:     controlplane.Grid{Hosts: grid.Hosts(), Arguments: grid.Arguments()},
		Fetching: source.Fetching(), Releasing: releasing, Entities: entities,
	}, controlplane.Gateway{Address: gateway, Identity: f.identity, Runtime: f.runtime})
	if err != nil {
		return err
	}
	defer plane.Close()
	listener, err := net.Listen("tcp", f.listen)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "frontseat-local: serving workspaces on %s to the owner of the token in %s, in sandboxes made by the OpenShell gateway at %s, building on the grid at %s\n", f.listen, f.token, gateway, f.grid)
	return httpserver.Serve(ctx, listener, signals.Handler(plane, "control plane"))
}
