const test = require('node:test');
const assert = require('node:assert/strict');
const { youtubeLoginHelp } = require('./youtube-help.js');

test('YouTube login points to an existing plugin configuration site', () => {
  const site = { name: 'YouTube', key: 'yt' };
  const config = { name: '配置页面', key: 'settings' };
  const help = youtubeLoginHelp(site, [site, config]);
  assert.equal(help.configSite, config);
  assert.match(help.message, /配置页面/);
  assert.match(help.message, /不会读取或显示登录凭据/);
});

test('YouTube guidance does not invent an app login route', () => {
  const help = youtubeLoginHelp({ name: 'YouTube' }, [{ name: 'YouTube' }]);
  assert.equal(help.configSite, null);
  assert.match(help.message, /后台管理界面扫码/);
  assert.match(help.message, /没有独立的 YouTube 登录接口/);
});

test('non-YouTube sites receive no provider-specific login guidance', () => {
  assert.equal(youtubeLoginHelp({ name: '电影站' }, []), null);
});
