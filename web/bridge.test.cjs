const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const bridge = fs.readFileSync(path.join(__dirname, '..', 'webassets', 'bridge.js'), 'utf8');

test('web bridge leaves the desktop mygo binding alone', () => {
  const mygo = { call() { return 'desktop'; } };
  const window = { mygo };
  vm.runInNewContext(bridge, { window }, { filename: 'bridge.js' });
  assert.equal(window.mygo, mygo);
  assert.equal(window.__JSTV_WEB__, undefined);
});

test('web bridge installs a caller when the page has no desktop binding', () => {
  const window = {};
  vm.runInNewContext(bridge, { window, location: { origin: 'http://player.local' } }, { filename: 'bridge.js' });
  assert.equal(window.__JSTV_WEB__, true);
  assert.equal(typeof window.mygo.call, 'function');
  assert.equal(typeof window.mygo.callWith, 'function');
  assert.equal(typeof window.mygo.channel, 'function');
  const channel = window.mygo.channel(() => {});
  assert.equal(channel.__channel, true);
});

test('web bridge forwards AbortSignal through callWith', async () => {
  const seen = [];
  const window = {};
  const fetch = async (url, opts) => {
    seen.push({ url, opts });
    return { ok: true, status: 200, text: async () => JSON.stringify({ result: { ok: true } }) };
  };
  vm.runInNewContext(bridge, { window, fetch, location: { origin: 'http://player.local' } }, { filename: 'bridge.js' });
  const ac = new AbortController();
  const result = await window.mygo.callWith('Player.Home', ['/api'], { signal: ac.signal });
  assert.equal(result.ok, true);
  assert.equal(seen.length, 1);
  assert.equal(seen[0].url, '/api/call');
  assert.equal(seen[0].opts.signal, ac.signal);
});
