local common = import 'common.libsonnet';

// How many actions run at once (GRID_CONCURRENCY): one per runner
// sandbox of the pool, each running one action at a time.
local concurrency = std.parseInt(std.extVar('GRID_CONCURRENCY'));
assert concurrency > 0 : 'GRID_CONCURRENCY is how many actions run at once, and is one or more';

// How many of the last slots reach the network (GRID_EGRESS): their
// sandboxes' policy lets them reach the hosts an action that fetches
// fetches from, and they advertise it (network=egress), so that such an
// action is sent to them and every other action to the rest.
local egress = std.parseInt(std.extVar('GRID_EGRESS'));
// How many tests with services run at once: each is a sandbox for the
// test and one for each service, which the services runner makes.
local services = std.parseInt(std.extVar('GRID_SERVICES'));
assert egress >= 0 && egress <= concurrency : 'GRID_EGRESS is how many of the slots reach the network, and is no more than GRID_CONCURRENCY';

// What one action may write: enough for the fetch of a toolchain, which
// unpacks an archive of a few gigabytes.
local written = 8 * 1024 * 1024 * 1024;

// A runner of the worker's build directory: what every runner advertises
// and is told.
local runner = {
  instanceNamePrefix: '',
  platform: {
    properties: [
      { name: 'ISA', value: std.extVar('GRID_ISA') },
      { name: 'OSFamily', value: 'linux' },
      // The image every runner sandbox is made from (RUNNER_IMAGE, which
      // the image was built with). It is in every action's digest.
      { name: 'container-image', value: 'docker://' + std.extVar('RUNNER_IMAGE') },
    ],
  },
  // Nothing is told an action of where it runs: there is no proxy, and
  // what an action fetches it fetches from where it names.
  environmentVariables: {},
  maximumFilePoolFileCount: 500000,
  maximumFilePoolSizeBytes: written,
};

