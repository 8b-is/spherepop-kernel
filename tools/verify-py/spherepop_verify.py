#!/usr/bin/env python3
"""Reference verifier for SpherePOP evidence (SPEC.md section 18).

Written against SPEC section 18. It shares no code and no data tables with
the Go implementation; its purpose is to catch errors the Go packages make in
common, and ambiguities in the specification. It is not fast.

    spherepop_verify.py trace  FILE [--require-strategy] [--max-steps N] [--fuel N] [--stop]
    spherepop_verify.py sched  FILE
    spherepop_verify.py effects FILE --schema FILE [--anchor HEX]
    spherepop_verify.py corpus MANIFEST

Exit status: 0 valid, 1 invalid, 2 usage or unreadable input.
Output: "valid" or "invalid: CLASS: message".
"""
import hashlib
import json
import re
import struct
import sys
import threading

# ---------------------------------------------------------------- failures


class Reject(Exception):
    def __init__(self, cls, msg):
        super().__init__(msg)
        self.cls = cls
        self.msg = msg


# ------------------------------------------------------- strict JSON (18.9)


class IntTok(int):
    """An integer that remembers how it was written."""

    text = ""


def _parse_int(s):
    v = IntTok(int(s))
    v.text = s
    return v


def _no_float(s):
    raise ValueError("number %r has a fraction or exponent" % s)


def _no_const(s):
    raise ValueError("constant %s is not allowed" % s)


def _pairs(pairs):
    d = {}
    for k, v in pairs:
        if k in d:
            raise ValueError("duplicate member %r" % k)
        d[k] = v
    return d


def load_json(text):
    return json.loads(
        text,
        object_pairs_hook=_pairs,
        parse_int=_parse_int,
        parse_float=_no_float,
        parse_constant=_no_const,
    )


# shapes: "str" "bool" "u64" "i64"; ("arr", shape); ("map", shape);
# ("obj", {name: (shape, required)})
def shape_check(v, shape, path="$"):
    if isinstance(shape, str):
        if shape == "str":
            if not isinstance(v, str):
                raise ValueError("%s: want a string" % path)
            return v == ""
        if shape == "bool":
            if not isinstance(v, bool):
                raise ValueError("%s: want a boolean" % path)
            return v is False
        if shape in ("u64", "i64"):
            if isinstance(v, bool) or not isinstance(v, IntTok):
                raise ValueError("%s: want an integer" % path)
            if shape == "u64" and not (0 <= v < 2**64 and not v.text.startswith("-")):
                raise ValueError("%s: out of range" % path)
            if shape == "i64" and not (-(2**63) <= v < 2**63):
                raise ValueError("%s: out of range" % path)
            return int(v) == 0
        raise AssertionError(shape)
    kind = shape[0]
    if kind == "arr":
        if not isinstance(v, list):
            raise ValueError("%s: want an array" % path)
        for i, x in enumerate(v):
            shape_check(x, shape[1], "%s[%d]" % (path, i))
        return len(v) == 0
    if kind == "map":
        if not isinstance(v, dict):
            raise ValueError("%s: want an object" % path)
        for k, x in v.items():
            shape_check(x, shape[1], path + "." + k)
        return len(v) == 0
    if kind == "obj":
        if not isinstance(v, dict):
            raise ValueError("%s: want an object" % path)
        for k in v:
            if k not in shape[1]:
                raise ValueError("%s: unknown member %r" % (path, k))
        for k, (sh, required) in shape[1].items():
            if k not in v:
                if required:
                    raise ValueError("%s: missing member %r" % (path, k))
                continue
            zero = shape_check(v[k], sh, path + "." + k)
            if zero and not required:
                raise ValueError("%s: optional member %r present with its zero value" % (path, k))
        return False
    raise AssertionError(shape)


CONFIG_SHAPE = (
    "obj",
    {
        "root": (("arr", "str"), True),
        "perm": ((("map", ("arr", "str"))), False),
        "gated": (("arr", "str"), True),
        "absorb": ("str", True),
        "sc": ("str", True),
    },
)

TRACE_SHAPE = (
    "obj",
    {
        "format": ("str", True),
        "config": (CONFIG_SHAPE, True),
        "start": ("str", True),
        "start_digest": ("str", True),
        "strategy": ("str", True),
        "fuel": ("u64", True),
        "steps": (
            ("arr", ("obj", {
                "rule": ("str", True),
                "path": ("str", True),
                "after": ("str", True),
                "link": ("str", True),
            })),
            True,
        ),
        "final": ("str", True),
        "final_digest": ("str", True),
        "exhausted": ("bool", True),
        "head": ("str", True),
    },
)

