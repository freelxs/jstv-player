import _dom


class _Element:
    def __init__(self, node):
        self._node = node

    @property
    def tag(self):
        return self._node.tag

    @property
    def attrib(self):
        return self._node.attrs

    @property
    def text(self):
        parts = self._node.direct_texts()
        return parts[0] if parts else None

    def get(self, key, default=None):
        return self._node.attrs.get(str(key).lower(), default)

    def xpath(self, expr):
        res = _dom.xpath_eval(self._node, expr)
        if isinstance(res, list):
            return [_Element(x) if isinstance(x, _dom.Node) else x for x in res]
        return res

    def cssselect(self, selector):
        return [_Element(n) for n in _dom.css_select(self._node, selector)]

    def itertext(self):
        out = []
        self._node._collect_text(out)
        return iter(out)

    def getchildren(self):
        return [_Element(c) for c in self._node.direct_elements()]

    def __iter__(self):
        return iter(self.getchildren())

    def __len__(self):
        return len(self._node.direct_elements())

    def __getitem__(self, idx):
        return self.getchildren()[idx]

    def __str__(self):
        return self._node.to_html(inner=False)


def HTML(text, parser=None):
    return _Element(_dom.parse_html(text))


def fromstring(text, parser=None):
    return HTML(text)


def tostring(element, encoding="unicode", method="html", **kwargs):
    s = element._node.to_html(inner=False) if isinstance(element, _Element) else str(element)
    if encoding == "unicode":
        return s
    return s.encode(encoding or "utf-8", errors="ignore")
