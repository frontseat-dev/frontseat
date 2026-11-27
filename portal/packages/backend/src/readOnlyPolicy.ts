import { createBackendModule } from '@backstage/backend-plugin-api';
import {
  AuthorizeResult,
  PolicyDecision,
} from '@backstage/plugin-permission-common';
import {
  PermissionPolicy,
  PolicyQuery,
} from '@backstage/plugin-permission-node';
import { policyExtensionPoint } from '@backstage/plugin-permission-node/alpha';

/**
 * Whoever reaches the portal is a guest, signed in by no one: so what they
 * are let do is read. A permission is allowed where it says it reads
 * (its `action` attribute) and refused otherwise: creating, updating and
 * deleting, and what says nothing of itself (analysing a location, which
 * fetches what it names, or a plugin's proxy). A portal that signs people
 * in sets a policy of its own.
 */
export class ReadOnlyPolicy implements PermissionPolicy {
  async handle(request: PolicyQuery): Promise<PolicyDecision> {
    return {
      result:
        request.permission.attributes.action === 'read'
          ? AuthorizeResult.ALLOW
          : AuthorizeResult.DENY,
    };
  }
}

export default createBackendModule({
  pluginId: 'permission',
  moduleId: 'read-only-policy',
  register(reg) {
    reg.registerInit({
      deps: { policy: policyExtensionPoint },
      async init({ policy }) {
        policy.setPolicy(new ReadOnlyPolicy());
      },
    });
  },
});