STATE_SHAPE = (
    "obj",
    {
        "id": ("str", True),
        "term": ("str", True),
        "digest": ("str", True),
        "normal": ("bool", False),
        "suspended": ("bool", False),
    },
)

SCHED_SHAPE = (
    "obj",
    {
        "format": ("str", True),
        "config": (CONFIG_SHAPE, True),
        "policy": (("obj", {"strategy": ("str", True), "budget": ("u64", True)}), True),
        "initial": (("arr", STATE_SHAPE), True),
        "events": (
            ("arr", ("obj", {
                "tick": ("i64", True),
                "entity": ("str", True),
                "decision": ("str", True),
                "budget": ("u64", False),
                "slice": (TRACE_SHAPE, False),
                "rearm": (("obj", {
                    "term": ("str", True),
                    "digest": ("str", True),
                    "preempted": ("bool", False),
                }), False),
                "resumed": ("bool", False),
            })),
            True,
        ),
        "final": (("arr", STATE_SHAPE), True),
    },
)

EFFECT_SHAPE = (
    "obj",
    {
        "seq": ("u64", True),
        "tick": ("i64", True),
        "subject": ("str", True),
        "ref": ("str", False),
        "index": ("i64", True),
        "request": ("str", False),
        "disposition": ("str", True),
        "reason": ("str", False),
        "prev": ("str", True),
        "hash": ("str", True),
    },
)

SCHEMA_SHAPE = (
    "obj",
    {"targets": (("map", ("obj", {
        "id": ("str", True),
        "verbs": (("map", ("arr", "str")), True),
    })), True)},
)


def strict_load(text, shape, cls):
    try:
        v = load_json(text)
        shape_check(v, shape)
    except (ValueError, RecursionError) as e:
        raise Reject(cls, "malformed document: %s" % e)
    return v


# -------------------------------------------------------- hashing (18.1, 18.6)


def sha(b):
    return hashlib.sha256(b).digest()


def hexsha(b):
    return hashlib.sha256(b).hexdigest()


def uvarint(n):
    out = bytearray()
    while n >= 0x80:
        out.append((n & 0x7F) | 0x80)
        n >>= 7
    out.append(n)
    return bytes(out)


def framed(s):
    b = s.encode("utf-8")
    return uvarint(len(b)) + b


def chain_sum(*parts):
    h = hashlib.sha256()
    for p in parts:
        h.update(p.encode("utf-8"))
        h.update(b"\x00")
    return h.hexdigest()


# ------------------------------------------------------------ terms (18.1, 18.2)

ATOM, CONST, SCOPE, APP, BRACKET = 1, 2, 3, 4, 5
CONSTANTS = {"S": 3, "K": 2, "I": 1, "B": 3, "C": 3, "W": 2}
DOMAIN = framed("spherepop-term/1")


class T:
    __slots__ = ("kind", "name", "a", "b", "_d", "_s")

    def __init__(self, kind, name=None, a=None, b=None):
        self.kind, self.name, self.a, self.b = kind, name, a, b
        self._d = None
        self._s = None

    def digest(self):
        if self._d is None:
            if self.kind in (ATOM, CONST, SCOPE):
                self._d = sha(DOMAIN + bytes([self.kind]) + framed(self.name))
            elif self.kind == APP:
                self._d = sha(DOMAIN + b"\x04" + self.a.digest() + self.b.digest())
            else:
                self._d = sha(DOMAIN + b"\x05" + framed(self.name) + self.b.digest())
        return self._d

    def hexdigest(self):
        return self.digest().hex()

    def text(self):
        if self._s is None:
            if self.kind in (ATOM, CONST):
                self._s = self.name
            elif self.kind == SCOPE:
                self._s = "sc_" + self.name
            elif self.kind == BRACKET:
                self._s = "<" + self.b.text() + ">_" + self.name
            else:
                arg = self.b.text()
                if self.b.kind == APP:
                    arg = "(" + arg + ")"
                self._s = self.a.text() + " " + arg
        return self._s


def app(f, x):
    return T(APP, a=f, b=x)


class Syntax(Exception):
    pass


WS = " \t\n\r"
MAX_SOURCE = 4194304
MAX_NEST = 100000


def isalpha(c):
    return ("a" <= c <= "z") or ("A" <= c <= "Z")


