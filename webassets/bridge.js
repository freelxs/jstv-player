// HTTP stand-in for window.mygo. The desktop webview injects mygo before this
// file runs; when that object already exists, web mode stays disabled.
(function () {
  if (window.mygo) return;
  window.__JSTV_WEB__ = true;

  function apiError(status, body) {
    let message = '请求失败（' + status + '）';
    try {
      const parsed = JSON.parse(body);
      if (parsed && parsed.error) message = parsed.error;
    } catch (err) { /* body was not JSON */ }
    const error = new Error(message);
    error.status = status;
    return error;
  }

  async function readBody(response) {
    const text = await response.text();
    if (response.status === 401) {
      location.href = '/login';
      throw apiError(401, text);
    }
    return text;
  }

  async function rpc(method, args) {
    const response = await fetch('/api/call', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json', 'X-JSTV-Request': '1' },
      body: JSON.stringify({ method, args: args || [] }),
    });
    const text = await readBody(response);
    if (!response.ok) throw apiError(response.status, text);
    const data = text ? JSON.parse(text) : {};
    if (data.error) throw new Error(data.error);
    return data.result;
  }

  async function streamSearch(wd, channel) {
    const response = await fetch('/api/stream', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', Accept: 'application/x-ndjson', 'X-JSTV-Request': '1' },
      body: JSON.stringify({ method: 'SearchAll', args: [wd] }),
    });
    if (!response.ok || !response.body) {
      throw apiError(response.status, await readBody(response));
    }
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buf = '';
    while (true) {
      const step = await reader.read();
      if (step.done) break;
      buf += decoder.decode(step.value, { stream: true });
      let nl;
      while ((nl = buf.indexOf('\n')) >= 0) {
        const line = buf.slice(0, nl).trim();
        buf = buf.slice(nl + 1);
        if (!line) continue;
        const msg = JSON.parse(line);
        if (msg.type === 'error') throw new Error(msg.error || '搜索失败');
        if (msg.type === 'item' && channel && typeof channel.onmessage === 'function') channel.onmessage(msg.data);
      }
    }
  }

  function playable(url) {
    return typeof url === 'string' && (url.startsWith('/media/') || url.startsWith(location.origin + '/media/'));
  }

  window.mygo = {
    channel(onmessage) {
      return { __channel: true, onmessage, close() {} };
    },
    async call(method, ...args) {
      const name = String(method || '').replace(/^Player\./, '');
      if (name === 'SearchAll') {
        await streamSearch(args[0], args[1]);
        return null;
      }
      if (name === 'PlayEpisodes') {
        const playback = await rpc('WebPlay', args);
        if (!playback) return [];
        if (playback.action === 'qualities') {
          return (playback.qualities || []).map((item) => ({ name: item.name || '播放', url: item.url, parse: false, media: item.media }));
        }
        if (playback.action === 'page') {
          if (!playback.pageUrl) throw new Error(playback.message || '无法在浏览器打开该网页');
          return [{ name: playback.title || '网页', url: playback.pageUrl, parse: true }];
        }
        if (!playable(playback.url)) throw new Error(playback.message || '没有可播放的地址');
        if (window.jstvWebPlay) window.jstvWebPlay(playback);
        return [];
      }
      if (name === 'Open') {
        const stream = args[0] || {};
        const title = args[1] || stream.name || '';
        if (stream.parse) {
          if (window.jstvWebOpenPage) window.jstvWebOpenPage(stream.url);
          else if (stream.url) window.open(stream.url, '_blank', 'noopener,noreferrer');
          return null;
        }
        if (!playable(stream.url)) throw new Error('没有可播放的地址');
        if (window.jstvWebPlay) {
          window.jstvWebPlay({
            action: 'play',
            title,
            url: stream.url,
            media: stream.media || (/\.m3u8(\?|$)/i.test(stream.url) ? 'hls' : 'file'),
            index: 0,
            count: 1,
          });
        }
        return null;
      }
      if (name === 'PickCustomPlayer') throw new Error('网页模式不能选择本机播放器');
      return rpc(name, args);
    },
  };
})();
