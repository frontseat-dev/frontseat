# Configuration reference

Everything a repository writes for Frontseat is in two places:

| File | Where | What it says |
|---|---|---|
| `frontseat.yaml` | the repository's root | which plugins build the repository, and the options they take |
| `catalog-info.yaml` | any directory, any number | the Backstage entities of the repository; under `spec.frontseat`, what Frontseat is told of one entity |

Two more files are read where they are: `mise.toml` pins the tools that
`tools` name, and `.frontseatignore`, in gitignore syntax, names what
Frontseat does not see.

Both files are read strictly. A key that is not listed here, a key
written twice or a value of another type is a mistake, and so is an
anchor, an alias or a merge key in `frontseat.yaml` or under
`spec.frontseat`: it names the file and the line, nothing is built, and
nothing is guessed. Text is written as text: a value that YAML reads as
a number or a switch, such as `70` or `true`, is quoted where text is
asked for.

Every example on this page is read by Frontseat's own reader in its
tests, beside a `frontseat.yaml` that lists `go` and `backstage`, so
each is one that reader accepts. What a plugin's option takes is held
to what the plugin declares when the repository is built, which those
tests do not do; the tables of options and defaults are prose.

## `frontseat.yaml`

One YAML document with one key. A file that is empty, or `{}`, lists
no plugin.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `plugins` | map of plugin name to its options | none | The plugins that build the repository, in the order written. A plugin with no options is written `name: {}`, or `name:` with nothing after it. |
| `plugins.<name>.<option>` | as the plugin declares it | the plugin's | An option of that plugin, for the whole repository. |

```yaml
# frontseat.yaml
plugins:
  go:
    test-timeout: 5m
    test-shards: 4
    tags: [integration]
    version:
      - main.version=version
      - main.commit=commit
  cargo:
    wasm: true
  backstage: {}
```

A plugin that is not listed does nothing in the repository. An option a
plugin does not take is refused with the options it takes.

### What a plugin turns on

Some of what this page describes is read only where the plugin that
reads it is listed:

| Plugin | What listing it turns on |
|---|---|
| `backstage` | Catalog files: without it no `catalog-info.yaml` is read into entities. |
| `container` | A service written `as: container`. |
| `postgres` | A service written `as: postgres`. |
| `release` | Releases: a repository that does not list it releases nothing, and says so. Its strategies are the defaults of `versioning` and `branching`. |
| `go`, `cargo`, `npm`, `jest`, `buf`, `apko`, `github` | The projects and actions of their ecosystems, as each plugin's README says. |

### Plugin options

An option is text, a switch (`true` or `false`), a whole number or a
list of text. A list replaces the whole list it overrides.

| Plugin | Option | Type | Default | Meaning |
|---|---|---|---|---|
| `go` | `test-timeout` | text, a Go duration | `"10m"` | How long each test action may run, as the go command's `-timeout` reads it. |
| `go` | `test-shards` | whole number, 1 or more | `1` | Into how many actions a package's tests are cut at most. |
| `go` | `tags` | list of text | `[]` | The build tags a module is linted, compiled and tested with. |
| `go` | `version` | list of `<package>.<variable>=<what>` | `[]` | The string variables a program says its version by. `<what>` is `version`, `tag`, `commit` or `unit`, of the release the program is linked for. |
| `cargo` | `wasm` | switch | `false` | Builds each `cdylib` library and example to a `wasm32-wasip1` module too. |

The plugins `apko`, `backstage`, `buf`, `container`, `github`, `jest`,
`npm`, `postgres` and `release` take no option. Each plugin's README
says what it builds.

## `catalog-info.yaml`

A catalog file holds Backstage entities, one per YAML document. Every
`catalog-info.yaml` of the repository is read, at any depth, except
beneath a `testdata` directory. Backstage's own fields are Backstage's:
Frontseat reads the ones below and leaves the rest alone.

