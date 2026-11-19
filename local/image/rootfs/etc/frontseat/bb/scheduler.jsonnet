local common = import 'common.libsonnet';

{
  clientGrpcServers: [{
    listenAddresses: ['127.0.0.1:8982'],
    authenticationPolicy: { allow: {} },
  }],
  workerGrpcServers: [{
    listenAddresses: ['127.0.0.1:8986'],
    authenticationPolicy: { allow: {} },
  }],
  buildQueueStateGrpcServers: [{
    listenAddresses: ['127.0.0.1:8984'],
    authenticationPolicy: { allow: {} },
  }],
  contentAddressableStorage: common.blobstore.contentAddressableStorage,
  maximumMessageSizeBytes: common.maximumMessageSizeBytes,
  global: common.global,
  executeAuthorizer: { allow: {} },
  modifyDrainsAuthorizer: { allow: {} },
  killOperationsAuthorizer: { allow: {} },
  synchronizeAuthorizer: { allow: {} },
  actionRouter: {
    simple: {
      platformKeyExtractor: { action: {} },
      invocationKeyExtractors: [
        { correlatedInvocationsId: {} },
        { toolInvocationId: {} },
      ],
      initialSizeClassAnalyzer: {
        defaultExecutionTimeout: '1800s',
        maximumExecutionTimeout: '7200s',
      },
    },
  },
  platformQueueWithNoWorkersTimeout: '900s',
}
