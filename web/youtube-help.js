// Keep provider-specific login guidance honest: only point to a config site
// the active user-supplied plugin actually declared.
(function (root) {
  function youtubeLoginHelp(site, sites) {
    if (!/youtube/i.test(`${site?.name || ''} ${site?.key || ''}`)) return null;
    const configSite = sites.find((s) => s !== site && /配置|设置|管理/.test(`${s.name || ''} ${s.key || ''}`)) || null;
    return {
      configSite,
      message: '请按当前插件页面指引完成登录。本应用不提供独立的 Google 登录表单。',
    };
  }
  if (typeof module !== 'undefined' && module.exports) module.exports = { youtubeLoginHelp };
  root.youtubeLoginHelp = youtubeLoginHelp;
})(globalThis);
