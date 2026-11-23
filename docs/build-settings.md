# What a build daemon is told

`frontseat build`, `test`, `run` and `query` are served by a build
daemon, which the command starts for the directory it is run in. The
daemon knows that directory and one file of settings: its plugins, the
grid its actions run on, the platform of the grid's workers, and what a
publish may be given.

| Where | The file |
|---|---|
| in a workspace's sandbox | `/var/lib/frontseat/build.yaml`, written by the sandbox's supervisor from what the control plane told it |
| anywhere else: a machine, a job | `build.yaml` in `$XDG_CONFIG_HOME/frontseat` (else `~/.config/frontseat`) |

```yaml
version: 1
# A directory: every <name>.wasm in it is loaded.
plugins: /opt/frontseat/plugins
grid:
  address: grid.example:8980
  instance: frontseat/p/b
  # The grid's asset service, which fetches what a build downloads.
  assets: assets.example:8983
  # A PEM file of the authorities the grid is known by; with it the
  # grid is asked over TLS. `tls: true` asks over TLS by the system's.
  authorities: /etc/fleet-ca/ca.crt
  # A file holding the token the grid admits the daemon by, read again
  # for every call. It is given with authorities.
  token: /run/secrets/grid-token
  # Which proxy of the grid's stands in for a public site a download
  # names: the first that matches, in the order given.
  rewrites:
    - from: '^https://static\.crates\.io/crates/([^/]+)/([^/]+)/download$'
      to: 'http://mirror:8080/cargo/$1/$2/download'
# What the grid's workers are: every action asks for these.
platform:
  OSFamily: linux
  ISA: x86-64
  container-image: docker://frontseat.dev/workers/v1
publish:
  # Variables of where the daemon runs that what publishes a release
  # may be given, by name.
  environment: [GH_TOKEN]
```

Only `version`, `plugins` and `grid.address` must be said. Told no
platform, the daemon asks for Linux on its machine's architecture; told
nothing of TLS, it asks the grid in the clear, as the local stack's is
asked.

The file is read strictly when the daemon starts: a key that is not
listed here, another version, a rewrite that is no regular expression or
plugins that are not there is a mistake, and nothing is built. A path is
from the file's own directory. The file holds no secret: a token is the
path of its file. A daemon whose file has changed is started again by
the next command.
