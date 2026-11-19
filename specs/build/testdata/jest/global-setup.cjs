const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const {execFileSync} = require('node:child_process');
module.exports = async () => {
  assert.equal(process.env.npm_lifecycle_event, 'test');
  assert.equal(process.env.npm_package_name, 'frontseat-jest-fixture');
  // Native npm puts installed package executables on PATH. A real CLI
  // lookup catches an environment that merely claims to be npm's.
  assert.equal(execFileSync('jest', ['--version'], {encoding: 'utf8'}).trim(), require('jest/package.json').version);
  const marker = path.join(process.env.TMPDIR, 'jest-suite-started');
  fs.writeFileSync(marker, 'one suite\n', {flag: 'wx'});
  process.env.JEST_SUITE_MARKER = marker;
};
