local common = import 'common.libsonnet';

// The asset service: what a daemon asks for a download, a URL and the
// sha256 of what it serves, through the Remote Asset API. It fetches the
// URL once, over HTTP, checks the sha256 before it keeps anything, puts
// the content in the grid's store and remembers the answer in the grid's
// action cache by URL and checksum: every repository and daemon of the
// grid shares one download. There is no proxy: it fetches a URL from where
// the URL says. It shares the container's loopback with the stack's
// other services, so a URL that names the loopback reaches them; what
// answers there without a credential (the gateway's health) can be read
// back through it. Holding its fetches to public addresses is the work of
// the stack's downloads (design: no proxy), and is not done yet. It keeps
// what it fetched under the address and the checksum together, so what a
// request without a checksum stored answers no request with one. Nothing
// pushes an asset: the Push API accepts no instance
// (allowUpdatesForInstances). The push authorizer is the service's own
// leave to record what it fetched, and is not what holds the Push API
// shut.
{
  grpcServers: [{
    listenAddresses: [':8983'],
    authenticationPolicy: { allow: {} },
  }],
  contentAddressableStorage: common.blobstore.contentAddressableStorage,
  // Over the completeness-checking action cache, so that an answer that
  // names content the store has let go is a miss, and fetched again.
  assetCache: { actionCache: common.blobstore.actionCache },
  fetcher: { http: {} },
  fetchAuthorizer: { allow: {} },
  pushAuthorizer: { allow: {} },
  allowUpdatesForInstances: [],
  maximumMessageSizeBytes: common.maximumMessageSizeBytes,
  global: common.global,
}
