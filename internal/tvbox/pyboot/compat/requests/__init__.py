import gzip
import http.cookiejar
import json as _json
import re
import ssl
import urllib.error
import urllib.parse
import urllib.request
import zlib

import urllib3


class _Packages:
    pass


packages = _Packages()
packages.urllib3 = urllib3


class RequestException(Exception):
    pass


class HTTPError(RequestException):
    pass


class ConnectionError(RequestException):
    pass


class Timeout(RequestException):
    pass


class TooManyRedirects(RequestException):
    pass


class _Exceptions:
    RequestException = RequestException
    HTTPError = HTTPError
    ConnectionError = ConnectionError
    Timeout = Timeout
    TooManyRedirects = TooManyRedirects


exceptions = _Exceptions()


class CaseInsensitiveDict(dict):
    def __init__(self, data=None, **kwargs):
        super().__init__()
        self._store = {}
        if data:
            self.update(data)
        if kwargs:
            self.update(kwargs)

    def __setitem__(self, key, value):
        lk = str(key).lower()
        self._store[lk] = (str(key), value)
        super().__setitem__(str(key), value)

    def __getitem__(self, key):
        return self._store[str(key).lower()][1]

    def __delitem__(self, key):
        lk = str(key).lower()
        orig, _ = self._store.pop(lk)
        super().pop(orig, None)

    def __contains__(self, key):
        return str(key).lower() in self._store

    def get(self, key, default=None):
        entry = self._store.get(str(key).lower())
        return entry[1] if entry is not None else default

    def pop(self, key, *args):
        lk = str(key).lower()
        if lk in self._store:
            orig, val = self._store.pop(lk)
            super().pop(orig, None)
            return val
        if args:
            return args[0]
        raise KeyError(key)

    def update(self, other=(), **kwargs):
        if hasattr(other, "items"):
            for k, v in other.items():
                self[k] = v
        else:
            for k, v in other:
                self[k] = v
        for k, v in kwargs.items():
            self[k] = v

    def copy(self):
        return CaseInsensitiveDict(self._store.values())


class CookieDict(dict):
    def get_dict(self):
        return dict(self)

    def set(self, name, value, **kwargs):
        self[name] = value


class _Utils:
    @staticmethod
    def dict_from_cookiejar(cj):
        if isinstance(cj, dict):
            return dict(cj)
        out = {}
        for c in cj:
            out[c.name] = c.value
        return out

    @staticmethod
    def cookiejar_from_dict(d):
        return CookieDict(d or {})

    @staticmethod
    def quote(s, safe="/"):
        return urllib.parse.quote(str(s), safe=safe)

    @staticmethod
    def unquote(s):
        return urllib.parse.unquote(str(s))


utils = _Utils()