def isdigit(c):
    return "0" <= c <= "9"


def isalnum(c):
    return isalpha(c) or isdigit(c)


def isident(c):
    return isalnum(c) or c in "_'"


def parse_term(src):
    try:
        raw = src.encode("utf-8")
    except UnicodeEncodeError:
        raise Syntax("not valid text")
    if len(raw) > MAX_SOURCE:
        raise Syntax("source too long")
    pos = 0
    n = len(src)
    depth = [0]

    def skip():
        nonlocal pos
        while pos < n and src[pos] in WS:
            pos += 1

    def term():
        nonlocal pos
        skip()
        left = None
        while True:
            skip()
            if pos >= n or src[pos] in ")>":
                break
            x = unit()
            left = x if left is None else app(left, x)
        if left is None:
            raise Syntax("expected a term at %d" % pos)
        return left

    def unit():
        nonlocal pos
        c = src[pos]
        if c == "(":
            depth[0] += 1
            if depth[0] > MAX_NEST:
                raise Syntax("nesting too deep")
            pos += 1
            t = term()
            if pos >= n or src[pos] != ")":
                raise Syntax("expected ) at %d" % pos)
            pos += 1
            depth[0] -= 1
            return t
        if c == "<":
            depth[0] += 1
            if depth[0] > MAX_NEST:
                raise Syntax("nesting too deep")
            pos += 1
            body = term()
            if pos >= n or src[pos] != ">":
                raise Syntax("expected > at %d" % pos)
            pos += 1
            if pos >= n or src[pos] != "_":
                raise Syntax("expected _ and a label after >")
            pos += 1
            start = pos
            while pos < n and isalnum(src[pos]):
                pos += 1
            if pos == start:
                raise Syntax("expected a label")
            depth[0] -= 1
            return T(BRACKET, name=src[start:pos], b=body)
        if isdigit(c):
            start = pos
            while pos < n and isdigit(src[pos]):
                pos += 1
            if pos < n and isident(src[pos]):
                raise Syntax("numeral followed by identifier character at %d" % pos)
            return T(ATOM, name=src[start:pos])
        if isalpha(c):
            start = pos
            while pos < n and isident(src[pos]):
                pos += 1
            name = src[start:pos]
            if name in CONSTANTS:
                return T(CONST, name=name)
            if name.startswith("sc_"):
                label = name[3:]
                if label == "" or not all(isalnum(ch) for ch in label):
                    raise Syntax("bad scope label in %r" % name)
                return T(SCOPE, name=label)
            if "a" <= name[0] <= "z":
                return T(ATOM, name=name)
            raise Syntax("identifier %r is not a constant" % name)
        raise Syntax("unexpected character %r at %d" % (c, pos))

    t = term()
    if pos != n:
        raise Syntax("unexpected %r at %d" % (src[pos], pos))
    return t


def labels(t, acc=None):
    acc = set() if acc is None else acc
    stack = [t]
    while stack:
        x = stack.pop()
        if x.kind == SCOPE or x.kind == BRACKET:
            acc.add(x.name)
        if x.kind == APP:
            stack.append(x.a)
            stack.append(x.b)
        elif x.kind == BRACKET:
            stack.append(x.b)
    return acc


# ------------------------------------------------------- configuration (18.3)

ALPHABET = ["W", "K", "C", "S"]
ABSORB_MODES = ("off", "any", "nf", "s", "u")
SCOPE_MODES = ("any", "safe", "settled")


def valid_label(l):
    return l != "" and all(isalnum(c) and ord(c) < 128 for c in l)


def perm_set(names, what):
    if len(set(names)) != len(names):
        raise Reject("format", "%s repeats a permission name" % what)
    for n in names:
        if n not in ALPHABET:
            raise Reject("format", "%s: %r is not in the permission alphabet" % (what, n))
    return frozenset(names)


