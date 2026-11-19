jest.retryTimes(1);
let attempts = 0;
test('native Jest retries stay inside the test atom', () => {
  attempts += 1;
  expect(attempts).toBe(2);
  console.log('native-retry-passed');
});
