import { greeting } from './index';

describe('greeting', () => {
  it('greets by name', () => {
    expect(greeting('Backstage')).toBe('Hello, Backstage!');
  });
});