class _NoRedirectHandler(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class Response:
    def __init__(self, url, status_code, headers, content, cookies=None, encoding=None):
        self.url = url
        self.status_code = int(status_code)
        self.headers = CaseInsensitiveDict(headers or {})
        self.content = content if isinstance(content, bytes) else bytes(content or b"")
        self.cookies = CookieDict(cookies or {})
        self.encoding = encoding or self._detect_header_encoding()
        self.reason = ""
        self.history = []

    def _detect_header_encoding(self):
        ct = self.headers.get("content-type", "")
        m = re.search(r"charset=([^\s;\"']+)", ct, re.I)
        if m:
            return m.group(1).strip()
        return None

    @property
    def apparent_encoding(self):
        if not self.content:
            return "utf-8"
        head = self.content[:2048].decode("latin-1", errors="ignore")
        m = re.search(r"charset=[\"']?([a-zA-Z0-9_-]+)", head, re.I)
        if m:
            return m.group(1)
        try:
            self.content.decode("utf-8")
            return "utf-8"
        except UnicodeDecodeError:
            return "gb18030"

    @property
    def ok(self):
        return 200 <= self.status_code < 400

    @property
    def text(self):
        enc = self.encoding or self.apparent_encoding or "utf-8"
        low = enc.lower()
        if low in ("gbk", "gb2312", "gb_2312", "gb_2312-80"):
            enc = "gb18030"
        try:
            return self.content.decode(enc, errors="replace")
        except LookupError:
            return self.content.decode("utf-8", errors="replace")

    def json(self, **kwargs):
        return _json.loads(self.text, **kwargs)

    def raise_for_status(self):
        if self.status_code >= 400:
            raise HTTPError(f"HTTP {self.status_code} for url: {self.url}")

    def iter_content(self, chunk_size=8192):
        for i in range(0, len(self.content), chunk_size):
            yield self.content[i : i + chunk_size]

    def close(self):
        return None

    def __enter__(self):
        return self

    def __exit__(self, *args):
        return False


def _decompress(raw, encoding_header):
    enc = (encoding_header or "").lower().strip()
    if not raw:
        return raw
    try:
        if enc == "gzip" or (not enc and raw[:2] == b"\x1f\x8b"):
            return gzip.decompress(raw)
        if enc == "deflate":
            try:
                return zlib.decompress(raw)
            except zlib.error:
                return zlib.decompress(raw, -zlib.MAX_WBITS)
    except Exception:
        pass
    return raw


def _merge_params(url, params):
    if not params:
        return url
    parts = urllib.parse.urlsplit(url)
    query_items = urllib.parse.parse_qsl(parts.query, keep_blank_values=True)
    if isinstance(params, dict):
        for k, v in params.items():
            if v is None:
                continue
            if isinstance(v, (list, tuple)):
                for item in v:
                    query_items.append((str(k), str(item)))
            else:
                query_items.append((str(k), str(v)))
    elif isinstance(params, (list, tuple)):
        for k, v in params:
            query_items.append((str(k), str(v)))
    elif isinstance(params, str):
        query_items.extend(urllib.parse.parse_qsl(params, keep_blank_values=True))
    new_query = urllib.parse.urlencode(query_items, doseq=True)
    return urllib.parse.urlunsplit((parts.scheme, parts.netloc, parts.path, new_query, parts.fragment))


def _encode_url_non_ascii(url):
    parts = urllib.parse.urlsplit(url)
    path = urllib.parse.quote(urllib.parse.unquote(parts.path), safe="/:@!$&'()*+,;=-._~%")
    return urllib.parse.urlunsplit((parts.scheme, parts.netloc, path, parts.query, parts.fragment))


def _normalize_timeout(timeout):
    if timeout is None:
        return 15
    if isinstance(timeout, (tuple, list)) and timeout:
        return float(timeout[-1])
    return float(timeout)


class Session:
    def __init__(self):
        self.headers = CaseInsensitiveDict({
            "User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
            "Accept-Encoding": "gzip, deflate",
            "Accept": "*/*",
        })
        self.cookies = CookieDict()
        self._jar = http.cookiejar.CookieJar()
        self.verify = True
        self.proxies = {}

    def mount(self, prefix, adapter):
        return None

    def request(
        self,
        method,
        url,
        params=None,
        data=None,
        headers=None,
        cookies=None,
        files=None,
        auth=None,
        timeout=15,
        allow_redirects=True,
        proxies=None,
        verify=None,
        stream=False,
        json=None,
        **kwargs,
    ):
        target = _encode_url_non_ascii(_merge_params(str(url), params))
        merged_headers = CaseInsensitiveDict(self.headers)
        if headers:
            merged_headers.update(headers)

        req_cookies = dict(self.cookies)
        if isinstance(cookies, dict):
            req_cookies.update(cookies)
        if req_cookies and "cookie" not in merged_headers:
            merged_headers["Cookie"] = "; ".join(f"{k}={v}" for k, v in req_cookies.items())

        body = None
        if json is not None:
            body = _json.dumps(json, ensure_ascii=False).encode("utf-8")
            if "content-type" not in merged_headers:
                merged_headers["Content-Type"] = "application/json; charset=utf-8"
        elif data is not None:
            if isinstance(data, bytes):
                body = data
            elif isinstance(data, str):
                body = data.encode("utf-8")
            elif isinstance(data, dict):
                body = urllib.parse.urlencode(data, doseq=True).encode("utf-8")
                if "content-type" not in merged_headers:
                    merged_headers["Content-Type"] = "application/x-www-form-urlencoded"
            else:
                body = str(data).encode("utf-8")

        use_verify = self.verify if verify is None else verify
        ctx = ssl.create_default_context() if use_verify else ssl._create_unverified_context()

        handlers = [
            urllib.request.HTTPSHandler(context=ctx),
            urllib.request.HTTPCookieProcessor(self._jar),
        ]
        eff_proxies = proxies if proxies is not None else self.proxies
        if eff_proxies:
            handlers.append(urllib.request.ProxyHandler(eff_proxies))
        if not allow_redirects:
            handlers.append(_NoRedirectHandler())

        opener = urllib.request.build_opener(*handlers)
        req = urllib.request.Request(target, data=body, method=str(method).upper())
        for hk, hv in merged_headers.items():
            if hk and hv is not None:
                req.add_header(str(hk), str(hv))

        t_sec = _normalize_timeout(timeout)
        try:
            with opener.open(req, timeout=t_sec) as resp:
                raw = resp.read()
                resp_headers = dict(resp.headers.items())
                raw = _decompress(raw, resp.headers.get("Content-Encoding", ""))
                resp_cookies = _Utils.dict_from_cookiejar(self._jar)
                self.cookies.update(resp_cookies)
                return Response(resp.geturl(), resp.status, resp_headers, raw, cookies=resp_cookies)
        except urllib.error.HTTPError as err:
            raw = err.read() if err.fp else b""
            resp_headers = dict(err.headers.items()) if err.headers else {}
            raw = _decompress(raw, err.headers.get("Content-Encoding", "") if err.headers else "")
            resp_cookies = _Utils.dict_from_cookiejar(self._jar)
            self.cookies.update(resp_cookies)
            return Response(err.geturl() or target, err.code, resp_headers, raw, cookies=resp_cookies)
        except TimeoutError as err:
            raise Timeout(str(err)) from err
        except urllib.error.URLError as err:
            if isinstance(err.reason, TimeoutError):
                raise Timeout(str(err)) from err
            raise ConnectionError(str(err)) from err

    def get(self, url, **kwargs):
        return self.request("GET", url, **kwargs)

    def post(self, url, **kwargs):
        return self.request("POST", url, **kwargs)

    def put(self, url, **kwargs):
        return self.request("PUT", url, **kwargs)

    def delete(self, url, **kwargs):
        return self.request("DELETE", url, **kwargs)

    def head(self, url, **kwargs):
        kwargs.setdefault("allow_redirects", False)
        return self.request("HEAD", url, **kwargs)

    def options(self, url, **kwargs):
        return self.request("OPTIONS", url, **kwargs)

    def patch(self, url, **kwargs):
        return self.request("PATCH", url, **kwargs)

    def close(self):
        return None

    def __enter__(self):
        return self

    def __exit__(self, *args):
        return False


class _Adapters:
    class HTTPAdapter:
        def __init__(self, *args, **kwargs):
            pass


adapters = _Adapters()


def session():
    return Session()


def request(method, url, **kwargs):
    with Session() as s:
        return s.request(method, url, **kwargs)


def get(url, params=None, **kwargs):
    return request("GET", url, params=params, **kwargs)


def post(url, data=None, json=None, **kwargs):
    return request("POST", url, data=data, json=json, **kwargs)


def put(url, data=None, **kwargs):
    return request("PUT", url, data=data, **kwargs)


def delete(url, **kwargs):
    return request("DELETE", url, **kwargs)


def head(url, **kwargs):
    kwargs.setdefault("allow_redirects", False)
    return request("HEAD", url, **kwargs)


def options(url, **kwargs):
    return request("OPTIONS", url, **kwargs)


def patch(url, data=None, **kwargs):
    return request("PATCH", url, data=data, **kwargs)
