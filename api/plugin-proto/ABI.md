# The plugin ABI

How the messages of `frontseat.plugin.v1alpha1` cross between the host and
a plugin. With the four `.proto` files beside it, this is the whole
wire contract: a plugin in any language that compiles to wasm needs
nothing else. The Rust plugin SDK (`sdk/plugin`, the crate
`frontseat-plugin`) implements this ABI once; a plugin written against it
implements its `Plugin` trait and uses `serve!`, and maintains no dispatch
or host imports of its own.

## A plugin is a wasm module

- Target `wasm32-wasip1`, built as a reactor: the host calls `_initialize`
  once, then the exports below, any number of times.
- It is given no directory, no clock it can rely on, no network and no
  environment. Everything it reads comes through the imports below.
- The host bounds its memory to 256 MiB, and ends a call whose caller has
  gone.

## Exports

| Export | Signature | What it does |
|---|---|---|
| `frontseat_abi` | `() -> i32` | The ABI the module speaks, and the one place it is said: `Describe` neither asks nor answers it. The host loads no module outside the range it supports, before it asks anything. This document is ABI 25. |
| `frontseat_alloc` | `(size: i32) -> i32` | Returns the address of `size` bytes the host may write to. |
| `frontseat_free` | `(ptr: i32, size: i32)` | Gives back what `frontseat_alloc` returned. |
| `frontseat_call` | `(ptr: i32, size: i32) -> i64` | Takes a serialized `Call`, returns a serialized `Reply`. |

A returned `i64` is an address in its high 32 bits and a size in its low
32. The host frees the `Call` it wrote, with `frontseat_free`. What
`frontseat_call` returns it reads and does not free: each call is made
on an instance of its own, which the host discards with its memory once
it has read the reply.

## Imports

Module `frontseat`. One import for each method of `HostService`, named in
lower case:

| Import | Request | Response |
|---|---|---|
| `read` | `ReadRequest` | `ReadResponse` |
| `glob` | `GlobRequest` | `GlobResponse` |

Each has the signature `(ptr: i32, size: i32) -> i64`: a serialized request
in, a serialized response out, written by the host into memory it took
from `frontseat_alloc`. The module frees it, with the size it was given.

A result of zero is an empty response: the host wrote nothing, and every
field has its default. A read the host cannot serve is not reported to the
module; the host fails the call it was made in.
The SDK refuses malformed response bytes rather than decoding them as defaults.
Its Rust `read` and `glob` interfaces return `Result`, preserving source failures
in native hosts as well; absence remains a successful `None`.

`glob` takes a literal repository-relative directory separately from patterns
relative to that directory, in the pattern language `host.proto` says, which
is the whole of what a pattern may be. The empty directory means the repository root.
Returned paths are relative to the repository. The host records both directory
and patterns when deciding whether a previously computed answer still holds.

## A call

1. The host serializes a `Call`: the method's name as `PluginService`
   spells it, and its request.
2. It writes it into the module and calls `frontseat_call`.
3. The module answers with a `Reply`: the response or a `google.rpc.Status`
   failure. A method it does not serve returns `UNIMPLEMENTED` for the
   method's own reason: `REPORT_NOT_SERVED`, `RECONCILE_NOT_SERVED` and
   `SERVICE_NOT_SERVED`, the method's name in capitals;
   `BRANCHING_STRATEGY_NOT_SERVED` for `Branch` and
   `VERSIONING_STRATEGY_NOT_SERVED` for `Version`, the kind of strategy.
   The same reason answers a strategy or a service the plugin does not
   claim. Every plugin serves `Describe`, `Analyze` and `Arrange`; asked
   to arrange actions it declares no arrangement for, it answers
   `INVALID_ARGUMENT` for the reason `UNSUPPORTED_ARRANGEMENT`. The Rust
   SDK answers a method it does not know `UNIMPLEMENTED` for the reason
   `UNKNOWN_METHOD`, which the host reads alike.

The methods are `Describe`, `Analyze`, `Arrange`, `Report`, `Reconcile`,
`Service`, `Branch` and `Version`. `Report` is
asked of a check once it has ended, with its exit code and what it
printed, each cut to its first MiB; a plugin that does not serve it
(`UNIMPLEMENTED`, `REPORT_NOT_SERVED`) has checks with no findings but
their exit code and what they printed, and the host records none.
`UNIMPLEMENTED` for any other reason is a failure of the call, as any
other code is. A check writes nothing on the
grid. What a check's fix may change is declared on the action
(`rewrites`); the fix runs where the checkout is, over a copy, and the
host compares the copy with what the run read. A plugin writes nothing
itself.

