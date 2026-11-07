// Run against a locally built binary with Playwright supplied by the caller.
// All plugin code and media in this test are project-owned fixtures.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const net = require('node:net');
const { spawn } = require('node:child_process');
const { once } = require('node:events');
const { chromium } = require(process.env.JSTV_PLAYWRIGHT_MODULE || 'playwright');

const binary = path.resolve(process.argv[2] || '.tmp/jstv-player');
const artifacts = path.resolve(process.argv[3] || '.tmp/e2e-web');
fs.mkdirSync(artifacts, { recursive: true });
const dataDir = fs.mkdtempSync(path.join(artifacts, 'data-'));
const evidence = { checks: [], errors: [], mediaRequests: 0 };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function main() {
  let fixtureBase;
  const headers = { 'User-Agent': 'jstv-e2e', Referer: 'https://fixture.example/' };
  const source = () => `let server;exports.start=()=>{server=catServerFactory(async(req,res)=>{
    let b={};if(req.method==='POST'){let text='';for await(const c of req)text+=c;b=JSON.parse(text||'{}')}
    const p=req.url.split('?')[0],send=o=>{res.setHeader('Content-Type','application/json');res.end(JSON.stringify(o))};
    if(p==='/config')return send({video:{sites:[{key:'fixture',name:'Fixture VOD',type:3,api:'/spider/fixture/3'}]}});
    if(p.endsWith('/init'))return send({});
    const vod={vod_id:'movie',vod_name:'Fixture Movie',vod_play_from:'A',vod_play_url:'Episode One$e1'};
    if(p.endsWith('/home'))return send({class:[{type_id:'1',type_name:'Fixture'}],list:[vod]});
    if(p.endsWith('/search')&&b.wd==='fixture-error'){res.statusCode=500;return send({statusCode:500,message:'private fixture token=fixture-secret'})}
    if(p.endsWith('/category')||p.endsWith('/search'))return send({page:1,pagecount:1,list:[vod]});
    if(p.endsWith('/detail'))return send({list:[vod]});
    if(p.endsWith('/play'))return send({parse:0,url:${JSON.stringify(fixtureBase + '/vod.mp4')},header:${JSON.stringify(headers)}});
    res.statusCode=404;res.end('no');});server.listen(0)};exports.stop=()=>server&&server.close();`;

  const fixture = http.createServer((req, res) => {
    const u = new URL(req.url, fixtureBase);
    if (u.pathname === '/channels.m3u') {
      res.setHeader('Content-Type', 'application/x-mpegURL');
      res.end(`#EXTM3U\n#EXTINF:-1 group-title="Fixture TV",Fixture Live\n${fixtureBase}/live.php|User-Agent=jstv-e2e&Referer=https%3A%2F%2Ffixture.example%2F\n`);
      return;
    }
    if (u.pathname === '/index.js') { res.end(source()); return; }
    if (u.pathname.endsWith('.md5') || u.pathname.endsWith('.config.js')) { res.writeHead(404).end(); return; }
    if (req.headers['user-agent'] !== headers['User-Agent'] || req.headers.referer !== headers.Referer) { res.writeHead(403).end('missing fixture headers'); return; }
    let name = path.basename(u.pathname);
    if (name === 'live.php') name = 'stream.m3u8';
    if (!/^(stream\.m3u8|segment-\d+\.ts|vod\.mp4)$/.test(name)) { res.writeHead(404).end(); return; }
    const file = path.join(__dirname, '..', 'testdata', 'playback', name);
    if (!fs.existsSync(file)) { res.writeHead(404).end(); return; }
    const data = fs.readFileSync(file);
    res.setHeader('Content-Type', name.endsWith('m3u8') ? 'application/vnd.apple.mpegurl' : name.endsWith('.ts') ? 'video/mp2t' : 'video/mp4');
    const range = /bytes=(\d+)-(\d*)/.exec(req.headers.range || '');
    if (range) {
      const start = Number(range[1]), end = Math.min(Number(range[2] || data.length - 1), data.length - 1);
      res.setHeader('Content-Range', `bytes ${start}-${end}/${data.length}`);
      res.writeHead(206); res.end(data.subarray(start, end + 1));
    } else res.end(data);
    evidence.mediaRequests++;
  });
  fixture.listen(0, '127.0.0.1'); await once(fixture, 'listening');
  fixtureBase = `http://127.0.0.1:${fixture.address().port}`;

  const reserve = net.createServer(); reserve.listen(0, '127.0.0.1'); await once(reserve, 'listening');
  const port = reserve.address().port; await new Promise((r) => reserve.close(r));
  const base = `http://127.0.0.1:${port}`;
  const child = spawn(binary, ['--web'], { env: { ...process.env, JSTV_MODE: 'web', JSTV_DATA_DIR: dataDir, JSTV_WEB_ADDR: `127.0.0.1:${port}`, JSTV_WEB_USER: 'fixture', JSTV_WEB_PASSWORD: 'fixture-password' }, stdio: ['ignore', 'pipe', 'pipe'] });
  let serverLog = ''; child.stdout.on('data', (b) => { serverLog += b; }); child.stderr.on('data', (b) => { serverLog += b; });
  let browser, page;
  let spawnError;
  child.on('error', (error) => { spawnError = error; });
  try {
    for (let i = 0; i < 150; i++) {
      if (spawnError) throw spawnError;
      if (child.exitCode !== null) throw new Error(`server exited: ${serverLog}`);
      try { if ((await fetch(base + '/healthz')).ok) break; } catch {}
      await sleep(100);
    }
    browser = await chromium.launch({ executablePath: process.env.CHROME || undefined, headless: true, args: ['--no-sandbox', '--autoplay-policy=no-user-gesture-required'] });
    page = await browser.newPage({ viewport: { width: 1280, height: 820 } });
    page.on('pageerror', (e) => evidence.errors.push(e.message));
    await page.goto(base);
    await page.locator('input[name=username]').fill('fixture');
    await page.locator('input[name=password]').fill('fixture-password');
    await page.locator('#login-form button').click();
    await page.locator('#settings[open]').waitFor();
    await page.locator('[data-tab=play]').click();
    await page.locator('#sAllowLanMedia').check();
    await page.locator('#sNode').fill('/nonexistent-node-for-native-test');
    await page.locator('#sSave').click();
    await page.locator('[data-tab=subs]').click();
    await page.locator('#subFormName').fill('Native Fixture');
    await page.locator('#subFormURL').fill(fixtureBase + '/channels.m3u');
    await page.locator('#subFormType').selectOption('live');
    await page.locator('#subFormBtn').click();
    await page.locator('.card', { hasText: 'Fixture Live' }).click();
    await page.locator('.ep', { hasText: 'Fixture Live' }).click();
    await page.waitForFunction(() => { const v = document.querySelector('.web-player video'); return v && v.readyState >= 2 && v.currentTime > 0.5; }, null, { timeout: 20000 });
    evidence.checks.push('native M3U loaded and HLS PHP endpoint decoded without Node');
    await page.screenshot({ path: path.join(artifacts, 'native-live.png') });
    await page.locator('.web-player [data-act=close]').click();
    await page.getByRole('button', { name: '收藏', exact: true }).click();
    await page.locator('#btnFavorites').click();
    await page.locator('.card', { hasText: 'Fixture Live' }).waitFor();
    evidence.checks.push('live favorite displayed through UI');

    await page.locator('#btnSettings').click(); await page.locator('[data-tab=play]').click();
    await page.locator('#sNode').fill(''); await page.locator('#sSave').click(); await page.locator('[data-tab=subs]').click();
    await page.locator('#subFormName').fill('Plugin Fixture'); await page.locator('#subFormURL').fill(fixtureBase + '/index.js'); await page.locator('#subFormType').selectOption('plugin'); await page.locator('#subFormBtn').click(); await page.locator('#sClose').click();
    await page.locator('#subBtn').click(); await page.getByRole('menuitemradio', { name: /Plugin Fixture/ }).click();
    await page.locator('#sourceTrust[open]').waitFor(); await page.locator('#trustApprove').click();
    await page.locator('.card', { hasText: 'Fixture Movie' }).waitFor();
    await page.locator('#q').fill('fixture-error'); await page.locator('#q').press('Enter');
    await page.getByText('所有站点搜索失败', { exact: true }).waitFor();
    assert.ok(!(await page.locator('#view').textContent()).includes('fixture-secret'));
    const streamed = page.waitForResponse((r) => new URL(r.url()).pathname === '/api/stream' && r.status() === 200);
    await page.locator('#q').fill('Fixture Movie'); await page.locator('#q').press('Enter'); await streamed;
    await page.locator('.card', { hasText: 'Fixture Movie' }).click();
    evidence.checks.push('streaming global search recovers after normalized all-site failure');
    await page.locator('.ep', { hasText: 'Episode One' }).click();
    await page.waitForFunction(() => document.querySelector('.web-player video')?.currentTime > 2, null, { timeout: 20000 });
    await page.locator('.web-player video').evaluate((v) => v.pause());
    await page.waitForFunction(async () => (await window.mygo.call('Player.GetHistory', '/spider/fixture/3', 'movie'))?.position > 1);
    const history = await page.evaluate(() => window.mygo.call('Player.GetHistory', '/spider/fixture/3', 'movie'));
    await page.locator('.web-player [data-act=close]').click();
    await page.locator('.ep', { hasText: 'Episode One' }).click();
    await page.waitForFunction((position) => document.querySelector('.web-player video')?.currentTime >= position, history.position, { timeout: 15000 });
    evidence.checks.push('plugin approval, MP4 decode, saved position and resume through UI');
    await page.screenshot({ path: path.join(artifacts, 'vod-resume.png') });
    assert.equal(evidence.errors.length, 0, evidence.errors.join('\n'));
    assert.ok(evidence.mediaRequests > 0);
    evidence.ok = true;
  } catch (error) {
    evidence.ok = false; evidence.failure = error.stack;
    if (page) await page.screenshot({ path: path.join(artifacts, 'failure.png') }).catch(() => {});
    throw error;
  } finally {
    fs.writeFileSync(path.join(artifacts, 'results.json'), JSON.stringify(evidence, null, 2) + '\n');
    fs.writeFileSync(path.join(artifacts, 'server.log'), serverLog);
    if (browser) await browser.close();
    child.kill('SIGTERM');
    await Promise.race([once(child, 'exit'), sleep(6000)]);
    if (child.exitCode === null) child.kill('SIGKILL');
    await new Promise((r) => fixture.close(r));
  }
  console.log(JSON.stringify(evidence));
}
main().catch((e) => { console.error(e); process.exitCode = 1; });
