// Keep provider-specific login guidance honest: only point to a config site
// the active user-supplied plugin actually declared.
(function (root) {
  function youtubeLoginHelp(site, sites) {
    if (!/youtube/i.test(`${site?.name || ''} ${site?.key || ''}`)) return null;
    const configSite = sites.find((s) => s !== site && /配置|设置|管理/.test(`${s.name || ''} ${s.key || ''}`)) || null;
    return {
      configSite,
      message: configSite
        ? `请打开插件提供的「${configSite.name || configSite.key}」站点，按页面提示扫码；本应用不会读取或显示登录凭据。`
        : '请按插件页面提示前往其后台管理界面扫码；本应用没有独立的 YouTube 登录接口，也不会读取或显示登录凭据。',
    };
  }
  if (typeof module !== 'undefined' && module.exports) module.exports = { youtubeLoginHelp };
  root.youtubeLoginHelp = youtubeLoginHelp;
})(globalThis);