The shared SDK encodes each failure with `google.rpc.ErrorInfo` and
`frontseat.type.v1alpha1.Diagnostic`. The reason is upper snake case in the
domain `<plugin>.plugins.frontseat.dev`; the diagnostic's rule ID is
`<plugin>/<lower-kebab-case-reason>`. The diagnostic retains its message,
source location and remediation independently of the human-readable status
message. A source-free failure still retains remediation. The host validates
the code, identity and literal relative path, and supplies the repository
identity. These details survive graph RPC errors and persisted run failures.
Additional details may use the standard `google.rpc` error-detail messages:
RetryInfo, DebugInfo, QuotaFailure, PreconditionFailure, BadRequest, RequestInfo,
ResourceInfo, Help and LocalizedMessage. Unknown types, nested statuses and
malformed payloads are refused. Plugins extend reasons and rule IDs, not message
schemas that a generic JSON client would be unable to read.

What a plugin finds valid and does not build yet it refuses as every
server of Frontseat refuses what it does not serve: `UNIMPLEMENTED`, for
the reason `<WHAT>_NOT_SERVED` (`WORKSPACES_NOT_SERVED`), with the file it
is about. The host tells it from a method the plugin does not serve by
its reason, which names what is not served and not the method. A file
that is not as the plugin reads one is `FAILED_PRECONDITION`, for a
reason of the plugin's own.

The host refuses a failure that is not as this section says, each rule
by a refusal of its own: a canonical code of a failure, and a message;
one `ErrorInfo`, its reason `[A-Z][A-Z0-9_]*`, its domain the plugin's;
one `Diagnostic`, its rule ID the plugin's name, a slash and the reason
as lower-case words of `[a-z0-9]` joined by single hyphens, with a
message, and where it names a file, a path relative to the repository's
root that stays within it, no repository and no line below zero. The
Rust SDK holds a failure to the same rules before it encodes one, and
sends in its place an `INTERNAL` failure for the reason
`invalid-failure` that says the rule broken and the message the plugin
wrote; one table of cases holds the two to agree
(`sdk/plugin-test/testdata/failures.json`).

A plugin's interests are patterns of the language `host.proto` says, of
files beneath the repository's root; the host refuses a plugin whose
interest is not one. The Rust SDK adds `!**/testdata/**` to any plugin's
interests, last: a `testdata` directory holds fixtures, and what is in
one is no project.

A module never traps to report a failure. A trap is a bug in the module,
and the host reports it as one, naming the plugin and the call, with the
end of what the module printed to standard error (its last 8 KiB): where
a Rust plugin's panic says its file, line and message. A call its caller
gave up, and one in which the host could not serve a read, are reported
as that, whatever the module did after.

## Rules a module can rely on, and must keep

- **The same reads and the same request give the same answer.** The host
  records every import a call makes: what an answer depended on.
- **An answer is used in the order it is given.** Projects, actions and
  what an action was handed keep the order a module returned them in, so
  a module returns them in an order that does not change from call to
  call.
- **The host holds an answer to the contract.** An answer that breaks it
  is an error the host reports, naming the plugin and the project.
- **Describe reads nothing.** A read through the host during Describe
  fails the call.
- **Options retain their types.** Defaults, call contexts and graph views use
  `frontseat.type.v1alpha1.OptionValue`: text, a switch, an integer, a list of
  text, a length of time or a map of text by name. The SDK checks every
  declared option before ecosystem code runs. Missing, unknown, unset and
  mistyped values fail; empty strings/lists/maps, false and zero are present
  values. All signed int64 values are carried exactly, without floating-point
  conversion.
- **An option says what it does and what it takes.** Each has a description,
  one sentence of at most 200 bytes, and may bound an integer (`minimum`,
  `maximum`) or allow only some words of a text, of each entry of a list or of
  each value of a map (`allowed`). The host refuses a plugin whose declaration
  does not hold together, or whose default it does not take, at load; and a
  value a repository or an entity sets that the option does not take, at the
  file and line that set it, naming the option and the rule and never the
  value. A plugin need not check these again.