class Config:
    def __init__(self, spec):
        self.root = perm_set(spec["root"], "root")
        self.gated = perm_set(spec["gated"], "gated")
        if spec["absorb"] not in ABSORB_MODES:
            raise Reject("format", "unknown absorption mode %r" % spec["absorb"])
        if spec["sc"] not in SCOPE_MODES:
            raise Reject("format", "unknown scope mode %r" % spec["sc"])
        self.absorb, self.sc = spec["absorb"], spec["sc"]
        self.perm = {}
        for l, names in spec.get("perm", {}).items():
            if not valid_label(l):
                raise Reject("format", "label %r is not letters and digits" % l)
            self.perm[l] = perm_set(names, "perm")
        parts = ['"root":' + json.dumps(spec["root"], separators=(",", ":"))]
        if spec.get("perm"):
            items = ",".join(
                json.dumps(l) + ":" + json.dumps(spec["perm"][l], separators=(",", ":"))
                for l in sorted(spec["perm"], key=lambda k: k.encode("utf-8"))
            )
            parts.append('"perm":{' + items + "}")
        parts.append('"gated":' + json.dumps(spec["gated"], separators=(",", ":")))
        parts.append('"absorb":' + json.dumps(spec["absorb"]))
        parts.append('"sc":' + json.dumps(spec["sc"]))
        self.canonical = "{" + ",".join(parts) + "}"

    def perm_of(self, label):
        return self.perm.get(label, frozenset())

    def admissible(self, c, mask):
        return c not in self.gated or c in mask


# --------------------------------------------------------- redexes (18.4)


def spine(node):
    """Return (head, number of applications) following function children."""
    n = 0
    while node.kind == APP:
        node = node.a
        n += 1
    return node, n


def is_contraction(node, mask, cfg):
    if node.kind != APP:
        return False
    head, n = spine(node)
    return head.kind == CONST and CONSTANTS[head.name] == n and cfg.admissible(head.name, mask)


def rnormal(v, mask, cfg):
    stack = [(v, mask)]
    while stack:
        x, m = stack.pop()
        if x.kind == APP:
            if is_contraction(x, m, cfg):
                return False
            stack.append((x.a, m))
            stack.append((x.b, m))
        elif x.kind == BRACKET:
            stack.append((x.b, m & cfg.perm_of(x.name)))
    return True


def settled(v, mask, cfg):
    stack = [(v, mask)]
    while stack:
        x, m = stack.pop()
        if x.kind == APP:
            if x.a.kind in (BRACKET, SCOPE) or is_contraction(x, m, cfg):
                return False
            stack.append((x.a, m))
            stack.append((x.b, m))
        elif x.kind == BRACKET:
            stack.append((x.b, m & cfg.perm_of(x.name)))
    return True


def insensitive(v, cfg):
    stack = [v]
    while stack:
        x = stack.pop()
        if x.kind == CONST and x.name in cfg.gated and x.name in ALPHABET:
            return False
        if x.kind == APP:
            stack.append(x.a)
            stack.append(x.b)
        elif x.kind == BRACKET:
            stack.append(x.b)
    return True


def abs_ok(v, mask, cfg):
    m = cfg.absorb
    if m == "off":
        return False
    if m == "any":
        return True
    if m == "nf":
        return rnormal(v, mask, cfg)
    if m == "s":
        return settled(v, mask, cfg)
    return rnormal(v, mask, cfg) and (settled(v, mask, cfg) or insensitive(v, cfg))


def sc_ok(v, mask, cfg):
    m = cfg.sc
    if m == "any":
        return True
    if m == "safe":
        return rnormal(v, mask, cfg) and (settled(v, mask, cfg) or insensitive(v, cfg))
    return settled(v, mask, cfg)


def redex_rule(node, mask, cfg):
    if node.kind != APP:
        return None
    f = node.a
    if f.kind == SCOPE:
        return "Sc" if sc_ok(node.b, mask, cfg) else None
    if f.kind == BRACKET:
        return "Abs" if abs_ok(node.b, mask, cfg) else None
    if is_contraction(node, mask, cfg):
        return spine(node)[0].name
    return None


def spine_args(node):
    args = []
    while node.kind == APP:
        args.append(node.b)
        node = node.a
    args.reverse()
    return args


def contract(rule, args):
    if rule == "I":
        return args[0]
    if rule == "K":
        return args[0]
    if rule == "W":
        return app(app(args[0], args[1]), args[1])
    if rule == "B":
        return app(args[0], app(args[1], args[2]))
    if rule == "C":
        return app(app(args[0], args[2]), args[1])
    if rule == "S":
        return app(app(args[0], args[2]), app(args[1], args[2]))
    raise AssertionError(rule)


class StepRejected(Exception):
    pass


RULES = ("I", "B", "C", "K", "W", "S", "Sc", "Abs")


