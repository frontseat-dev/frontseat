local common = import 'common.libsonnet';

{
  grpcServers: [{
    listenAddresses: [common.internal.storage],
    authenticationPolicy: { allow: {} },
  }],
  maximumMessageSizeBytes: common.maximumMessageSizeBytes,
  global: common.global,
  contentAddressableStorage: {
    backend: {
      'local': {
        keyLocationMapOnBlockDevice: {
          file: {
            path: '/var/lib/frontseat/storage-cas/key_location_map',
            // About 4x the blobs 32 GiB holds at 4.1 KiB each (see worker.jsonnet).
            sizeBytes: 2 * 1024 * 1024 * 1024,
          },
        },
        keyLocationMapMaximumGetAttempts: 16,
        keyLocationMapMaximumPutAttempts: 64,
        oldBlocks: 8,
        currentBlocks: 24,
        newBlocks: 3,
        blocksOnBlockDevice: {
          source: {
            file: {
              path: '/var/lib/frontseat/storage-cas/blocks',
              sizeBytes: 32 * 1024 * 1024 * 1024,
            },
          },
          spareBlocks: 3,
        },
        persistent: {
          stateDirectoryPath: '/var/lib/frontseat/storage-cas/persistent_state',
          minimumEpochInterval: '300s',
        },
      },
    },
    getAuthorizer: { allow: {} },
    putAuthorizer: { allow: {} },
    findMissingAuthorizer: { allow: {} },
  },
  actionCache: {
    backend: {
      'local': {
        keyLocationMapOnBlockDevice: {
          file: {
            path: '/var/lib/frontseat/storage-ac/key_location_map',
            // About 2x the entries 256 MiB holds at the 584 B they average.
            sizeBytes: 64 * 1024 * 1024,
          },
        },
        keyLocationMapMaximumGetAttempts: 16,
        keyLocationMapMaximumPutAttempts: 64,
        oldBlocks: 8,
        currentBlocks: 24,
        newBlocks: 1,
        blocksOnBlockDevice: {
          source: {
            file: {
              path: '/var/lib/frontseat/storage-ac/blocks',
              sizeBytes: 256 * 1024 * 1024,
            },
          },
          spareBlocks: 3,
        },
        persistent: {
          stateDirectoryPath: '/var/lib/frontseat/storage-ac/persistent_state',
          minimumEpochInterval: '300s',
        },
      },
    },
    getAuthorizer: { allow: {} },
    putAuthorizer: { allow: {} },
  },
  fileSystemAccessCache:  {
    backend: {
      'local': {
        keyLocationMapOnBlockDevice: {
          file: {
            path: '/var/lib/frontseat/storage-fsac/key_location_map',
            sizeBytes: 1024 * 1024,
          },
        },
        keyLocationMapMaximumGetAttempts: 16,
        keyLocationMapMaximumPutAttempts: 64,
        oldBlocks: 8,
        currentBlocks: 24,
        newBlocks: 1,
        blocksOnBlockDevice: {
          source: {
            file: {
              path: '/var/lib/frontseat/storage-fsac/blocks',
              sizeBytes: 20 * 1024 * 1024,
            },
          },
          spareBlocks: 3,
        },
        persistent: {
          stateDirectoryPath: '/var/lib/frontseat/storage-fsac/persistent_state',
          minimumEpochInterval: '300s',
        },
      },
    },
    getAuthorizer: { allow: {} },
    putAuthorizer: { allow: {} },
  },
}
