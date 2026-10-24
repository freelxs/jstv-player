// In-browser player for web deployment. Loaded only by the web server, after app.js.
(function () {
  if (!window.__JSTV_WEB__) return;

  const root = document.createElement('div');
  root.className = 'web-player';
  root.hidden = true;
  root.innerHTML = '<div class="web-player-bar"><strong class="web-player-title"></strong><span class="web-player-err"></span><button type="button" data-act="prev">上一集</button><button type="button" data-act="next">下一集</button><button type="button" data-act="close">关闭</button></div><video controls playsinline></video><button type="button" class="web-player-go" hidden>点击播放</button>';
  document.body.append(root);
  const video = root.querySelector('video');
  const titleEl = root.querySelector('.web-player-title');
  const errEl = root.querySelector('.web-player-err');
  const goBtn = root.querySelector('.web-player-go');
  const prevBtn = root.querySelector('[data-act="prev"]');
  const nextBtn = root.querySelector('[data-act="next"]');
  let hls = null;
  let hlsPromise = null;
  let current = null;
  let token = 0;
  let suppressVideoError = false;

  function pageToast(message, isError) {
    const toast = document.querySelector('#toast');
    if (!toast) return;
    toast.hidden = false;
    toast.className = 'toast' + (isError ? ' err' : '');
    toast.textContent = message;
    clearTimeout(pageToast.timer);
    pageToast.timer = setTimeout(() => { toast.hidden = true; }, 5000);
  }

  function loadHls() {
    if (window.Hls) return Promise.resolve();
    if (!hlsPromise) {
      hlsPromise = new Promise((resolve, reject) => {
        const script = document.createElement('script');
        script.src = '/vendor/hls.min.js';
        script.onload = () => resolve();
        script.onerror = () => reject(new Error('无法加载播放器组件'));
        document.head.append(script);
      });
    }
    return hlsPromise;
  }

  function destroyHls() {
    suppressVideoError = true;
    if (hls) {
      hls.destroy();
      hls = null;
    }
    video.pause();
    video.removeAttribute('src');
    video.load();
  }

  function showError(message) {
    errEl.textContent = message || '播放失败';
  }

  function syncButtons() {
    const hasList = !!(current && current.playlistId);
    prevBtn.disabled = !hasList || current.index <= 0;
    nextBtn.disabled = !hasList || current.index + 1 >= current.count;
  }

  function tryPlay() {
    goBtn.hidden = true;
    const pending = video.play();
    if (pending && typeof pending.catch === 'function') {
      pending.catch(() => { goBtn.hidden = false; });
    }
  }

  async function attach(playback) {
    const mine = ++token;
    errEl.textContent = '';
    goBtn.hidden = true;
    destroyHls();
    await loadHls();
    if (mine !== token) return;
    const url = playback.url;
    const useHls = playback.media === 'hls' || /\.m3u8(\?|$)/i.test(url);
    suppressVideoError = false;
    if (useHls && window.Hls && window.Hls.isSupported()) {
      hls = new window.Hls({ enableWorker: false, lowLatencyMode: false });
      hls.on(window.Hls.Events.ERROR, (_, data) => {
        if (!data || !data.fatal || mine !== token) return;
        showError('播放失败，请换一条线路或稍后重试');
      });
      hls.on(window.Hls.Events.MANIFEST_PARSED, () => { if (mine === token) tryPlay(); });
      hls.loadSource(url);
      hls.attachMedia(video);
      return;
    }
    if (useHls && video.canPlayType('application/vnd.apple.mpegurl')) {
      video.src = url;
      tryPlay();
      return;
    }
    video.src = url;
    tryPlay();
  }

  async function rpc(method, args) {
    const response = await fetch('/api/call', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json', 'X-JSTV-Request': '1' },
      body: JSON.stringify({ method, args }),
    });
    const data = await response.json().catch(() => ({}));
    if (response.status === 401) {
      location.href = '/login';
      throw new Error('未登录');
    }
    if (!response.ok || data.error) throw new Error(data.error || '请求失败');
    return data.result;
  }

  function show(playback) {
    current = playback;
    titleEl.textContent = playback.title || '正在播放';
    root.hidden = false;
    syncButtons();
    attach(playback).catch((err) => showError(err.message || '播放失败'));
  }

  async function go(index) {
    if (!current || !current.playlistId) return;
    try {
      const next = await rpc('WebAdvance', [current.playlistId, index]);
      show(next);
    } catch (err) {
      showError(err.message || '换集失败');
    }
  }

  prevBtn.onclick = () => { if (current) go(current.index - 1); };
  nextBtn.onclick = () => { if (current) go(current.index + 1); };
  root.querySelector('[data-act="close"]').onclick = () => {
    token++;
    destroyHls();
    root.hidden = true;
    current = null;
  };
  goBtn.onclick = () => tryPlay();
  video.addEventListener('ended', () => {
    if (!current || !current.playlistId || current.index + 1 >= current.count) return;
    go(current.index + 1);
  });
  video.addEventListener('error', () => {
    if (suppressVideoError) return;
    if (!video.getAttribute('src') && !hls) return;
    if (!errEl.textContent) showError('浏览器无法解码这个流，请换一条线路');
  });

  window.jstvWebPlay = show;
  window.jstvWebOpenPage = (url) => {
    if (!url) {
      pageToast('没有可打开的网页地址', true);
      return;
    }
    const opened = window.open(url, '_blank', 'noopener,noreferrer');
    if (!opened) pageToast('浏览器拦截了弹窗。请允许本站弹窗后重试。', true);
  };

  const foot = document.querySelector('.sidebar-foot');
  if (foot) {
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'ghost icon-text';
    button.textContent = '退出登录';
    button.onclick = async () => {
      try {
        await fetch('/api/logout', {
          method: 'POST',
          credentials: 'same-origin',
          headers: { 'Content-Type': 'application/json', Accept: 'application/json', 'X-JSTV-Request': '1' },
          body: '{}',
        });
      } finally {
        location.href = '/login';
      }
    };
    foot.append(button);
  }
  const playPane = document.querySelector('[data-pane="play"]');
  if (playPane) {
    const note = document.createElement('p');
    note.className = 'hint';
    note.textContent = '网页模式在浏览器里播放 HLS / MP4，不会启动 mpv、PotPlayer 或其他本机播放器。';
    playPane.prepend(note);
  }
})();
