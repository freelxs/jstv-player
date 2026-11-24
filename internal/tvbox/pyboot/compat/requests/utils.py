import urllib.parse
from . import CookieDict


def dict_from_cookiejar(cj):
    if isinstance(cj, dict):
        return dict(cj)
    out = {}
    for c in cj:
        out[c.name] = c.value
    return out


def cookiejar_from_dict(d):
    return CookieDict(d or {})


def quote(s, safe="/"):
    return urllib.parse.quote(str(s), safe=safe)


def unquote(s):
    return urllib.parse.unquote(str(s))
