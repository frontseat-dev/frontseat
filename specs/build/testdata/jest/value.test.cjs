const fs = require('node:fs');
test('the complete native suite receives setup and source', () => {
  expect(fs.readFileSync(process.env.JEST_SUITE_MARKER, 'utf8')).toBe('one suite\n');
  expect(require('./value.cjs')).toBe(41);
});
