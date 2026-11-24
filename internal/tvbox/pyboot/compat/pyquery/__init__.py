import _dom


class PyQuery:
    def __init__(self, context=None, **kwargs):
        if context is None:
            self._nodes = []
        elif isinstance(context, PyQuery):
            self._nodes = list(context._nodes)
        elif isinstance(context, _dom.Node):
            self._nodes = [context]
        elif isinstance(context, list):
            self._nodes = [n._nodes[0] if isinstance(n, PyQuery) and n._nodes else n for n in context if n is not None]
        else:
            self._nodes = [_dom.parse_html(context)]

    def __call__(self, selector):
        return self.find(selector)

    def find(self, selector):
        out = []
        seen = set()
        for n in self._nodes:
            for m in _dom.css_select(n, selector):
                if id(m) not in seen:
                    seen.add(id(m))
                    out.append(m)
        return PyQuery(out)

    def items(self, selector=None):
        target = self.find(selector) if selector else self
        for n in target._nodes:
            yield PyQuery([n])

    def eq(self, index):
        try:
            return PyQuery([self._nodes[index]])
        except IndexError:
            return PyQuery([])

    def first(self):
        return self.eq(0)

    def last(self):
        return self.eq(-1)

    def text(self):
        return " ".join(n.get_text(strip=True) for n in self._nodes if n.get_text(strip=True)).strip()

    def html(self):
        return self._nodes[0].to_html(inner=True) if self._nodes else ""

    def outer_html(self):
        return self._nodes[0].to_html(inner=False) if self._nodes else ""

    outerHtml = outer_html

    def attr(self, name):
        if not self._nodes:
            return None
        return self._nodes[0].attrs.get(str(name).lower())

    def children(self, selector=None):
        out = []
        for n in self._nodes:
            for c in n.direct_elements():
                if not selector or _dom._match_simple_css(c, selector):
                    out.append(c)
        return PyQuery(out)

    def parent(self):
        out = [n.parent for n in self._nodes if n.parent is not None]
        return PyQuery(out)

    def remove(self, selector=None):
        if selector:
            targets = set(id(m) for n in self._nodes for m in _dom.css_select(n, selector))
            for n in self._nodes:
                for el in [n] + list(n.iter_elements()):
                    el.children = [c for c in el.children if not (isinstance(c, _dom.Node) and id(c) in targets)]
        return self

    @property
    def length(self):
        return len(self._nodes)

    def __len__(self):
        return len(self._nodes)

    def __bool__(self):
        return len(self._nodes) > 0

    def __iter__(self):
        return iter(self._nodes)

    def __getitem__(self, idx):
        return self._nodes[idx]

    def __str__(self):
        return "".join(n.to_html(inner=False) for n in self._nodes)
