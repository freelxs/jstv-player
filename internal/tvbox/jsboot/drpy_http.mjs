import http from "node:http";
import https from "node:https";
import dns from "node:dns";
import { parentPort } from "node:worker_threads";

const hosts = {};
try {
  if (process.env.JSTV_HOSTS) Object.assign(hosts, JSON.parse(process.env.JSTV_HOSTS));
} catch { /* ignore a bad map; requests then use normal DNS */ }

let reply = null;
parentPort.on("message", (msg) => {
  if (msg && msg.type === "bind") {
    reply = msg.port;
    return;
  }
  if (!msg || msg.type !== "req" || !reply) return;
  const flag = new Int32Array(msg.sab);
  doReq(msg.url, msg.obj || {}, 0).then((result) => {
    reply.postMessage({ id: msg.id, ok: true, result });
    Atomics.store(flag, 0, 1);
    Atomics.notify(flag, 0);
  }, (err) => {
    reply.postMessage({ id: msg.id, ok: false, error: String(err && err.message || err) });
    Atomics.store(flag, 0, 1);
    Atomics.notify(flag, 0);
  });
});

function timeoutMs(obj) {
  const t = obj && obj.timeout;
  if (typeof t !== "number" || !Number.isFinite(t) || t <= 0) return 20000;
  return t < 1000 ? t * 1000 : t;
}

function hostLookup(hostname, options, cb) {
  const repl = hosts[String(hostname || "").toLowerCase()];
  if (!repl) return dns.lookup(hostname, options, cb);
  if (netIsIP(repl)) return cb(null, repl, repl.includes(":") ? 6 : 4);
  return dns.lookup(repl, options, cb);
}

function netIsIP(value) {
  if (/^\d{1,3}(\.\d{1,3}){3}$/.test(value)) return true;
  return value.includes(":");
}

function repairLatin1(value) {
  if (!value) return value;
  for (const ch of value) {
    if (ch.codePointAt(0) > 255) return value;
  }
  const repaired = Buffer.from(value, "latin1").toString("utf8");
  return repaired.includes("\uFFFD") ? value : repaired;
}

function doReq(rawURL, obj, redirects) {
  return new Promise((resolve, reject) => {
    let target;
    try { target = new URL(rawURL); }
    catch (err) { reject(err); return; }
    if (target.protocol !== "http:" && target.protocol !== "https:") {
      reject(new Error("不支持的地址"));
      return;
    }
    const lib = target.protocol === "https:" ? https : http;
    const headers = { ...(obj.headers || {}) };
    const method = String(obj.method || "GET").toUpperCase();
    let body = obj.body;
    if (body && typeof body === "object") {
      body = new URLSearchParams(body).toString();
      if (!headers["Content-Type"] && !headers["content-type"]) headers["Content-Type"] = "application/x-www-form-urlencoded";
    }
    const req = lib.request({
      protocol: target.protocol,
      hostname: target.hostname,
      port: target.port || (target.protocol === "https:" ? 443 : 80),
      path: target.pathname + target.search,
      method,
      headers,
      timeout: timeoutMs(obj),
      lookup: hostLookup,
    }, (res) => {
      const status = res.statusCode || 0;
      const loc = repairLatin1(res.headers.location);
      if (loc && obj.redirect !== 0 && [301, 302, 303, 307, 308].includes(status) && redirects < 5) {
        res.resume();
        const next = new URL(loc, target).href;
        const nextObj = { ...obj };
        if (status === 303 || (status === 302 && method !== "GET" && method !== "HEAD")) {
          nextObj.method = "GET";
          delete nextObj.body;
        }
        resolve(doReq(next, nextObj, redirects + 1));
        return;
      }
      const chunks = [];
      let size = 0;
      res.on("data", (chunk) => {
        size += chunk.length;
        if (size > 8 * 1024 * 1024) {
          req.destroy(new Error("响应过大"));
          return;
        }
        chunks.push(chunk);
      });
      res.on("end", () => {
        const buf = Buffer.concat(chunks);
        const headersOut = {};
        for (const [key, value] of Object.entries(res.headers)) {
          headersOut[key] = Array.isArray(value) ? value.join(", ") : String(value ?? "");
        }
        resolve({
          code: status,
          content: obj.buffer === 2 ? buf.toString("base64") : buf.toString("utf8"),
          headers: headersOut,
        });
      });
    });
    req.on("error", reject);
    req.on("timeout", () => req.destroy(new Error("请求超时")));
    if (body && method !== "GET" && method !== "HEAD") req.write(String(body));
    req.end();
  });
}
