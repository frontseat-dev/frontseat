module.exports = {
  testMatch: ['**/*.test.cjs'],
  maxWorkers: 2,
  globalSetup: './global-setup.cjs',
  globalTeardown: './global-teardown.cjs',
};
