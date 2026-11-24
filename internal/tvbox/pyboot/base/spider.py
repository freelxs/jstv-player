import importlib.util
import json
import os
import re
import sys
import threading

import requests


class Spider:
    _cache_lock = threading.RLock()

    def __init__(self):
        self._site_key = ""
        self._proxy_base = ""
        self._cache_dir = ""
        self._mem_cache = {}
        self._extend = ""

    def getName(self):
        return self.__class__.__name__

    def init(self, extend=""):
        self._extend = extend

    def getDependence(self):
        return []

    def setExtendInfo(self, extend):
        self._extend = extend

    def isVideoFormat(self, url):
        if not url:
            return False
        low = str(url).lower().split("?", 1)[0]
        return any(low.endswith(ext) for ext in (".m3u8", ".mp4", ".flv", ".mkv", ".ts", ".mpd", ".webm", ".avi", ".mov"))

    def manualVideoCheck(self):
        return False

    def destroy(self):
        pass

    def homeContent(self, filter):
        return {}

    def homeVideoContent(self):
        return {}

    def categoryContent(self, tid, pg, filter, extend):
        return {}

    def detailContent(self, ids):
        return {}

    def searchContent(self, key, quick, pg="1"):
        return {}

    def playerContent(self, flag, id, vipFlags):
        return {}

    def localProxy(self, param):
        return [404, "text/plain", b""]

    def action(self, action):
        return ""

    def log(self, msg):
        try:
            sys.stderr.write(f"[py:{self._site_key or self.getName()}] {msg}\n")
            sys.stderr.flush()
        except Exception:
            pass

    def fetch(self, url, params=None, cookies=None, headers=None, timeout=15, verify=True, stream=False, allow_redirects=True, proxies=None):
        return requests.get(
            url,
            params=params,
            cookies=cookies,
            headers=headers,
            timeout=timeout,
            verify=verify,
            stream=stream,
            allow_redirects=allow_redirects,
            proxies=proxies,
        )

    def post(self, url, params=None, data=None, json=None, cookies=None, headers=None, timeout=15, verify=True, stream=False, allow_redirects=True, proxies=None):
        return requests.post(
            url,
            params=params,
            data=data,
            json=json,
            cookies=cookies,
            headers=headers,
            timeout=timeout,
            verify=verify,
            stream=stream,
            allow_redirects=allow_redirects,
            proxies=proxies,
        )

    def html(self, content):
        from lxml import etree
        if isinstance(content, bytes):
            content = content.decode("utf-8", errors="ignore")
        return etree.HTML(content or "")

    def regStr(self, reg, src, group=1):
        if not src:
            return ""
        m = re.search(reg, str(src), re.M | re.S)
        if not m:
            return ""
        try:
            return m.group(group) or ""
        except IndexError:
            return m.group(0) or ""

    def cleanText(self, src):
        if src is None:
            return ""
        return re.sub(r"[\x00-\x1f\x7f\u200b-\u200f\ufeff]+", "", str(src)).strip()

    def str2json(self, s):
        return json.loads(s)

    def json2str(self, obj):
        return json.dumps(obj, ensure_ascii=False)

    def getProxyUrl(self, local=True):
        if self._proxy_base:
            return f"{self._proxy_base}?do=py"
        return ""

    def loadModule(self, name, fileName):
        spec = importlib.util.spec_from_file_location(name, fileName)
        if spec is None or spec.loader is None:
            raise ImportError(f"cannot load module {name} from {fileName}")
        mod = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(mod)
        return mod

    def _cache_path(self):
        if not self._cache_dir:
            return ""
        safe = re.sub(r"[^a-zA-Z0-9_.-]+", "_", self._site_key or self.getName())
        return os.path.join(self._cache_dir, f"cache_{safe}.json")

    def getCache(self, key):
        with self._cache_lock:
            if key in self._mem_cache:
                return self._mem_cache[key]
            path = self._cache_path()
            if path and os.path.isfile(path):
                try:
                    with open(path, "r", encoding="utf-8") as fp:
                        data = json.load(fp)
                    if isinstance(data, dict):
                        self._mem_cache.update(data)
                        return self._mem_cache.get(key)
                except Exception:
                    pass
            return None

    def setCache(self, key, value):
        with self._cache_lock:
            self._mem_cache[key] = value
            path = self._cache_path()
            if path:
                try:
                    os.makedirs(os.path.dirname(path), exist_ok=True)
                    with open(path, "w", encoding="utf-8") as fp:
                        json.dump(self._mem_cache, fp, ensure_ascii=False)
                except Exception:
                    pass

    def delCache(self, key):
        with self._cache_lock:
            self._mem_cache.pop(key, None)
            path = self._cache_path()
            if path:
                try:
                    with open(path, "w", encoding="utf-8") as fp:
                        json.dump(self._mem_cache, fp, ensure_ascii=False)
                except Exception:
                    pass
