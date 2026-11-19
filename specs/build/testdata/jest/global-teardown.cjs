const fs = require('node:fs');
module.exports = async () => {
  if (fs.readFileSync(process.env.JEST_SUITE_MARKER, 'utf8') !== 'one suite\n') {
    throw new Error('suite setup was not preserved');
  }
  console.log('jest-suite-once');
};
