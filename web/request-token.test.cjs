const test = require('node:test');
const assert = require('node:assert/strict');
const { createRequestToken } = require('./request-token.js');

test('a newer browse request makes earlier responses stale', () => {
  const requests = createRequestToken();
  const oldCategory = requests.begin();
  const newCategory = requests.begin();
  assert.equal(requests.current(oldCategory), false);
  assert.equal(requests.current(newCategory), true);
});

test('navigation invalidates work still in flight', () => {
  const requests = createRequestToken();
  const pending = requests.begin();
  requests.invalidate();
  assert.equal(requests.current(pending), false);
});

test('begin and invalidate abort the previous request signal', () => {
  const requests = createRequestToken();
  requests.begin();
  const first = requests.signal();
  assert.equal(first.aborted, false);
  requests.begin();
  const second = requests.signal();
  assert.equal(first.aborted, true);
  assert.equal(second.aborted, false);
  requests.invalidate();
  assert.equal(second.aborted, true);
});
