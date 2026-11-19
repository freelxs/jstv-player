package app

// sniffScript records media requests inside a hidden, untrusted page.
// It does not call Go. The host polls window.__jstvMedia.
const sniffScript = `
(() => {
  if (window.__jstvSniff) return;
  window.__jstvSniff = 1;
  const media = (url) => {
    try { url = new URL(String(url || ''), location.href).href; } catch (e) { return ''; }
    if (!/^https?:/i.test(url)) return '';
    if (!/\.(m3u8|mp4|m4v|flv|mkv|webm|mpd)(?:$|\?)/i.test(url.split('#')[0])) return '';
    return url;
  };
  const keep = (url, headers) => {
    url = media(url);
    if (!url || window.__jstvMedia) return;
    const h = {};
    if (headers) {
      try {
        if (typeof headers.forEach === 'function') headers.forEach((v, k) => { h[k] = String(v); });
        else Object.keys(headers).forEach((k) => { h[k] = String(headers[k]); });
      } catch (e) {}
    }
    window.__jstvMedia = { url, headers: h, referer: location.href };
    try { if (parent && parent !== window) parent.postMessage({ jstvMedia: window.__jstvMedia }, '*'); } catch (e) {}
  };
  addEventListener('message', (ev) => {
    const data = ev && ev.data;
    if (data && data.jstvMedia && data.jstvMedia.url) keep(data.jstvMedia.url, data.jstvMedia.headers);
  });
  if (window.fetch) {
    const orig = window.fetch;
    window.fetch = function (input, init) {
      try { keep(typeof input === 'string' ? input : (input && input.url), init && init.headers); } catch (e) {}
      return orig.apply(this, arguments);
    };
  }
  const xp = XMLHttpRequest && XMLHttpRequest.prototype;
  if (xp) {
    const open = xp.open, setHeader = xp.setRequestHeader, send = xp.send;
    xp.open = function (method, url) { this.__jstvURL = url; return open.apply(this, arguments); };
    xp.setRequestHeader = function (k, v) { (this.__jstvH || (this.__jstvH = {}))[k] = v; return setHeader.apply(this, arguments); };
    xp.send = function () { try { keep(this.__jstvURL, this.__jstvH); } catch (e) {} return send.apply(this, arguments); };
  }
  const scan = () => {
    const nodes = document.querySelectorAll ? document.querySelectorAll('video,source,audio') : [];
    for (const el of nodes) keep(el.currentSrc || el.src, null);
  };
  if (window.PerformanceObserver) {
    try {
      new PerformanceObserver((list) => { for (const entry of list.getEntries()) keep(entry.name, null); }).observe({ type: 'resource', buffered: true });
    } catch (e) {}
  }
  const root = document.documentElement || document;
  if (window.MutationObserver && root) new MutationObserver(scan).observe(root, { subtree: true, childList: true, attributes: true });
  scan();
  setInterval(scan, 250);
})();
`