// A worker whose actions run in a mount of the storage, which Buildbarn
// calls its most efficient configuration: what an action is given costs
// nothing until it is read, so a tool of sixty thousand files costs an
// action what a tool of sixty does. Mounting takes privilege, which the
// worker has and the runner, where an action runs, has not. Its runner
// advertises the standard properties frontseat routes on, OSFamily and
// ISA, for the host the stack runs on (GRID_ISA: aarch64 or x86-64), and
// the image an action runs in, and serves every instance name,
// frontseat's among them.
{
  // What an action reads is kept beside the worker once it has been
  // read, so that the next action does not ask the storage for it again.
  blobstore: {
    actionCache: common.blobstore.actionCache,
    contentAddressableStorage: {
      readCaching: {
        slow: common.blobstore.contentAddressableStorage,
        fast: {
          'local': {
            keyLocationMapOnBlockDevice: {
              // About 4x the blobs 16 GiB holds at the 4.1 KiB this grid's blobs
              // average (metrics, 2026-11-16): bb-storage asks 2-10x, and below it
              // drops index entries, which reads as 'Blob absent from sink'.
              file: { path: '/var/lib/frontseat/worker-kept/cas/key_location_map', sizeBytes: 1024 * 1024 * 1024 },
            },
            keyLocationMapMaximumGetAttempts: 16,
            keyLocationMapMaximumPutAttempts: 64,
            oldBlocks: 8,
            currentBlocks: 24,
            newBlocks: 3,
            blocksOnBlockDevice: {
              source: {
                file: { path: '/var/lib/frontseat/worker-kept/cas/blocks', sizeBytes: 16 * 1024 * 1024 * 1024 },
              },
              spareBlocks: 3,
              dataIntegrityValidationCache: {
                cacheSize: 50000,
                cacheDuration: '14400s',
                cacheReplacementPolicy: 'LEAST_RECENTLY_USED',
              },
            },
            persistent: {
              stateDirectoryPath: '/var/lib/frontseat/worker-kept/cas/persistent_state',
              minimumEpochInterval: '300s',
            },
          },
        },
        replicator: { deduplicating: { 'local': {} } },
      },
    },
  },
  maximumMessageSizeBytes: common.maximumMessageSizeBytes,
  scheduler: { address: '127.0.0.1:8986' },
  global: common.global,
  // An action's root is a mount of the storage: a file of it costs
  // nothing until the action reads it, however many there are, and is
  // never the action's to change.
  // A build directory for each runner sandbox of the pool, each the only
  // one bound into its sandbox, and each sandbox a runner of one action at
  // a time: an action sees its own directory and no other (frontseat-runner
  // makes the pool, at /var/lib/frontseat/worker/<slot>).
  buildDirectories: [{
    virtual: {
      maximumExecutionTimeoutCompensation: '3600s',
      maximumWritableFileUploadDelay: '60s',
      mount: {
        mountPath: '/var/lib/frontseat/worker/%d/build' % slot,
        fuse: {
          directoryEntryValidity: '300s',
          inodeAttributeValidity: '300s',
          allowOther: true,
          mountMethod: 'DIRECT',
        },
      },
    },
    runners: [runner {
      concurrency: 1,
      platform+: if slot > concurrency - egress then { properties+: [{ name: 'network', value: 'egress' }] } else {},
      // What reaches the network from a sandbox goes through OpenShell's
      // proxy, which inspects it with an authority of its own: an action
      // there is told where that authority is, as the sandbox's first
      // process is. The worker's own, in no action's key.
      environmentVariables+: if slot > concurrency - egress then {
        SSL_CERT_FILE: '/run/openshell-supervisor-ca/material/ca-bundle.crt',
        CURL_CA_BUNDLE: '/run/openshell-supervisor-ca/material/ca-bundle.crt',
        GIT_SSL_CAINFO: '/run/openshell-supervisor-ca/material/ca-bundle.crt',
        REQUESTS_CA_BUNDLE: '/run/openshell-supervisor-ca/material/ca-bundle.crt',
        NODE_EXTRA_CA_CERTS: '/run/openshell-supervisor-ca/material/ca.crt',
      } else {},
      endpoint: { address: 'unix:///var/lib/frontseat/worker/runners/%d' % slot },
      workerId: { host: 'worker', slot: std.toString(slot) },
    }],
  } for slot in std.range(1, concurrency)] + [{
    // Tests with services: the services runner (frontseat-runner) makes a
    // sandbox for each service a test names and one for the test, over
    // the test's directory of this build directory alone, and an action
    // says it is one by the property frontseat-runner.
    virtual: {
      maximumExecutionTimeoutCompensation: '3600s',
      maximumWritableFileUploadDelay: '60s',
      mount: {
        mountPath: '/var/lib/frontseat/worker/services/build',
        fuse: {
          directoryEntryValidity: '300s',
          inodeAttributeValidity: '300s',
          allowOther: true,
          mountMethod: 'DIRECT',
        },
      },
    },
    runners: [runner {
      concurrency: services,
      platform+: { properties+: [{ name: 'frontseat-runner', value: 'sandbox-v2' }] },
      endpoint: { address: 'unix:///var/lib/frontseat/worker/services/own/runner' },
      workerId: { host: 'worker', runner: 'services' },
    }],
  }],
  // What an action writes is kept here while it runs.
  filePool: {
    blockDevice: {
      // As many actions as run at once, each writing as much as it may:
      // the pool grows with how many run, and none finds it full.
      file: { path: '/var/lib/frontseat/worker-kept/filepool', sizeBytes: (concurrency + services) * written },
    },
  },
  inputDownloadConcurrency: 10,
  outputUploadConcurrency: 11,
  directoryCache: {
    maximumCount: 1000,
    maximumSizeBytes: 1000 * 1024,
    cacheReplacementPolicy: 'LEAST_RECENTLY_USED',
  },
}
