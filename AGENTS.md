# Working in this repository

Frontseat is a remote-first, agent-first build tool, and this repository
builds it. These are the rules every change keeps, each with what holds
it. A rule marked *target* is where the code is going, not where it is;
the change that brings the code there also turns on what holds it.

`server/`, `local/`, `specs/` and `plugins/` (which covers the plugin
SDK and its testkit too) each have an `AGENTS.md` of the mistakes made
there and the check that stops each. Read the one of the part you change
before you change it; a new mistake is added there, with its check, in
the change that fixes it.

## Before you build

1. **Research it in the tools that came before.** Read how Bazel, Buck2,
   Pants, Please, Nx, moon and bob solve it, in their source (clones sit
   beside this repository; clone a missing one whole), and the ecosystem's
   own rules (rules_go, rules_rust, Buck2's prelude, Pants's backends).
   Say what each does, where, at which commit, and what frontseat takes.
2. **Look for what exists**: this repository's shared libraries (below),
   then a maintained library. New code is written only when neither serves.
3. **A use case is a spec that fails first**, then the code. A spec that
   expects a refusal has a control that shows the same thing allowed.
4. **Ask before a choice costly to undo**: a dependency, a framework, a file
   format, a contract change, a change to a rule here.
5. **Say what changes in a contract before changing it**: the API
   (`api/`), the plugin contract and ABI, the command line.

## While you work

- Do only what was asked. Nothing is committed or pushed unless the owner
  asks. Never end a turn with the tree red; if something must stay broken,
  say so first.
- A number comes from a run, with the command that produced it.
- A gate (`mise run check`, `mise run test`) is green by its exit status.
  Batch fixes before running it.
- A performance budget is the owner's to raise. Over budget, remove the
  cost or ask.
- A slice is done when someone who did not write it has attacked it and
  nothing they found is open. Security code is always reviewed so.

## Building

- **mise bootstraps, Frontseat builds itself.** mise installs the pinned
  tools and builds Frontseat into `dist/` (`mise run build`); `mise start`
  brings the local stack up from the checkout (`frontseat-local up`): one
  container of `local/image`, as it is for anyone who runs Frontseat
  locally.
  From there a contributor builds in a workspace with `frontseat build`.
  Held by: the self-build spec (`specs/build/self_test.go`). Frontseat
  runs this repository's own tests too, on the grid, as `frontseat test`
  does (`TestRunsThisRepositorysOwnTestsOnTheGrid`); a test that needs the
  local stack itself says so first and is skipped there, its Component's
  catalog file telling it there is none (`spec.frontseat.test.environment`),
  and `mise run test` runs it. *Target*: Frontseat builds its images and
  docs too, and runs the stack's specs beside a stack of the commit; until
  then they run through mise, and `CONTRIBUTING.md` says which.
