const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { test } = require('node:test');

const root = path.resolve(__dirname, '..');

test('release metadata and changelog use the same application version', () => {
  const metadata = JSON.parse(fs.readFileSync(path.join(root, 'mygo.json'), 'utf8'));
  const changelog = fs.readFileSync(path.join(root, 'CHANGELOG.md'), 'utf8');
  const html = fs.readFileSync(path.join(root, 'web', 'index.html'), 'utf8');
  const version = metadata.version;

  assert.match(version, /^\d+\.\d+\.\d+$/);
  assert.match(changelog, new RegExp(`^## ${version.replaceAll('.', '\\.')}\\s*$`, 'm'));
  assert.match(html, new RegExp(`id="appVersion"[^>]*>JSTV Player ${version.replaceAll('.', '\\.')}<`));
});

test('macOS app category metadata is entertainment', () => {
  const metadata = JSON.parse(fs.readFileSync(path.join(root, 'mygo.json'), 'utf8'));

  assert.equal(metadata.identifier, 'com.freelxs.jstvplayer');
  assert.equal(metadata.macos.infoPlist.LSApplicationCategoryType, 'public.app-category.entertainment');
});
