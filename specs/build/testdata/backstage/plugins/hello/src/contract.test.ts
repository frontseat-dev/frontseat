import { readFileSync } from 'fs';
import { join } from 'path';
import { greeting } from './index';

describe('greeting', () => {
  it('greets as the shared contract says', () => {
    const contract = readFileSync(join(__dirname, '../../../shared/greeting.txt'), 'utf8');
    expect(greeting('Backstage')).toBe(contract.trim());
  });
});
