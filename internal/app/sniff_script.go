package app

// sniffScript records media requests inside a hidden, untrusted page.
// It does not call Go. The host polls window.__jstvMedia.
const sniffScript = `
(() => {
  if (window.__jstvSniff) return;
  window.__jstvSniff = 1;
  const isAdURL = (url) => /(googleads|googlesyndication\.com|doubleclick\.net|prestrain\.html|prestrain%2ehtml|adtrafficquality|adposter|preroll|pre-roll)/i.test(String(url || ''));
  const normalizeHTTP = (url) => {
    try { url = new URL(String(url || ''), location.href).href; } catch (e) { return ''; }
    if (!/^https?:/i.test(url) || isAdURL(url)) return '';
    return url;
  };
  const media = (url) => {
    url = normalizeHTTP(url);
    if (!url) return '';
    if (!/\.(m3u8|mp4|m4v|flv|mkv|webm|mpd)(?:$|\?)/i.test(url.split('#')[0])) return '';
    return url;
  };
  const decodeIframeMedia = (src) => {
    const norm = normalizeHTTP(src);
    if (!norm) return '';
    try {
      const u = new URL(norm);
      for (const [, val] of u.searchParams.entries()) {
        const direct = media(val);
        if (direct) return direct;
        try {
          const decoded = decodeURIComponent(val);
          const m = media(decoded);
          if (m) return m;
        } catch (e) {}
      }
    } catch (e) {}
    return '';
  };
  const keep = (url, headers, verified) => {
    url = verified ? normalizeHTTP(url) : media(url);
    if (!url || window.__jstvMedia) return;
    const h = {};
    if (headers) {
      try {
        if (typeof headers.forEach === 'function') headers.forEach((v, k) => { h[k] = String(v); });
        else Object.keys(headers).forEach((k) => { h[k] = String(headers[k]); });
      } catch (e) {}
    }
    window.__jstvMedia = { url, headers: h, referer: location.href, verified: !!verified };
    try { if (parent && parent !== window) parent.postMessage({ jstvMedia: window.__jstvMedia }, '*'); } catch (e) {}
  };
  addEventListener('message', (ev) => {
    const data = ev && ev.data;
    if (data && data.jstvMedia && data.jstvMedia.url) keep(data.jstvMedia.url, data.jstvMedia.headers, !!data.jstvMedia.verified);
  });
  if (window.fetch) {
    const orig = window.fetch;
    window.fetch = function (input, init) {
      try { keep(typeof input === 'string' ? input : (input && input.url), init && init.headers, false); } catch (e) {}
      return orig.apply(this, arguments);
    };
  }
  if (window.Response && window.Response.prototype && window.Response.prototype.text) {
    const origText = window.Response.prototype.text;
    window.Response.prototype.text = function () {
      const respURL = this && this.url;
      return origText.apply(this, arguments).then((text) => {
        try {
          if (typeof text === 'string' && text.trim().startsWith('#EXTM3U') && respURL) {
            keep(respURL, null, true);
          }
        } catch (e) {}
        return text;
      });
    };
  }
  const xp = XMLHttpRequest && XMLHttpRequest.prototype;
  if (xp) {
    const open = xp.open, setHeader = xp.setRequestHeader, send = xp.send;
    xp.open = function (method, url) { this.__jstvURL = url; return open.apply(this, arguments); };
    xp.setRequestHeader = function (k, v) { (this.__jstvH || (this.__jstvH = {}))[k] = v; return setHeader.apply(this, arguments); };
    xp.send = function () {
      try { keep(this.__jstvURL, this.__jstvH, false); } catch (e) {}
      try {
        this.addEventListener('load', () => {
          try {
            if (typeof this.responseText === 'string' && this.responseText.trim().startsWith('#EXTM3U')) {
              keep(this.responseURL || this.__jstvURL, this.__jstvH, true);
            }
          } catch (e) {}
        });
      } catch (e) {}
      return send.apply(this, arguments);
    };
  }
  const scan = () => {
    const lazyFrames = document.querySelectorAll ? document.querySelectorAll('iframe[loading="lazy"]') : [];
    for (const f of lazyFrames) { try { f.removeAttribute('loading'); } catch (e) {} }
    const iframes = document.querySelectorAll ? document.querySelectorAll('iframe[src]') : [];
    for (const f of iframes) {
      const embedded = decodeIframeMedia(f.getAttribute('src') || f.src);
      if (embedded) keep(embedded, null, true);
    }
    const nodes = document.querySelectorAll ? document.querySelectorAll('video,source,audio') : [];
    for (const el of nodes) {
      const src = el.currentSrc || (el.getAttribute && el.getAttribute('src')) || el.src;
      if (src && !String(src).trim().startsWith('blob:')) keep(src, null, true);
    }
  };
  if (window.PerformanceObserver) {
    try {
      new PerformanceObserver((list) => { for (const entry of list.getEntries()) keep(entry.name, null, false); }).observe({ type: 'resource', buffered: true });
    } catch (e) {}
  }
  const root = document.documentElement || document;
  if (window.MutationObserver && root) new MutationObserver(scan).observe(root, { subtree: true, childList: true, attributes: true });
  scan();
  setInterval(scan, 250);
})();
`
