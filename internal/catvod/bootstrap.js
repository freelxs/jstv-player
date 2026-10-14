// JSTV Player bootstrap: hosts a CatPawOpen-compatible plugin (index.js)
// as the CatPawOpen interface expects, then lets the Go side talk to the
// fastify server it starts over HTTP on 127.0.0.1:$DEV_HTTP_PORT.
const http = require('http');
const path = require('path');
const fs = require('fs');

const entry = process.argv[2];
const port = Number(process.env.DEV_HTTP_PORT || 0);

// The host app ("Dart" side in CatPaw) supplies the HTTP server factory.
globalThis.catServerFactory = (handle) => {
  const server = http.createServer((req, res) => handle(req, res));
  // Bundles listen on 0.0.0.0; keep them on loopback and on our port.
  const listen = server.listen.bind(server);
  server.listen = (...args) => {
    const cb = args.find((a) => typeof a === 'function');
    return listen({ port, host: '127.0.0.1' }, cb);
  };
  server.on('listening', () => {
    console.log('JSTV_READY ' + server.address().port);
  });
  return server;
};
// No native host server: messageToDart() calls become no-ops.
globalThis.catDartServerPort = () => 0;

// Default config shape the bundles read (pans/sites/danmu/color/sitedomain).
let config = { pans: { list: [] }, sites: { list: [] }, danmu: {}, color: [], sitedomain: { list: [] } };
// index.config.js next to index.js, as CatPaw loads it.
const cfgFile = process.env.JSTV_CONFIG_FILE;
if (cfgFile && fs.existsSync(cfgFile)) {
  try {
    const c = require(path.resolve(cfgFile));
    config = Object.assign(config, (c && c.default) || c);
  } catch (e) { console.error('index.config.js 加载失败', e); }
}
try {
  const extra = process.env.JSTV_CONFIG ? JSON.parse(process.env.JSTV_CONFIG) : null;
  if (extra) config = Object.assign(config, extra);
} catch (e) { console.error('bad JSTV_CONFIG', e); }

async function load() {
  try {
    return require(path.resolve(entry));
  } catch (e) {
    if (e instanceof SyntaxError || e.code === 'ERR_REQUIRE_ESM') {
      // ES module bundle: copy to .mjs and import it.
      const mjs = path.resolve(entry) + '.mjs';
      fs.copyFileSync(entry, mjs);
      return await import('file://' + mjs);
    }
    throw e;
  }
}

(async () => {
  const mod = await load();
  const start = mod.start || (mod.default && mod.default.start);
  if (typeof start !== 'function') throw new Error('index.js does not export start()');
  await start(config);
})().catch((e) => {
  console.error('JSTV_FATAL', e && e.stack || e);
  process.exit(3);
});

process.on('unhandledRejection', (e) => console.error('unhandledRejection', e));
process.on('uncaughtException', (e) => console.error('uncaughtException', e));
// Exit when the parent app goes away.
process.stdin.on('end', () => process.exit(0));
process.stdin.resume();