def apply_step(root, rule, path, cfg):
    if rule not in RULES:
        raise StepRejected("unknown rule %r" % rule)

    def go(node, i, mask):
        if i == len(path):
            return rewrite(node, mask)
        c = path[i]
        if c == "L" and node.kind == APP:
            return app(go(node.a, i + 1, mask), node.b)
        if c == "R" and node.kind == APP:
            return app(node.a, go(node.b, i + 1, mask))
        if c == "B" and node.kind == BRACKET:
            m = mask & cfg.perm_of(node.name)
            return T(BRACKET, name=node.name, b=go(node.b, i + 1, m))
        raise StepRejected("path character %r does not apply at offset %d" % (c, i))

    def rewrite(node, mask):
        if rule == "Sc":
            if node.kind == APP and node.a.kind == SCOPE and sc_ok(node.b, mask, cfg):
                return T(BRACKET, name=node.a.name, b=node.b)
            raise StepRejected("Sc does not apply")
        if rule == "Abs":
            if node.kind == APP and node.a.kind == BRACKET and abs_ok(node.b, mask, cfg):
                return T(BRACKET, name=node.a.name, b=app(node.a.b, node.b))
            raise StepRejected("Abs does not apply")
        if node.kind == APP:
            head, n = spine(node)
            if head.kind == CONST and head.name == rule and CONSTANTS[rule] == n \
                    and cfg.admissible(rule, mask):
                return contract(rule, spine_args(node))
        raise StepRejected("%s does not apply" % rule)

    for ch in path:
        if ch not in "LRB":
            raise StepRejected("path character %r" % ch)
    return go(root, 0, cfg.root)


# --------------------------------------------------------- strategies (18.5)


def first_redex(root, strategy, cfg):
    """The (rule, path) the strategy takes at root, or None."""
    outer = strategy in ("lo", "ro")
    left_first = strategy in ("lo", "li")

    def visit(node, mask, path):
        if node.kind == APP:
            kids = [(node.a, "L"), (node.b, "R")]
            if not left_first:
                kids.reverse()
            if outer:
                r = redex_rule(node, mask, cfg)
                if r:
                    return (r, path)
            for k, c in kids:
                found = visit(k, mask, path + c)
                if found:
                    return found
            if not outer:
                r = redex_rule(node, mask, cfg)
                if r:
                    return (r, path)
            return None
        if node.kind == BRACKET:
            return visit(node.b, mask & cfg.perm_of(node.name), path + "B")
        return None

    return visit(root, cfg.root, "")


def has_redex(root, cfg):
    return first_redex(root, "lo", cfg) is not None


# ------------------------------------------------------------- trace (18.6)

STRATEGIES = ("lo", "ro", "li", "ri")


def at_boundary(t):
    """True for exactly (yield k): the execution boundary of SPEC 18.6."""
    return t.kind == APP and t.a.kind == ATOM and t.a.name == "yield"


def check_trace(doc, require_strategy=False, max_steps=None, fuel=None, stop=False):
    if doc["format"] != "spherepop-trace/1":
        raise Reject("format", "format %r" % doc["format"])
    steps = doc["steps"]
    if max_steps is not None and len(steps) > max_steps:
        raise Reject("format", "%d steps exceeds the limit %d" % (len(steps), max_steps))
    cfg = Config(doc["config"])
    try:
        term = parse_term(doc["start"])
    except Syntax as e:
        raise Reject("format", "start does not parse: %s" % e)
    if term.hexdigest() != doc["start_digest"]:
        raise Reject("start-mismatch", "start term does not match start_digest")
    for l in labels(term):
        if l not in cfg.perm:
            raise Reject("format", "start term uses label %r with no permission entry" % l)
    if require_strategy and doc["strategy"] not in STRATEGIES:
        raise Reject("format", "strategy %r" % doc["strategy"])
    cd = chain_sum("spherepop-config/1", cfg.canonical)
    prev = chain_sum("spherepop-trace/1", doc["start_digest"], cd)
    for i, st in enumerate(steps):
        want = chain_sum(prev, st["rule"], st["path"], st["after"])
        if st["link"] != want:
            raise Reject("chain", "step %d: link does not verify" % i)
        prev = st["link"]
        if stop and at_boundary(term):
            raise Reject("step-after-stop", "step %d is taken from a term at the execution boundary" % i)
        if require_strategy:
            fr = first_redex(term, doc["strategy"], cfg)
            if fr is None or fr != (st["rule"], st["path"]):
                raise Reject("strategy-deviation", "step %d is not the %s step" % (i, doc["strategy"]))
        try:
            term = apply_step(term, st["rule"], st["path"], cfg)
        except StepRejected as e:
            raise Reject("step-rejected", "step %d: %s" % (i, e))
        if term.hexdigest() != st["after"]:
            raise Reject("digest-mismatch", "step %d: result digest differs from after" % i)
    if doc["head"] != prev:
        raise Reject("chain", "head does not match the last link")
    if term.hexdigest() != doc["final_digest"] or term.text() != doc["final"]:
        raise Reject("final-mismatch", "final term does not match")
    if doc["exhausted"] != (has_redex(term, cfg) and not (stop and at_boundary(term))):
        raise Reject("status-mismatch", "exhausted flag disagrees with the final term")
    if fuel is not None and fuel != doc["fuel"]:
        raise Reject("status-mismatch", "fuel %d, want %d" % (doc["fuel"], fuel))
    if doc["exhausted"] and len(steps) != doc["fuel"]:
        raise Reject("status-mismatch", "exhausted with %d steps of fuel %d" % (len(steps), doc["fuel"]))
    if len(steps) > doc["fuel"]:
        raise Reject("status-mismatch", "more steps than fuel")
    return term


