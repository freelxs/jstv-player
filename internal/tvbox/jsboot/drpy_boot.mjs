import { createServer } from "node:http";
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
import { Worker, receiveMessageOnPort, MessageChannel } from "node:worker_threads";

const { port1, port2 } = new MessageChannel();
const worker = new Worker(new URL("./drpy_http.mjs", import.meta.url));
worker.postMessage({ type: "bind", port: port2 }, [port2]);
worker.on("error", (err) => console.error("drpy http worker:", err && err.stack || err));

let seq = 1;
function timeoutMs(obj) {
  const t = obj && obj.timeout;
  if (typeof t !== "number" || !Number.isFinite(t) || t <= 0) return 20000;
  return t < 1000 ? t * 1000 : t;
}

function req(url, obj) {
  obj = obj || {};
  const id = seq++;
  const sab = new SharedArrayBuffer(4);
  const flag = new Int32Array(sab);
  worker.postMessage({ type: "req", id, url, obj, sab });
  const deadline = Date.now() + timeoutMs(obj) + 1000;
  let msg = null;
  while (!msg) {
    const left = deadline - Date.now();
    if (left <= 0) throw new Error("请求超时: " + url);
    Atomics.wait(flag, 0, 0, Math.min(left, 500));
    msg = receiveMessageOnPort(port1);
    if (msg && msg.message && msg.message.id !== id) msg = null;
    if (!msg && Atomics.load(flag, 0) !== 0) {
      msg = receiveMessageOnPort(port1);
    }
  }
  if (!msg.message.ok) throw new Error(msg.message.error || "请求失败");
  return msg.message.result;
}

globalThis.req = req;
globalThis.fetch = (url, obj) => (req(url, obj).content || "");
globalThis.log = (...args) => console.log(args.map((x) => (typeof x === "string" ? x : JSON.stringify(x))).join(" "));
globalThis.print = globalThis.log;

let ext = process.env.DRPY_EXT_URL || "";
if (!ext && process.env.DRPY_EXT_FILE) {
  const text = readFileSync(process.env.DRPY_EXT_FILE, "utf8");
  ext = process.env.DRPY_EXT_JSON === "1" ? JSON.parse(text) : text;
}

const mod = await import(pathToFileURL(process.env.DRPY_MODULE).href);
const engine = mod.default && typeof mod.default.init === "function" ? mod.default : mod;
if (typeof engine.init !== "function") throw new Error("JavaScript 站点没有导出 init");
engine.init(ext);

function call(body) {
  switch (body.fn) {
    case "home": return engine.home(!!body.filter);
    case "homeVod": return typeof engine.homeVod === "function" ? engine.homeVod() : "";
    case "category": return engine.category(String(body.tid ?? ""), body.pg, body.filter, body.extend || {});
    case "detail": return engine.detail(String(body.id ?? ""));
    case "search": return engine.search(String(body.wd ?? ""), !!body.quick, body.pg || 1);
    case "play": return engine.play(String(body.flag ?? ""), String(body.id ?? ""), body.flags || []);
    default: throw new Error("未知调用 " + body.fn);
  }
}

const server = createServer(async (request, response) => {
  if (request.method !== "POST") {
    response.statusCode = 404;
    response.end("no");
    return;
  }
  const chunks = [];
  for await (const chunk of request) chunks.push(chunk);
  let body = {};
  try { body = JSON.parse(Buffer.concat(chunks).toString("utf8") || "{}"); }
  catch { response.statusCode = 400; response.end(JSON.stringify({ ok: false, error: "bad json" })); return; }
  try {
    const result = call(body);
    response.setHeader("content-type", "application/json; charset=utf-8");
    response.end(JSON.stringify({ ok: true, result: result === undefined ? null : result }));
  } catch (err) {
    response.statusCode = 500;
    response.setHeader("content-type", "application/json; charset=utf-8");
    response.end(JSON.stringify({ ok: false, error: String(err && err.stack || err) }));
  }
});
server.listen(0, "127.0.0.1", () => {
  console.log("DRPY_READY " + server.address().port);
});