- **Profiles are explicit.** Analyze, Arrange, Report and Reconcile
  require development or production. The SDK rejects unspecified and unknown values before source
  access, just as it validates options before ecosystem code.
- **Each call is made on an instance of its own.** Nothing a call
  leaves in memory is there for the next, and calls do not wait for
  each other.
- **Exports have the shapes above.** The host checks each export's
  parameters and results, and that the module exports a memory, before
  it runs any of the module; one that differs is not loaded.

## A catalog's files

A plugin that finds entities is asked, for a file out of step, what it
should say (`Reconcile`): for each entity, the components the source says
it depends on and that depend on it. It answers edits, each a sequence of
strings at a key path of one document of a file it was asked about; no
values take the key out. The plugin owns the schema: which field, how a
reference is spelled, which document is which entity. The host owns the
write, keeping the file's comments and other fields, and reads the
result back through the plugin: an edit after which the plugin's own
reading disagrees with the source is refused. A plugin that finds no
entities refuses the call as unsupported.

## A project's workspace

A tool may keep what a project is built with above the project: cargo
keeps `Cargo.lock` and the manifest its members inherit from at the
workspace root, and finds a path dependency by the path the manifest
names. The contract carries this in three places. `Project.workspace_files`
names literal files above the project, relative to the repository, that
every action of the project reads where they are. A dependency input's
`into` may be `{root}`, and what is handed is then placed at its
own root in the repository; `Handed.into` tells the plugin the way there
from the project, such as `../greet`. A file an Arrange writes may sit
directly in a directory above the project, never beside it, for the
workspace, in place of the repository's own
(the workspace manifest, narrowed); staging refuses one in place of a
workspace file the project reads. A project that says `always_arranged`
has its actions with dependency inputs arranged even when no workspace
handoffs resolve. Actions without dependency inputs are not arranged. A project at the
repository's root has nothing above it, and names no workspace file.

## Generated source

The contract carries `Describe.generates_sources`, `Project.generated_sources` and
`InputSet.accepts_generated`. Producers are analyzed first using committed
metadata. The host rejects discovery reads of generated content, including
cached observations. A contribution names a required directory output of its
action and where that output goes in the source: `directory`, relative to the
producing project and beneath it, or `destination`, relative to the repository,
for a place outside it and not holding it, never both. Either is written
plainly, with no `..` and no `.frontseat` part, and must be at or beneath the
root of a project that accepts generated source; the nearest such root receives
it. The action itself still writes only beneath its own project; the host
places what it left. Where the place belongs to the producing project's own
Component (or the workspace has no Components), nothing more is asked. Where it
belongs to another Component, that Component must say, in its catalog file,
that the Component owning the producing project generates it:

```yaml
spec:
  frontseat:
    generated:
      - directory: gen        # relative to this catalog file
        by: component:api     # the Component whose project generates it
```

Anything else is refused by name: no such statement, one naming another
Component, one naming anything but a Component, one for a directory owned by a
Component nested in the one that says it, and any place with another
Component's directory beneath it. Patterns define authoritative generated paths;
output files outside them are errors, while handwritten checkout files outside
them remain visible. Nested accepting input selections retain each enclosing
scope. The host adds producer edges and transfers selected CAS files to both
the owning module and downstream source consumers. A copy of generated files
the checkout already holds is kept in step with what the worker generated, and
`build --fix` writes it there; a checkout that holds no copy is never given one.

## What an action reads

`Action.inputs` is the one list of what an
action reads, encoded as `Input` messages with an explicit `kind`:

- `source`: a project-relative source glob, including exclusions. No reference
  prefixes are interpreted within it.
- `input_set`: a named set, expanded with its generated-source acceptance scopes.
- `output`: a declared output, or an action for all its outputs, of this plugin's
  project. The name has no leading colon. Missing, ambiguous and outputless
  producers are refused, as are references to another plugin's actions.
- `dependency`: a selection of all declared project requirements or one declared
  coordinate, with a required `into` placement. Transitive workspace handoffs
  are included; requirements with no workspace provider remain external.

An input without a supported kind is refused. Input sets may contain only
source and nested-set variants. Repeated transitive handoffs are included once;
conflicting placements are refused. Arrangement groups use the actual handed
coordinates and placements, not just a common destination template.

