# Container plugin

Starts a service for tests as a container, exactly as a catalog entity
writes it under `spec.frontseat.service`, with `as: container`: the
image, its arguments, ports, environment, readiness, its endpoints and
what a test is told. It is the plugin for whatever no other plugin knows
how to start, and adds nothing of its own; the host holds what was
written to what every service must say.

It finds no projects and declares no actions.

## Options

None. The plugin is listed with nothing set, and an option written for
it is refused by name.

```yaml
# frontseat.yaml
plugins:
  container: {}
```

## Catalog keys

| Key | Read by | What it does |
|---|---|---|
| `spec.frontseat.service` with `as: container` | the host, which asks the plugin | The service, started as it is written: `image`, `args`, `env`, `ports`, `ready`, `timeout`, `user`, `writable`, `exports` and `endpoints`. The plugin gives none a default. |

```yaml
# catalog-info.yaml
kind: Resource
spec:
  frontseat:
    service:
      as: container
      image: docker.io/library/busybox:1.37@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec
      args: [nc, -lk, -p, "7000", -e, echo, hello]
      ports: {greet: 7000}
      ready: [{port: greet}]
      exports: {GREETER_ADDR: "{host}:{port.greet}"}
```
