import html as _html
from html.parser import HTMLParser
import re

VOID_TAGS = {
    "area", "base", "br", "col", "embed", "hr", "img", "input",
    "link", "meta", "param", "source", "track", "wbr",
}


class Node:
    def __init__(self, tag, attrs=None, parent=None):
        self.tag = tag
        self.attrs = dict(attrs or {})
        self.parent = parent
        self.children = []
        self.text_parts = []

    def add_child(self, child):
        self.children.append(child)
        if isinstance(child, Node):
            child.parent = self

    def iter_elements(self):
        for c in self.children:
            if isinstance(c, Node):
                yield c
                yield from c.iter_elements()

    def direct_elements(self):
        return [c for c in self.children if isinstance(c, Node)]

    def direct_texts(self):
        return [c for c in self.children if isinstance(c, str)]

    def get_text(self, separator="", strip=False):
        out = []
        self._collect_text(out)
        if strip:
            out = [s.strip() for s in out if s.strip()]
        return separator.join(out)

    def _collect_text(self, out):
        for c in self.children:
            if isinstance(c, str):
                out.append(c)
            else:
                c._collect_text(out)

    def to_html(self, inner=False):
        inner_html = "".join(
            _html.escape(c) if isinstance(c, str) else c.to_html(inner=False)
            for c in self.children
        )
        if inner or self.tag == "[document]":
            return inner_html
        attr_str = ""
        for k, v in self.attrs.items():
            if v is None:
                attr_str += f" {k}"
            else:
                attr_str += f' {k}="{_html.escape(str(v), quote=True)}"'
        if self.tag in VOID_TAGS:
            return f"<{self.tag}{attr_str}>"
        return f"<{self.tag}{attr_str}>{inner_html}</{self.tag}>"

    def classes(self):
        raw = self.attrs.get("class", "")
        if isinstance(raw, list):
            return raw
        return str(raw).split()


