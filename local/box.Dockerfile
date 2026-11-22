# The box: what a sandbox that holds a daemon runs. It is the sandbox
# image, which frontseat-images builds with apko and which a worker runs
# too, with frontseat laid on top: the daemon, the client and the
# plugins. It holds no toolchain: the tools of a build are in the roots
# of its actions.
#
# The context holds frontseatd, frontseat, frontseat-runner (the runner
# of the workers that run tests with services), frontseat-local (the
# control plane of the local stack) and frontseat-action (what a worker
# runs every action by, which the stack installs where each runner
# sandbox holds it) built for Linux, and plugins/. Nothing is fetched while the image is built.
ARG SANDBOX_IMAGE
FROM ${SANDBOX_IMAGE}

COPY --chmod=0755 frontseatd frontseat frontseat-runner frontseat-local frontseat-action /opt/frontseat/bin/
COPY plugins /opt/frontseat/plugins

ENV PATH=/opt/frontseat/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

# What runs in a box is not root: the daemon needs nothing root has, and
# a sandbox is given no device and no capability. Its workspace and what
# it keeps (its state, /var/lib/frontseat) are its user's own.
USER 0
RUN mkdir -p /work /var/lib/frontseat && chown -R 65532:65532 /work /var/lib/frontseat
USER 65532:65532
WORKDIR /work
# What a box runs: the daemon, in its workspace, with every plugin the
# image holds, on a box's ports (its build API, its source API, which
# the control plane alone dials, and its shell) (local/ports, which local/ports_test.go
# holds this to) and keeping its state where frontseatd says by default.
# What the sandbox is told beside, the workspace's name, the key let
# into its shell and the grid it builds on, are the arguments it is
# started with, which follow these: its image's ENTRYPOINT is the daemon.
ENTRYPOINT ["/usr/sbin/tini", "--", "/opt/frontseat/bin/frontseatd", \
  "--listen", "0.0.0.0:7600", "--source", "0.0.0.0:7601", "--shell", "0.0.0.0:2222", \
  "--workspace", "/work", "--plugins", "/opt/frontseat/plugins"]