An output is written plainly beneath its project or, as `~/…`, in its action's
home; never the whole of either or `.frontseat`. Source patterns never select
outputs. Catalog shorthand is no part of the plugin wire format: the host reads it
into source patterns and references directly, and it never becomes these
messages. The SDK's constructors build each kind.

## Services for tests

A plugin may say how a service is started for a test. It says in
`Describe` the names it answers for (`services`), as a catalog entity
names what its service is: `spec.frontseat.service.as`. The host asks
`Service` of the plugin of the repository that claims the name, with the
name and what the entity's file wrote beside it, and is answered the
whole service: an image from outside, its arguments, the ports it serves
by name, its environment, when it is ready, how long it is given, where
a test reaches it (`endpoints`) and what a test is told. A service has
one shape, `frontseat.type.v1alpha1.Service`: it is what the catalog
file is read into, what the plugin is told and answers, and what the
worker's runner is told to start, so nothing is converted between them.
`{host}`, `{port.<name>}` and `{<name>.port.<name>}` in arguments,
environment, a readiness command, exports and endpoints are filled in by
the host.

- **Service reads nothing.** It is told a name and what was written, and
  nothing of a repository: a read through the host fails the answer. The
  same question has the same answer, and the host keeps it.
- **What was written is kept, whole.** Each field of `written` that is
  set is one a person wrote; the answer has it unchanged, and the host
  refuses an answer that has not. A field left empty is the plugin's to
  say, to fit what was written. The host refuses a catalog key written
  to say nothing, so a field that is set is always one a person wrote.
  What a plugin cannot make fit it refuses, and the host says so where
  the service is written.
- **The answer is held to what every service is**, whoever said it: an
  image named with its registry and digest, a port for each name,
  readiness and endpoints that name ports it serves, exports the shell
  can export. One implementation holds it (`server/internal/testservice`),
  for the daemon and again for the runner.
- **A service runs as no root, and writes what it says.** Each service
  runs in a sandbox of its own, which runs nothing as root: `user` is the
  user its image's processes run as, by number (an image that starts as
  root to give itself to a user of its own is told that user), and
  `writable` the directories of its image it writes. A service that says
  neither runs as the sandbox's own user, writing its temporary and
  working directories and the routes it is reached by alone.
- **A service may serve its Component's APIs.** It binds an API its
  Component provides to a port it names and to what a consumer is told,
  and the host checks that a plugin's answer keeps those bindings.
- **Endpoints are told to the test.** Each test given services is told,
  in `FRONTSEAT_ENDPOINTS`, a JSON object of each service by its entity
  reference, holding each endpoint it can reach with `{host}` and its
  port filled in where the test reaches them.
- **A test is told where its repository is.** Every test, whatever plugin
  made it, is told in `FRONTSEAT_TEST_SRCDIR` the path from the
  directory it runs in to its repository's root in its input root: what
  it reads beyond its package, as its Component's catalog file names it
  (`spec.frontseat.test.inputs`), is beneath it, and it never looks up
  from where it runs. A test given the name otherwise, by its
  Component's catalog file, a service or its plugin's command, is
  refused by name.
- **A name is claimed once.** Two plugins of a repository that claim one
  name are refused, and a name none claims is refused with those that
  are. A plugin asked for a name it did not claim refuses.

In the SDK, a plugin answers `Service` by implementing
`Plugin::service`; one that claims no name leaves it, and answers
`UNIMPLEMENTED`.

A plugin that only answers for services and reads no file of a
repository declares no interests and finds nothing. `container` starts
what is written and adds nothing; `postgres` says everything of a
PostgreSQL where nothing is written.

## Releasing

A plugin says what it serves of releasing (`release.proto`): the roles it
declares in `Describe.release`, branching strategies and versioning
strategies, and the calls the host asks of each: `Branch` and `Version`,
which read nothing through the host. The host asks them of a plugin that
says it serves a branching or a versioning strategy, when a release is
planned. Every answer is checked before any of it is in a plan.

A branching strategy says of a branch that it releases nothing, with
why; that it releases, held to a line or to none; or that it releases a
prerelease (`BranchResponse.prerelease`), held to no line, with how far
above the unit's highest release it is at least (`at_least`), where it
is heading for more than its commits say. It never spells a prerelease:
the host does, from the unit's own setting, the branch and the commit.