class _Builder(HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.root = Node("[document]")
        self.stack = [self.root]

    def handle_starttag(self, tag, attrs):
        t = tag.lower()
        attr_map = {}
        for k, v in attrs:
            attr_map[k.lower()] = "" if v is None else v
        node = Node(t, attr_map, parent=self.stack[-1])
        self.stack[-1].add_child(node)
        if t not in VOID_TAGS:
            self.stack.append(node)

    def handle_startendtag(self, tag, attrs):
        t = tag.lower()
        attr_map = {k.lower(): ("" if v is None else v) for k, v in attrs}
        node = Node(t, attr_map, parent=self.stack[-1])
        self.stack[-1].add_child(node)

    def handle_endtag(self, tag):
        t = tag.lower()
        for i in range(len(self.stack) - 1, 0, -1):
            if self.stack[i].tag == t:
                del self.stack[i:]
                break

    def handle_data(self, data):
        if data:
            self.stack[-1].add_child(data)


def parse_html(text):
    if isinstance(text, bytes):
        text = text.decode("utf-8", errors="ignore")
    builder = _Builder()
    builder.feed(str(text or ""))
    builder.close()
    return builder.root


def _match_simple_css(node, token):
    if not isinstance(node, Node) or node.tag == "[document]":
        return False
    tok = token.strip()
    if not tok or tok == "*":
        return True

    # Strip pseudo-classes like :first-child, :not(...)
    pseudo = None
    m_ps = re.search(r":([a-zA-Z-]+)(?:\(([^)]*)\))?$", tok)
    if m_ps:
        pseudo = (m_ps.group(1).lower(), m_ps.group(2))
        tok = tok[: m_ps.start()]

    # Attribute selectors [...]
    attr_checks = []
    for am in re.finditer(r"\[([^\]]+)\]", tok):
        expr = am.group(1).strip()
        m_op = re.match(r"^([a-zA-Z0-9_:-]+)\s*([*^$|~]?=)\s*[\"']?(.*?)[\"']?$", expr)
        if m_op:
            attr_checks.append((m_op.group(1).lower(), m_op.group(2), m_op.group(3)))
        else:
            attr_checks.append((expr.lower(), None, None))
    tok = re.sub(r"\[[^\]]+\]", "", tok)

    tag_part = ""
    id_part = ""
    cls_parts = []
    parts = re.findall(r"([.#]?[^.#]+)", tok)
    for p in parts:
        if p.startswith("#"):
            id_part = p[1:]
        elif p.startswith("."):
            cls_parts.append(p[1:])
        elif p != "*":
            tag_part = p.lower()

    if tag_part and node.tag != tag_part:
        return False
    if id_part and node.attrs.get("id") != id_part:
        return False
    if cls_parts:
        node_cls = set(node.classes())
        for c in cls_parts:
            if c not in node_cls:
                return False
    for ak, op, av in attr_checks:
        if ak not in node.attrs:
            return False
        val = str(node.attrs.get(ak, ""))
        if op == "=" and val != av:
            return False
        if op == "*=" and av not in val:
            return False
        if op == "^=" and not val.startswith(av):
            return False
        if op == "$=" and not val.endswith(av):
            return False
        if op == "~=" and av not in val.split():
            return False
    if pseudo:
        pname, parg = pseudo
        if pname == "contains" and parg is not None:
            if parg.strip("\"'") not in node.get_text():
                return False
        elif pname == "first-child" and node.parent is not None:
            sibs = node.parent.direct_elements()
            if not sibs or sibs[0] is not node:
                return False
        elif pname == "last-child" and node.parent is not None:
            sibs = node.parent.direct_elements()
            if not sibs or sibs[-1] is not node:
                return False
        elif pname == "nth-child" and parg and node.parent is not None:
            try:
                idx = int(parg.strip())
                sibs = node.parent.direct_elements()
                if idx < 1 or idx > len(sibs) or sibs[idx - 1] is not node:
                    return False
            except ValueError:
                pass
    return True


def css_select(root, selector):
    selector = str(selector or "").strip()
    if not selector:
        return []
    results = []
    seen = set()
    for group in selector.split(","):
        group = group.strip()
        if not group:
            continue
        tokens = re.findall(r">|[^\s>]+", group)
        cur = [root]
        direct = False
        for tok in tokens:
            if tok == ">":
                direct = True
                continue
            # Handle jQuery :eq(N)/:first/:last/:gt(N)/:lt(N)
            jq = None
            m_jq = re.search(r":(eq|gt|lt)\((-?\d+)\)$|:(first|last)$", tok)
            base_tok = tok
            if m_jq:
                base_tok = tok[: m_jq.start()]
                jq = (m_jq.group(1) or m_jq.group(3), int(m_jq.group(2)) if m_jq.group(2) else 0)
            nxt = []
            nxt_seen = set()
            for node in cur:
                cands = node.direct_elements() if direct else list(node.iter_elements())
                for c in cands:
                    if id(c) not in nxt_seen and _match_simple_css(c, base_tok):
                        nxt_seen.add(id(c))
                        nxt.append(c)
            if jq:
                kind, num = jq
                if kind == "first":
                    nxt = nxt[:1]
                elif kind == "last":
                    nxt = nxt[-1:] if nxt else []
                elif kind == "eq":
                    idx = num if num >= 0 else len(nxt) + num
                    nxt = [nxt[idx]] if 0 <= idx < len(nxt) else []
                elif kind == "gt":
                    nxt = nxt[num + 1 :] if num + 1 >= 0 else nxt
                elif kind == "lt":
                    nxt = nxt[: max(0, num)]
            cur = nxt
            direct = False
        for n in cur:
            if id(n) not in seen:
                seen.add(id(n))
                results.append(n)
    return results


def _split_xpath_steps(expr):
    steps = []
    i = 0
    n = len(expr)
    while i < n:
        axis = "/"
        if expr.startswith("//", i):
            axis = "//"
            i += 2
        elif expr[i] == "/":
            axis = "/"
            i += 1
        elif i == 0:
            axis = "rel"
        start = i
        depth_b = 0
        depth_p = 0
        quote = ""
        while i < n:
            ch = expr[i]
            if quote:
                if ch == quote:
                    quote = ""
                i += 1
                continue
            if ch in ("'", '"'):
                quote = ch
            elif ch == "[":
                depth_b += 1
            elif ch == "]":
                depth_b = max(0, depth_b - 1)
            elif ch == "(":
                depth_p += 1
            elif ch == ")":
                depth_p = max(0, depth_p - 1)
            elif ch == "/" and depth_b == 0 and depth_p == 0:
                break
            i += 1
        seg = expr[start:i].strip()
        if seg:
            steps.append((axis, seg))
    return steps


def _eval_predicate(node, pred, pos, total):
    p = pred.strip()
    if p.isdigit():
        return pos == int(p)
    if p == "last()":
        return pos == total
    m_pos = re.match(r"^position\(\)\s*([<>]=?|=)\s*(\d+)$", p)
    if m_pos:
        op, num = m_pos.group(1), int(m_pos.group(2))
        if op == ">":
            return pos > num
        if op == ">=":
            return pos >= num
        if op == "<":
            return pos < num
        if op == "<=":
            return pos <= num
        if op == "=":
            return pos == num
    if " and " in p:
        return all(_eval_predicate(node, part, pos, total) for part in p.split(" and "))
    if " or " in p:
        return any(_eval_predicate(node, part, pos, total) for part in p.split(" or "))
    if p.startswith("not(") and p.endswith(")"):
        return not _eval_predicate(node, p[4:-1], pos, total)

    m_contains = re.match(r'^contains\(\s*(@[a-zA-Z0-9_:-]+|text\(\)|\.)\s*,\s*["\'](.*)["\']\s*\)$', p)
    if m_contains:
        lhs, rhs = m_contains.group(1), m_contains.group(2)
        if lhs.startswith("@"):
            val = str(node.attrs.get(lhs[1:].lower(), ""))
        else:
            val = node.get_text()
        return rhs in val

    m_eq = re.match(r'^(@[a-zA-Z0-9_:-]+|text\(\)|\.)\s*(=|!=)\s*["\'](.*)["\']$', p)
    if m_eq:
        lhs, op, rhs = m_eq.group(1), m_eq.group(2), m_eq.group(3)
        if lhs.startswith("@"):
            val = str(node.attrs.get(lhs[1:].lower(), ""))
        else:
            val = node.get_text().strip()
        return (val == rhs) if op == "=" else (val != rhs)

    if p.startswith("@"):
        return p[1:].lower() in node.attrs

    # Child existence predicate e.g. [a] or [span[@class='x']]
    return len(xpath_eval(node, "./" + p)) > 0


def xpath_eval(root, expr):
    expr = str(expr or "").strip()
    if not expr:
        return []
    if expr.startswith("string(") and expr.endswith(")"):
        inner = expr[7:-1].strip()
        if inner in (".", ""):
            return root.get_text()
        res = xpath_eval(root, inner)
        if not res:
            return ""
        first = res[0]
        return first if isinstance(first, str) else first.get_text()

    # Handle top-level union |
    branches = []
    depth_b = 0
    depth_p = 0
    quote = ""
    start = 0
    for i, ch in enumerate(expr):
        if quote:
            if ch == quote:
                quote = ""
            continue
        if ch in ("'", '"'):
            quote = ch
        elif ch == "[":
            depth_b += 1
        elif ch == "]":
            depth_b = max(0, depth_b - 1)
        elif ch == "(":
            depth_p += 1
        elif ch == ")":
            depth_p = max(0, depth_p - 1)
        elif ch == "|" and depth_b == 0 and depth_p == 0:
            branches.append(expr[start:i].strip())
            start = i + 1
    branches.append(expr[start:].strip())
    if len(branches) > 1:
        out = []
        for b in branches:
            res = xpath_eval(root, b)
            if isinstance(res, list):
                out.extend(res)
            elif res is not None:
                out.append(res)
        return out

    steps = _split_xpath_steps(expr)
    cur = [root]
    for axis, seg in steps:
        if seg == ".":
            continue
        if seg == "..":
            cur = [n.parent for n in cur if isinstance(n, Node) and n.parent is not None]
            continue
        if seg.startswith("@"):
            aname = seg[1:].lower()
            out = []
            for n in cur:
                if isinstance(n, Node) and aname in n.attrs:
                    out.append(str(n.attrs[aname]))
            cur = out
            continue
        if seg == "text()":
            out = []
            for n in cur:
                if not isinstance(n, Node):
                    continue
                if axis == "//":
                    for el in [n] + list(n.iter_elements()):
                        out.extend(el.direct_texts())
                else:
                    out.extend(n.direct_texts())
            cur = out
            continue

        # Extract predicates from seg
        preds = []
        base = seg
        m_first_b = re.search(r"\[", seg)
        if m_first_b:
            base = seg[: m_first_b.start()].strip()
            rest = seg[m_first_b.start() :]
            idx = 0
            while idx < len(rest):
                if rest[idx] == "[":
                    depth = 0
                    q = ""
                    j = idx
                    while j < len(rest):
                        c = rest[j]
                        if q:
                            if c == q:
                                q = ""
                        elif c in ("'", '"'):
                            q = c
                        elif c == "[":
                            depth += 1
                        elif c == "]":
                            depth -= 1
                            if depth == 0:
                                preds.append(rest[idx + 1 : j])
                                idx = j + 1
                                break
                        j += 1
                    else:
                        break
                else:
                    idx += 1

        tag_target = base.lower()
        nxt = []
        seen = set()
        for n in cur:
            if not isinstance(n, Node):
                continue
            if axis == "//":
                cands = list(n.iter_elements())
            else:
                cands = n.direct_elements()
            matched = [c for c in cands if tag_target in ("*", "") or c.tag == tag_target]
            for pred in preds:
                total = len(matched)
                matched = [c for idx_m, c in enumerate(matched, 1) if _eval_predicate(c, pred, idx_m, total)]
            for c in matched:
                if id(c) not in seen:
                    seen.add(id(c))
                    nxt.append(c)
        cur = nxt
    return cur