| Backstage field | What Frontseat does with it |
|---|---|
| `kind`, `metadata.name`, `metadata.namespace` | The entity's reference, read one way for every key of `spec.frontseat`. A kind Backstage defines is spelled as Backstage spells it (`Component`, `API`, `Resource`, `System`, `Domain`, `Group`, `User`, `Location`): `component` is refused. An entity that says `spec.frontseat` has a name. The namespace is `default` where none is written. |
| a Component's directory | A Component owns the projects beneath the directory of its catalog file. |
| `spec.system`, `spec.domain` | What an entity is released with (see `release`). |
| `spec.providesApis`, `spec.consumesApis` | A Component that consumes an API is given the Component that provides it as a service for its tests, where the provider has a `service`. |
| `spec.dependsOn` | Each entry names its kind: `component:<name>` or `resource:<name>`. A Component's dependencies on Components are kept in step with its source by Frontseat. A `resource:` entry, written by hand, gives the Component's tests that Resource's `service`. |

A reference is written as Backstage writes one,
`[<kind>:][<namespace>/]<name>`, and is read in the namespace of the
entity that writes it.

### `spec.frontseat`

A map of the keys below, written directly: not through an anchor, an
alias or a merge key.

| Key | Said by | Type | What it says |
|---|---|---|---|
| `plugins` | Component | map | Plugin options for the Component's directory. |
| `platforms` | Component | list | The platforms what is built beneath the directory ships for. |
| `test` | Component | map | What the Component's tests are given beyond their own package. |
| `generated` | Component | list | Which Component generates a directory of this one. |
| `actions` | any entity | map | Actions written by hand, for the catalog file's directory. |
| `service` | Resource, Component | map | How the entity is started beside a test that depends on it. |
| `release` | Domain, System, Component | map | How the entity is versioned and tagged. |

### What a value reaches

A key is inherited in one of three ways, and in no other.

| Rule | Keys | How it holds |
|---|---|---|
| By directory | `plugins`, `platforms` | The value holds for the catalog file's directory and every directory beneath it. The nearest file above a directory wins, whole: a nearer `platforms` replaces the list, and a nearer option replaces that option. For an option, the order is the Component's catalog file, then `frontseat.yaml`, then the plugin's default. It is said once for a directory. |
| By group | `release`, except `grouping` | A Component takes each setting it does not say from its System, then from the System's Domain, then the default. `grouping` is each entity's own. |
| Not inherited | `test`, `generated`, `actions`, `service`, `release.grouping` | The value is the entity's alone. |

### `plugins`

The options of `frontseat.yaml`'s `plugins`, set for one Component's
directory. The plugin is one the repository lists.

```yaml
# gateway/catalog-info.yaml
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: gateway
spec:
  type: service
  lifecycle: production
  owner: team
  frontseat:
    plugins:
      go:
        test-shards: 8
        tags: [integration, e2e]
```

### `platforms`

A list of `<os>/<arch>`, in small letters and digits, each once.
Default: none. An empty list is a value: what is beneath ships for no
platform, whatever a directory above says.

```yaml
# ctl/catalog-info.yaml
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: ctl
spec:
  type: tool
  lifecycle: production
  owner: team
  frontseat:
    platforms: [linux/amd64, linux/arm64, darwin/arm64]
```

### `test`

A test is given its package's directory and what its project depends
on. This adds to it, for every test of the Component.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `inputs` | list of text | `[]` | What the tests read besides. Each entry is one of the forms below. |
| `tools` | list of text | `[]` | Tools the tests run with, each by the name `mise.toml` pins it under. |
| `environment` | map of name to text | `{}` | Variables the tests are started with. A name is letters, digits and underscores, beginning with no digit. A variable the test's own command sets is refused. |

An entry of `inputs`, and of an action's `inputs`:

| Written | Meaning |
|---|---|
| `fixtures/**` | Files matching the pattern, from the catalog file's directory. |
| `/api/**` | Files matching the pattern, from the repository's root. |
| `!fixtures/tmp/**`, `!/api/tmp/**` | Files that are left out again. |
| `:go.compile` | What an action of this directory left, by its name. |
| `//plugins/go:cargo.wasm` | What an action of another directory left: the directory from the repository's root, then the action's name. |

```yaml
# ledger/catalog-info.yaml
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: ledger
spec:
  type: service
  lifecycle: production
  owner: team
  frontseat:
    test:
      inputs:
        - fixtures/**
        - /api/**
        - "!/api/tmp/**"
        - //plugins/go:cargo.wasm
      tools: [buf]
      environment:
        LOG_LEVEL: debug
        RETRIES: "3"
```

### `generated`

A generator writes into a directory outside its own project only where
the Component that owns the directory says so. A list, never empty, of:

