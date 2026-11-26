import { greetingApiRef } from './api';

describe('greetingApiRef', () => {
  it('is registered under the plugin id', () => {
    expect(greetingApiRef.id).toBe('plugin.hello.greeting');
  });
});