# ---------------------------------------------------------- scheduler (18.7)


def check_sched(doc):
    def bad(msg):
        raise Reject("log-rejected", msg)

    if doc["format"] != "spherepop-sched/1":
        bad("format %r" % doc["format"])
    try:
        cfg = Config(doc["config"])
    except Reject as e:
        bad("config: %s" % e.msg)
    strat, budget = doc["policy"]["strategy"], doc["policy"]["budget"]
    if strat not in STRATEGIES or budget == 0:
        bad("policy")

    ids, digest, normal, cur, susp = [], {}, {}, {}, {}
    for s in doc["initial"]:
        i = s["id"]
        if i == "" or i in digest:
            bad("initial entity has an empty or repeated id")
        try:
            t = parse_term(s["term"])
        except Syntax:
            bad("initial term of %r does not parse" % i)
        if t.hexdigest() != s["digest"]:
            bad("initial term of %r does not match its digest" % i)
        if any(l not in cfg.perm for l in labels(t)):
            bad("initial term of %r uses an unknown label" % i)
        if s.get("normal", False):
            bad("initial state of %r carries a finished flag" % i)
        if s.get("suspended", False):
            bad("initial state of %r carries a suspended flag" % i)
        ids.append(i)
        digest[i] = s["digest"]
        normal[i] = False
        cur[i], susp[i] = t, False
    if ids != sorted(ids, key=lambda x: x.encode("utf-8")):
        bad("initial entities are not in identifier order")

    n = len(ids)
    events = doc["events"]
    if n == 0:
        if events:
            bad("events without entities")
    elif len(events) % n != 0:
        bad("events do not make whole ticks")
    for k, ev in enumerate(events):
        i = ids[k % n]
        if ev["tick"] != k // n or ev["entity"] != i:
            bad("event %d is out of order" % k)
        if "rearm" in ev:
            r = ev["rearm"]
            try:
                t = parse_term(r["term"])
            except Syntax:
                bad("event %d: replacement does not parse" % k)
            if t.hexdigest() != r["digest"]:
                bad("event %d: replacement does not match its digest" % k)
            if any(l not in cfg.perm for l in labels(t)):
                bad("event %d: replacement uses an unknown label" % k)
            if r.get("preempted", False) == normal[i]:
                bad("event %d: preempted flag disagrees with the entity" % k)
            digest[i], normal[i] = r["digest"], False
            cur[i], susp[i] = t, False
        if normal[i]:
            if ev["decision"] != "skip" or "slice" in ev or "budget" in ev or "resumed" in ev:
                bad("event %d: finished entity must be skipped" % k)
            continue
        if ev["decision"] != "run" or ev.get("budget") != budget or "slice" not in ev:
            bad("event %d: unfinished entity must run with the budget" % k)
        if ("resumed" in ev) != (susp[i] and "rearm" not in ev):
            bad("event %d: resumed flag disagrees with the entity" % k)
        if "resumed" in ev:
            cur[i] = cur[i].b
            digest[i], susp[i] = cur[i].hexdigest(), False
        sl = ev["slice"]
        if sl["start_digest"] != digest[i]:
            bad("event %d: slice does not start at the entity's term" % k)
        if sl["strategy"] != strat:
            bad("event %d: slice strategy differs from the policy" % k)
        try:
            if Config(sl["config"]).canonical != cfg.canonical:
                bad("event %d: slice configuration differs from the log's" % k)
            check_trace(sl, require_strategy=True, fuel=budget, stop=True)
        except Reject as e:
            bad("event %d: slice rejected (%s): %s" % (k, e.cls, e.msg))
        try:
            cur[i] = parse_term(sl["final"])
        except Syntax:
            bad("event %d: final term of the slice does not parse" % k)
        digest[i], susp[i] = sl["final_digest"], at_boundary(cur[i])
        normal[i] = not sl["exhausted"] and not susp[i]

    if len(doc["final"]) != n:
        bad("wrong number of final states")
    for i, f in zip(ids, doc["final"]):
        if f["id"] != i or f["digest"] != digest[i] or f.get("normal", False) != normal[i] \
                or f.get("suspended", False) != susp[i]:
            bad("final state of %r does not match the chain" % i)
        try:
            t = parse_term(f["term"])
        except Syntax:
            bad("final term of %r does not parse" % i)
        if t.hexdigest() != f["digest"]:
            bad("final term of %r does not match its digest" % i)


