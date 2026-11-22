# apko plugin

Builds OCI images with [apko](https://github.com/chainguard-dev/apko) from a
configuration and the lock apko wrote of it. The Rust plugin uses the shared SDK
for strict JSON and YAML, source tracking, errors and the plugin ABI. `lock.rs`
reads `apko.lock.json`; `config.rs` holds the configuration to what is built;
`actions.rs` declares the fetches and the builds; `plugin.rs` implements
discovery. The common real-Wasm conformance harness uses independent fixtures
under `testdata/`.

## What it builds

An image is found by its lock: a directory that holds `apko.lock.json` is a
project of kind `apko-image`, and the configuration is the file beside the lock
that the lock says it was written of. A configuration with no lock is not built;
lock it with `apko lock` and commit the lock.

For each architecture the lock holds packages of there is one action,
`image-<architecture>` (`image-aarch64`, `image-x86-64`), which runs

```
apko build --offline --lockfile apko.lock.json --vcs=false --sbom=false \
    --arch <architecture> -- <configuration> image:latest oci/<architecture>
```

with the apko the repository pins in mise, and leaves the image as an OCI
layout in `oci/<architecture>/` (`index.json`, `oci-layout`, `blobs/`). apko
holds the configuration to the checksum the lock has of it, so a configuration
changed since it was locked fails the build.

## How packages are fetched

An apk lock holds no checksum of a whole package: it holds a sha1 of the
package's control section, a sha256 of its data section, and where each lies in
the file. So each package of each architecture is a shared fetch of its own,
`fetch/<host>/<path>`, that runs no tool. The grid's asset service downloads
the package through the proxy, unchecked; the fetch, on a worker with no
network, cuts the two sections out by the lock's ranges, checks each against
the lock's checksum, and keeps them where apko's cache reads them
(`~/.cache/dev.chainguard.go-apk/<repository>/<architecture>/<package>/`),
each named by its checksum in hex (`Download.parts` in the plugin contract). Both
sections are checked before either is kept; one that is not what the lock says
fails the fetch, which leaves nothing, and nothing is built with it. apko
checks each control section against the lock and each data
section against its control section again when it builds. The signature section
is not kept: apko reads none when it installs from a lock.

A signing key is in the lock whole, so its fetch downloads nothing and writes
the key where apko's cache reads it when it starts offline.

rules_apko fetches each section by an HTTP range request. The grid's proxy did
not serve ranges when this was written, so the package is fetched whole and cut
on the worker instead; it is one download for a package where ranges are two.

## Options

None. The plugin is listed with nothing set, and an option written for
it is refused by name. The architectures built are the
ones the lock holds packages of.

```yaml
# frontseat.yaml
plugins:
  apko: {}
```

## Catalog keys

None: nothing a catalog file says reaches the plugin.

## Not served yet, each refused by name

What a configuration says that is valid and not built yet is refused
`UNIMPLEMENTED`, each by a reason of its own:

- `include`: a configuration in more than one file (`include-not-served`).
- `contents.baseimage` (`base-image-not-served`).
- A repository that is a directory (`local-repository-not-served`), or a key
  that is a file (`local-key-not-served`), as images built from local melange
  packages use. Only `https` repositories and keys are read.

What follows is refused as a lock that is not as apko writes one
(`invalid-lock`):

- A package or key address that is not plain: another scheme, a port, a query,
  an escape, a step with anything but letters, digits and `._+-@`; a key not
  named `*.rsa.pub`, or at its host's root.
- An architecture apko does not write in a lock (`aarch64`, `x86_64`, `armv7`,
  `armhf`, `x86`, `ppc64le`, `riscv64`, `s390x`, `loongarch64`).
- A configuration whose file name holds anything but letters, digits and
  `._+-`, or begins with `-`.

The configuration is the file beside the lock, whatever directories the lock
names before it: `apko lock images/app/apko.yaml`, run from the repository's
root, writes a lock that says `images/app/apko.yaml`, and the configuration
read is `apko.yaml` in the lock's directory.

## What is not proven, or not built

- Only Wolfi (`packages.wolfi.dev`) is built on the local grid, whose proxy
  stands in for no other package repository; a lock of Alpine's or Chainguard's
  packages is read, and its fetches are refused where the grid has no proxy
  for them.
- apko finds further signing keys for Alpine and Chainguard repositories when
  it runs with a network, and puts them in the image. Offline it cannot, by its
  source (apko 26953696, `pkg/apk/apk/implementation.go`), so such an image
  would be built without them. Not tried.
- The asset service keeps a package it fetched by its address, and the proxy
  keeps it too. One that fails its check fails it until one of them lets it
  go; nothing here asks either to.
- One multi-architecture index over the per-architecture layouts, a bill of
  materials, and publishing; an image is not yet something a service can
  start from or `ship` can publish.
- This repository holds no apko image of its own: the plugin is proven on its
  fixture, the smallest image Wolfi makes, whose packages must stay published
  for the acceptance spec to fetch them on a grid that has not kept them.
