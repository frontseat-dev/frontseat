# The box: what a workspace's sandbox runs. It is the sandbox image,
# which frontseat-images builds with apko and which a worker runs too,
# with frontseat laid on top: the box's supervisor, the command line,
# which is the build daemon it starts too, and the plugins. It holds no
# toolchain: the tools of a build are in the roots of its actions.
#
# The context holds frontseat-box (the supervisor), frontseat,
# frontseat-runner (the runner of the workers that run tests with
# services), frontseat-server (the control plane of the local stack),
# frontseat-stack (what readies the stack's container as its first
# process) and frontseat-confine (what a worker runs every action by,
# which the stack installs where each runner sandbox holds it) built for
# Linux, and plugins/. Nothing is fetched while the image is built.
ARG SANDBOX_IMAGE
FROM ${SANDBOX_IMAGE}

COPY --chmod=0755 frontseat-box frontseat frontseat-runner frontseat-server frontseat-stack frontseat-confine /opt/frontseat/bin/
COPY plugins /opt/frontseat/plugins

ENV PATH=/opt/frontseat/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

# What runs in a box is not root: the supervisor and a build need
# nothing root has, and a sandbox is given no device and no capability.
# Its workspace and what is kept there (/var/lib/frontseat: the
# sandbox's lock, what a build is told, and what each build daemon
# keeps) are its user's own.
USER 0
RUN mkdir -p /work /var/lib/frontseat && chown -R 65532:65532 /work /var/lib/frontseat
USER 65532:65532
WORKDIR /work
# What a box runs: its supervisor, in its workspace, on a box's ports
# (its source API, which the control plane alone dials, and its shell)
# (core/ports, which stack/local/ports_test.go holds this to), told
# where the image's plugins are, which it writes, with the grid, where a
# build in the box reads its settings. What the sandbox is told beside,
# the workspace's name, the key let into its shell and the grid it
# builds on, are the arguments it is started with, which follow these:
# its image's ENTRYPOINT is the supervisor. A build is no part of it: a
# command that builds starts the build daemon of the workspace's
# directory, as on any machine.
ENTRYPOINT ["/usr/sbin/tini", "--", "/opt/frontseat/bin/frontseat-box", \
  "--source", "0.0.0.0:7601", "--shell", "0.0.0.0:2222", \
  "--workspace", "/work", "--plugins", "/opt/frontseat/plugins"]
