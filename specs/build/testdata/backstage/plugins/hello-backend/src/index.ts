import {
  coreServices,
  createBackendPlugin,
} from '@backstage/backend-plugin-api';
import { greeting } from '@hello/common';

/**
 * Logs the greeting when the backend starts.
 *
 * @public
 */
export const helloPlugin = createBackendPlugin({
  pluginId: 'hello',
  register(env) {
    env.registerInit({
      deps: { logger: coreServices.logger },
      async init({ logger }) {
        logger.info(greeting('backend'));
      },
    });
  },
});