# -------------------------------------------------------------- effects (18.8)

DISPOSITIONS = ("committed", "denied", "failed", "malformed", "no-output", "exhausted", "truncated")
EFFECTS_DOMAIN = "spherepop-effects/2"
GENESIS = hexsha(EFFECTS_DOMAIN.encode("ascii"))


def entry_hash(e):
    b = framed(EFFECTS_DOMAIN)
    b += struct.pack(">Q", e["seq"])
    b += struct.pack(">q", e["tick"])
    b += framed(e["subject"]) + framed(e.get("ref", ""))
    b += struct.pack(">q", e["index"])
    b += framed(e.get("request", "")) + framed(e["disposition"])
    b += framed(e.get("reason", "")) + framed(e["prev"])
    return hexsha(b)


def load_schema(text):
    doc = strict_load(text, SCHEMA_SHAPE, "schema")
    out = {}
    for kind, t in doc["targets"].items():
        if t["id"] not in ("sym", "int"):
            raise Reject("schema", "unknown id type %r" % t["id"])
        verbs = {}
        for v, args in t["verbs"].items():
            if any(a not in ("sym", "int") for a in args):
                raise Reject("schema", "unknown argument type in %r" % v)
            verbs[v] = args
        out[kind] = (t["id"], verbs)
    return out


SYM = re.compile(r"[a-z][A-Za-z0-9_']*\Z")
INT = re.compile(r"(0|[1-9][0-9]*)\Z")


def atom_ok(node, typ):
    if node.kind != ATOM:
        return False
    if typ == "sym":
        return SYM.match(node.name) is not None
    return INT.match(node.name) is not None and int(node.name) <= 4294967295


def decodes(t, schema):
    head, n = spine(t)
    if head.kind == ATOM and head.name == "atomic":
        args = spine_args(t)
        return len(args) >= 1 and all(decodes_single(a, schema) for a in args)
    return decodes_single(t, schema)


def decodes_single(t, schema):
    head, n = spine(t)
    if not (head.kind == ATOM and head.name == "effect" and n >= 2):
        return False
    args = spine_args(t)
    target = args[0]
    if not (target.kind == APP and target.a.kind == ATOM and target.b.kind == ATOM):
        return False
    if target.a.name not in schema:
        return False
    idtype, verbs = schema[target.a.name]
    if not atom_ok(target.b, idtype):
        return False
    verb = args[1]
    if verb.kind != ATOM or verb.name not in verbs:
        return False
    want = verbs[verb.name]
    rest = args[2:]
    return len(rest) == len(want) and all(atom_ok(a, ty) for a, ty in zip(rest, want))


def load_effects(text):
    entries = []
    for ln, line in enumerate(text.split("\n")):
        if line.strip(" \t\r") == "":
            continue
        entries.append(strict_load(line, EFFECT_SHAPE, "chain"))
    return entries


def verify_chain(entries):
    prev = GENESIS
    for i, e in enumerate(entries):
        if e["seq"] != i or e["prev"] != prev or e["index"] < -1 or e["hash"] != entry_hash(e):
            raise Reject("chain", "entry %d does not verify" % i)
        prev = e["hash"]


