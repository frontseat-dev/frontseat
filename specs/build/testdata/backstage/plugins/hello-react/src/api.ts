import { createApiRef } from '@backstage/core-plugin-api';

/**
 * Greets a person by name.
 *
 * @public
 */
export type GreetingApi = {
  greet(name: string): string;
};

/**
 * The reference the greeting API is registered under.
 *
 * @public
 */
export const greetingApiRef = createApiRef<GreetingApi>({
  id: 'plugin.hello.greeting',
});
