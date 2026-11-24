import hashlib
import http.server
import importlib.util
import inspect
import json
import os
import socket
import sys
import threading
import traceback
import types
import urllib.parse

BOOT_DIR = os.path.dirname(os.path.abspath(__file__))
COMPAT_DIR = os.path.join(BOOT_DIR, "compat")
if BOOT_DIR not in sys.path:
    sys.path.insert(0, BOOT_DIR)
if COMPAT_DIR not in sys.path:
    sys.path.append(COMPAT_DIR)

if "java" not in sys.modules:
    _java = types.ModuleType("java")
    _java_lang = types.ModuleType("java.lang")
    _java.lang = _java_lang
    sys.modules["java"] = _java
    sys.modules["java.lang"] = _java_lang

HOSTS = {}
try:
    raw_hosts = os.environ.get("JSTV_HOSTS", "")
    if raw_hosts:
        parsed = json.loads(raw_hosts)
        if isinstance(parsed, dict):
            HOSTS = {str(k).lower(): str(v) for k, v in parsed.items() if k and v}
except Exception:
    HOSTS = {}

if HOSTS:
    _orig_getaddrinfo = socket.getaddrinfo

    def _patched_getaddrinfo(host, port, family=0, type=0, proto=0, flags=0):
        if isinstance(host, str):
            repl = HOSTS.get(host.lower())
            if repl:
                host = repl
        return _orig_getaddrinfo(host, port, family, type, proto, flags)

    socket.getaddrinfo = _patched_getaddrinfo

from base.spider import Spider as BaseSpider

TOKEN = os.environ.get("JSTV_PY_TOKEN", "")
CACHE_DIR = os.environ.get("JSTV_PY_CACHE", os.path.join(BOOT_DIR, "cache"))
os.makedirs(CACHE_DIR, exist_ok=True)

_spiders_lock = threading.RLock()
_spiders = {}
_site_locks = {}


def _get_site_lock(site_key):
    with _spiders_lock:
        lock = _site_locks.get(site_key)
        if lock is None:
            lock = threading.RLock()
            _site_locks[site_key] = lock
        return lock


def _load_spider(site_key, script_path, ext, proxy_base):
    lock = _get_site_lock(site_key)
    with lock:
        entry = _spiders.get(site_key)
        if entry is not None and entry["script"] == script_path:
            if proxy_base:
                entry["instance"]._proxy_base = proxy_base
            return entry["instance"]

        script_dir = os.path.dirname(os.path.abspath(script_path))
        if script_dir and script_dir not in sys.path:
            sys.path.insert(0, script_dir)

        digest = hashlib.sha256(f"{site_key}:{script_path}".encode("utf-8")).hexdigest()[:16]
        mod_name = f"tvbox_py_{digest}"
        spec = importlib.util.spec_from_file_location(mod_name, script_path)
        if spec is None or spec.loader is None:
            raise RuntimeError(f"无法加载 Python 站点脚本: {script_path}")
        mod = importlib.util.module_from_spec(spec)
        sys.modules[mod_name] = mod
        spec.loader.exec_module(mod)

        spider_cls = getattr(mod, "Spider", None)
        if spider_cls is None or not isinstance(spider_cls, type):
            for attr in vars(mod).values():
                if isinstance(attr, type) and issubclass(attr, BaseSpider) and attr is not BaseSpider:
                    spider_cls = attr
                    break
        if spider_cls is None:
            raise RuntimeError("Python 脚本未定义 Spider 类")

        inst = spider_cls()
        inst._site_key = site_key
        inst._proxy_base = proxy_base or ""
        inst._cache_dir = CACHE_DIR

        ext_val = "" if ext is None else ext
        if not isinstance(ext_val, str):
            ext_val = json.dumps(ext_val, ensure_ascii=False)
        inst._extend = ext_val

        init_fn = getattr(inst, "init", None)
        if callable(init_fn):
            try:
                sig = inspect.signature(init_fn)
                params = [
                    p for p in sig.parameters.values()
                    if p.kind in (inspect.Parameter.POSITIONAL_ONLY, inspect.Parameter.POSITIONAL_OR_KEYWORD)
                ]
                has_var = any(p.kind == inspect.Parameter.VAR_POSITIONAL for p in sig.parameters.values())
                if len(params) == 0 and not has_var:
                    init_fn()
                else:
                    init_fn(ext_val)
            except ValueError:
                init_fn(ext_val)

        _spiders[site_key] = {"script": script_path, "instance": inst}
        return inst


