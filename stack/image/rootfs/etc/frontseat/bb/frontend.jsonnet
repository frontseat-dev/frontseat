local common = import 'common.libsonnet';

{
  grpcServers: [{
    listenAddresses: [':8980'],
    authenticationPolicy: { allow: {} },
  }],
  schedulers: {
    '': {
      endpoint: {
        address: '127.0.0.1:8982',
        addMetadataJmespathExpression: {
          expression: |||
            {
              "build.bazel.remote.execution.v2.requestmetadata-bin": incomingGRPCMetadata."build.bazel.remote.execution.v2.requestmetadata-bin"
            }
          |||,
        },
      },
    },
  },
  maximumMessageSizeBytes: common.maximumMessageSizeBytes,
  global: common.global,
  contentAddressableStorage: {
    backend: common.blobstore.contentAddressableStorage,
    getAuthorizer: { allow: {} },
    putAuthorizer: { allow: {} },
    findMissingAuthorizer: { allow: {} },
  },
  // Only a worker says what an action produced. A worker writes an
  // action's result straight to the storage, which nothing outside the
  // grid reaches; a client reads results here and writes none.
  actionCache: {
    backend: common.blobstore.actionCache,
    getAuthorizer: { allow: {} },
    putAuthorizer: { deny: {} },
  },
  fileSystemAccessCache: {
    backend: common.fileSystemAccessCache,
    getAuthorizer: { allow: {} },
    putAuthorizer: { allow: {} },
  },
  executeAuthorizer: { allow: {} },
}