| Key | Type | Meaning |
|---|---|---|
| `directory` | text, required | A directory beneath the catalog file, written plainly: no `..`, no leading or trailing slash. Each directory is said once. |
| `by` | reference, required | The Component whose project generates it. The kind is `component` where none is written. |

```yaml
# library/catalog-info.yaml
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: library
spec:
  type: library
  lifecycle: production
  owner: team
  frontseat:
    generated:
      - directory: api
        by: component:contracts
```

### `actions`

The one place a command is written by hand. A map of action name to
action; a name is lower-case letters, digits and dashes. An action
`lint` in `handbook/` is `//handbook:custom.lint`.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `command` | list of text, required | | The program and its arguments, run in the catalog file's directory. |
| `description` | text | | What the action does, for whoever reads the file. |
| `tools` | list of text | `[]` | Tools it runs with, each by the name `mise.toml` pins it under. |
| `inputs` | list of text | `[]` | What it reads, in the forms `test.inputs` takes. |
| `outputs` | list of text | `[]` | What it writes, relative to the directory. A directory ends in `/`. |
| `check` | switch | `false` | It checks the source. |
| `fix` | list of text | | The command that puts right what the check finds. Only with `check`, and with `rewrites`. |
| `rewrites` | list of text | `[]` | Patterns of the inputs `fix` may change. Only with `fix`. |
| `program` | switch | `false` | It is a program that keeps running, started by `frontseat run`. It is named `custom.run/<name>`. |
| `versioned` | switch | `false` | It is given the version of the release it is built for, where its command names it. |
| `package` | switch | `false` | It makes what the entity ships: each file it leaves is an artifact of a release. It says its `outputs`. |
| `publish` | switch | `false` | It publishes what a release shipped. It is named `custom.publish/<name>`, says no `inputs`, and is none of the other kinds. |
| `inherit-environment` | list of text | `[]` | Names of variables of where the daemon runs that the action is given, their values as the daemon has them. Only with `publish`. |

```yaml
# handbook/catalog-info.yaml
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: handbook
spec:
  type: documentation
  lifecycle: production
  owner: team
  frontseat:
    actions:
      lint:
        description: Checks the Markdown.
        check: true
        command: [markdownlint, "**/*.md"]
        tools: [markdownlint]
        inputs: ["**/*.md"]
        fix: [markdownlint, --fix, "**/*.md"]
        rewrites: ["**/*.md"]
      site:
        command: [mkdocs, build, --site-dir, site]
        tools: [mkdocs]
        inputs: ["**/*.md", mkdocs.yml]
        outputs: [site/]
      preview:
        program: true
        command: [mkdocs, serve]
        tools: [mkdocs]
      announce:
        publish: true
        command: [gh, release, create]
        tools: [gh]
        inherit-environment: [GH_TOKEN]
```

### `service`

How an entity is started beside the tests of the Components that
depend on it. A Resource is started as what a plugin starts (`as`); a
Component is started as one of its own programs (`program`). Exactly
one of the two is written.

