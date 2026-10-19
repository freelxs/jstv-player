const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { test } = require('node:test');

const root = path.resolve(__dirname, '..');
const html = fs.readFileSync(path.join(root, 'web', 'index.html'), 'utf8');
const js = fs.readFileSync(path.join(root, 'web', 'app.js'), 'utf8');

test('playback settings offer SenPlayer and a custom player', () => {
  assert.match(html, /<option value="senplayer" data-os="mac">SenPlayer（macOS）<\/option>/);
  assert.match(html, /<option value="custom">自定义播放器<\/option>/);
  assert.match(html, /id="sCustomPlayer"/);
  assert.match(html, /id="sCustomArgs"/);
  assert.match(html, /id="sBrowsePlayer"/);
  assert.match(html, /\{url\}/);
  assert.match(js, /o\.remove\(\)/);
  assert.match(js, /function syncCustomPlayerFields\(\)/);
  assert.match(js, /call\('PickCustomPlayer'\)/);
  assert.ok(js.includes("customPlayer: $('#sCustomPlayer').value.trim()"));
  assert.ok(js.includes("customArgs: $('#sCustomArgs').value.trim()"));
});
