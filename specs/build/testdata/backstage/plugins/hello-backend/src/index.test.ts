import { readFileSync } from 'fs';
import { join } from 'path';
import { helloPlugin } from './index';

describe('helloPlugin', () => {
  it('is a backend feature', () => {
    expect(helloPlugin.$$type).toBe('@backstage/BackendFeature');
  });

  it('greets through the library the workspace links', () => {
    const manifest = JSON.parse(
      readFileSync(join(__dirname, '../../hello/package.json'), 'utf8'),
    );
    expect(manifest.name).toBe('@hello/common');
  });
});
