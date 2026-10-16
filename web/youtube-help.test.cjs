const test = require('node:test');
const assert = require('node:assert/strict');
const { youtubeLoginHelp } = require('./youtube-help.js');

test('YouTube login points to an existing plugin configuration site', () => {
  const site = { name: 'YouTube', key: 'yt' };
  const config = { name: '配置页面', key: 'settings' };
  const help = youtubeLoginHelp(site, [site, config]);
  assert.equal(help.configSite, config);
  assert.match(help.message, /按当前插件页面指引完成登录/);
  assert.match(help.message, /不提供独立的 Google 登录表单/);
});

test('YouTube guidance does not invent an app login route', () => {
  const help = youtubeLoginHelp({ name: 'YouTube' }, [{ name: 'YouTube' }]);
  assert.equal(help.configSite, null);
  assert.match(help.message, /按当前插件页面指引完成登录/);
  assert.match(help.message, /不提供独立的 Google 登录表单/);
});

test('non-YouTube sites receive no provider-specific login guidance', () => {
  assert.equal(youtubeLoginHelp({ name: '电影站' }, []), null);
});
