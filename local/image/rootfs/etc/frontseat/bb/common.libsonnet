// What every Buildbarn process of the stack shares: the storage, on the
// container's loopback. No diagnostics server: each would take :80.
// The ports the processes serve one another on, on the container's
// loopback alone, each said here once: the storage's, the scheduler's
// for the frontend and its workers'.
local internal = { storage: 8981, scheduler: 8982, workers: 8986 };
local storage = { grpc: { client: { address: '127.0.0.1:%d' % internal.storage } } };
{
  internal: { [name]: '127.0.0.1:%d' % internal[name] for name in std.objectFields(internal) },
  blobstore: {
    contentAddressableStorage: storage,
    actionCache: { completenessChecking: { backend: storage, maximumTotalTreeSizeBytes: 64 * 1024 * 1024 } },
  },
  fileSystemAccessCache: storage,
  maximumMessageSizeBytes: 16 * 1024 * 1024,
  global: {},
}