## What an action is

Every action is of one kind (`Action.kind`), and the host refuses one
that says none: a step of a build (`Build`), a test (`Test`), a check
(`Check`), a fetch of its project's lock (`Fetch`), a shared fetch
(`SharedFetch`), a program to run (`Program`), a package (`Package`) or
what publishes (`Publish`). What only one kind says is said in that
kind: a test whether it may be idle, a check its fix and what the fix
may change, a shared fetch its files and downloads, what publishes the
variables it is given. So no action is two kinds at once, and nothing
of one kind is said of another.

## What a project ships

An action that makes what its project ships is of the kind `Package`: a program for a platform, a packed module, an
archive, as its tool makes one. Each file it leaves is an artifact of
the release that builds it, known by the file's own name. A plugin
names nothing for a release and decides no layout of one: it lets its
tool write what that tool's users expect, `greet-1.4.0.tgz` or
`greet-linux-amd64`.

`Context.platforms` are the platforms the project's entity ships for,
each `<os>/<arch>`. A plugin whose tool makes one thing a platform makes
a package for each; one whose tool makes the same on every platform
makes nothing of them.

A package is an ordinary step of the build it is declared for, read by
whatever names what it leaves. What a release builds is what a
production build builds: declare a package for the production profile
alone where a development build should not pay for it. Name it
`package`, or `package`, a slash and what it is of
(`package/greet-linux-amd64`), unless the tool has a name for it; the
packages named so, and those alone, are asked for and pointed at
together, by `package`.

Refused: a package that leaves nothing, a directory, or what it may not
leave; and an action named as a package that is none.

## What publishes a release

An action that sends what a release shipped somewhere is of the kind
`Publish`, and runs its tool's own way of doing so: a forge's
command line, a registry's client. Nothing of frontseat's speaks to a
forge or a registry for it.

It is no step of a build. No build runs it; a release does, once every
tag of the release is pushed, where the daemon is and never on the
grid, with its tools as the repository pins them, and never from a
cache. It runs for a unit that moves: the one that owns its project,
or, where no entity owns its project, each unit of its repository, one
at a time. It runs in a directory that holds that unit's artifacts and,
where there are any, `checksums.txt`, their sha256s as `sha256sum`
writes them. It is ended after thirty minutes.

Its command names what it is given as a versioned action names the
version, with `{frontseat.version}`, `{frontseat.tag}`,
`{frontseat.commit}` and `{frontseat.unit}`, and with:

| Written | Filled with |
|---|---|
| `{frontseat.artifacts}` | each file it is given, an argument each; written as an argument by itself |
| `{frontseat.notes}` | a file that holds the unit's release notes |
| `{frontseat.remote}` | where the unit's repository was brought from |
| `{frontseat.prerelease}` | `true` or `false` |

`Publish.environment` names the variables of where the daemon runs that
its tool reads a token from, as `GH_TOKEN`. It is given one only where
whoever runs the daemon said it may be (`frontseatd
--publish-environment`); its environment holds nothing else of where
the daemon runs. It runs as the daemon does, and is not confined. Name it `publish`, a slash and what it publishes to
(`publish/release`).

It passes or fails by its exit code. One that fails fails the release,
which says so on the unit with what the tool printed; the tags stay
pushed.

Refused: one that runs nothing or names its program by something to be
filled, leaves or reads something, or says
where it runs; one said to be versioned; a variable that is no
name a tool reads one by, or one the host sets; and
`{frontseat.artifacts}` anywhere but as an argument by itself.

## A release's version

An action that writes a version into what it makes says it is versioned
(`Action.versioned`), and names what it needs in its command:
`{frontseat.version}`, `{frontseat.tag}`, `{frontseat.commit}`,
`{frontseat.unit}`, in an argument or in a value of its environment. The
host fills them before the action is sent anywhere. A plugin is told no
version, and decides nothing by one: it says where its tool takes it, a
linker's flag, a variable its packer reads, and the same action serves
every build.

Only the build a release makes for production is given what was planned
for the unit the action's project is released with. Every other build is
given `0.0.0-dev` for the version and nothing for the rest, each time, so
a versioned action is one action from a commit to the next. Mark the one
action that writes the version, never a test or a check: each versioned
action is built again at every release. A prerelease its unit does not tag has no tag to give:
`{frontseat.tag}` is then nothing in the release's own build too.

