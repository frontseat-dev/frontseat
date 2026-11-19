# Postgres plugin

Starts PostgreSQL as a service for the tests of a component that
depends on a Resource whose `spec.frontseat.service` says `as: postgres`.
With nothing more written, a test is given PostgreSQL 17 (the official
Alpine image, by digest), ready when `pg_isready` says so over the
network, reached at its endpoint `sql` (`{host}:{port.sql}`, in
`FRONTSEAT_ENDPOINTS`), and told how to reach it by the names every
PostgreSQL client reads: `PGHOST`, `PGPORT`, `PGUSER`, `PGPASSWORD` and
`PGDATABASE`.
A service runs as no root, so the server runs as the image's own
`postgres` user (70, on the Alpine image), writing its data and its
socket's directory (`/var/lib/postgresql`, `/var/run/postgresql`)
alone. Another `image` written is told its user by a `user` written
beside it; what it writes is the same on every official image.

Whatever is written beside `as` is kept, whole, and the rest is said to
fit it: another `image`; a `ports: {sql: …}` the server is then told to
listen on; an `env` whose `POSTGRES_USER`, `POSTGRES_PASSWORD` and
`POSTGRES_DB` readiness and the exports follow; `exports` of other names;
`endpoints` of other names or addresses. `exports` and `endpoints`
written replace the plugin's own, all of them: a test is then told and
given only what is written.

What cannot be made to fit is refused by name, since it would be a
server that never becomes ready: an `env` that gives no way in (no
`POSTGRES_PASSWORD`, or an empty one, unless `POSTGRES_HOST_AUTH_METHOD`
is `trust`, and then a test is told no `PGPASSWORD`); a password kept in
a file, which nothing can place yet; `PGPORT` in `env`, and a port said
among `args` in any spelling the server reads, since the port is said by
`ports` alone; and `args` beside a port other than 5432, since the
server is told its port by arguments, and those written are kept as
they are.
A key written to say nothing (`exports: {}`) is refused where the
catalog file is read.

It finds no projects and declares no actions.

Not implemented: a database or schema of each test's own on a shared
server, `init` scripts and a snapshot to start from.
