const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { test } = require('node:test');

const root = path.resolve(__dirname, '..');
const scriptSource = fs.readFileSync(path.join(root, 'webassets', 'web-player.js'), 'utf8');

function createWebPlayerHarness() {
  const listeners = new Map();
  const rpcCalls = [];
  const attrs = new Map();
  const video = {
    paused: false,
    currentTime: 0,
    duration: 0,
    error: null,
    set src(value) { attrs.set('src', value); },
    get src() { return attrs.get('src') || ''; },
    getAttribute(name) { return attrs.has(name) ? attrs.get(name) : null; },
    removeAttribute(name) { attrs.delete(name); },
    canPlayType() { return ''; },
    play() { this.paused = false; return Promise.resolve(); },
    pause() { this.paused = true; },
    load() {},
    addEventListener(name, fn) {
      if (!listeners.has(name)) listeners.set(name, []);
      listeners.get(name).push(fn);
    },
    emit(name) {
      for (const fn of listeners.get(name) || []) fn();
    },
  };

  const titleEl = { textContent: '' };
  const errEl = { textContent: '' };
  const goBtn = { hidden: true, onclick: null };
  const prevBtn = { disabled: false, onclick: null };
  const nextBtn = { disabled: false, onclick: null };
  const closeBtn = { onclick: null };
  const rootEl = {
    className: '',
    hidden: true,
    innerHTML: '',
    querySelector(sel) {
      if (sel === 'video') return video;
      if (sel === '.web-player-title') return titleEl;
      if (sel === '.web-player-err') return errEl;
      if (sel === '.web-player-go') return goBtn;
      if (sel === '[data-act="prev"]') return prevBtn;
      if (sel === '[data-act="next"]') return nextBtn;
      if (sel === '[data-act="close"]') return closeBtn;
      return null;
    },
  };

  const windowObj = {
    __JSTV_WEB__: true,
    Hls: { isSupported: () => false },
    addEventListener() {},
  };

  const ctx = {
    window: windowObj,
    document: {
      body: { append() {} },
      head: { append() {} },
      createElement(tag) {
        if (tag === 'div') return rootEl;
        return { tagName: tag, type: '', className: '', textContent: '', onclick: null };
      },
      querySelector() { return null; },
    },
    location: { href: '' },
    fetch: async (_url, opts) => {
      const payload = JSON.parse(opts.body);
      rpcCalls.push(payload);
      if (payload.method === 'WebAdvance') {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            result: {
              title: 'Show - Ep2',
              url: '/media/ep2.mp4',
              media: 'mp4',
              playlistId: 'pl1',
              index: payload.args[1],
              count: 3,
              progressId: 'prog2',
            },
          }),
        };
      }
      return { ok: true, status: 200, json: async () => ({ result: null }) };
    },
    setTimeout,
    clearTimeout,
    Date,
    Number,
    Array,
    Promise,
    Error,
  };
  vm.runInNewContext(scriptSource, ctx);
  return { windowObj, video, errEl, closeBtn, rpcCalls };
}

test('web player stops auto-advance and preserves progress when playback errors', async () => {
  const { windowObj, video, errEl, rpcCalls } = createWebPlayerHarness();
  windowObj.jstvWebPlay({
    title: 'Show - Ep1',
    url: '/media/ep1.mp4',
    media: 'mp4',
    playlistId: 'pl1',
    index: 0,
    count: 3,
    progressId: 'prog1',
  });
  await new Promise((r) => setImmediate(r));

  video.duration = 120;
  video.emit('loadedmetadata');
  video.currentTime = 45;
  video.emit('timeupdate');
  video.error = { code: 2 };
  video.emit('error');
  video.emit('ended');
  await new Promise((r) => setImmediate(r));

  assert.match(errEl.textContent, /已停止连播/);
  assert.equal(rpcCalls.filter((c) => c.method === 'WebAdvance').length, 0);
  const progressCall = rpcCalls.find((c) => c.method === 'ReportProgress');
  assert.ok(progressCall, 'expected progress to be saved before stopping');
  assert.equal(progressCall.args[1], 45);
  assert.equal(progressCall.args[2], 120);
});

test('web player stops auto-advance on premature ended event', async () => {
  const { windowObj, video, errEl, rpcCalls } = createWebPlayerHarness();
  windowObj.jstvWebPlay({
    title: 'Show - Ep1',
    url: '/media/ep1.mp4',
    media: 'mp4',
    playlistId: 'pl1',
    index: 0,
    count: 3,
    progressId: 'prog1',
  });
  await new Promise((r) => setImmediate(r));

  video.duration = 120;
  video.emit('loadedmetadata');
  video.currentTime = 15;
  video.emit('timeupdate');
  video.emit('ended');
  await new Promise((r) => setImmediate(r));

  assert.match(errEl.textContent, /已停止连播/);
  assert.equal(rpcCalls.filter((c) => c.method === 'WebAdvance').length, 0);
});

test('web player advances to next episode on normal completion', async () => {
  const { windowObj, video, closeBtn, rpcCalls } = createWebPlayerHarness();
  windowObj.jstvWebPlay({
    title: 'Show - Ep1',
    url: '/media/ep1.mp4',
    media: 'mp4',
    playlistId: 'pl1',
    index: 0,
    count: 3,
    progressId: 'prog1',
  });
  await new Promise((r) => setImmediate(r));

  video.duration = 120;
  video.emit('loadedmetadata');
  video.currentTime = 120;
  video.emit('timeupdate');
  video.emit('ended');
  await new Promise((r) => setImmediate(r));

  const adv = rpcCalls.filter((c) => c.method === 'WebAdvance');
  assert.equal(adv.length, 1);
  assert.deepEqual(adv[0].args, ['pl1', 1]);
  closeBtn.onclick();
});