Whatever begins `{frontseat.` is the host's to read. Refused: one of
these in an action that is not versioned; one the host does not fill,
or that does not close; one anywhere but an argument or a value of the
environment, as in a fix, a directory, a name of the environment, an
output, an input, a tool or a file; a versioned action that names none;
and a versioned fetch, shared fetch or program to run.

What is filled in is data, and a tag is whatever the repository's
catalog says a tag is, within what git allows a tag to be named: give
each to the tool as an argument or an environment value of its own,
never inside text a shell or another interpreter reads as a program.

## What a shared fetch downloads

A shared fetch that names `downloads` runs no tool. Each download is a plain
public https address and what the lock holds of what it serves, in one of two
forms, never both:

- `sha256` and `into`: the lock holds the sha256 of the whole download, a
  gzipped tar. The grid's asset service checks it before it keeps anything, and
  the action unpacks it into a directory of its home.
- `parts`: the lock holds a checksum of each of some runs of bytes and none of
  the whole, as an apk lock does of a package's sections. The asset service
  fetches the whole unchecked; the action cuts each part out by its offset and
  length, checks it against its `sha1` or `sha256`, and keeps it as the file of
  its home the part names. Where a part is not what the lock says the action
  fails and leaves nothing.

## Programs

An action may be a program to run, of the kind `Program`. Such
an action is no step of a build: no build runs it, and a run
(`RunService` of the build API) builds what it reads and then starts its
command where the daemon is, in the daemon's sandbox, with the caller's
arguments after its own. A plugin offers one for what its ecosystem's files
say is a program, as the Go plugin does for each directory that says
`package main`.

- **It is named `run/` and what it runs**, in letters, digits and
  `._-`: `run/greeter`.
- **Its program comes from preparation**: `arguments[0]` is a plain path
  beneath the project, `~/…` in the action's home, or a bare name resolved
  only within prepared mise tools. Its inputs and tools are brought from the
  grid before a test service starts. Daemon `RunService` serves programs
  reading built artifacts alone and refuses source/tool preparation by name.
- **It leaves nothing.**
- **Refused by name**: dependency inputs. What runs is the command, and
  its environment is the command's; ports, readiness, endpoints and
  exports are a service's, said in the catalog
  (`spec.frontseat.service`).

## Tests in pieces

A project's tests may be several actions, and a command may say where it
runs.

- **A test may be named `test/` and what piece it is**, in letters, digits
  and `._~+@-/`, as its tool spells it: `test/internal/ledger`, `test/.`. A
  plugin says a piece for each thing its tool tests by itself, such as a
  package of a Go module. A run asked for `test` runs every piece, and the
  label that ends in `test` points at them all.
- **Only a test is named so**, and a project declares `test` or pieces of
  it, never both: either is refused by name.
- **A piece may be cut into shards**, each named its piece, `@`, which of
  them it is and of how many, from 1: `test/internal/ledger@2of4`. No
  piece's own name holds `@`, and a name that holds one and is no shard's
  is refused, as are shards that are not one cut of their piece: some of
  them missing, two counts, or the piece whole beside them. The label of a
  piece cut into shards points at its shards.
- **`Command.directory` is where a command runs**: a directory beneath the
  project, relative to it; empty is the project's own. One that is not a
  plain path beneath the project is refused. A Go package's test program
  runs in its package's directory, as the go command runs it.
- **A test may have nothing to do** (`Test.idle_without_inputs`):
  where an output of another action that it reads was not left, and that
  action may pass without leaving it (`Output.optional`), it is not run and
  ends idle, which fails nothing. A plugin whose tool decides on the worker
  what there is to do does not foretell it: the Go plugin's test of a
  package is idle where the go command built no test program for it. A
  test that says so and reads, by itself, no output another action may
  pass without leaving is refused, as is one that says it leaves something.
- **Not served yet, and refused by name**: a `directory` said by a fix or
  by a program to run.

## Artifacts

`Provide.artifacts` names exact outputs of the provider's own finite
actions, handed to consumers through their coordinates and dependency
placement. Outputs are worker-owned immutable artifacts; source patterns
never select them. Optionality is the producer's output's: an absent
optional output contributes no files, and a missing required output fails.
