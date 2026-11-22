// Command frontseat-runner keeps the grid's runner sandboxes and runs
// what each runs. Told `pool`, it keeps one OpenShell sandbox for each slot
// of the worker (package runner, Pool); told `serve`, it is the runner in
// one, which runs each action the worker sends it, confined, and nothing
// else (package runner, Confined); the local stack runs it so. Otherwise
// it is the runner of the workers that run tests with services: a runner
// sandbox for each action, and a sandbox for each service it names, each
// one the OpenShell gateway makes. It is told by its flags alone.
//
//	frontseat-runner pool --gateway https://127.0.0.1:8090 --identity DIR \
//	  --image IMAGE --bb /var/lib/frontseat/bb --slots /var/lib/frontseat/worker \
//	  --count 8 --egress 2 --hosts /etc/frontseat/egress-hosts
//	frontseat-runner serve --listen /slot/listen --build /slot/build
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/frontseat-dev/frontseat/server/internal/runner"
)

// registries collects --registry, given once for each registry.
type registries map[string]string

func (r registries) String() string { return fmt.Sprint(map[string]string(r)) }

func (r registries) Set(v string) error {
	from, to, ok := strings.Cut(v, "=")
	if !ok || from == "" || to == "" {
		return fmt.Errorf("%q is not registry=proxy", v)
	}
	if _, twice := r[from]; twice {
		return fmt.Errorf("%s is given twice", from)
	}
	r[from] = to
	return nil
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "pool" {
		os.Exit(pool(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		os.Exit(serve(os.Args[2:]))
	}
	var config runner.Config
	given := registries{}
	flag.StringVar(&config.Socket, "socket", "", "where it listens, in a directory of its own in the worker's, which only the worker may enter")
	flag.StringVar(&config.Worker, "worker", "", "the worker's directory, as the runner sees it")
	flag.StringVar(&config.Host, "worker-host", "", "the worker's directory, as the container runtime sees it")
	flag.StringVar(&config.Image, "image", "", "the image actions run in")
	flag.StringVar(&config.Gateway, "gateway", "", "the OpenShell gateway that makes every sandbox, as a URL")
	flag.StringVar(&config.Identity, "identity", "", "the directory holding what the gateway is known by and knows this program by: ca.crt, tls.crt and tls.key")
	flag.StringVar(&config.Runtime, "runtime", "", "the command line of the gateway's container runtime (podman), asked what an image is started with")
	flag.StringVar(&config.BB, "bb", "", "the directory the runner and the confiners are in, as the container runtime sees it")
	flag.Var(given, "registry", "a registry an image may come from and the proxy's address for it, as registry=proxy; give it once for each; without any, images are pulled from their registries")
	flag.StringVar(&config.State, "state", "", "the directory it keeps what is its own in: the sandbox service's records and the socket it is asked on")
	flag.Parse()
	config.Registries = given

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runner.Serve(ctx, config); err != nil {
		log.Fatal(err)
	}
}

// serve is the runner of a runner sandbox (frontseat-runner serve): it
// answers the worker on the socket at --listen, running each action in
// the build directory --build names, until it is stopped, and returns its
// exit status.
func serve(arguments []string) int {
	options := flag.NewFlagSet("frontseat-runner serve", flag.ContinueOnError)
	listen := options.String("listen", "", "the socket the worker asks it on")
	build := options.String("build", "", "the slot's build directory, which the worker's paths are relative to")
	if err := options.Parse(arguments); err != nil {
		return 2
	}
	if *listen == "" || *build == "" || options.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "frontseat-runner serve: --listen and --build are given, and nothing else")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runner.ServeConfined(ctx, *listen, &runner.Confined{Build: *build}); err != nil {
		fmt.Fprintln(os.Stderr, "frontseat-runner serve:", err)
		return 1
	}
	return 0
}

// pool keeps the grid's runner sandboxes (frontseat-runner pool): one
// for each slot of the worker, made by an OpenShell gateway, until it is
// stopped, and returns its exit status.
func pool(arguments []string) int {
	options := flag.NewFlagSet("frontseat-runner pool", flag.ContinueOnError)
	gateway := options.String("gateway", "", "the OpenShell gateway that makes the sandboxes, as https://HOST:PORT")
	identity := options.String("identity", "", "the directory holding what the gateway is known by and knows this program by: ca.crt, tls.crt and tls.key")
	image := options.String("image", "", "the runner image each sandbox is made of, by its digest")
	bb := options.String("bb", "", "the directory holding the runner and the confiners, as the gateway's host has it")
	slots := options.String("slots", "", "the worker's directory holding each slot's, by its number from one, as the gateway's host has it")
	count := options.Int("count", 0, "how many slots the worker has, numbered from one")
	egress := options.Int("egress", 0, "how many of the last slots reach the hosts --hosts names")
	listed := options.String("hosts", "", "a file of the hosts an egress slot reaches, one NAME:PORT a line")
	if err := options.Parse(arguments); err != nil || options.NArg() > 0 {
		return 2
	}
	for name, given := range map[string]string{"--gateway": *gateway, "--identity": *identity, "--image": *image, "--bb": *bb, "--slots": *slots, "--hosts": *listed} {
		if given == "" {
			fmt.Fprintf(os.Stderr, "frontseat-runner pool: %s is not given\n", name)
			return 2
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	read, err := os.ReadFile(*listed)
	if err != nil {
		fmt.Fprintln(os.Stderr, "frontseat-runner pool:", err)
		return 1
	}
	hosts, err := runner.EgressHosts(string(read))
	if err != nil {
		fmt.Fprintln(os.Stderr, "frontseat-runner pool:", err)
		return 1
	}
	p := runner.Pool{Image: *image, BB: *bb, Slots: *slots, Count: *count, Egress: *egress, Hosts: hosts}
	if err := runner.KeepPool(ctx, *gateway, *identity, p); err != nil {
		fmt.Fprintln(os.Stderr, "frontseat-runner pool:", err)
		return 1
	}
	return 0
}
