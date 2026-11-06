import cryptoFromCJS from "./crypto-js.cjs";
import nodeRSAFromCJS from "./node-rsa.cjs";
import pakoFromCJS from "./pako.min.cjs";
import jinjaFromCJS from "./jinja.cjs";
import json5FromCJS from "./json5.cjs";
import cheerio from "./cheerio.min.js";

function unwrap(mod) {
  if (mod && typeof mod === "object" && mod.default && mod.__esModule) return mod.default;
  return mod;
}

const cryptoJS = unwrap(cryptoFromCJS);
globalThis.CryptoJS = cryptoJS.CryptoJS || cryptoJS;

const nodeRSA = unwrap(nodeRSAFromCJS);
globalThis.NODERSA = nodeRSA.NODERSA || nodeRSA;

const pakoNS = unwrap(pakoFromCJS);
globalThis.pako = typeof pakoNS.gzip === "function" ? pakoNS : (pakoNS.default || pakoNS);

const jinjaNS = unwrap(jinjaFromCJS);
globalThis.jinja = typeof jinjaNS.render === "function" ? jinjaNS : (jinjaNS.jinja || jinjaNS);

const json5NS = unwrap(json5FromCJS);
globalThis.JSON5 = typeof json5NS.parse === "function" ? json5NS : (json5NS.default || json5NS);

const memory = new Map();
globalThis.local = {
  set(scope, key, value) { memory.set(scope + "\0" + key, value == null ? "" : String(value)); },
  get(scope, key) { return memory.get(scope + "\0" + key) || ""; },
  delete(scope, key) { memory.delete(scope + "\0" + key); },
};

globalThis.joinUrl = function joinUrl(from, now) {
  try { return new URL(now || "", from || undefined).href; }
  catch { return now || from || ""; }
};

function ruleParts(rule) {
  return String(rule || "").split("&&").map((part) => part.trim()).filter(Boolean);
}

function isAttribute(token) {
  if (!token || token === "Text" || token === "text" || token === "Html" || token === "html") return false;
  if (/[.#\s:[\]>]/.test(token)) return false;
  return !/^(html|body|div|span|a|img|p|ul|li|ol|video|source|script|table|tr|td|h[1-6]|input|meta|link)$/i.test(token);
}

function narrow(selection, token) {
  let eq = null;
  let sel = token;
  const indexed = sel.match(/:eq\((\d+)\)$/);
  if (indexed) {
    eq = Number(indexed[1]);
    sel = sel.slice(0, indexed.index);
  } else if (sel.endsWith(":first")) {
    eq = 0;
    sel = sel.slice(0, -6);
  } else if (sel.endsWith(":last")) {
    eq = -1;
    sel = sel.slice(0, -5);
  }
  const found = selection.find(sel).add(selection.filter(sel));
  if (eq === -1) return found.last();
  if (eq != null) return found.eq(eq);
  return found;
}

function query(html, rule, dropLast) {
  const $ = cheerio.load(typeof html === "string" ? html : "");
  const parts = ruleParts(rule);
  const chain = dropLast ? parts.slice(0, -1) : parts;
  let cur = $.root();
  for (const part of chain) cur = narrow(cur, part);
  return { $, cur, parts };
}

globalThis.pdfa = function pdfa(html, rule) {
  if (html == null || !rule) return [];
  const { $, cur } = query(html, rule, false);
  const out = [];
  cur.each((_, el) => out.push($.html(el)));
  return out;
};

globalThis.pdfh = function pdfh(html, rule) {
  if (html == null || !rule) return "";
  const parts = ruleParts(rule);
  if (!parts.length) return "";
  const last = parts[parts.length - 1];
  const { $, cur } = query(html, rule, isAttribute(last) || /^(text|html)$/i.test(last));
  const el = cur.first();
  if (!el.length) return "";
  if (/^text$/i.test(last)) return el.text().replace(/\s+/g, " ").trim();
  if (/^html$/i.test(last)) return $.html(el) || "";
  if (isAttribute(last)) {
    const own = el.attr(last);
    if (own) return own;
    const nested = el.find("[" + last + "]").first();
    return nested.attr(last) || "";
  }
  return el.text().replace(/\s+/g, " ").trim();
};

globalThis.pd = function pd(html, rule, base) {
  const value = globalThis.pdfh(html, rule);
  if (!value) return "";
  if (/^[a-z][a-z0-9+.-]*:/i.test(value)) return value;
  if (!base) return value;
  try { return new URL(value, base).href; }
  catch { return value; }
};
