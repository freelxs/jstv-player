// A tiny fake plugin bundle in CatPawOpen shape: exports start/stop and calls
// globalThis.catServerFactory, serving /config and /spider/mock/3/*.
const sites = [{ key: 'nodejs_mock', name: 'Mock', type: 3, api: '/spider/mock/3' }];
let server;
function body(req) {
  return new Promise((r) => { let d = ''; req.on('data', (c) => (d += c)); req.on('end', () => r(d ? JSON.parse(d) : {})); });
}
async function start(config) {
  if (!config || !config.pans) throw new Error('config missing');
  server = globalThis.catServerFactory(async (req, res) => {
    const send = (o) => { res.setHeader('content-type', 'application/json'); res.end(JSON.stringify(o)); };
    const b = req.method === 'POST' ? await body(req) : {};
    const p = req.url.split('?')[0];
    if (p === '/config') return send({ video: { sites } });
    const host = 'http://' + req.headers.host;
    switch (p) {
      case '/spider/mock/3/init': return send({});
      case '/spider/mock/3/home': return send({ class: [{ type_id: '1', type_name: '电影' }], filters: {
        '1': [{ key: 'area', label: '地区', options: [{ value: 'cn', label: '中国大陆' }, { value: 'hk', label: '中国香港' }] }],
      } });
      case '/spider/mock/3/category': return send({ page: b.page, pagecount: 2, filters: b.filters, list: [{ vod_id: 'v' + b.id + '-' + b.page, vod_name: '片' + b.page }] });
      case '/spider/mock/3/detail': return send({ list: [{ vod_id: b.id, vod_name: 'Demo', vod_play_from: 'A$$$B', vod_play_url: '第1集$e1#第2集$e2$$$HD$e3' }] });
      case '/spider/mock/3/play': return send({ parse: 0, url: ['原画', host + '/v/' + b.flag + '/' + b.id + '.m3u8'], header: { Referer: 'https://example.com/' } });
      case '/spider/mock/3/search': return send({ page: 1, list: [{ vod_id: 's1', vod_name: b.wd }] });
    }
    res.statusCode = 404; res.end('not found');
  });
  server.listen(Number(process.env.DEV_HTTP_PORT), '127.0.0.1');
}
async function stop() { server && server.close(); }
module.exports = { start, stop };
