# Crossplane plugin

Builds [Crossplane](https://www.crossplane.io) Configurations into packages
with the Crossplane command line, and validates each Configuration's examples
against its own definitions. The Rust plugin uses the shared SDK for strict
YAML, source tracking, errors and the plugin ABI. `manifest.rs` reads a
package's metadata; `actions.rs` declares the actions; `validated.rs` reads
what the check found; `plugin.rs` implements discovery. The common real-Wasm
conformance harness uses independent fixtures under `testdata/`.

## What it builds

A directory that holds `crossplane.yaml`, a package's metadata
(`apiVersion: meta.pkg.crossplane.io/<version>`) of `kind: Configuration`, is
a project of kind `crossplane-configuration`, named as its `metadata.name`
says. A file of that name that is metadata of no package, another tool's, is
no project. Metadata in a file of another name (`crossplane.yml`), which
`crossplane xpkg build` would find by its kind, is not looked for. A package
beneath another is a project of its own, and no file of it is read by the
outer one's actions.

It has two actions, each run with the `crossplane` the repository pins in
mise:

- `package`, which runs

  ```
  crossplane xpkg build --package-root . --examples-root ./examples \
      --ignore catalog-info.yaml --package-file <name>.xpkg
  ```

  over every YAML file of the Configuration, its examples among them, and
  leaves `<name>.xpkg` at its root. It is what the project ships (the
  contract's action kind `Package`): a release lists the file as one of its
  artifacts. Crossplane writes the same bytes for the same objects, whenever
  the files were written and whatever their comments say; it reads nothing
  else, writes nothing but the package and reaches nothing. A Component's
  catalog file beside the package is no object of it, and is not read; nor,
  for a Configuration at the repository's root, is `frontseat.yaml`. Any other
  YAML beneath the Configuration that is no Crossplane object (a workflow of
  `.github/`, a kustomization) fails the package, as it fails
  `crossplane xpkg build`.
- `validate`, a check, which runs

  ```
  crossplane resource validate --crossplane-image= --output json -- \
      ./<definition>,./<definition>... ./examples
  ```

  and holds every example to the definitions of the Configuration (every YAML
  file that is neither the metadata nor what xpkg takes for examples: every
  path that begins `examples`, as `examples-old.yaml`). It is given no schema of
  Crossplane's own (`--crossplane-image=`), which the tool would otherwise
  download from a floating tag, and not the package's metadata, whose
  dependencies it would download: it reaches nothing, and keeps nothing
  beyond its own home. Each thing wrong with an example is a finding
  (`crossplane/validate`) at the file of the example that holds the resource,
  at the line its document begins: the tool names a resource, by its
  apiVersion, kind, namespace and name, and not where it read it, so the
  plugin looks for it in the examples, file by file and document by document.
  One that is in no example is said of `crossplane.yaml`. There is no check
  where the Configuration has no example, or no definition.

## Options

None. The plugin is listed with nothing set, and an option written for
it is refused by name.

```yaml
# frontseat.yaml
plugins:
  crossplane: {}
```

## Catalog keys

None: nothing a catalog file says reaches the plugin.

## Refused by name

- Package metadata (`meta.pkg.crossplane.io`) of a kind that is no
  package's, or of a Configuration that names no package or names one by
  what is no name Kubernetes takes (or of more than 250 characters, so that
  `<name>.xpkg` is a file name), is refused as a manifest that is wrong
  (`invalid-manifest`), saying which field, and not what it holds. Crossplane itself builds a Configuration of no name;
  the plugin names its package for it.
- A Function's and a Provider's metadata are valid, and refused as not built
  yet (`functions-not-served`, `providers-not-served`): each package embeds
  the image it runs, which its metadata does not name and no action of the
  build makes.
- A definition whose file name holds a comma (`invalid-file-name`): the tool
  reads its definitions as one argument, the files apart by commas.
- A repository that pins no `crossplane` (`unpinned-tool`).

## What is not proven, or not built

- Where a finding's document begins is read by the plugin's own splitting of
  the file at `---`, beside the SDK's reading of its documents; where the two
  count otherwise (a `%YAML` directive), the finding is at the file, line 0.
  An example the SDK's strict reader refuses holds no resource the plugin
  finds, and what validate found in it is said of `crossplane.yaml`. The SDK
  says no line of a document it reads.

- An example of a kind the Configuration does not define, a provider's
  managed resource or one of Crossplane's own, is said to have no schema, and
  passes: the schemas of the Configuration's dependencies (`spec.dependsOn`)
  and of Crossplane are OCI images, which only a fetch from a registry
  brings, and the contract fetches nothing from a registry.
- `crossplane composition render`, which runs each function of a pipeline in
  a container: an action's sandbox has no container runtime. Nothing here
  declares it.
- Publishing: `crossplane xpkg push` of what a release shipped. A publish is
  given every artifact of its unit, and a unit of more than one package would
  push each package's to the others' repositories.
- Dependencies between Configurations of the workspace (`spec.dependsOn`) are
  not edges of the build: a Configuration is built from its own files alone.
- `crossplane-project.yaml` (`crossplane project build`), which reaches a
  registry for every dependency and a container runtime for most schema
  languages, is not read.
- The `crossplane` the repository pins is fetched from cli.crossplane.io,
  which publishes no checksum: the lock holds the one mise computed when it
  locked it.
