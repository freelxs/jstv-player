const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawn, spawnSync } = require('node:child_process');

function chromePath() {
  if (process.env.CHROME) return process.env.CHROME;
  for (const name of ['google-chrome', 'google-chrome-stable', 'chromium', 'chromium-browser']) {
    const found = spawnSync('bash', ['-lc', `command -v ${name}`], { encoding: 'utf8' });
    if (found.status === 0 && found.stdout.trim()) return found.stdout.trim();
  }
  return '';
}

// A square code image must stay fully visible inside a short, narrow frame.
// The catalog poster crops with a 2:3 cover box; a configuration code cannot.
function dumpDOM(chrome, args) {
  return new Promise((resolve, reject) => {
    const child = spawn(chrome, args, { stdio: ['ignore', 'pipe', 'ignore'] });
    let out = '';
    const timer = setTimeout(() => { child.kill('SIGKILL'); resolve(out); }, 45000);
    child.stdout.on('data', (buf) => {
      out += buf;
      if (out.includes('id="result"')) {
        clearTimeout(timer);
        child.kill('SIGKILL');
        resolve(out);
      }
    });
    child.on('error', (err) => { clearTimeout(timer); reject(err); });
    child.on('exit', () => { clearTimeout(timer); resolve(out); });
  });
}

test('configuration code image stays fully visible in a constrained frame', async (t) => {
  const chrome = chromePath();
  if (!chrome) {
    t.skip('no Chrome or Chromium on PATH');
    return;
  }
  const css = fs.readFileSync(path.join(__dirname, 'style.css'), 'utf8');
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'jstv-config-page-'));
  const htmlPath = path.join(dir, 'layout.html');
  const svg = `<svg xmlns='http://www.w3.org/2000/svg' width='200' height='200'><rect width='200' height='200' fill='%23000'/><rect x='0' y='0' width='20' height='20' fill='%23fff'/></svg>`;
  const src = `data:image/svg+xml,${svg}`;
  fs.writeFileSync(htmlPath, `<!doctype html><meta charset="utf-8"><style>${css}</style>
<body style="width:420px;margin:0">
<div class="grid">
  <div class="card"><div class="poster code" id="code"><img id="codeimg" alt="" src="${src}"></div></div>
  <div class="card"><div class="poster" id="plain"><img id="plainimg" alt="" src="${src}"></div></div>
</div>
<div id="limit" style="width:120px;height:80px;overflow:auto">
  <div class="poster code" id="limited"><img id="limitedimg" alt="" src="${src}"></div>
</div>
<script>
function coverage(img) {
  const box = img.getBoundingClientRect();
  const fit = getComputedStyle(img).objectFit;
  const nw = img.naturalWidth || 200, nh = img.naturalHeight || 200;
  const scale = fit === 'contain' ? Math.min(box.width / nw, box.height / nh) : Math.max(box.width / nw, box.height / nh);
  const shownW = box.width / scale, shownH = box.height / scale;
  return { fit, full: shownW >= nw - 1 && shownH >= nh - 1, boxW: box.width, boxH: box.height };
}
function report() {
  const code = document.getElementById('code');
  const limited = document.getElementById('limited');
  const codeBox = code.getBoundingClientRect();
  const limit = document.getElementById('limit');
  const result = {
    code: coverage(document.getElementById('codeimg')),
    plain: coverage(document.getElementById('plainimg')),
    square: Math.abs(codeBox.width / codeBox.height - 1) < 0.08,
    codeOverflow: getComputedStyle(code).overflowY,
    scrolls: limit.scrollHeight >= limited.getBoundingClientRect().height - 1 && getComputedStyle(limit).overflowY === 'auto',
  };
  const pre = document.createElement('pre');
  pre.id = 'result';
  pre.textContent = JSON.stringify(result);
  document.body.appendChild(pre);
}
report();
</script>
`);
  const userData = path.join(dir, 'chrome');
  const out = await dumpDOM(chrome, [
    '--headless=new', '--disable-gpu', '--no-sandbox', '--disable-dev-shm-usage',
    `--user-data-dir=${userData}`, '--virtual-time-budget=2000', '--dump-dom', htmlPath,
  ]);
  const marker = out.match(/<pre id="result">([^<]*)<\/pre>/);
  assert.ok(marker, out.slice(0, 500));
  const result = JSON.parse(marker[1]);
  assert.equal(result.code.fit, 'contain');
  assert.equal(result.code.full, true);
  assert.equal(result.square, true);
  assert.equal(result.codeOverflow, 'auto');
  assert.equal(result.plain.fit, 'cover');
  assert.equal(result.plain.full, false);
  assert.equal(result.scrolls, true);
});