def _invoke(inst, fn, args):
    args = args or {}
    if fn == "init":
        return {}
    if fn == "home":
        return inst.homeContent(bool(args.get("filter", True)))
    if fn == "homeVod":
        hv = getattr(inst, "homeVideoContent", None)
        return hv() if callable(hv) else {}
    if fn == "category":
        tid = str(args.get("tid", ""))
        pg = str(args.get("pg", "1"))
        flt = bool(args.get("filter", True))
        ext = args.get("extend") or {}
        if not isinstance(ext, dict):
            ext = {}
        return inst.categoryContent(tid, pg, flt, ext)
    if fn == "detail":
        vid = str(args.get("id", ""))
        return inst.detailContent([vid])
    if fn == "search":
        wd = str(args.get("wd", ""))
        quick = bool(args.get("quick", False))
        pg = str(args.get("pg", "1"))
        fn_obj = inst.searchContent
        try:
            sig = inspect.signature(fn_obj)
            params = [
                p for p in sig.parameters.values()
                if p.kind in (inspect.Parameter.POSITIONAL_ONLY, inspect.Parameter.POSITIONAL_OR_KEYWORD)
            ]
            has_var = any(p.kind == inspect.Parameter.VAR_POSITIONAL for p in sig.parameters.values())
            if len(params) >= 3 or has_var:
                return fn_obj(wd, quick, pg)
            return fn_obj(wd, quick)
        except ValueError:
            return fn_obj(wd, quick)
    if fn == "play":
        flag = str(args.get("flag", ""))
        pid = str(args.get("id", ""))
        vip = args.get("flags") or []
        if not isinstance(vip, list):
            vip = []
        return inst.playerContent(flag, pid, vip)
    if fn == "action":
        act = getattr(inst, "action", None)
        return act(str(args.get("action", ""))) if callable(act) else ""
    raise ValueError(f"未知调用: {fn}")


class _Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, format, *args):
        return

    def _authorized(self):
        if not TOKEN:
            return True
        return self.headers.get("X-JSTV-Token", "") == TOKEN

    def _write_json(self, status, payload):
        body = json.dumps(payload, ensure_ascii=False, default=str).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        if not self._authorized():
            self._write_json(403, {"ok": False, "error": "forbidden"})
            return
        length = int(self.headers.get("Content-Length") or "0")
        raw = self.rfile.read(length) if length > 0 else b"{}"
        try:
            req = json.loads(raw.decode("utf-8"))
        except Exception:
            self._write_json(400, {"ok": False, "error": "bad json"})
            return
        try:
            site = str(req.get("site", ""))
            script = str(req.get("script", ""))
            ext = req.get("ext", "")
            proxy_base = str(req.get("proxy_base", ""))
            fn = str(req.get("fn", ""))
            args = req.get("args") or {}
            inst = _load_spider(site, script, ext, proxy_base)
            result = _invoke(inst, fn, args)
            self._write_json(200, {"ok": True, "result": result})
        except Exception as exc:
            err_msg = f"{type(exc).__name__}: {exc}"
            sys.stderr.write(traceback.format_exc() + "\n")
            sys.stderr.flush()
            self._write_json(500, {"ok": False, "error": err_msg})

    def do_GET(self):
        if not self._authorized():
            self._write_json(403, {"ok": False, "error": "forbidden"})
            return
        parsed = urllib.parse.urlsplit(self.path)
        if parsed.path != "/proxy":
            self._write_json(404, {"ok": False, "error": "not found"})
            return
        qs = urllib.parse.parse_qs(parsed.query, keep_blank_values=True)
        params = {k: (v[0] if v else "") for k, v in qs.items()}
        site = params.pop("_site", "")
        with _spiders_lock:
            entry = _spiders.get(site)
        if entry is None:
            self._write_json(404, {"ok": False, "error": "spider not loaded"})
            return
        try:
            res = entry["instance"].localProxy(params)
            if not isinstance(res, (list, tuple)) or len(res) < 3:
                self._write_json(502, {"ok": False, "error": "invalid localProxy response"})
                return
            status = int(res[0])
            ctype = str(res[1] or "application/octet-stream")
            data = res[2]
            extra_headers = res[3] if len(res) >= 4 and isinstance(res[3], dict) else {}
            if data is None:
                body = b""
            elif isinstance(data, bytes):
                body = data
            elif isinstance(data, bytearray):
                body = bytes(data)
            else:
                body = str(data).encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", ctype)
            for hk, hv in extra_headers.items():
                if hk and hv is not None and hk.lower() not in ("content-length", "transfer-encoding"):
                    self.send_header(str(hk), str(hv))
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        except Exception as exc:
            sys.stderr.write(traceback.format_exc() + "\n")
            sys.stderr.flush()
            self._write_json(502, {"ok": False, "error": str(exc)})


class _Server(http.server.ThreadingHTTPServer):
    allow_reuse_address = True
    daemon_threads = True


def main():
    srv = _Server(("127.0.0.1", 0), _Handler)
    port = srv.server_address[1]
    sys.stdout.write(f"PY_READY {port}\n")
    sys.stdout.flush()
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        srv.server_close()


if __name__ == "__main__":
    main()
