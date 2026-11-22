# Backstage plugin

The Rust plugin discovers every document in `catalog-info.yaml` through the
SDK's tracked, bounded YAML reader. `Analyze` returns catalog entities separately
from build projects. A component governs its descriptor's directory; organizational
and API entities acquire no source roots merely by sharing that file.

`catalog.rs` interprets the descriptor and built-in specification fields,
`metadata.rs` validates standard metadata, and `relations.rs` maps built-in fields
to Backstage relations and inverse names. Extension fields remain allowed.
Component, API, Resource, System, Domain, Group and User use Backstage's built-in
relation mappings; other kinds contribute their identity without invented relations.
A Location is held to Backstage's schema (a specification, whose `type` and
`target` are nonempty text, `targets` a list of them and `presence` required or
optional) and is otherwise kept as written, with no source and no relation: its
targets are Backstage's to read. Frontseat finds catalog files by their name and
follows no Location. This is catalog discovery, not a Backstage processor runtime:
Location targets, `$text`/`$json` substitutions and scaffolding are not executed
here. This repository's APIs name their definitions as paths, from the catalog
file's directory, and not as `$text` substitutions, since frontseat reads none:
Backstage shows such a definition as the text of the path, not the document it
names. The architecture test holds each path to what is in the repository.

The host validates all answers, refuses duplicate identities and ambiguous source
ownership, and assembles the graph. References are canonical and namespace relative.
External targets remain visible. Component `dependsOn` and `dependencyOf` are held
to source inference; authored catalog fields cannot create build edges. When a file
is out of step, the host asks the plugin what it should say (`Reconcile`), with the
components the source says each entity depends on and is depended on by. The
plugin answers edits: `spec.dependsOn` of the document that describes the entity,
and `spec.dependencyOf` only where the file states it. An entry that is no
component stays where it was, a component the source still names stays as it was
written, and the rest is added in canonical order. The host writes the edits
keeping comments and every other field, and refuses them where the plugin's own
reading of the result disagrees with the source.

`Arrange` has no work for catalog discovery. The SDK accepts an empty request and
refuses invented handed actions. The common real-Wasm harness covers the same
description, options, profiles, errors, tracked reads, cancellation, repetition and
concurrency checks used for Go. API specs live in
[`specs/build/catalog_test.go`](../../specs/build/catalog_test.go).

General plugin rule/rewrite results, CODEOWNERS inference and remote catalog
processing remain separate implementation work.

## Options

None. The plugin is listed with nothing set, and an option written for
it is refused by name.

```yaml
# frontseat.yaml
plugins:
  backstage: {}
```

## Catalog keys

The plugin reads each document of a `catalog-info.yaml` as Backstage
writes one, and nothing beneath `spec.frontseat`, which is the host's.

| Key | Read by | What it does |
|---|---|---|
| `apiVersion`, `kind` | the plugin | What the document is: a built-in kind is held to `backstage.io/v1alpha1` or `backstage.io/v1beta1`. |
| `metadata.name`, `metadata.namespace` | the plugin | The entity's identity; a namespace left out is `default`, as it is Backstage's. |
| `metadata` (`title`, `description`, `labels`, `annotations`, `tags`, `links`) | the plugin | Held to the shape Backstage gives each, and otherwise kept as written. |
| `spec.type`, `spec.lifecycle`, `spec.owner`, `spec.definition` | the plugin | Required of the kinds Backstage requires them of. |
| `spec.owner`, `spec.system`, `spec.domain`, `spec.subdomainOf`, `spec.subcomponentOf`, `spec.providesApis`, `spec.consumesApis`, `spec.dependsOn`, `spec.dependencyOf`, `spec.parent`, `spec.children`, `spec.members`, `spec.memberOf` | the plugin | The entity's relations, each with its inverse, as Backstage maps them. `dependsOn` and `dependencyOf` between Components are held to what the source says. |

```yaml
# catalog-info.yaml
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: greet
spec:
  type: service
  lifecycle: production
  owner: team-a
  system: examples
```