- **A test is given its package's own directory, and what its project
  depends on**: its program, every file of its package's directory and
  its `testdata`, as the go command runs a test there, and what the
  build hands it (other modules, its module's fetched dependencies). What
  it reads beyond that, its Component's catalog file names
  (`spec.frontseat.test.inputs`, a path from the repository's root
  written `/…`, or what another action left, `//path:what`, its path
  from the repository's root too, as it is in an action's `inputs` and
  a service's `program`, wherever a workspace holds the repository): a read of anything else fails, for want of the file, and
  is never found by walking up. A `testdata` directory holds fixtures,
  to every plugin: what is in one is no project.
  Held by: `TestRunsThisRepositorysOwnTestsOnTheGrid`,
  `TestACatalogInputFromTheRootIsReadFromTheRepositorysRoot`,
  `TestACatalogLabelFromTheRootNamesAProjectOfItsOwnRepository` and the Go
  plugin's `served_plugin_composes_manifest_inputs_and_profile_actions`.
- **The box is built one way** (`local/box`): by `mise run build`, by the
  stack and by the gate, and tagged by what it holds. The stack and the
  tests each run the box built from their checkout, by that tag, never
  `frontseat-box:local`, which any checkout moves (the tests are told
  the gate's box, `FRONTSEAT_TEST_BOX`). Held for the stack by
  `TestAStackRunsTheImageAndBoxOfItsCheckout`.
- **What to build is pointed at**: an entity, a path, or an action as it is
  printed, by `frontseat build` and `frontseat test` alike. `test` is a
  build that keeps the actions their plugins say run tests (the
  contract's test flag, never an action's name), and ends
  `NO_TESTS_FOUND`, exit 4, where there are none. `--only NAME` keeps the
  actions of a name. `frontseat run PROGRAM` runs a program (a run, of
  `RunService`); a build is of `BuildService`. `frontseat query` reads
  what the workspace is made of and why one entity depends on another,
  with the version each lock resolves a requirement to; there is no
  query language.
- **Tools come from mise and are pinned nowhere else.** Never name a tool
  and a version to `mise exec`; after any mise command, `git status --short
  mise.lock`.
- **A program is configured by its flags.** No production program reads a
  `FRONTSEAT_*` variable to decide what it does, and no test switch is
  read by production code: a test passes flags or uses test support. A
  variable is kept only where a process boundary forces it:
  `FRONTSEAT_CRED_*`, a credential's stand-in inside a sandbox;
  `FRONTSEAT_ENDPOINTS`, the services a test is given;
  `FRONTSEAT_TEST_SRCDIR`, where a test's repository is, from where it
  runs (the gate gives its root whole), so that no test looks up for it
  (`TestATestIsToldWhereItsRepositoryIs`, `TestNoTestLooksUpForWhatItReads`,
  `TestWhatATestIsToldOfItsRepositoryIsSaidByOneName`). The control plane tells a box's daemon by the
  arguments it starts it with (`SandboxSpec.arguments`).
- **The command line asks one server, chosen one way.** Every command
  that asks a server (`version` and `completion` ask none) asks the
  control plane `$XDG_CONFIG_HOME/frontseat/local/server` names,
  else the local stack's; the token beside it is presented to that
  origin alone, never to an address a server answers with, and no
  redirect is followed with it. No command takes a server by a flag.
  Inside a workspace's sandbox, which the command line knows by the
  sandbox's lock its daemon holds (`Lock` in `local/ports`), a command asks
  that daemon's build API on the loopback and no control plane, and
  every `workspace` command and `--workspace` is refused by name.
  `frontseat --help` says so. Held by:
  `TestEveryCommandAsksTheOneControlPlaneTheRuleNames`,
  `TestInsideAWorkspaceItsSourceIsFixed`,
  `TestTheTokenIsPresentedToTheControlPlaneAlone` and the SDK's
  `TestATokenIsPresentedToItsOriginAlone`.
- **A workspace has one writer.** The control plane alone serves
  `frontseat.workspace.v1alpha1` and keeps what a workspace is asked for;
  it tells the workspace's daemon what to hold through the daemon's
  source API (`frontseat.source.v1alpha1`), at a generation it keeps,
  and reads back what the daemon holds, acting on a refusal's reason,
  never its words. The daemon keeps no spec: one started again, or told
  otherwise from inside its sandbox, is told the control plane's again.
  Inside a sandbox the source is fixed; it is filled from outside. The
  source port is no boundary inside the box: what holds the workspace is
  that nothing is read back as what was asked for.
  Held by: `TestADaemonStartedAgainOrToldFromInsideIsToldWhatTheControlPlaneKeeps`,
  `TestADaemonThatIsBringingIsKnownByItsReasonAndNotByItsWords`,
  `TestWhatADaemonRefusesToHoldIsRefusedByAReason` and the sandbox
  specs `TestInsideAWorkspaceItsSourceIsFixedAndItIsFilledFromOutside`
  and `TestTheDaemonsSourceAPIIsTheControlPlanesAlone`.
- **One state root per program.** What a program keeps is beneath one
  directory: `frontseatd --state` (its runs; the sandbox's one lock is
  the sandbox's, at one path whatever the daemon is told),
  `frontseat-local --state`, `frontseat-runner --state`, and the command
  line's `$XDG_CONFIG_HOME/frontseat` (else `~/.config/frontseat`) on
  every system.
- **A port is said once**, in `local/ports`: the daemon's defaults, the
  control plane, the command line and the test support read it there;
  the stack's image's files (`local/image`) and `local/box.Dockerfile`,
  which cannot, are held to it by `local/ports_test.go`.
- **The local stack is one container** of `local/image`, for anyone who
  runs Frontseat locally and for contributors alike: the grid, the
  OpenShell gateway and the control plane, each a process s6-overlay
  supervises, and every sandbox in rootless Podman inside it. It is
  started with what nesting needs and nothing of this machine mounted
  (`local/stack/container.go`); `frontseat-local init` delegates its
  cgroups, lays out its one volume, and tells the gateway where a
  sandbox's names of this machine (`host.openshell.internal`,
  `host.docker.internal`) lead: to this machine, as the container runtime
  names it, where the person's own services are and the grid's ports are
  published. A sandbox resolves a name by OpenShell's DNS alone: Podman
  writes nothing of its own into a sandbox's hosts file. Held by:
  `TestTheStackBootsAsOneContainer`,
  `TestTheContainerIsGivenWhatNestingNeedsAndNothingOfThisMachine`,
  `TestInitSaysWhereASandboxsNamesOfThisMachineLead` and
  `TestTheStackMakesAWorkspaceWithNothingStartedByHand`. Not held: on a
  Linux machine's own Docker, the grid is published on the loopback, which
  the address the runtime gives that machine does not reach.
- **OpenShell is the only sandbox**, here and in enterprise Kubernetes:
  every workspace, every runner sandbox of the grid and every CI runner is
  an OpenShell sandbox, so that the runtime beneath any of them is changed
  by changing the gateway's driver. OpenShell refuses a sandbox made
  inside one: what confines an action within it makes no namespace.
  *Target*: the workspaces, the runner pool and the services a test is
  given are OpenShell sandboxes now; the CI runners are not yet.
- **An action is confined twice**: by the runner sandbox it runs in, one
  of a pool `frontseat-runner pool` keeps, one OpenShell sandbox for each
  slot of the worker, running one action at a time, holding that slot's
  build directory and nothing of another's, made anew whenever the worker
  mounts its slot anew or its runner ends; and, within it, by Landlock,
  which `frontseat-action` asks for before it runs the command, so that
  an action writes its own root, the output files beside it, the
  devices that keep nothing and a temporary directory of its own (its
  `TMPDIR`, in the slot's `/tmp`, a directory of the stack's disk the pool
  binds there, since the worker's mount keeps no file's mode; the runner
  clears every action's before and after each run), and nothing else of
  the slot, and is not run where the kernel has no Landlock. It reads the rest of the system,
  the slot too. What runs in each runner sandbox is Frontseat's runner
  (`frontseat-runner serve`), in bb_runner's place, over Buildbarn's
  runner protocol: it runs a command only where it begins with a
  confiner installed beside it and is held to the action's own input
  root, reached through no link, and tells it where it runs itself,
  refusing any other command, as any client of the grid may send; and,
  a subreaper, it kills everything an action started, after it and
  before the next, until nothing of it is alive, whatever became of the
  action's confiner, and ends itself, its sandbox with it, where that
  takes more than thirty seconds. The
  runner's socket is root's, and linked where the worker dials it in a
  directory of root's no sandbox holds (the pool, root, hands it over),
  so nothing in a sandbox reaches the runner nor replaces it; on Linux
  7.1 and later Landlock refuses it too. A test with services runs
  beside its own runner, in a sandbox of its own whose user holds the
  runner's socket: there Landlock alone refuses it, on 7.1 and later. The pool continues a runner an
  action stopped. An action that fetches is sent to the slots whose
  policy lets them reach the hosts named (`network=egress`); every other
  reaches nothing. `frontseat-action`, built of `server/internal/confine`
  alone so that its form is what it does, sets the action up, confines
  it and runs it. Held by: `TestTheConfinerIsConfineAlone`,
  `TestAnActionWritesItsOwnRootAndNothingAboveIt`,
  `TestNothingAnActionStartedOutlivesIt`,
  `TestAnActionReachesNoSocketAboveItsRoot`,
  `TestAnActionIsNotRunUnconfined`, `TestOnlyAConfinedCommandIsRun`,
  `TestTheRunnerRunsAConfinedActionAndRefusesAnother`,
  `TestNothingAnActionStartedOutlivesItsRun`,
  `TestWhereAnActionRunsIsWhatTheRunnerChecked`,
  `TestARunnersSocketIsHandedOverOnceOutsideTheSlot`,
  `TestARunnersSocketIsHandedOverAsWhatWasChecked`,
  `TestAnActionThatReachesTheNetworkIsSentWhereItMay`,
  `TestTheInstallOfAToolIsSentWhereTheNetworkIsReached`, the spec
  `TestAnActionCannotReachItsRunnerNorWriteItsSlot` and the pool's tests
  (`server/internal/runner/pool_test.go`). Not held: Landlock's scoping of
  signals and abstract sockets holds in a plain container and was
  measured not to inside an OpenShell sandbox, so an action can signal
  its slot's runner (one stopped is continued, one killed is made anew,
  each within a round of the pool, though one that stops it again and
  again stalls its slot), and actions that run one after another in a
  slot share its loopback. The runner runs any form of the confiner
  installed beside it, and every checkout that tested against the stack
  installed its own. The confiner stays dumpable, since OpenShell reads
  a connection's ancestors through /proc and refuses what it cannot
  read: what an action could do to its confiner's other threads through
  /proc is held by the kernel's ptrace rules alone.

## Entities

- The catalog uses Domain, System, Component, API, Resource and Location
  (no Template, User or Group). One Domain, `frontseat`, is the product
  and its release unit, saying `grouping: synced`: every component of
  its Systems releases at one version and one tag,
  `frontseat@<version>` (`tag: "{unit}@{version}"`). Its Systems are its functions (`build`,
  `plugins`, `workspaces`, `grid`, `clients`), written in the root
  `catalog-info.yaml`. A System and the Domain are entities, never
  directories: what is part of one says so (`spec.system`, `spec.domain`),
  in this repository or another. `specs` is part of no System; `api`,
  the public contracts, is part of `clients`.
- Every entity is in the Backstage namespace `frontseat`
  (`metadata.namespace`), and a reference in a catalog file is written
  bare, read in that namespace. A name is unique in the repository and,
  where two would clash, says its kind: a plugin is `<x>-plugin`, an API
  `<x>-api`, an SDK `go-sdk` or `plugin-sdk`; its directory is the last
  element of its natural name (`plugins/go` is `go-plugin`, `sdk/go`
  `go-sdk`, `api/build-proto` `build-api`). Held by: the self-build's
  catalog spec (`specs/build/self_test.go`) and
  `TestEveryEntityOfThisRepositoryHasANameOfItsOwn`.
- A Component owns the projects beneath its directory; nothing else owns
  projects. One directory per Component; a Component inside another's
  directory is only where it says `subcomponentOf`. A directory at the
  top is one Component (`api`, `cli`, `local`, `server`, `specs`) or a
  directory of Components of one kind: `plugins/` and `sdk/`; `docs/`
  holds the TechDocs site and is no Component.
- `dependsOn` is inferred and never written by hand, except the Resources a
  component's tests need. Each API has its own catalog file beside its
  proto module (`api/<name>-proto/catalog-info.yaml`), as each plugin has
  one beside its crate; its `definition` is the path of its proto package,
  from that file; Backstage shows it as text. Held by:
  `TestEveryAPIsDefinitionIsThere`.
- A Component's `providesApis` and `consumesApis` are written by hand.
  Once a Component that provides an API has a service
  (`spec.frontseat.service`), each Component that consumes it is given
  that provider as a service for its tests; an API with more than one
  provider that has one is refused
  (`server/internal/engine/evaluate/services.go`).
- Every catalog file is listed by the root `Location`, which Frontseat never
  follows: it finds catalog files by name. Every entity a catalog file names
  exists. Held by: `TestThisRepositorysRootLocationListsEveryCatalogFile`
  and `TestEveryEntityThisRepositorysCatalogNamesIsInIt`.
- A program built from source is a dependency no go.mod says: a file
  that has go build, run or install one says what of whose
  (`fromSource`, `specs/architecture/builds_test.go`), and test support
  never does it. The box image and this checkout's `frontseat-action`
  are built by the gate before the tests that need the stack, and the
  stack's image on that box (`FRONTSEAT_TEST_BOX`,
  `FRONTSEAT_TEST_CONFINER`, `FRONTSEAT_TEST_LOCAL_IMAGE`).
- Components depend on each other without a cycle, tests included; nothing
  depends on `specs`. Held by: the self-build's catalog check, and
  `TestThisRepositorysComponentsDependOnEachOtherWithoutACycle`.

## Releases

- A unit gets one version and one tag, `<unit>/v<version>` by default,
  from one repository: a component alone, or a System or a Domain that
  says `grouping: synced` as one group. A System is synced unless it says
  independent; a Domain only where it says synced. Groups are the
  catalog's own containment (`spec.system`, `spec.domain`), never a list
  a person writes. Held by: `specs/build/releases_test.go`.
- A branch that releases nothing stable releases a prerelease, where
  its branching strategy says so (Release Flow: every branch that is no
  release branch): of the version its commits give since the unit's
  highest release, wherever that one's tag is, and by a minor at least
  on the default branch, which a clone must know (where it does not,
  the branch releases nothing and says so). The host spells it, from the unit's setting
  (`spec.frontseat.release.prerelease`, `{branch}-{timestamp}` by
  default), the branch, and when the commit planned at was made: the
  commit's time, never the clock's, so a plan is of the repository
  alone. It is never a unit's last release, and has a tag only where
  the unit says so (`prerelease-tag`). How an entity is released is
  said the same whichever strategy it names: what only one strategy
  means is that strategy's plugin's option, never a release setting.
  Held by: `TestABranchThatReleasesNothingStableReleasesAPrerelease`,
  `TestAPrereleaseIsBuiltAtItsVersionAndNotTagged`,
  `TestAStrategysAnswerIsHeldToTheContract` and the release plugin's
  `under_release_flow_a_branch_that_is_no_release_branch_releases_a_prerelease`.
  Not held: two commits of a branch made in one second have one
  version, and a commit's time is what whoever made it says; a release
  made again of a commit that carries a prerelease's tag and a
  release's is another plan, and is not completed; a prerelease that is
  tagged is tagged again, and not found released, where it is released
  again; nothing publishes a prerelease, under its channel or any, so
  one that is not tagged is built and kept nowhere.
- A release pushes each tag it writes to where its repository was
  brought from, the tag alone, never over one the remote has at another
  commit, and never one of a commit no branch of the remote has, by
  the workspace's own git and what its sandbox was told. A push goes to
  the remote git was given and sends and runs nothing the repository's
  own settings add (`server/git`). A release that could not send a tag
  keeps its tags and ends unpublished, saying which and why, and
  `frontseat ship` exits 1; made again at the same commit it sends what
  was not. Held by:
  `TestAReleasePushesItsTagsAndSaysWhichItCouldNot`,
  `TestATagIsPushedAloneAndNeverOverAnother` and
  `TestAPushGoesToTheRemoteGivenAndSaysNothingTheRepositoryAdds`. Not
  held: the daemon is not told whether it may push, and tries, so a
  workspace opened without `--push` ends unpublished by its gateway's
  refusal, which no test makes; of several tags, those sent before one is
  refused stay sent, and those after it are not tried; a remote given
  as a directory runs its own hooks as the daemon; why a push failed
  is what git said, the remote's own words among it.
- A release publishes with actions, and with each tool's own way of
  publishing: an action says it publishes (the contract's
  `Action.publish`; `publish: true` on an entity's own action) and runs
  a forge's command line or a registry's client. Nothing of Frontseat's
  speaks to a forge or a registry. No build runs it. A release runs it
  once every tag is pushed, where the daemon is
  (`server/internal/exec/publish`, beside a fix in
  `server/internal/exec/here`) and never on the grid, with the tools the
  repository pins, for each unit it tagged then: the unit that owns the
  action, or, where no entity owns it, each unit of its repository. It
  is given the unit's artifacts, brought from the grid, with
  `checksums.txt` beside them, and is told the release where its command
  names it (`{frontseat.artifacts}`, `{frontseat.notes}`,
  `{frontseat.remote}`, `{frontseat.prerelease}`, and the version's).
  Its environment holds, of where the daemon runs, the variables it
  names that the daemon was told it may be given (`frontseatd
  --publish-environment`), and no other: that is how a tool is handed
  its token, in a job. It is ended, with whatever it started, after
  thirty minutes. What is said of it is the end of what it printed,
  with each of those values left out. An artifact it could not be given,
  by its name, fails the release before anything is tagged. One that
  fails fails the release, on the unit; the tags stay pushed. Held by:
  `TestAReleasePublishesWhatItShippedWhereTheDaemonIs`,
  `TestAPublishThatFailsFailsTheReleaseAndItsTagsStay`,
  `TestAPublishNoEntityOwnsPublishesEachUnitOfItsRepository`,
  `TestAnArtifactAPublishCannotBeGivenFailsTheReleaseBeforeItTags`,
  `TestAnArtifactIsNamedAsAPublishCanBeGivenIt`,
  `TestWhatAPublishPrintedIsSaidWithoutWhatItWasGiven`,
  `TestAnActionThatPublishesIsThatAlone` and
  `TestAnActionThatPublishesIsGivenTheReleaseAndItsArtifacts`. A
  repository that lists the GitHub plugin (`plugins/github`) is
  published by `gh release create`, a GitHub release for each unit, the
  tag verified and never made, and one that pins no `gh` is refused
  before anything is built; this repository lists it. A plugin names
  what publishes `publish`, a slash and what it publishes to. Held by:
  `TestTheGitHubPluginsPublishIsRunByARelease`,
  `TestOnlyWhatPublishesIsNamedPublishAndWhatItPublishesTo` and the
  plugin's own tests. Not held: no publish was run with a real tool or
  against a forge; no artifact was brought from a real grid; a release
  whose publish failed is not finished by making it again, its unit
  being found released then with nothing built: its tag and what was
  published are taken back first; a unit found released already, and a
  prerelease that is not tagged, are never published; a publish runs
  with the workers' tools, so only where the daemon's platform is
  theirs; it runs as the daemon, confined by where the daemon runs
  alone: the variables it is given are what its environment holds and
  no boundary, since it can read the daemon's own (through /proc), the
  grid's token and the daemon's state, so a checkout that says what
  publishes says what runs there as the daemon, in a release someone
  asked for; a daemon given a token this way holds a credential, which
  is for a job that already holds it and never for a workspace's
  sandbox, where credentials stay outside; the time limit, that
  nothing a publish started outlives it, and the daemon's flag have no
  test; a token a tool prints changed, as encoded, is not left out; no
  spec publishes against the local stack.
- A job releases from its own checkout. A daemon may be given a checkout
  (`frontseatd --checkout`, with `--name`, by which the command line
  beside it finds it): one repository, of no name, in its workspace's
  directory, which it builds and releases from as it is and brings
  nothing into. It plans from the checkout's history, tags there, and
  pushes each tag to where the checkout says it was brought from: its
  `origin`, read of its own file and held as an address a caller names
  is, so one with a password is refused and never repeated, one over
  ssh is pushed to over https, and whoever it names before the host is
  left out. What else the checkout says of itself is input from outside,
  held as a repository the daemon brought is: another address to push
  to and a hook are not followed. A checkout that could not be pushed
  from is not planned from, so that nothing is built and tagged that
  cannot be sent: one that keeps a credential of its own for an address,
  as a job's checkout does unless it is made with `persist-credentials:
  false`, which the refusal says; one of no origin; one of part of its
  history, which says to bring all of it; and a directory within a
  repository. How the push is let in the daemon is told as a sandbox's
  is, by the settings its environment counts for git. Held by:
  `TestADaemonGivenACheckoutReleasesFromIt`,
  `TestWhatAGivenCheckoutSaysOfItselfIsNotFollowed` (git asked plainly
  follows each, beside it),
  `TestACheckoutThatCouldNotBePushedFromIsNotPlannedFrom`,
  `TestACheckoutOfPartOfItsHistoryIsNotReleasedFrom`,
  `TestWhereARepositorySaysItWasBroughtFrom`,
  `TestADirectoryWithinARepositoryIsNotOne` and
  `TestADaemonGivenACheckoutIsNamed`. Not held: where the tag goes is
  what the checkout says, so whoever makes the checkout says where, and
  to what the credential is presented is whatever the job's environment
  told git, which nothing here scopes; an origin that is a directory is
  pushed to as it is written, and its own hooks run as the daemon. A
  checkout made as `actions/checkout` makes one records no default
  branch of its remote, so a branch that is no release branch releases
  nothing there until the job records it
  (`git symbolic-ref refs/remotes/origin/HEAD`); one that stands on no
  branch, as a pull request's does, is refused; one of all its history
  and none of its remote's tags plans a first release, and learns of it
  at the push. No daemon was started with the flag by a test, none ran
  in a job, and no push from a given checkout was made over http with a
  credential the environment told. What a release says of a checkout
  names it by its empty name.
- A release ships what its packages leave. An action says it makes
  what its project ships (the contract's `Action.package`; `package:
  true` on a Component's own action), and each file it leaves is an
  artifact of the release that builds it, by the name its tool gave it,
  with the sha256 and the size the grid holds of it
  (`ReleasedUnit.artifacts`). How a tool packages is its plugin's to
  know: the Go plugin makes each of a module's programs for each
  platform, `<program>-<os>-<arch>`, and a crate that is a wasm module
  ships that module. What an entity ships for is said once, by the
  Component (`spec.frontseat.platforms`, each `<os>/<arch>`, for what
  is built beneath its directory; a list of none ships for none), and
  told to every plugin that builds beneath it. What a release builds is what a
  production build builds, so a package that costs what a development
  build should not pay is declared for production alone; the packages
  a plugin names `package/…` are tried together by `frontseat build
  --profile production --only package`, and one its tool names, as
  `wasm`, by that name. Two artifacts of one name in a release, and a
  package that left a directory, fail it before it tags. Held by:
  `TestAReleaseListsWhatItsPackagesLeft`,
  `TestAReleaseBuildsAProgramForEachPlatformItsEntityShipsFor`,
  `TestTwoArtifactsOfOneNameFailTheReleaseBeforeItTags`,
  `TestAPackageThatLeavesWhatCannotBeShippedFailsTheRelease`,
  `TestAPackageLeavesFilesAndIsAStepThatMakesSomething`,
  `TestWhatADirectoryShipsForIsWhatTheNearestEntityAboveItSays` and the
  Go plugin's
  `served_plugin_packages_each_program_for_each_platform_it_ships_for`.
  Not held: a Go program is found by what builds for Linux, so one
  that does not build for a platform its entity names fails its
  package, and the release, and one that builds for another system
  alone is not packaged; each package compiles its module anew, and
  what that costs a release of this repository is not measured; no Go
  package was run on a grid; what a package left is listed for the
  entity that owns its action, and dropped where that entity is no
  member of a unit that moves; nothing brings an artifact's bytes to
  whoever asks, nor publishes one; a unit found released already lists
  none.
- A workspace made to push may ask its repositories' forge for their
  releases, and for those of no other repository: when it is made, the
  control plane lets its sandbox through for what the forge asks of a
  release (`server/internal/forge`: to list a repository's releases,
  make one, change one and add a file to one), with its owner's
  credential for the host asked, which the gateway holds and the
  sandbox never does. That is any release of the repository, and not
  one alone: whoever may push to a repository may already write
  anything to it. A forge is GitHub, or one that answers as GitHub does
  at an address the control plane is told (`frontseat-local --github`),
  written as it is compared and refused at start where it is not.
  GitHub is asked at hosts beside the one its repositories are fetched
  from: a credential given for that one is kept for those too, under
  names of its own, and let go of with them, the host's own first, so
  that one in use is let go of in no part; a credential a person gave
  for one of those hosts themselves is another, and stays theirs. A
  workspace that also fetches from a host its forge is asked at is
  refused. A request's path may hold a part that is any one part,
  written `*`, after its first, since a forge numbers a release as it
  makes it; nothing else of a pattern is taken. Held by:
  `TestAWorkspaceMadeToPushMayPublishItsRepositorysReleasesAndNoOther`,
  `TestACredentialForAForgeIsKeptForTheHostsItIsAskedAt`,
  `TestPublishingAsksAForgeForItsRepositorysReleasesAlone`,
  `TestAForgeIsToldOfByItsAddressAsItIsCompared` and
  `TestWhatARuleSaysIsHeld`. Not held: nothing publishes yet, and the
  program the rules name is not built. **The program a rule names is
  no boundary**: the gateway lets a request through for a program or
  any that started it, so whatever an allowed program starts (an alias
  of git's, a hook, what a publisher runs) asks what that program may,
  with its credential; what holds a sandbox is the requests and the
  credential, and that is so of git's own rule too. A forge asked at
  the host its repositories are fetched from has one rule there for
  both. A rule lets a request through and does not require the
  credential on it. A credential kept for a host and not for those
  beside it, as one given before the control plane was told of its
  forge, or where the second could not be kept, leaves a workspace let
  through to publish with none, and the forge refuses it: the
  credential is given again. That a gateway reads `*` as one part and
  no more was read in its source and probed by hand with a part said
  (`frontseat-probes/publish-egress-probe`); no spec holds it, nor what
  it makes of a part that is empty.
- Versions never go backwards: a unit with no release of its own
  continues above the highest version its members, their Systems or
  their Domain were released at, by the next bump its strategy gives.
- A System inside a synced Domain says `grouping: independent` or
  nothing; `synced` there is refused by name, and so is any other release
  setting, which is the Domain's to say.
- Refused by name, not served: a Domain within a Domain
  (`spec.subdomainOf`), anywhere in what a unit is part of; releasing a
  unit's dependents; a manual version.
- The workspace holds every System and Domain a released entity is part
  of; where it does not, planning is refused by name, since the group
  decides what the entity releases as.
- **What a release builds is given its version, the same way whatever
  builds it.** An action says it is versioned (the contract's
  `Action.versioned`; `versioned: true` on a Component's own action), and
  the host fills `{frontseat.version}`, `{frontseat.tag}`,
  `{frontseat.commit}` and `{frontseat.unit}` where its command names
  them, in its arguments and the values of its environment. How a tool
  carries a version is its plugin's to know: the Go plugin links it into
  a module's programs, into the variables its `version` option names.
  Only the production build a release makes fills in what was planned
  for the action's unit; every other build fills in `0.0.0-dev` and
  nothing for the rest, the same each time, so a versioned action is
  built once between releases. No time, user or machine is ever a value.
  The source is never rewritten for a version: a release commits
  nothing. A name of these in an action that is not versioned, or one
  the host does not fill, is refused. Frontseat says its own with
  `frontseat version` and `frontseatd --version`. Held by:
  `TestWhatAReleaseBuildsIsGivenItsVersion`,
  `TestAVersionedActionIsGivenTheVersionWhereItNamesIt`,
  `TestOnlyAVersionedActionNamesTheVersion` and the Go plugin's
  `served_plugin_links_the_version_where_the_option_names_its_variables`.
  Not held: a unit the release does not move is built at `0.0.0-dev`
  where the release's build needs it, and so is one already tagged at
  the commit when its release is made again; a version goes to the
  entity that owns the action's project, so a Go program of a Component
  nested in another's module says the module's owner's; a tool that
  reads its version from a file is given none, and writes it from its
  command; what is filled in is data, and a command that puts it inside
  text a shell reads runs whatever a tag's form lets a tag hold; nothing
  yet delivers what a release builds, so no released `frontseat` exists
  to say a version.

## Modules and visibility

- There are five Go modules: `sdk/go` (the Go SDK), `local` (the local
  stack and the box), `server` (every server program, what it is made
  of, and the test support), `cli` (the command line) and `specs`. Each
  requires only modules before it in that list, go.work uses and
  replaces each, and no go.mod replaces anything. A Component is a
  catalog entity, not a module: the daemon, the control plane, the
  sandbox service and the runner are packages of the one module
  `server`. A new module is a change to this rule. Held by:
  `TestEveryGoModuleIsInTheWorkspaceAndValidation`,
  `TestLocalModuleRequirementsAreAcyclic`, and the catalog's check for a
  cycle.
- `api/` holds the contracts and nothing else: the protos, Buf's workspace
  and templates, the API entities' catalog files, and prose. No code, no
  generated code, no Go module, no Cargo package. Held by:
  `TestTheContractsDirectoryHoldsNoCode`.
- Public, for developers, is in an SDK: the Go SDK (`sdk/go`, package
  `frontseat`: one `Client` from which workspaces, builds, tests, runs,
  releases, the graph and credentials are reached; beneath it the API's
  generated Go, `sdk/go/api`, and entity references, `sdk/go/entity`) and
  the Rust plugin SDK (`sdk/plugin`) and its testkit
  (`sdk/plugin-test`). The sandbox API has no client in
  the SDK: it is served to no one outside. An SDK depends on nothing of
  this repository but itself; what a program adds to how a client
  reaches a server (its telemetry) it passes as an option. Held by:
  `TestTheGoSDKRequiresNothingOfThisRepository`, and for the plugin SDK
  `TestRustCratesUseTheSDKAndNeverAnotherPlugin`. The plugin SDK and its
  testkit (`sdk/plugin-test`, the crate `frontseat-plugin-test`) are
  publishable, released at the SDK's version, the testkit depending on
  the SDK by path and that version; every other crate says `publish =
  false`, and every `Cargo.toml` is a member of the workspace or a
  fixture beneath `testdata`
  (`TestOnlyThePluginSDKAndItsTestkitArePublishable`). The testkit is a
  dependency of tests alone
  (`TestRustCratesUseTheSDKAndNeverAnotherPlugin`, its controls in
  `TestTheTestkitIsADependencyOfTestsAlone`).
- Everything else of a module is under its `<module>/internal/`, but
  what another module or frontseat-enterprise needs, each on one list
  with why (`public`, in `specs/architecture/visibility_test.go`): the
  sandbox service, its driver port and the OpenShell driver
  (`server/sandbox`, `server/sandbox/driver`,
  `server/sandbox/driver/openshell`), which frontseat-enterprise embeds
  and composes around and never copies; git and telemetry (`server/git`,
  `server/telemetry`), which the command line runs too (and with git
  the confiner's one way to end a process group, `confine.SignalGroup`,
  which a sandbox's refusal of a signal to a group does not stop); and the local
  stack (`local`, `local/ports`, `local/box`, `local/stack`), which the
  stack's program, the server, the command line and the test support
  read. Beside them are a binary's `main` (`cmd/<binary>`), a package of
  tests alone, and the test support (`server/testsupport/<name>`), which
  the import rules hold to tests. A directory is one package, and a
  fixture beneath `testdata` is a program or internal. Held by:
  `TestEveryPublicPackageIsInAnSDKOrOnTheList`.
- Generated code is in the Component that ships it, never beside the
  contract, and committed: the Go SDK's (`sdk/go/api`), the server's of
  the plugin contract (`server/internal/pluginapi`), of what a runner is
  told (`server/internal/runnerapi`) and of the daemon's source API
  (`server/internal/sourceapi`), each from a Buf template of its own
  (`api/buf.gen.*.yaml`), and the plugin SDK's Rust
  (`sdk/plugin/src/gen`, from `sdk/plugin/generate`). `mise run protos`
  writes it; `mise run check` fails where it is not what its generator
  writes now (`gate generated`, `frontseat-plugin-generate --check`).
  The self-build generates the Go through the Buf plugin and holds the
  committed copies to it; a Component written into says so in its
  catalog file (`spec.frontseat.generated`).
- A port is an interface package; its implementations are private
  (`server/internal/source/vcs` and its
  `server/internal/source/internal/vcs/git`; the sandbox `driver` and
  its container driver, `server/internal/driver/container`; the command
  line's `cli/internal/vcs` and its `cli/internal/vcs/git`). The
  OpenShell driver is public, for frontseat-enterprise; the driver held
  in memory is test support, in `drivertest`. Only one package on each
  side names a version control system
  (`TestOnlyOnePackageNamesAVersionControlSystem`), and only the grid's
  runner makes sandboxes with the container runtime
  (`TestOnlyTheGridsRunnerMakesSandboxesWithTheContainerRuntime`).
- A plugin uses the SDK and nothing else of the repository, except a crate
  nested in its own directory. What two plugins need goes into the SDK.
- Held by: the architecture test (`specs/architecture`):
  every import against a per-package allow-list, each new permission with
  an allowed and a refused case
  (`TestFeaturePoliciesHaveAllowedAndForbiddenControls`).

## Naming

- A package or crate is named for what it is: no `util`, `common` or
  `helpers`. A test file is named for its subject, never for the
  occasion it was written on (a review, a proof of concept, a proof):
  `TestATestFileIsNamedForItsSubject`.
- Test support for one package is `<package>test` beside it
  (`sourcetest`, `reapitest`, `plantest`, `toolstest`), as Go's
  `httptest`; test support shared across packages, and what starts a
  daemon, a control plane or a sandbox driver for a test (`buildtest`,
  `controlplanetest`, `drivertest`), is `server/testsupport/<name>`,
  named what its callers call it (`testgrid`, `testbox`, `testpins`).
  Test support is never imported by production code.
- A package is imported by its own name. An alias is written only where
  Go forces one, the name taken by another import of this repository or
  of the standard library, or by a declaration of the file's package
  (`execport` beside `os/exec`, `runexec` where the package declares
  `run`); where a file's own package has the name, which Go allows but
  reads as itself (`rungit` in a package `git`); and then only under the
  one name the architecture test gives that package. The API's generated
  packages are named for their service and version (`buildv1`,
  `pluginv1`). Held by: `TestARepositoryPackageIsImportedByItsOwnName`,
  which leaves out only the generated `*.pb.go` and `*.connect.go`.
- A binary is `cmd/<binary>` of its module (`server/cmd/frontseatd`,
  `specs/cmd/gate`); a `main` anywhere else is refused by
  `TestEveryPublicPackageIsInAnSDKOrOnTheList`. *Target*: each is a thin
  `main` over a package of its module's, as `cli/cmd/frontseat` is over
  `cli/internal/cmd`; `specs/cmd/gate`, `specs/cmd/prepare-modules`,
  `server/cmd/frontseatd` and `server/cmd/frontseat-local` are not yet,
  and nothing checks it.
- Every path of this repository a file names is there
  (`TestEveryPathAFileNamesIsThere`); nothing is reserved, as a package
  that declares nothing or a directory of a README alone
  (`TestNothingIsReserved`); `mise.toml` writes out no list of modules,
  packages or plugins (`TestMiseWritesOutNoModuleOrPlugin`); and what
  must be said twice, because a tool reads it where it is, agrees
  (`TestWhatIsSaidTwiceAgrees`).

## Nothing written twice

- One implementation of each thing, tests included. Before writing a
  helper, look for it; when a second caller needs it, move the first to
  where both can use it.
- Held by: jscpd at 50 tokens over Go and Rust, tests included, no source
  exceptions and no `jscpd:ignore` markers, in `mise run check`. Two
  implementations written apart are found by review.

## APIs

- A contract holds only what is served and proven: nothing unserved, no
  `reserved`, no field kept for later. No backwards compatibility until
  Frontseat is public: a change replaces what was there.
- Calls are named `Describe` (never `Get`), `List`, `Create`, `Update`,
  `Delete`, `Wait`, `Stream…`, `Cancel`, and a few named verbs (`Plan`,
  `Exec`, `Pause`, `Resume`, `Set`). Every call has its own request and
  response; a response wraps its resource in a field named for it.
- An id the server mints is `<resource>_id`; a name the caller chooses is
  `name`. Identifying fields are `REQUIRED`; fields the server sets are
  `OUTPUT_ONLY`.
- A `List` pages: it takes `page_size` and `page_token` and answers its
  list and `next_page_token`, empty on the last page
  (`server/internal/page`); the Go SDK gathers every page (`Listed`). A
  `Create` that a retry would make twice takes a `request_id`, and the
  same id sent again returns what the first call made; one keyed by a
  name the caller chooses, or by a plan's `etag`, is that already.
- Long-running work: Create returns at once, with every id a caller follows
  it by (a run's and a release's `build_id`), Describe reads, one unary
  Wait returns when it has ended, and `Stream<X>Events` and
  `Stream<X>Logs` follow it. Work that ends but stays readable is
  cancelled; a resource that is gone is deleted.
- A resource that can fail says why in one string field; its state enum is
  nested in it. Times are `Timestamp` named `*_time`; lengths of time are
  `Duration`.
- Errors carry a canonical code and `ErrorInfo{reason, domain}`, a domain
  per service; something refused by name has a reason that says so
  (`server/internal/refusal`), and every reason a server gives is named
  in the contract of the call that gives it, which a client reads it by
  (`frontseat.Reason`), never by the message. Held by:
  `TestEveryReasonAServerGivesIsNamedInItsContract`, its control
  `TestAReasonNoContractNamesIsFound`.
- A call or a field may be served by an enterprise server alone, or only
  where the operator has configured what serves it; its comment in the
  contract says so. A server that does not serve it refuses it by name,
  `UNIMPLEMENTED` with `ErrorInfo{reason: <WHAT>_NOT_SERVED}` in the
  service's domain, and never ignores it: no other code and no other
  suffix, a capability a driver lacks (`CAPABILITY_NOT_SERVED`) and what
  an operator did not configure (`IMAGE_NOT_SERVED`, `FETCH_NOT_SERVED`)
  alike. A spec proves the refusal, with a control showing it served
  where it can be. Held by: those specs.
- A contract names no other contract's types: what two need is
  `frontseat.type.v1alpha1`'s (an entity's reference, a version's bump).
  What is served by nothing, as what a runner is told
  (`frontseat.runner.v1alpha1`), is no API and is generated for the
  server alone; so is what one part of the server serves another alone,
  the daemon's source API (`frontseat.source.v1alpha1`), on a port of
  its own (`Source` in `local/ports`) that no route of the control plane's
  leads to.
- A change to the plugin contract raises `ABI` in the SDK and the host's
  range in the same change; the conformance fixtures name the number.
- Held by: the API's rules over the contracts as Buf builds them
  (`specs/architecture/api_test.go`): allowed verbs, a request and a
  response of its own, only `Stream` calls stream, a response wraps its
  resource, identifying fields are required, a List pages, no `reserved`,
  every message reached by a call, one failure shape, times and lengths
  of time, a contract imports only shared types, a Create can be sent
  again, and a handler in the server for every call; each with a
  fixture it refuses and one it allows.

## Security

- **A checkout is input from outside.** Git is run over one told to run
  nothing it names and given nothing of git's from the environment, except
  what the control plane tells a sandbox; where a remote is reached, git
  fetches from and pushes to the address it was given alone, and a
  repository's own settings that would reach another address, or reach it
  otherwise than the sandbox says, are refused by name (`server/git`).
  Each claim is proven by a fixture that plants the thing beside git asked
  plainly, which follows it; the package doc lists what is not held.
- Whatever a caller, a store or a file names is checked before it reaches
  a file, a process or a runtime; what reaches a command line is read by
  its own parser first and passed after `--`.
- A refusal never repeats what it refused: a value may hold a token.
- Credentials stay outside sandboxes, except a test's disposable
  credentials for its own services.
- **A grid that asks who a daemon is, is told by a token the daemon reads
  from a file** (owner, 2026-11-16): `frontseatd --grid-token FILE`, with
  `--grid-ca FILE`, for the grid and its asset service alike
  (`server/internal/exec/access`). A token is told only to a grid
  known by authorities the daemon was named: the system's (`--grid-tls`)
  are chosen by the environment, and a variable would choose who is told
  the token. The token is a JSON Web Token that admits its bearer to the
  grid and nothing else, lives for minutes, and is replaced, the file
  whole and by a rename, by whoever was given it: the daemon knows
  nothing of where it comes from. It is sent to the grid and to the
  asset service `--assets` names; never in the clear, never an argument
  or an environment variable, and no refusal repeats it. Whatever the
  daemon's sandbox runs as the daemon's user can read the file: a fix, a
  launched program, a shell. So the token is one that may ask the grid
  for what a build asks and may write no result others rely on; that is
  the grid's authorizers' to hold, and frontseat cannot check it. A grid
  that does not admit the daemon, one that cannot be asked over TLS as
  it was told, and a token that could not be read are each said as what
  they are and not waited for as a grid not up yet; a grid that stops
  admitting the daemon while it builds says so of the action asked for,
  and nothing of that action is kept. That a grid could not be asked over
  TLS is known by what TLS said to the daemon (a certificate not
  verified, an answer in the clear or not in HTTP/2, an alert of
  refusing, during the handshake or after it), never by a grid's words;
  and
  neither the grid nor its asset service is reached through a proxy the
  environment names. Held by: `TestATokenIsNotSentInTheClear`,
  `TestAGridThatCannotBeAskedOverTLSIsNotOneThatIsNotUpYet`,
  `TestAGridThatRefusesTheDaemonsSideOfTLSIsNotWaitedFor`,
  `TestAGridThatCannotBeAskedAsToldIsNotWaitedFor`,
  `TestAGridsOwnWordsAreNotTakenForTheDaemonsTransport`,
  `TestATokenThatCouldNotBeReadIsNotTheGridsRefusal`,
  `TestAnAssetServiceThatCannotBeAskedOverTLSIsSaidSo`,
  `TestAnEnvironmentsProxyIsNotTakenToTheGrid`, and actions run through
  stock Buildbarn asking as such a grid does
  (`TestAnActionRunsOnAGridThatAsksWhoTheDaemonIs`,
  `TestAnActionAskedForWithATokenThatRanOutIsNotAdmittedAndRunsOnceItIsRenewed`).
  Not held: what a dial learns of its TLS is one record for all its
  connections, so one answered hides another's refusal; a refusal is not
  waited for though it may be a server's for a while (a front showing a
  certificate of its own making until its real one is loaded, or
  answering handshake_failure until it is ready), and the daemon is then
  refused at its start; a server that asks for a certificate of the
  daemon's and closes without an alert is waited on as not up; and Go's
  GODEBUG settings shape the dial's TLS, though not its authorities
  (`server/internal/exec/access`).
  Not served: a token told to a workspace's sandbox, which is still told
  of an unauthenticated grid alone; a certificate of the daemon's own; a
  server name other than the address's; a token the daemon renews itself;
  an asset service asked otherwise than its grid is; a token for a grid
  known only by the system's authorities; a bearer token that is no JSON
  Web Token, or a key sent in a header of a service's own.
- What deletes says, in its own code, why the thing is its own to delete.
- Whatever a goroutine uses is closed only after it has stopped; tests run
  with `-race`.

## Done means

- `mise run check` passes, and `mise run test` with the local stack up; say
  so where one could not be run, and why.
- The use case has a spec, seen failing before the code, with its control.
- What a change costs is measured; code on a path every build takes has a
  budget of time and allocations, held by `mise run check`: its
  allocations wherever its test runs, its time only by the gate's
  budget command, which runs nothing beside it
  (`server/testsupport/testbudget`).
- This file, the `AGENTS.md` of the part changed, `README.md` and
  `CONTRIBUTING.md` say what is now true.
- Someone who did not write it has reviewed it, and nothing they found is
  open. The answer says what was not done.

## Commands

```
mise install              # the pinned tools
mise run build            # Frontseat into dist/, and the box image, tagged frontseat-box:local too
mise start                # build, then bring the local stack up from this checkout, on its own box (never `mise up`)
mise stop                 # bring the local stack down, whichever checkout started it
mise run check            # what must always pass; needs no stack
mise run test             # everything, against the local stack
mise run go:test -- ARGS  # go test with ARGS, over the wasm modules of this checkout
mise run protos           # lint the contracts and regenerate their Go and Rust
mise run bench            # measure what is budgeted
mise run perf             # hold Frontseat to Bazel on the local grid
mise run compare          # time one workspace with Frontseat and the other build tools
mise run docs             # build the TechDocs site into site/
mise run docs:serve       # serve the TechDocs site on http://localhost:8000
```

## Shared libraries

| Where | What it holds |
|---|---|
| `sdk/go` | The Go SDK's one client (`frontseat.New`), and the one reader of a stream to its end (`Received`), of every page of a List (`Listed`), of a refusal's reason (`Reason`) and of a workspace's name (`IsWorkspaceName`) |
| `sdk/go/api` | The API's generated Go and Connect clients and handlers |
| `sdk/go/entity` | Entity references: the one parser and formatter, for a file (`String`), as typed (`Written`) and on a line (`Short`) |
| `sdk/go/bearer` | A token presented to its origin alone, following no redirect, and what an origin is (`Of`): the SDK's and the server's |
| `sdk/plugin` | The plugin SDK: contract types and their constructors, repository access and paths, strict readers, a refusal (`Error`) and its decoder, findings read from what a check printed; `examples/make.rs`, the plugin an author starts from |
| `server/git` | Git, run hardened over a checkout, and remotes read |
| `server/telemetry` | OpenTelemetry providers, propagation, logs, measurements |
| `server/internal/model`, `.../wire` | Build labels, coordinates; their protobuf conversion |
| `server/internal/pluginhost` | Wasm calls, read tracking, plugin-answer validation |
| `server/internal/httpserver` | A server command's HTTP listener and request lifetime |
| `server/internal/image` | An image's reference, read by Docker's own parser |
| `server/internal/testservice` | What a service for tests is held to, for the daemon and the runner |
| `server/internal/confine` | How a worker starts and confines an action (`frontseat-action`) |
| `server/internal/exec/here` | A command made ready to run where the daemon is, with the grid's tools: a fix, and what publishes |
| `server/internal/plain` | Text and names that every reader reads alike |
| `server/internal/secretfile` | A secret read from a file and a directory that are the server's alone |
| `server/internal/backstage` | The Backstage catalog the control plane resolves entities through |
| `server/internal/refusal` | A refusal by name: its code, `ErrorInfo` reason and the domain of each service |
| `server/internal/page` | A List cut into pages, by `page_size` and `page_token` |
| `server/internal/source/vcs` | What a workspace asks of a version control system |
| `server/internal/forge` | What publishing a release asks of a forge: its hosts and the requests of each |
| `local` | What the local stack is, read from its files: the workers' image and platform, what a sandbox is told; the checkout a program is started at (`Checkout`) |
| `local/ports` | Every port a box and the local stack are reached on |
| `server/testsupport/*` | Test facilities: pins, Wasm modules, the grid, fixtures (`testfiles`, `testforge`, `commits`), budgets |
| `server/testsupport/buildtest`, `server/testsupport/controlplanetest` | A daemon and a control plane started for a test, as `frontseatd` and `frontseat-local` start them |
| `server/testsupport/drivertest` | What any sandbox driver is tested with: the conformance, a running sandbox of the test's own, the driver that holds sandboxes in memory |
| `server/testsupport/testsandbox` | The sandbox API's client and a tunnel to a sandbox's port, for tests: the SDK has none |
| `server/testsupport/testwire` | A test's one way to a generated client over the wire, for what the SDK does not serve |
| `specs/cmd/gate`, `specs/cmd/prepare-modules` | The gate programs, and what builds the plugins for the tests and for `dist/` |
| `specs/architecture` | The architecture test: the repository's rules, its catalog, its imports and what is public |

A helper that is in two places is a defect: move it here and delete the
copy.
