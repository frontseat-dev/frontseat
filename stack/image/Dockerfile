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

FROM quay.io/ai-openshell/gateway:0.1.10@sha256:9ee945079d1f9b784d59232ba38e133b9b2797b821dc5cadb4abfccb51902f1d AS gateway
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

# Node, by the archive and checksum mise.lock records for each Linux
# architecture (MIT): the Node this repository pins (mise.toml), which
# builds the portal and runs its backend.
FROM scratch AS node-arm64
ADD --checksum=sha256:261af2af250dfd7e9f25cf151d1b2713a0adf8f10a9ad116c4947f68627affe8 https://nodejs.org/dist/v24.22.3/node-v24.22.3-linux-arm64.tar.gz /node.tar.gz
FROM scratch AS node-amd64
ADD --checksum=sha256:061ec3e3062115705b56b51fa9cc330ab50d34b6969387d4e4b1a50c6809acc2 https://nodejs.org/dist/v24.22.3/node-v24.22.3-linux-x64.tar.gz /node.tar.gz

FROM node-${TARGETARCH} AS node

# The portal (portal/, which the stack's build lays beside this file),
# built as Backstage builds a backend's image (its docs, "Host Build"
# and "Multi-stage Build"), with what the image runs and nothing it was
# built with: the app and the backend built by the Backstage CLI, then
# the backend's skeleton installed for production alone and its bundle
# unpacked over it. Yarn is its own npm package, by the checksum
# mise.lock records (BSD-2-Clause). A native module (better-sqlite3) is
# built from its source against the pinned Node's headers, by Wolfi's
# compiler and Python, each at the version it resolved to when it was
# chosen; what the compiler left of it is removed, but the module, and
# Python writes nothing of its own beside it.
FROM ${WOLFI} AS portal
RUN apk add --no-cache build-base=1-r11 python-3.13=3.13.18_git20261122-r0
COPY --from=node /node.tar.gz /tmp/node.tar.gz
ADD --checksum=sha256:96a5c3ec72472c44e16042caded154ad4c5cf267ab42ed337a9aa1f501a4f735 https://registry.npmjs.org/@yarnpkg/cli-dist/-/cli-dist-4.19.0.tgz /tmp/yarn.tgz
RUN mkdir -p /opt/node /opt/yarn \
 && tar -C /opt/node --strip-components=1 -xzf /tmp/node.tar.gz \
 && tar -C /opt/yarn --strip-components=1 -xzf /tmp/yarn.tgz \
 && chmod +x /opt/yarn/bin/yarn && rm /tmp/node.tar.gz /tmp/yarn.tgz
ENV PATH=/opt/node/bin:/opt/yarn/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    PYTHONDONTWRITEBYTECODE=1 npm_config_build_from_source=true npm_config_nodedir=/opt/node
WORKDIR /portal
COPY portal/ ./
RUN yarn install --immutable && yarn tsc && yarn build:backend
WORKDIR /out
RUN tar -xzf /portal/packages/backend/dist/skeleton.tar.gz \
 && tar -xzf /portal/packages/backend/dist/bundle.tar.gz package.json yarn.lock \
 && cp /portal/.yarnrc.yml /portal/backstage.json . \
 && NODE_ENV=production yarn workspaces focus --all --production \
 && tar -xzf /portal/packages/backend/dist/bundle.tar.gz \
 && cp -R /portal/app-config.yaml /portal/examples . \
 && rm -rf .yarn .yarnrc.yml \
 && find node_modules -name binding.gyp -exec dirname {} \; | while read -r module; do \
      [ -d "$module/build" ] || continue; \
      find "$module/build" -type f ! -name '*.node' -delete; \
      find "$module/build" -depth -type d -empty -delete; \
    done

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
      libsodium=1.0.22-r5 bash=5.3-r14 bash-binsh=5.3-r14 libstdc++=16.2.0-r5 \
 && printf 'frontseat:x:1000:1000::/home/frontseat:/sbin/nologin\nbb:x:1001:1001::/home/bb:/sbin/nologin\ndns:x:1002:1002::/var/empty:/sbin/nologin\nportal:x:1003:1003::/var/lib/frontseat/portal:/sbin/nologin\n' >> /etc/passwd \
 && printf 'frontseat:x:1000:\nbb:x:1001:\ndns:x:1002:\nportal:x:1003:\n' >> /etc/group \
 && printf 'frontseat:!:0:0:99999:7:::\nbb:!:0:0:99999:7:::\ndns:!:0:0:99999:7:::\nportal:!:0:0:99999:7:::\n' >> /etc/shadow \
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
# The portal, as its build left it, and the Node it runs on, which needs
# libstdc++ beside it.
COPY --from=node /node.tar.gz /tmp/node.tar.gz
RUN mkdir /tmp/node && tar -C /tmp/node --strip-components=1 -xzf /tmp/node.tar.gz \
 && cp /tmp/node/bin/node /usr/local/bin/node && rm -rf /tmp/node /tmp/node.tar.gz
COPY --from=portal /out /opt/frontseat/portal
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
