import re
import _dom


class Tag:
    def __init__(self, node):
        self._node = node

    @property
    def name(self):
        return self._node.tag

    @property
    def attrs(self):
        return self._node.attrs

    @property
    def text(self):
        return self._node.get_text()

    @property
    def string(self):
        t = self._node.get_text()
        return t if t else None

    @property
    def parent(self):
        return Tag(self._node.parent) if self._node.parent is not None else None

    @property
    def children(self):
        for c in self._node.children:
            yield Tag(c) if isinstance(c, _dom.Node) else c

    @property
    def contents(self):
        return [Tag(c) if isinstance(c, _dom.Node) else c for c in self._node.children]

    def get_text(self, separator="", strip=False):
        return self._node.get_text(separator=separator, strip=strip)

    def get(self, key, default=None):
        k = str(key).lower()
        if k == "class":
            v = self._node.attrs.get("class")
            return v.split() if isinstance(v, str) else (v if v is not None else default)
        return self._node.attrs.get(k, default)

    def has_attr(self, key):
        return str(key).lower() in self._node.attrs

    def __getitem__(self, key):
        v = self.get(key)
        if v is None:
            raise KeyError(key)
        return v

    def __str__(self):
        return self._node.to_html(inner=False)

    def decode_contents(self):
        return self._node.to_html(inner=True)

    def decompose(self):
        if self._node.parent is not None and self._node in self._node.parent.children:
            self._node.parent.children.remove(self._node)

    extract = decompose

    def _matches(self, node, name, attrs, string):
        if name is not None:
            if isinstance(name, str):
                if node.tag != name.lower():
                    return False
            elif isinstance(name, (list, tuple, set)):
                if node.tag not in [x.lower() for x in name]:
                    return False
            elif hasattr(name, "search"):
                if not name.search(node.tag):
                    return False
            elif callable(name):
                if not name(Tag(node)):
                    return False
        for ak, av in attrs.items():
            key = "class" if ak == "class_" else ak.lower()
            if av is True:
                if key not in node.attrs:
                    return False
                continue
            if av is False:
                if key in node.attrs:
                    return False
                continue
            if key == "class":
                node_cls = node.classes()
                if isinstance(av, str):
                    wanted = av.split()
                    if not all(w in node_cls for w in wanted):
                        return False
                elif isinstance(av, (list, tuple)):
                    if not any(w in node_cls for w in av):
                        return False
                elif hasattr(av, "search"):
                    if not av.search(node.attrs.get("class", "")):
                        return False
                continue
            val = node.attrs.get(key)
            if val is None:
                return False
            if isinstance(av, str):
                if val != av:
                    return False
            elif isinstance(av, (list, tuple)):
                if val not in av:
                    return False
            elif hasattr(av, "search"):
                if not av.search(str(val)):
                    return False
        if string is not None:
            txt = node.get_text()
            if isinstance(string, str) and string != txt:
                return False
            if hasattr(string, "search") and not string.search(txt):
                return False
        return True

    def find_all(self, name=None, attrs=None, recursive=True, string=None, limit=None, **kwargs):
        merged = dict(attrs or {})
        merged.update(kwargs)
        if string is None and "text" in merged:
            string = merged.pop("text")
        cands = self._node.iter_elements() if recursive else self._node.direct_elements()
        out = []
        for n in cands:
            if self._matches(n, name, merged, string):
                out.append(Tag(n))
                if limit is not None and len(out) >= limit:
                    break
        return out

    findAll = find_all

    def __call__(self, *args, **kwargs):
        return self.find_all(*args, **kwargs)

    def find(self, name=None, attrs=None, recursive=True, string=None, **kwargs):
        res = self.find_all(name=name, attrs=attrs, recursive=recursive, string=string, limit=1, **kwargs)
        return res[0] if res else None

    def select(self, selector):
        return [Tag(n) for n in _dom.css_select(self._node, selector)]

    def select_one(self, selector):
        res = _dom.css_select(self._node, selector)
        return Tag(res[0]) if res else None

    def __getattr__(self, item):
        if item.startswith("_"):
            raise AttributeError(item)
        return self.find(item)


class BeautifulSoup(Tag):
    def __init__(self, markup="", features="html.parser", **kwargs):
        super().__init__(_dom.parse_html(markup))