| Key | Said by | Type | Default | Meaning |
|---|---|---|---|---|
| `as` | Resource | text | | What starts it, a name a listed plugin claims: `container` for the image written here, or `postgres`. The plugin says every key that is left out. |
| `program` | Component, Resource | text | | The program action that is the service, as `:go.run/orders`. It takes no `image` and no `arguments`. |
| `image` | Resource | text | the plugin's | The image, named with its registry and digest. |
| `arguments` | Resource | list of text | the plugin's | The image's arguments. `{host}`, `{port.<name>}` (a port of its own) and `{<resource>.port.<name>}` (a port of a Resource it depends on, by that Resource's name) are filled in. |
| `environment` | both | map of name to text | `{}` | Variables it is started with, filled in as `arguments` is. |
| `ports` | both | map of name to whole number | the plugin's | The ports it listens on, 1 to 65535, each under a name of lower-case letters, digits and dashes. At least one. |
| `ready` | both | list of checks | the plugin's | When it is ready. At least one check, each one of `port: <name>`, `http: {port, path, body}` (`body` a regular expression) or `command: [...]`, whose arguments are filled in as `arguments` is. |
| `ready-timeout` | both | a length of time, as Go writes one (`90s`, `2m`, `1h`) | `2m` | How long it is given to be ready. In whole seconds, more than zero, at most `1h`. |
| `exports` | Resource | map of name to text | the plugin's | Variables each test given the service is told, with `{host}` and `{port.<name>}` filled in. |
| `apis` | Component | map of API to `{port, exports}` | | For each API the Component provides, written as a reference is (`orders-api`, `api:orders-api` or `api:<namespace>/orders-api`; the kind is `api` where none is written): the port it is served on, and the variables a test that consumes that API is told. |
| `endpoints` | both | map of name to text | `{}` | Addresses a test reads from `FRONTSEAT_ENDPOINTS`, each `{host}` with one `{port.<name>}`. That variable is a JSON object of each service the test is given, by its entity reference, holding its endpoints filled in: `{"resource:default/cache": {"redis": "127.0.0.1:41001"}}`. |
| `user` | both | text | the sandbox's own | The user it runs as, by number and quoted, never `"0"`. |
| `writable` | both | list of text | `[]` | The directories of its image it writes, each an absolute path, at most 32. |

A key is written with a value or left out: an empty value and a null
are refused. The image below is named as an image must be, by its
registry and digest; the digest is an example's, and a copy names its
own image.

```yaml
# cache/catalog-info.yaml
apiVersion: backstage.io/v1alpha1
kind: Resource
metadata:
  name: cache
spec:
  type: database
  owner: team
  frontseat:
    service:
      as: container
      image: registry.example.com/team/redis@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec
      arguments: [--port, "{port.redis}"]
      environment: {MODE: test}
      ports: {redis: 6379}
      ready:
        - port: redis
        - command: [redis-cli, ping]
      ready-timeout: 1m
      exports: {REDIS_ADDR: "{host}:{port.redis}"}
      endpoints: {redis: "{host}:{port.redis}"}
      user: "999"
      writable: [/data]
---
apiVersion: backstage.io/v1alpha1
kind: Resource
metadata:
  name: db
spec:
  type: database
  owner: team
  frontseat:
    service:
      as: postgres
```

```yaml
# orders/catalog-info.yaml
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: orders
spec:
  type: service
  lifecycle: production
  owner: team
  providesApis: [orders-api]
  dependsOn: [resource:db]
  frontseat:
    service:
      program: ":go.run/orders"
      ports: {http: 8080}
      ready:
        - http: {port: http, path: /healthz, body: ok}
      apis:
        orders-api:
          port: http
          exports: {ORDERS_URL: "http://{host}:{port.http}"}
```

### `release`

A unit gets one version and one tag, where the repository lists the
`release` plugin. A Component is a unit alone, or
is released with its System or its Domain where that group is synced.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `grouping` | `synced` or `independent` | a System is synced; a Domain is not; a Component follows its System | Whether the members of a group release at one version (`synced`) or each alone. A Component of a synced System says `independent` to release alone. |
| `tag` | text | `{unit}/v{version}` | How the tag is spelled: `{version}` once, and `{unit}` where the unit's name is wanted. |
| `initial-version` | text, `MAJOR.MINOR.PATCH` | `0.1.0` | The version a unit with no release starts from. |
| `versioning` | text, `<plugin>:<name>` | `release:conventional-commits` | The strategy that says the next version. |
| `branching` | text, `<plugin>:<name>` | `release:release-flow` | The strategy that says which branches release what. |
| `prerelease` | map of `format` and `tagged` | | How a prerelease is released, where a branch releases one. |
| `prerelease.format` | text | `{branch}-{timestamp}` | How a prerelease is spelled after the version's dash, from `{branch}`, `{timestamp}`, `{epoch}` and `{sha}`. |
| `prerelease.tagged` | switch | `false` | Whether a prerelease is tagged. |

A member of a synced group says nothing but `grouping`: the rest is the
group's to say.

```yaml
# catalog-info.yaml
apiVersion: backstage.io/v1alpha1
kind: Domain
metadata:
  name: shop
spec:
  owner: team
  frontseat:
    release:
      grouping: synced
      tag: "{unit}@{version}"
      initial-version: 1.0.0
      versioning: release:conventional-commits
      branching: release:release-flow
      prerelease:
        format: "{branch}-{sha}"
        tagged: true
---
apiVersion: backstage.io/v1alpha1
kind: System
metadata:
  name: tools
spec:
  owner: team
  frontseat:
    release:
      grouping: independent
```
