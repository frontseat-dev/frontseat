# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e
#
# The local stack as one container (frontseat-stack): the grid, the
# OpenShell gateway, the control plane and the pool of the grid's runner
# sandboxes, each a process s6-overlay supervises, and every sandbox (each
# workspace, each of the grid's runners) an OpenShell sandbox in rootless
# Podman inside it, and so is each service a test is given and the test
# beside it. It is built on the box
# (stack/box, --build-arg BOX), whose programs it runs. Every download is
# named by its digest or its checksum.

ARG TARGETARCH
ARG BOX
ARG WOLFI=cgr.dev/chainguard/wolfi-base@sha256:e9b8c3739e5fb5fc778843a2ce74f8620588c9ddc9c44918cead527abcb0b628

FROM ghcr.io/nvidia/openshell/gateway:0.1.9@sha256:1fd9e1e807990e66c9486a28d3ce9d649fd7528df48d0e830d2de2148b52dfe5 AS gateway
FROM ghcr.io/buildbarn/bb-storage:20260906T092937Z-ae61334@sha256:05760070ed561be9090e9483c6ee0cf55e7f806cf7c28752f1301f31015002c7 AS storage
FROM ghcr.io/buildbarn/bb-scheduler:20261008T101024Z-582d07f@sha256:3e61fd8824706c174b0c7e87ca02b5c1ae99ba27a1f586b2e1b3a0af909bb491 AS scheduler
FROM ghcr.io/buildbarn/bb-worker:20261008T101024Z-582d07f@sha256:9188ee7ba21f78ecdfd8ee7304dba555af4bec6cc00b2fe12d11a31a0b7005c7 AS worker
FROM ghcr.io/buildbarn/bb-remote-asset:20261008T155419Z-07cb3ab@sha256:e550abb1b3c46eafc82e985e589e24238a63f143caa3e3f899c0cb838e1b5e40 AS assets

# s6-overlay, by its release's checksums (ISC).
FROM scratch AS s6-arm64
ADD --checksum=sha256:b17f17a82e7a515c682a91edaf2ffdabb73f891981b6c1fd712115693a2f8b4c https://github.com/just-containers/s6-overlay/releases/download/v3.2.3.2/s6-overlay-aarch64.tar.xz /s6-arch.tar.xz
FROM scratch AS s6-amd64
ADD --checksum=sha256:e6befcc96a437a3831386ecfc51808c5d3e939dc5fe3c02ae9284599e8aa2408 https://github.com/just-containers/s6-overlay/releases/download/v3.2.3.2/s6-overlay-x86_64.tar.xz /s6-arch.tar.xz

FROM s6-${TARGETARCH} AS s6

FROM ${BOX} AS box

FROM ${WOLFI}
# Every package at the version it resolved to when it was last chosen, so
# that the same source builds the same image; Podman 5.8, since Podman 6
# dropped slirp4netns, its rootless networking here, and Wolfi packages
# no passt; with neither its remote client nor its systemd generator,
# which nothing here runs; Unbound, the container's own resolver (BSD),
# which runs as dns, and what its package requires: among them bash
# (GPL-3), which becomes /bin/sh, as the owner accepted, beside the GPL
# programs the image held already (busybox, crun, util-linux). The users
# are written as they are, with no date in them.
RUN apk add --no-cache podman=5.8.3-r0 crun=1.30.1-r0 conmon=2.2.1-r3 \
      netavark=2.1.0-r6 aardvark-dns=2.1.0-r4 fuse-overlayfs=1.18-r2 fuse3=3.18.3-r3 \
      slirp4netns=1.3.7-r1 shadow=4.20.3-r4 shadow-subids=4.20.3-r4 mount=2.42.4-r3 \
      util-linux-misc=2.42.4-r3 ca-certificates=20261020-r0 xz=5.8.4-r2 unbound=1.25.1-r1 \
      unbound-libs=1.25.1-r1 unbound-config=1.25.1-r1 libevent=2.1.13-r4 libexpat1=2.10.0-r1 \
      libsodium=1.0.22-r5 bash=5.3-r14 bash-binsh=5.3-r14 \
 && printf 'frontseat:x:1000:1000::/home/frontseat:/sbin/nologin\nbb:x:1001:1001::/home/bb:/sbin/nologin\ndns:x:1002:1002::/var/empty:/sbin/nologin\n' >> /etc/passwd \
 && printf 'frontseat:x:1000:\nbb:x:1001:\ndns:x:1002:\n' >> /etc/group \
 && printf 'frontseat:!:0:0:99999:7:::\nbb:!:0:0:99999:7:::\ndns:!:0:0:99999:7:::\n' >> /etc/shadow \
 && mkdir -p /home/frontseat /home/bb && chown 1000:1000 /home/frontseat && chown 1001:1001 /home/bb \
 && echo frontseat:100000:65536 > /etc/subuid && echo frontseat:100000:65536 > /etc/subgid \
 && rm /usr/bin/podman-remote /usr/libexec/podman/quadlet
ADD --checksum=sha256:5379750ed30a84bbd2e2dd74847ba6b5bd29cd0b2e3ea2ec58049b57eb2eda12 https://github.com/just-containers/s6-overlay/releases/download/v3.2.3.2/s6-overlay-noarch.tar.xz /tmp/s6-noarch.tar.xz
COPY --from=s6 /s6-arch.tar.xz /tmp/s6-arch.tar.xz
RUN tar -C / -Jxpf /tmp/s6-noarch.tar.xz && tar -C / -Jxpf /tmp/s6-arch.tar.xz && rm /tmp/s6-*.tar.xz
COPY --from=gateway /usr/local/bin/openshell-gateway /usr/local/bin/
COPY --from=storage /app/bb_storage /usr/local/bin/
COPY --from=scheduler /app/bb_scheduler /usr/local/bin/
COPY --from=worker /app/bb_worker /usr/local/bin/
COPY --from=assets /app/bb_remote_asset /usr/local/bin/
# Of the box's programs, those the stack runs: the control plane and init,
# the runner pool, and the confiner its installer installs. The supervisor and
# the command line run in a workspace's sandbox, of the box itself.
COPY --from=box /opt/frontseat/bin/frontseat-server /opt/frontseat/bin/frontseat-stack /opt/frontseat/bin/frontseat-runner /opt/frontseat/bin/frontseat-confine /opt/frontseat/bin/
COPY rootfs/ /
# What the image was built with, for its services to read where a flag is
# given: the box a workspace runs, and the workers' architecture as the
# grid names it (local.ISA), which the builder says for the architecture
# it builds for. The image every runner sandbox is made of is the one
# rootfs/etc/frontseat/runner-image names, by its digest, and no other.
ARG BOX
ARG ISA
RUN test -n "$ISA" && echo "$BOX" > /etc/frontseat/box && echo "$ISA" > /etc/frontseat/isa
ENV PATH=/opt/frontseat/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    S6_KEEP_ENV=1 S6_BEHAVIOUR_IF_STAGE2_FAILS=2 S6_CMD_WAIT_FOR_SERVICES_MAXTIME=0
# frontseat-stack init delegates cgroups and lays out the state while it
# is the only process, then becomes s6-overlay's /init.
ENTRYPOINT ["/opt/frontseat/bin/frontseat-stack", "init", "/init"]
