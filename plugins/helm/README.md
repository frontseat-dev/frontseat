# Helm plugin

A directory that holds a `Chart.yaml` is a Helm chart, named as its
`Chart.yaml` names it (a project of the kind `helm-chart`). A chart
beneath another chart's `charts/` is part of that chart, as Helm reads
it, and no project of its own; a directory named `charts` that no chart
holds is any directory.

```yaml
# frontseat.yaml
plugins:
  helm: {}
```

```toml
# mise.toml
[tools]
helm = "4.3.1"
```

## Actions

| Action | Kind | Command | Reads |
|---|---|---|---|
| `lint` | check | `helm lint --strict .` | the chart's files, and what it depends on |
| `template` | check | `helm template .` | the same |

`helm lint` is a check: each error and warning it prints (`--strict`
fails on a warning) is a finding (`helm/lint`). Helm names a template by
the chart's name, `[ERROR] templates/: parse error at
(web/templates/a.yaml:2): ...`, and the finding is at that file and line
of the chart's directory, wherever it is in the repository; a finding of
a file Helm names without a line is at the file, and one of the chart's
directory at its `Chart.yaml`. Information (`[INFO]`) is no finding.

`helm template` renders the chart as an install would, with no cluster,
and fails where it cannot; what it could not render is a finding
(`helm/template`) at the template it names. Its output is not checked
against Kubernetes' schemas: kubeconform needs the schemas, which are
fetched from the network, and is not run yet.

## Dependencies

Helm reads a chart's dependencies from `Chart.yaml`, or from
`requirements.yaml` where that says some (null there says none), and its
lock from `Chart.lock`, or from `requirements.lock` where that is there,
as Helm's loader reads them; a file that holds no document is read as
empty, as Helm reads it.

A chart is provided by its directory (`helm:<directory>`, `helm:.` at the
repository's root), not its name: two charts of one name, in two
directories, are two charts.

- **None**: the chart is served.
- **In the chart's own `charts/`** (a dependency with no repository, a
  `file://` directory beneath `charts/`, or one fetched from outside
  whose chart the checkout holds there already, as `charts/<name>/` or
  `charts/<name>-<version>.tgz`): they are the chart's files, and served.
  A `file://` directory within the chart and not beneath its `charts/` is
  refused (`invalid-manifest`): Helm looks for a dependency in `charts/`
  alone.
- **Another chart of the repository, by `file://`**: served. The chart
  requires it (by its directory, at the line that names it, at the
  version the lock resolved it to), and its lint and template are handed
  that chart's files into `charts/<name>/`, where Helm looks for it; what
  the checkout holds there of it, a copy or the archive `helm dependency
  build` leaves (`charts/<name>-<version>.tgz`), is not read. A finding of
  its templates, which Helm names by the dependency's alias where it has
  one, is at its own directory. The directory must hold a chart of the
  name the dependency says, and that chart may depend on nothing outside
  its own directory (`transitive-dependencies-not-served`): what it
  depends on would be handed beside it, where Helm does not look.
- **Fetched from outside** (`oci://`, an `https` or `http` repository, a
  repository's alias) and not held in `charts/`: refused by name,
  `remote-dependencies-not-served`, since a chart is not fetched yet.

Not held: the lock's digest of the dependencies is not held to
`Chart.yaml`, as Helm holds it only when it builds them (`helm dependency
build`); a `name` that `requirements.yaml` gives the chart is not read; a
chart held in `charts/` is known by its file's name, not by the name
inside it; two repositories of a workspace with a chart in the same
directory provide one coordinate twice, and the workspace is refused.

Not served yet: packaging (`helm package`) and publishing (`helm push`),
fetching a remote dependency, and checking a render against Kubernetes'
schemas.

## Options

None. The plugin is listed with nothing set, and an option written for
it is refused by name.
