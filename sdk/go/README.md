# Frontseat Go SDK

The Go client of Frontseat: one `Client`, made for a server, from which
workspaces, builds, tests, runs, releases, the graph and credentials are
reached. The `frontseat` command line asks its servers with it.
Frontseat is in preview, and so is this package: its API is
`v1alpha1`.

```
go get github.com/frontseat-dev/frontseat/sdk/go
```

The package is `frontseat`.

## A build, from nothing

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/frontseat-dev/frontseat/sdk/go"
)

func main() {
	ctx := context.Background()

	// The token the control plane knows you by. The local stack lays
	// its owner's out in ~/.config/frontseat/local/token.
	token, err := os.ReadFile(os.ExpandEnv("$HOME/.config/frontseat/local/token"))
	if err != nil {
		log.Fatal(err)
	}
	fs, err := frontseat.New("http://127.0.0.1:7700", frontseat.WithToken(strings.TrimSpace(string(token))))
	if err != nil {
		log.Fatal(err)
	}

	// The workspace of a repository and a branch: a new one, or the one
	// there is, as it was left.
	opened, err := fs.Open(ctx, frontseat.WorkspaceSpec{
		Repositories: []frontseat.Repository{{Remote: "https://forge.example/acme/services.git", Ref: "main"}},
	})
	if err != nil {
		log.Fatal(err)
	}
	ws := opened.Workspace

	// A build goes on whether or not it is followed. Its events come to
	// the one that says how it ended.
	build, err := ws.Build(ctx, frontseat.BuildRequest{Targets: []string{"greeter"}})
	if err != nil {
		log.Fatal(err)
	}
	for event, err := range build.Events(ctx, 0) {
		if err != nil {
			log.Fatal(err)
		}
		if result := event.GetActionFinished().GetResult(); result != nil {
			fmt.Println(result.GetOutcome(), result.GetLabel())
		}
	}
	ended, err := build.Wait(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(ended.GetExitCode().GetName())
}
```

## What is reached from a Client

| From | What |
|---|---|
| `frontseat.New(server, options...)` | A `Client` of a control plane, or why the address names none. Nothing is asked until a call is made. |
| `Client.Open`, `Client.Workspace`, `Client.Workspaces` | A workspace made or opened, one by its name, and all of them. |
| `Client.Credentials` | Your credentials for the hosts your workspaces fetch from and push to. The control plane keeps them outside every sandbox. |
| `Workspace.Build`, `Workspace.Test` | A `Build`: `Events`, `Wait`, `Describe`, `Cancel`, `Actions`, `Logs`. |
| `Workspace.Run` | A `Run` of a program of the workspace: `Logs`, `Wait`, `Describe`, `Cancel`. |
| `Workspace.PlanRelease`, `Workspace.Release` | A release planned, which changes nothing, and a `Release` of that plan by its etag. |
| `Workspace.Entities`, `Workspace.Projects` | What the workspace is made of. Reading it runs nothing. |
| `Workspace.Describe`, `Workspace.Add`, `Workspace.Delete`, `Workspace.Shell` | The workspace itself: what it holds, more for it to hold, its end, and a connection an SSH client speaks over. |
| `Client.Here` | Inside a workspace's sandbox, the workspace a `Client` of the daemon there is in. |

`Build`, `Run` and `Release` are handles by id (`ID()`):
`Workspace.BuildByID`, `RunByID` and `ReleaseByID` give one for an id
kept from before, and ask nothing until a call is made. `Run.BuildID`
and `Release.BuildID` give the id of the build each made.

Every list (`Workspaces`, `Builds`, `Runs`, `Entities`, `Projects`,
`Build.Actions`) and every log (`Build.Logs`, `Run.Logs`) is an
`iter.Seq2[T, error]`, read with `for item, err := range …`; a list is
asked for a page at a time as it is read. `frontseat.Collect` gathers
one into a slice. A release is asked for as a build and a run are, by a
request (`ReleaseRequest{Entities, Etag}`).

What a call hands back is the API's own message, generated from its
contract into the packages beneath `sdk/go/api` (for a build,
`sdk/go/api/frontseat/build/v1alpha1`). Their comments are the
contract's.

## The token

`WithToken` presents a token with every request to the server the
`Client` was made for, and to no other origin: not to an address a
server answers with, and no redirect is followed with it. Name a control
plane on another machine with `https`: `New` refuses a `Client` that
would present its token over http to another machine
(`bearer.ErrInClear`), and an address that is more than a scheme, a host
and a port.

## Errors

Every call a server refuses fails with a `*frontseat.Error`: its
canonical `Code`, and, where the server refused it by name, its
`Reason` and the `Domain` of the service that refused. A program reads
the code and the reason, never the message, which is for a person:

```go
var refused *frontseat.Error
if errors.As(err, &refused) && refused.Reason == frontseat.ReasonProfileUnknown {
	// ...
}
if errors.Is(err, frontseat.ErrWorkspaceNotFound) {
	// ...
}
```

Every reason a server gives is a `Reason…` constant; the refusals a
caller most often acts on are sentinels for `errors.Is`
(`ErrWorkspaceNotFound`, `ErrWorkspaceNotReady`, `ErrBuildNotFound`,
`ErrRunNotFound`, `ErrReleaseNotFound`, `ErrEtagMismatch`,
`ErrEntitiesNotServed`). Each call's reasons are listed with it in the
contract, and in the comments of the generated client beneath
`sdk/go/api`. What the SDK refuses itself is a sentinel too:
`ErrWorkspaceUnnamed`, `ErrHere`, and, of a workspace that cannot be
worked in, `ErrWorkspaceFailed` and `ErrWorkspacePaused`, each also
`ErrWorkspaceNotReady`. `frontseat.Reason(err)` is a shorthand for the
reason and the domain.

## The other packages

- `entity` reads and writes an entity's reference, `component:default/gateway`.
- `bearer` presents a token to one origin and follows no redirect, for a
  program that makes HTTP requests of its own.
- `api` is the generated Go of the contracts, with a Connect client for
  each service, for a program that wants the wire itself. `Received` and
  `Listed` read a stream and a paged list to their ends.

## Examples

`example_test.go` holds an example for each of `New`, `Client.Open`,
`Workspace.Build`, `Workspace.Release` and `Error`. Each runs against a
server of its own on this machine, and `go test` holds it to what it
prints.