def verify_schema(entries, schema):
    def bad(i, msg):
        raise Reject("log-entry", "entry %d: %s" % (i, msg))

    for i, e in enumerate(entries):
        d = e["disposition"]
        if d not in DISPOSITIONS:
            bad(i, "unknown disposition %r" % d)
        req = e.get("request", "")
        if d in ("committed", "denied", "failed", "malformed"):
            if req == "" or e["index"] < 0:
                bad(i, "no request or no request index")
            try:
                t = parse_term(req)
            except Syntax as ex:
                if d == "malformed":
                    continue
                bad(i, "request does not parse: %s" % ex)
            if t.text() != req:
                bad(i, "request is not in canonical text")
            ok = decodes(t, schema)
            if d == "malformed" and ok:
                bad(i, "recorded malformed but decodes")
            if d != "malformed" and not ok:
                bad(i, "recorded %s but does not decode" % d)
        elif e["index"] != -1 or req != "":
            bad(i, "%s concerns the whole output" % d)


def check_effects(entries, schema=None, anchor=None):
    verify_chain(entries)
    if schema is not None:
        verify_schema(entries, schema)
    if anchor is not None:
        head = entries[-1]["hash"] if entries else GENESIS
        if head != anchor:
            raise Reject("anchor", "head does not match the anchor")


# -------------------------------------------------------------------- driver


def read(path):
    try:
        with open(path, "rb") as f:
            return f.read().decode("utf-8")
    except (OSError, UnicodeDecodeError) as e:
        raise SystemExit_(2, "cannot read %s: %s" % (path, e))


class SystemExit_(Exception):
    def __init__(self, code, msg):
        super().__init__(msg)
        self.code, self.msg = code, msg


def verify_file(kind, path, opts, base=""):
    text = read(base + path)
    if kind == "trace":
        doc = strict_load(text, TRACE_SHAPE, "format")
        check_trace(doc, opts.get("require_strategy", False), opts.get("max_steps"), opts.get("fuel"),
                    opts.get("stop", False))
    elif kind == "sched":
        doc = strict_load(text, SCHED_SHAPE, "log-rejected")
        check_sched(doc)
    elif kind == "effects":
        schema = load_schema(read(base + opts["schema"])) if opts.get("schema") else None
        check_effects(load_effects(text), schema, opts.get("anchor"))
    else:
        raise SystemExit_(2, "unknown kind %r" % kind)


def verdict(kind, path, opts, base=""):
    try:
        verify_file(kind, path, opts, base)
        return ("valid", "")
    except Reject as r:
        return ("invalid", r.cls)
    except RecursionError:
        return ("invalid", "limit")


def cmd_corpus(manifest_path):
    import os

    base = os.path.dirname(os.path.abspath(manifest_path)) + os.sep
    man = json.loads(read(manifest_path))
    failures = 0
    for c in man["cases"]:
        got, cls = verdict(c["kind"], c["file"], c.get("options", {}), base)
        ok = got == c["expect"] and (c["expect"] == "valid" or "class" not in c or c["class"] == cls)
        print(json.dumps({"id": c["id"], "verdict": got, "class": cls, "ok": ok}))
        failures += not ok
    return 1 if failures else 0


def main(argv):
    if len(argv) < 2:
        print(__doc__, file=sys.stderr)
        return 2
    cmd, args = argv[1], argv[2:]
    try:
        if cmd == "corpus" and len(args) == 1:
            return cmd_corpus(args[0])
        if cmd not in ("trace", "sched", "effects") or not args:
            print(__doc__, file=sys.stderr)
            return 2
        path, opts, i = args[0], {}, 1
        while i < len(args):
            a = args[i]
            if a == "--require-strategy" and cmd == "trace":
                opts["require_strategy"] = True
                i += 1
            elif a == "--stop" and cmd == "trace":
                opts["stop"] = True
                i += 1
            elif a in ("--max-steps", "--fuel") and cmd == "trace" and i + 1 < len(args):
                opts[a[2:].replace("-", "_")] = int(args[i + 1])
                i += 2
            elif a in ("--schema", "--anchor") and cmd == "effects" and i + 1 < len(args):
                opts[a[2:]] = args[i + 1]
                i += 2
            else:
                print(__doc__, file=sys.stderr)
                return 2
        try:
            verify_file(cmd, path, opts)
        except Reject as r:
            print("invalid: %s: %s" % (r.cls, r.msg))
            return 1
        print("valid")
        return 0
    except SystemExit_ as e:
        print(e.msg, file=sys.stderr)
        return e.code


if __name__ == "__main__":
    result = []
    sys.setrecursionlimit(200000)
    threading.stack_size(512 * 1024 * 1024)
    t = threading.Thread(target=lambda: result.append(main(sys.argv)))
    t.start()
    t.join()
    sys.exit(result[0] if result else 2)
