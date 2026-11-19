// What every Buildbarn process of the stack shares: the storage, on the
// container's loopback. No diagnostics server: each would take :80.
local storage = { grpc: { client: { address: '127.0.0.1:8981' } } };
{
  blobstore: {
    contentAddressableStorage: storage,
    actionCache: { completenessChecking: { backend: storage, maximumTotalTreeSizeBytes: 64 * 1024 * 1024 } },
  },
  fileSystemAccessCache: storage,
  maximumMessageSizeBytes: 16 * 1024 * 1024,
  global: {},
}
