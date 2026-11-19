# Security Policy

## Supported Versions

Frontseat is in preview. Only the `main` branch is supported, and fixes
land there.

## Reporting a Vulnerability

Please do not report security issues in public issues, pull requests or
discussions. Report them privately through this repository's
[GitHub security advisories](https://github.com/frontseat-dev/frontseat/security/advisories/new).

Include:

* a description of the issue and its impact,
* the steps to reproduce it, ideally with a small repository or command,
* the revision you tested, your host architecture and container runtime,
* and, if known, mitigations.

Remove tokens, credentials and private source from anything you share.
If the issue is confirmed, it is fixed on `main` and disclosed through a
GitHub security advisory.

## Scope

Frontseat confines what it builds: each action runs in an OpenShell
sandbox of the grid with only its declared inputs, confined within it to
its own directory; only fetches reach a network, and only the hosts the
grid lets them reach; and credentials are kept outside sandboxes. A way
around any of these is a vulnerability.

Not held yet, and known: an action can signal the runner of the sandbox it
runs in, which the grid then continues or makes anew; one that stops it
again and again holds its slot, and the network an egress slot reaches,
past the action's timeout, until the stack is started again; a test
with services can reach its own sandbox's runner on Linux before 7.1,
which runs nothing for it while its own run lasts; actions that run one after another in a sandbox share its
loopback; a download's host name that resolves to an address of the
grid's own, or redirects there, is fetched by the grid's asset service.

The local stack (`frontseat-local up`) is one container for a trusted
development machine. Its grid asks no one who they are and listens on the
loopback alone. That it is unauthenticated is not a vulnerability; a way to
reach it from another machine or from inside a sandbox is.
