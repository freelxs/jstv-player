const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { test } = require('node:test');

const root = path.resolve(__dirname, '..');
const html = fs.readFileSync(path.join(root, 'web', 'index.html'), 'utf8');
const js = fs.readFileSync(path.join(root, 'web', 'app.js'), 'utf8');

test('library view and privacy controls are wired in HTML and JS', () => {
  assert.match(html, /<meta name="referrer" content="no-referrer">/);
  assert.match(html, /id="btnFavorites"[^>]*>媒体库</);
  assert.match(html, /id="sHistoryEnabled"/);
  assert.match(html, /id="sAdBlock"/);
  assert.match(html, /id="sHlsAdFilter"/);

  for (const method of ['ListHistory', 'RemoveHistory', 'ClearHistory', 'SetWatched', 'ExportLibrary', 'ImportLibrary']) {
    assert.ok(js.includes(`call('${method}'`), `missing RPC call ${method}`);
  }
  assert.ok(js.includes("disableHistory: !$('#sHistoryEnabled').checked"));
  assert.ok(js.includes("disableAdBlock: !$('#sAdBlock').checked"));
  assert.ok(js.includes("disableHlsAdFilter: !$('#sHlsAdFilter').checked"));
});
