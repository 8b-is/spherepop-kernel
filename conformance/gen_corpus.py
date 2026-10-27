#!/usr/bin/env python3
"""Generates conformance/cases and conformance/manifest.json.

Valid records come from two places: seeds/ (written by the Go implementation)
and this script's own reducer and sealer (written from SPEC section 18 with
the Python reference verifier's functions). Invalid records are made by
mutation. Every case states the verdict and failure class SPEC section 18
requires. Both verifiers are then run against the whole set, so a case is a
test of the verifiers and of the specification, not of the generator.

    python3 gen_corpus.py        (run from anywhere)
"""
import copy
import json
import os
import random
import shutil
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "..", "tools", "verify-py"))
import spherepop_verify as V  # noqa: E402

sys.setrecursionlimit(100000)

CASES = os.path.join(HERE, "cases")
SEEDS = os.path.join(HERE, "seeds")
manifest = []
used = set()


def seed(name):
    with open(os.path.join(SEEDS, name), encoding="utf-8") as f:
        return f.read()


def seed_json(name):
    return json.loads(seed(name))


def dumps(doc):
    return json.dumps(doc, indent=2) + "\n"


def add(cid, kind, text, expect, cls=None, options=None, note="", ext=None):
    assert cid not in used, cid
    used.add(cid)
    ext = ext or ("jsonl" if kind == "effects" else "json")
    fname = "%s.%s" % (cid, ext)
    with open(os.path.join(CASES, fname), "w", encoding="utf-8", newline="") as f:
        f.write(text if isinstance(text, str) else dumps(text))
    c = {"id": cid, "kind": kind, "file": "cases/" + fname, "expect": expect}
    if cls:
        c["class"] = cls
    if options:
        c["options"] = options
    if note:
        c["note"] = note
    manifest.append(c)


# ------------------------------------------------------------ trace builders


def cfg_obj(spec):
    return V.Config(spec)


def seal_trace(doc):
    cfg = V.Config(doc["config"])
    prev = V.chain_sum("spherepop-trace/1", doc["start_digest"],
                       V.chain_sum("spherepop-config/1", cfg.canonical))
    for st in doc["steps"]:
        st["link"] = V.chain_sum(prev, st["rule"], st["path"], st["after"])
        prev = st["link"]
    doc["head"] = prev
    return doc


def all_redexes(root, cfg):
    out = []

    def visit(node, mask, path):
        if node.kind == V.APP:
            r = V.redex_rule(node, mask, cfg)
            if r:
                out.append((r, path))
            visit(node.a, mask, path + "L")
            visit(node.b, mask, path + "R")
        elif node.kind == V.BRACKET:
            visit(node.b, mask & cfg.perm_of(node.name), path + "B")

    visit(root, cfg.root, "")
    return out


def mk_trace(start, spec, strategy="lo", fuel=50, pick=None, stop=False):
    """Reduce start by the strategy (or by pick(redexes) if given)."""
    cfg = V.Config(spec)
    t = V.parse_term(start)
    doc = {"format": "spherepop-trace/1", "config": copy.deepcopy(spec), "start": start,
           "start_digest": t.hexdigest(), "strategy": strategy, "fuel": fuel, "steps": []}
    n = 0
    while n < fuel:
        if stop and V.at_boundary(t):
            break
        if pick:
            rs = all_redexes(t, cfg)
            fr = pick(rs) if rs else None
        else:
            fr = V.first_redex(t, strategy, cfg)
        if fr is None:
            break
        t2 = V.apply_step(t, fr[0], fr[1], cfg)
        doc["steps"].append({"rule": fr[0], "path": fr[1], "after": t2.hexdigest(), "link": ""})
        t, n = t2, n + 1
    doc["final"], doc["final_digest"] = t.text(), t.hexdigest()
    doc["exhausted"] = V.has_redex(t, cfg) and not (stop and V.at_boundary(t))
    doc["head"] = ""
    return seal_trace(doc)


ALL = ["W", "K", "C", "S"]
CFG = {"root": ALL, "perm": {"p": ALL, "q": ["S"]}, "gated": ["W", "K", "C"], "absorb": "s", "sc": "settled"}
START = "sc_p (K a b) d (<K c e>_q (S I I x))"


def cfg_with(**kw):
    c = copy.deepcopy(CFG)
    c.update(kw)
    return c


# ------------------------------------------------------------------ traces


def gen_traces():
    base = mk_trace(START, CFG)
    assert len(base["steps"]) >= 5, len(base["steps"])
    add("trace-valid-scoped", "trace", base, "valid", options={"require_strategy": True})
    for n in ("go_ski", "go_scoped", "go_gated", "go_exhausted"):
        add("trace-valid-seed-" + n, "trace", seed(n + ".json"), "valid", options={"require_strategy": True})
    add("trace-valid-zero-steps", "trace", mk_trace("a b c", CFG), "valid", options={"require_strategy": True})
    ex = mk_trace("W W W", CFG, fuel=4)
    assert ex["exhausted"]
    add("trace-valid-exhausted", "trace", ex, "valid", options={"require_strategy": True, "fuel": 4})
    add("trace-exhausted-agreed-fuel-differs", "trace", ex, "invalid", "status-mismatch", {"fuel": 5})

    # Non-canonical start text with the right digest is accepted.
    d = copy.deepcopy(base)
    d["start"] = "  sc_p ( K a b )   d  ( <K   c e>_q (S I I x ) )  "
    add("trace-valid-noncanonical-start-text", "trace", seal_trace(d), "valid", options={"require_strategy": True})

    # Fuel above steps for a normal form is accepted unless agreed otherwise.
    d = copy.deepcopy(base)
    d["fuel"] = 1000
    add("trace-valid-larger-fuel-than-needed", "trace", seal_trace(d), "valid")
    add("trace-fuel-larger-than-agreed", "trace", seal_trace(d), "invalid", "status-mismatch", {"fuel": 50})

    # Execution boundary (SPEC 18.6): a root (yield k) ends a run.
    stp = {"stop": True, "require_strategy": True}
    y0 = mk_trace("yield (K a b)", CFG, stop=True)
    assert y0["steps"] == [] and not y0["exhausted"]
    add("trace-stop-at-start-valid", "trace", y0, "valid", options=stp)
    add("trace-stop-ignored-without-option", "trace", y0, "invalid", "status-mismatch", {"require_strategy": True},
        note="a stopped trace is not a finished one unless the checker is told about the boundary")
    y1 = mk_trace("I (yield (K a b))", CFG, stop=True)
    assert len(y1["steps"]) == 1 and y1["final"] == "yield (K a b)"
    add("trace-stop-after-one-step-valid", "trace", y1, "valid", options=stp)
    y2 = mk_trace("I (yield (K a b))", CFG, "li", stop=True)
    add("trace-stop-innermost-reduces-argument-first", "trace", y2, "valid", options=stp,
        note="the residual continuation depends on the strategy: yield a")
    add("trace-stop-yield-with-two-arguments-is-not-a-boundary", "trace",
        mk_trace("yield (K a b) c", CFG, stop=True), "valid", options=stp)
    add("trace-stop-discarded-yield-never-stops", "trace", mk_trace("K x (yield y)", CFG, stop=True), "valid", options=stp)
    past = mk_trace("yield (K a b)", CFG)  # reduces past the boundary
    assert len(past["steps"]) == 1
    add("trace-step-after-stop", "trace", past, "invalid", "step-after-stop", stp)
    add("trace-step-past-yield-accepted-without-boundary", "trace", past, "valid", options={"require_strategy": True})
    add("trace-stop-fuel-spent-exactly-at-the-root-yield", "trace", mk_trace("I (I (yield z))", CFG, fuel=2, stop=True), "valid", options=stp)
    d = copy.deepcopy(y1)
    d["exhausted"] = True
    add("trace-stop-claims-exhausted", "trace", seal_trace(d), "invalid", "status-mismatch", stp)

    # Format-level rejections.
    def top(cid, fn, cls="format", opts=None, note=""):
        d = copy.deepcopy(base)
        fn(d)
        add(cid, "trace", d, "invalid", cls, opts, note)

    top("trace-bad-format-string", lambda d: d.update(format="spherepop-trace/2"))
    top("trace-empty-format-string", lambda d: d.update(format=""))
    for k in list(base):
        top("trace-missing-" + k, lambda d, k=k: d.pop(k))
        top("trace-null-" + k, lambda d, k=k: d.update({k: None}))
    top("trace-unknown-member", lambda d: d.update(extra=1))
    top("trace-unknown-member-in-step", lambda d: d["steps"][0].update(extra=1))
    top("trace-unknown-member-in-config", lambda d: d["config"].update(extra=1))
    top("trace-missing-member-in-step", lambda d: d["steps"][0].pop("after"))
    top("trace-missing-config-gated", lambda d: d["config"].pop("gated"))
    top("trace-null-in-step", lambda d: d["steps"][0].update(path=None))
    top("trace-fuel-is-string", lambda d: d.update(fuel="50"))
    top("trace-fuel-negative", lambda d: d.update(fuel=-1))
    top("trace-fuel-2pow64", lambda d: d.update(fuel=2**64))
    top("trace-exhausted-is-int", lambda d: d.update(exhausted=0))
    top("trace-steps-is-object", lambda d: d.update(steps={}))
    top("trace-config-is-array", lambda d: d.update(config=[]))
    top("trace-optional-perm-empty-object", lambda d: d["config"].update(perm={}),
        note="an optional member present with its zero value has a second spelling")
    top("trace-capitalised-member", lambda d: d.update(Fuel=d.pop("fuel")))
    add("trace-not-an-object", "trace", "[]\n", "invalid", "format")
    text = dumps(base)
    add("trace-fuel-fraction", "trace", text.replace('"fuel": 50', '"fuel": 50.0'), "invalid", "format")
    add("trace-fuel-exponent", "trace", text.replace('"fuel": 50', '"fuel": 5e1'), "invalid", "format")
    add("trace-duplicate-member", "trace", text.replace('"fuel": 50,', '"fuel": 50,\n  "fuel": 50,'), "invalid", "format")
    add("trace-trailing-data", "trace", text + "{}\n", "invalid", "format")
    add("trace-nan", "trace", text.replace('"fuel": 50', '"fuel": NaN'), "invalid", "format")
    add("trace-empty-file", "trace", "", "invalid", "format")
    add("trace-not-json", "trace", "this is not json\n", "invalid", "format")
    for i, frac in enumerate((0.1, 0.3, 0.5, 0.7, 0.9)):
        add("trace-truncated-%d" % i, "trace", text[: int(len(text) * frac)], "invalid", "format")

    # Configuration validity.
    def cfgmut(cid, fn, cls="format"):
        d = copy.deepcopy(base)
        fn(d["config"])
        add(cid, "trace", d, "invalid", cls)

    cfgmut("trace-config-unknown-absorb", lambda c: c.update(absorb="all"))
    cfgmut("trace-config-unknown-sc", lambda c: c.update(sc="never"))
    cfgmut("trace-config-bad-permission-name", lambda c: c.update(root=["W", "X"]))
    cfgmut("trace-config-lowercase-permission-name", lambda c: c.update(root=["w"]))
    cfgmut("trace-config-repeated-permission", lambda c: c.update(root=["W", "W"]))
    cfgmut("trace-config-repeated-in-label", lambda c: c["perm"].update(p=["K", "K"]))
    cfgmut("trace-config-label-with-underscore", lambda c: c["perm"].update(a_b=["K"]))
    cfgmut("trace-config-empty-label", lambda c: c["perm"].update({"": ["K"]}))
    cfgmut("trace-config-label-with-quote", lambda c: c["perm"].update({'a"b': ["K"]}))
    cfgmut("trace-config-label-non-ascii", lambda c: c["perm"].update({"é": ["K"]}))
    cfgmut("trace-config-gated-bad-name", lambda c: c.update(gated=["I"]))

    # Config array order is hashed as given, so a reordered root is a valid
    # configuration whose chain differs; resealed, it verifies.
    d = copy.deepcopy(base)
    d["config"]["root"] = ["S", "C", "K", "W"]
    add("trace-valid-root-order-reordered-and-resealed", "trace", seal_trace(d), "valid")
    d = copy.deepcopy(base)
    d["config"]["root"] = ["S", "C", "K", "W"]
    add("trace-root-order-reordered-not-resealed", "trace", d, "invalid", "chain")

    # Start term.
    d = copy.deepcopy(base)
    d["start"] = "sc_p (K a b) e (<K c e>_q (S I I x))"
    add("trace-start-text-changed", "trace", d, "invalid", "start-mismatch")
    d = copy.deepcopy(base)
    d["start"] = "sc_p (K a b"
    add("trace-start-syntax-error", "trace", d, "invalid", "format")
    d = copy.deepcopy(base)
    d["start_digest"] = d["start_digest"].upper()
    add("trace-start-digest-uppercase", "trace", d, "invalid", "start-mismatch")
    d = copy.deepcopy(base)
    d["start_digest"] = d["start_digest"][:-1]
    add("trace-start-digest-short", "trace", d, "invalid", "start-mismatch")
    d = mk_trace("<a>_z b", CFG)  # label z has no entry
    d["start_digest"] = V.parse_term("<a>_z b").hexdigest()
    add("trace-start-label-without-entry", "trace", seal_trace(d), "invalid", "format")
    d = copy.deepcopy(base)
    d["start"] = "SK a"
    add("trace-start-juxtaposed-constants", "trace", d, "invalid", "format")
    d = copy.deepcopy(base)
    d["start"] = "sc_ a"
    add("trace-start-empty-scope-label", "trace", d, "invalid", "format")

    # Strategy field.
    d = copy.deepcopy(base)
    d["strategy"] = "zz"
    add("trace-unknown-strategy-not-required", "trace", d, "valid")
    add("trace-unknown-strategy-required", "trace", d, "invalid", "format", {"require_strategy": True})
    d = copy.deepcopy(base)
    d["strategy"] = "ro"
    add("trace-declared-strategy-wrong", "trace", d, "invalid", "strategy-deviation", {"require_strategy": True},
        note="the steps are lo steps; whether ro would also take them depends on the term")
    add("trace-declared-strategy-wrong-not-required", "trace", d, "valid")

    # Steps made by a strategy that is not the declared one.
    def second(rs):
        return rs[1] if len(rs) > 1 else rs[0]

    off = mk_trace(START + " (K u v) (K w z)", CFG, "lo", pick=second)
    off["strategy"] = "lo"
    add("trace-nonstrategy-steps-not-required", "trace", off, "valid")
    add("trace-nonstrategy-steps-required", "trace", off, "invalid", "strategy-deviation", {"require_strategy": True})
    add("trace-max-steps-exceeded", "trace", base, "invalid", "format", {"max_steps": 2})
    add("trace-max-steps-equal", "trace", base, "valid", options={"max_steps": len(base["steps"])})

    # Chain tampering without resealing.
    def step(cid, fn, cls="chain", opts=None, note=""):
        d = copy.deepcopy(base)
        fn(d)
        add(cid, "trace", d, "invalid", cls, opts, note)

    step("trace-link-altered", lambda d: d["steps"][1].update(link="0" * 64))
    step("trace-link-uppercase", lambda d: d["steps"][1].update(link=d["steps"][1]["link"].upper()))
    step("trace-after-altered", lambda d: d["steps"][1].update(after="1" * 64))
    step("trace-rule-altered", lambda d: d["steps"][0].update(rule="S"))
    step("trace-path-altered", lambda d: d["steps"][0].update(path=d["steps"][0]["path"] + "L"))
    step("trace-head-altered", lambda d: d.update(head="0" * 64))
    step("trace-head-empty", lambda d: d.update(head=""))
    step("trace-steps-swapped", lambda d: d["steps"].insert(0, d["steps"].pop(1)))
    step("trace-step-deleted", lambda d: d["steps"].pop(1))
    step("trace-step-duplicated", lambda d: d["steps"].insert(1, copy.deepcopy(d["steps"][1])))
    step("trace-last-step-dropped", lambda d: d["steps"].pop())
    step("trace-steps-emptied", lambda d: d.update(steps=[]))

    # Resealed semantic tampering: the chain verifies, the step does not.
    def resealed(cid, fn, cls, opts=None, note=""):
        d = copy.deepcopy(base)
        fn(d)
        add(cid, "trace", seal_trace(d), "invalid", cls, opts, note)

    resealed("trace-resealed-rule-changed", lambda d: d["steps"][0].update(rule="S"), "step-rejected")
    resealed("trace-resealed-rule-unknown", lambda d: d["steps"][0].update(rule="Z"), "step-rejected")
    resealed("trace-resealed-rule-lowercase", lambda d: d["steps"][0].update(rule="k"), "step-rejected")
    resealed("trace-resealed-rule-empty", lambda d: d["steps"][0].update(rule=""), "step-rejected")
    resealed("trace-resealed-path-bad-character", lambda d: d["steps"][0].update(path="X"), "step-rejected")
    resealed("trace-resealed-path-too-long", lambda d: d["steps"][0].update(path=d["steps"][0]["path"] + "RRRRRRRR"), "step-rejected")
    resealed("trace-resealed-path-lowercase", lambda d: d["steps"][0].update(path=d["steps"][0]["path"].lower() or "l"), "step-rejected")
    resealed("trace-resealed-after-wrong", lambda d: d["steps"][0].update(after="2" * 64), "digest-mismatch")
    resealed("trace-resealed-after-uppercase", lambda d: d["steps"][0].update(after=d["steps"][0]["after"].upper()), "digest-mismatch")
    resealed("trace-resealed-after-of-step-before", lambda d: d["steps"][1].update(after=d["steps"][0]["after"]), "digest-mismatch")
    resealed("trace-resealed-final-text-changed", lambda d: d.update(final="a b"), "final-mismatch")
    resealed("trace-resealed-final-digest-changed", lambda d: d.update(final_digest="3" * 64), "final-mismatch")
    resealed("trace-resealed-final-noncanonical-text",
             lambda d: d.update(final=d["final"].replace(" ", "  ", 1)), "final-mismatch",
             note="final must equal the canonical text, unlike start")
    resealed("trace-resealed-exhausted-flipped", lambda d: d.update(exhausted=not d["exhausted"]), "status-mismatch")
    resealed("trace-resealed-fuel-below-steps", lambda d: d.update(fuel=len(d["steps"]) - 1), "status-mismatch")
    d = copy.deepcopy(ex)
    d["fuel"] = 5
    add("trace-exhausted-with-steps-not-equal-fuel", "trace", seal_trace(d), "invalid", "status-mismatch")
    d = copy.deepcopy(ex)
    d["exhausted"] = False
    add("trace-exhausted-claimed-normal", "trace", seal_trace(d), "invalid", "status-mismatch")
    d = copy.deepcopy(base)
    d["steps"].pop()
    d["final"] = "unused"
    add("trace-cut-short-claims-normal", "trace", seal_trace(d), "invalid", "final-mismatch")

    # Permission and side-condition violations, each resealed.
    gated = mk_trace("<K a b>_q (S I I x)", CFG)
    # Inside q only S is permitted, so K at path LB is not a redex there.
    add("trace-valid-gated-k-untouched", "trace", gated, "valid", options={"require_strategy": True})
    g = {"format": "spherepop-trace/1", "config": copy.deepcopy(CFG), "start": "<K a b>_q (S I I x)",
         "start_digest": V.parse_term("<K a b>_q (S I I x)").hexdigest(), "strategy": "lo", "fuel": 1,
         "steps": [{"rule": "K", "path": "LB", "after": V.parse_term("<a>_q (S I I x)").hexdigest(), "link": ""}],
         "final": "<a>_q (S I I x)", "final_digest": V.parse_term("<a>_q (S I I x)").hexdigest(),
         "exhausted": False, "head": ""}
    add("trace-gated-constant-not-permitted-in-bracket", "trace", seal_trace(g), "invalid", "step-rejected")
    g2 = copy.deepcopy(g)
    g2["config"]["perm"]["q"] = ["K"]
    g2["config"]["gated"] = ["W", "C"]  # K ungated: permitted everywhere
    g2["config"]["perm"]["q"] = []
    g2["exhausted"] = V.has_redex(V.parse_term(g2["final"]), V.Config(g2["config"]))
    add("trace-ungated-constant-always-permitted", "trace", seal_trace(g2), "valid")
    g3 = copy.deepcopy(g)
    g3["config"]["perm"]["q"] = ["K"]
    g3["exhausted"] = V.has_redex(V.parse_term(g3["final"]), V.Config(g3["config"]))
    add("trace-gated-constant-permitted-in-bracket", "trace", seal_trace(g3), "valid")

    # Absorption and scope side conditions, mode by mode.
    def one_step_trace(start, after, rule, path, spec, final_exh=None):
        t0, t1 = V.parse_term(start), V.parse_term(after)
        d = {"format": "spherepop-trace/1", "config": spec, "start": start, "start_digest": t0.hexdigest(),
             "strategy": "lo", "fuel": 1,
             "steps": [{"rule": rule, "path": path, "after": t1.hexdigest(), "link": ""}],
             "final": t1.text(), "final_digest": t1.hexdigest(),
             "exhausted": V.has_redex(t1, V.Config(spec)), "head": ""}
        return seal_trace(d)

    # <u>_p v with v = K a b (contractible): Abs needs a settled/normal argument in modes s, nf, u.
    st = "<z>_p (K a b)"
    af = "<z (K a b)>_p"
    for mode, ok in (("off", False), ("any", True), ("nf", False), ("s", False), ("u", False)):
        spec = cfg_with(absorb=mode)
        d = one_step_trace(st, af, "Abs", "", spec)
        add("trace-abs-contractible-arg-mode-%s" % mode, "trace", d, "valid" if ok else "invalid",
            None if ok else "step-rejected")
    # v = a b (normal, settled): allowed in every mode but off.
    st, af = "<z>_p (a b)", "<z (a b)>_p"
    for mode, ok in (("off", False), ("any", True), ("nf", True), ("s", True), ("u", True)):
        d = one_step_trace(st, af, "Abs", "", cfg_with(absorb=mode))
        add("trace-abs-settled-arg-mode-%s" % mode, "trace", d, "valid" if ok else "invalid",
            None if ok else "step-rejected")
    # v = <a>_p b : R-normal but not settled (bracket as function child).
    st, af = "<z>_p (<a>_p b)", "<z (<a>_p b)>_p"
    for mode, ok in (("nf", True), ("s", False), ("u", True)):
        d = one_step_trace(st, af, "Abs", "", cfg_with(absorb=mode, root=["W", "K", "C", "S"], gated=["W", "K", "C"]))
        add("trace-abs-bracket-headed-arg-mode-%s" % mode, "trace", d, "valid" if ok else "invalid",
            None if ok else "step-rejected",
            note="u accepts it because the argument is insensitive only if no gated constant occurs: none does here")
    # Same argument with a gated constant inside: R-normal because K is blocked in q, not insensitive.
    st = "<z>_p (<K a>_q b)"
    af = "<z (<K a>_q b)>_p"
    for mode, ok in (("nf", True), ("s", False), ("u", False)):
        d = one_step_trace(st, af, "Abs", "", cfg_with(absorb=mode))
        add("trace-abs-blocked-gated-in-bracket-arg-mode-%s" % mode, "trace", d, "valid" if ok else "invalid",
            None if ok else "step-rejected")
    # Sc side condition.
    for arg, tag in (("(K a b)", "contractible"), ("(a b)", "settled"), ("(<a>_p b)", "bracket-headed"),
                     ("(<K a>_q b)", "blocked-gated")):
        st, af = "sc_p " + arg, "<" + arg[1:-1] + ">_p"
        for mode in ("any", "safe", "settled"):
            spec = cfg_with(sc=mode)
            cfg = V.Config(spec)
            argt = V.parse_term(arg)
            ok = V.sc_ok(argt, cfg.root, cfg)  # independent of the step machinery under test
            d = one_step_trace(st, af, "Sc", "", spec)
            add("trace-sc-%s-arg-mode-%s" % (tag, mode), "trace", d, "valid" if ok else "invalid",
                None if ok else "step-rejected")

    # Random differential traces, valid by construction.
    rng = random.Random(20260101)
    leaves = ["a", "b", "c", "x", "S", "K", "I", "B", "C", "W"]

    def rterm(depth):
        r = rng.random()
        if depth <= 0 or r < 0.25:
            return rng.choice(leaves)
        if r < 0.55:
            return "%s %s" % (rterm(depth - 1), "(%s)" % rterm(depth - 1))
        if r < 0.7:
            return "sc_%s (%s)" % (rng.choice("pq"), rterm(depth - 1))
        if r < 0.9:
            return "<%s>_%s" % (rterm(depth - 1), rng.choice("pq"))
        return "(%s) (%s)" % (rterm(depth - 1), rterm(depth - 1))

    n = 0
    while n < 60:
        spec = {
            "root": [x for x in ALL if rng.random() < 0.8],
            "perm": {"p": [x for x in ALL if rng.random() < 0.6], "q": [x for x in ALL if rng.random() < 0.4]},
            "gated": [x for x in ["W", "K", "C", "S"] if rng.random() < 0.6],
            "absorb": rng.choice(V.ABSORB_MODES),
            "sc": rng.choice(V.SCOPE_MODES),
        }
        src = rterm(4)
        try:
            V.parse_term(src)
        except V.Syntax:
            continue
        strat = rng.choice(V.STRATEGIES)
        d = mk_trace(src, spec, strat, fuel=rng.choice([3, 8, 40]))
        if not d["steps"]:
            continue
        add("trace-random-%02d" % n, "trace", d, "valid", options={"require_strategy": True},
            note="%s %s/%s" % (strat, spec["absorb"], spec["sc"]))
        n += 1


# ------------------------------------------------------------------ sched


def reseal_all_slices(doc):
    for ev in doc["events"]:
        if "slice" in ev:
            seal_trace(ev["slice"])


def mk_sched(spec, strategy, budget, entities, ticks, rearms=None):
    """A scheduler log built by the rules of SPEC 18.7."""
    ids = sorted(entities)
    cur = {i: V.parse_term(entities[i]) for i in ids}
    done = {i: False for i in ids}
    susp = {i: False for i in ids}
    doc = {"format": "spherepop-sched/1", "config": copy.deepcopy(spec),
           "policy": {"strategy": strategy, "budget": budget},
           "initial": [{"id": i, "term": cur[i].text(), "digest": cur[i].hexdigest()} for i in ids],
           "events": [], "final": []}
    rearms = rearms or {}
    for tick in range(ticks):
        for i in ids:
            ev = {"tick": tick, "entity": i}
            if (tick, i) in rearms:
                nt = V.parse_term(rearms[(tick, i)])
                r = {"term": nt.text(), "digest": nt.hexdigest()}
                if not done[i]:
                    r["preempted"] = True
                ev["rearm"] = r
                cur[i], done[i], susp[i] = nt, False, False
            elif susp[i]:
                cur[i] = cur[i].b
                ev["resumed"] = True
                susp[i] = False
            if done[i]:
                ev["decision"] = "skip"
            else:
                sl = mk_trace(cur[i].text(), spec, strategy, budget, stop=True)
                ev.update(decision="run", budget=budget, slice=sl)
                cur[i] = V.parse_term(sl["final"])
                susp[i] = V.at_boundary(cur[i])
                done[i] = not sl["exhausted"] and not susp[i]
            doc["events"].append(ev)
    for i in ids:
        f = {"id": i, "term": cur[i].text(), "digest": cur[i].hexdigest()}
        if done[i]:
            f["normal"] = True
        if susp[i]:
            f["suspended"] = True
        doc["final"].append(f)
    return doc


SCHED_SPEC = {"root": ALL, "perm": {"p": ALL, "q": ["S"]}, "gated": ["W", "K", "C"], "absorb": "s", "sc": "settled"}


def gen_sched():
    base = mk_sched(SCHED_SPEC, "lo", 3,
                    {"a": "K x y", "b": "W W W", "c": "I (I (I (I (I z))))", "d": "sc_p (K u v) w"},
                    5, rearms={(2, "a"): "K m n", (3, "b"): "I (K q r)", (4, "c"): "B a b c"})
    kinds = {(e["decision"], bool(e.get("rearm")), (e.get("rearm") or {}).get("preempted", False)) for e in base["events"]}
    assert ("skip", False, False) in kinds and ("run", True, True) in kinds, kinds
    assert any(f.get("normal") for f in base["final"])
    add("sched-valid-with-skips-and-rearms", "sched", base, "valid")
    for n in ("go_arena_sched", "go_sim_sched"):
        add("sched-valid-seed-" + n, "sched", seed(n + ".json"), "valid")
    empty = mk_sched(SCHED_SPEC, "lo", 3, {}, 0)
    add("sched-valid-no-entities", "sched", empty, "valid")
    one = mk_sched(SCHED_SPEC, "ri", 1, {"only": "a b"}, 3)
    add("sched-valid-one-entity-finished-immediately", "sched", one, "valid")
    add("sched-valid-zero-ticks", "sched", mk_sched(SCHED_SPEC, "lo", 3, {"a": "K x y"}, 0), "valid")

    def mut(cid, fn, cls="log-rejected", note="", doc=None):
        d = copy.deepcopy(doc or base)
        fn(d)
        add(cid, "sched", d, "invalid", cls, None, note)

    mut("sched-bad-format", lambda d: d.update(format="spherepop-sched/2"))
    for k in list(base):
        mut("sched-missing-" + k, lambda d, k=k: d.pop(k))
        mut("sched-null-" + k, lambda d, k=k: d.update({k: None}))
    mut("sched-unknown-member", lambda d: d.update(extra=1))
    mut("sched-unknown-member-in-policy", lambda d: d["policy"].update(extra=1))
    mut("sched-unknown-member-in-event", lambda d: d["events"][0].update(extra=1))
    mut("sched-unknown-member-in-initial", lambda d: d["initial"][0].update(extra=1))
    mut("sched-initial-normal-false-present", lambda d: d["initial"][0].update(normal=False),
        note="optional member present with its zero value")
    mut("sched-initial-normal-true", lambda d: d["initial"][0].update(normal=True))
    mut("sched-tick-is-string", lambda d: d["events"][0].update(tick="0"))
    mut("sched-budget-zero-in-policy", lambda d: d["policy"].update(budget=0))
    mut("sched-policy-strategy-unknown", lambda d: d["policy"].update(strategy="xx"))
    mut("sched-config-invalid-absorb", lambda d: d["config"].update(absorb="z"))
    add("sched-trailing-data", "sched", dumps(base) + "[]\n", "invalid", "log-rejected")
    text = dumps(base)
    for i, frac in enumerate((0.2, 0.5, 0.8)):
        add("sched-truncated-%d" % i, "sched", text[: int(len(text) * frac)], "invalid", "log-rejected")
    add("sched-empty-file", "sched", "", "invalid", "log-rejected")

    # Suspension (SPEC 18.7).
    sus = mk_sched(SCHED_SPEC, "lo", 3,
                   {"a": "yield (K x y)", "b": "I (yield (S K K z))", "c": "yield (yield (I q))",
                    "d": "K p (yield q)", "e": "I (I (I (I (I (yield w)))))",
                    "f": "yield (yield (yield (yield (yield (yield (yield (yield z)))))))"},
                   6, rearms={(3, "a"): "K m n"})
    assert any(e.get("resumed") for e in sus["events"]) and any(f.get("suspended") for f in sus["final"])
    add("sched-valid-suspensions", "sched", sus, "valid")
    add("sched-valid-seed-go-sim-yield", "sched", seed("go_sim_yield.json"), "valid")
    ys = mk_sched(SCHED_SPEC, "lo", 3, {"a": "yield (K x y)"}, 1)
    assert ys["final"][0].get("suspended")
    add("sched-valid-ends-suspended", "sched", ys, "valid")
    yr = mk_sched(SCHED_SPEC, "lo", 3, {"a": "yield (K x y)"}, 3, rearms={(1, "a"): "K m n"})
    assert yr["events"][1]["rearm"].get("preempted") and "resumed" not in yr["events"][1]
    add("sched-valid-rearm-of-suspended-entity-is-not-a-resume", "sched", yr, "valid")
    res_ev = next(i for i, e in enumerate(sus["events"]) if e.get("resumed"))
    ys2 = mk_sched(SCHED_SPEC, "lo", 3, {"a": "yield (K x y)"}, 2)
    stuck = mk_trace("yield (K x y)", SCHED_SPEC, "lo", 3, stop=True)
    def never_resumes(d):
        d["events"][1].pop("resumed")
        d["events"][1]["slice"] = stuck
        d["final"][0] = {"id": "a", "term": stuck["final"], "digest": stuck["final_digest"], "suspended": True}
    mut("sched-suspended-entity-never-resumed", never_resumes, doc=ys2,
        note="the slice repeats the suspension instead of resuming; only the resumed flag rule rejects it")
    mut("sched-resumed-flag-dropped", lambda d: d["events"][res_ev].pop("resumed"), doc=sus)
    mut("sched-resumed-on-running-entity", lambda d: d["events"][0].update(resumed=True), doc=sus)
    mut("sched-resumed-together-with-rearm", lambda d: d["events"][res_ev].update(
        rearm={"term": "K m n", "digest": V.parse_term("K m n").hexdigest(), "preempted": True}), doc=sus)
    mut("sched-resume-slice-starts-at-the-yield", lambda d: d["events"][res_ev].update(
        slice=mk_trace(sus["events"][res_ev - len(sus["initial"])]["slice"]["final"], SCHED_SPEC, "lo", 3, stop=True)), doc=sus)
    mut("sched-suspended-entity-skipped", lambda d: (d["events"][res_ev].update(decision="skip"), d["events"][res_ev].pop("slice"),
                                                    d["events"][res_ev].pop("budget"), d["events"][res_ev].pop("resumed")), doc=sus)
    mut("sched-final-suspended-flag-dropped", lambda d: d["final"][0].pop("suspended"), doc=ys)
    mut("sched-final-suspended-entity-marked-normal", lambda d: (d["final"][0].pop("suspended"), d["final"][0].update(normal=True)), doc=ys)
    mut("sched-final-suspended-and-normal", lambda d: d["final"][0].update(normal=True), doc=ys)
    mut("sched-final-suspended-on-finished-entity", lambda d: d["final"][3].update(suspended=True), doc=sus)
    mut("sched-initial-suspended-flag", lambda d: d["initial"][0].update(suspended=True), doc=ys)
    mut("sched-suspended-present-with-false", lambda d: d["final"][1].update(suspended=False), doc=sus)
    ign = mk_trace("yield (K x y)", SCHED_SPEC, "lo", 3)  # carries on past the boundary
    def carry_on(d):
        d["events"][0]["slice"] = ign
        d["final"][0] = {"id": "a", "term": ign["final"], "digest": ign["final_digest"], "normal": True}
    mut("sched-slice-carries-on-past-the-yield", carry_on, doc=ys)
    short = mk_trace("I (I (yield (K x y)))", SCHED_SPEC, "lo", 3, stop=True)
    stopped_early = copy.deepcopy(short)
    stopped_early["steps"].pop()
    stopped_early["final"], stopped_early["final_digest"] = "I (yield (K x y))", V.parse_term("I (yield (K x y))").hexdigest()
    seal_trace(stopped_early)
    ye = mk_sched(SCHED_SPEC, "lo", 3, {"a": "I (I (yield (K x y)))"}, 1)
    def stop_early(d):
        d["events"][0]["slice"] = stopped_early
        d["final"][0] = {"id": "a", "term": stopped_early["final"], "digest": stopped_early["final_digest"]}
    mut("sched-slice-stops-before-the-boundary-and-before-fuel", stop_early, doc=ye,
        note="neither exhausted nor finished: the claimed status is wrong")

    # Faults isolated from every later check: logs with no events, so that
    # only the check under test can reject them.
    z = mk_sched(SCHED_SPEC, "lo", 3, {"a": "K x y", "b": "I z"}, 0)
    add("sched-valid-two-entities-no-events", "sched", z, "valid")
    mut("sched-isolated-config-invalid", lambda d: d["config"].update(sc="never"), doc=z)
    mut("sched-isolated-policy-zero-budget", lambda d: d["policy"].update(budget=0), doc=z)
    mut("sched-isolated-policy-unknown-strategy", lambda d: d["policy"].update(strategy="qq"), doc=z)
    mut("sched-isolated-initial-empty-id", lambda d: (d["initial"][0].update(id=""), d["final"][0].update(id="")), doc=z)
    mut("sched-isolated-initial-duplicate-id",
        lambda d: (d["initial"][1].update(id="a"), d["final"][1].update(id="a")), doc=z)
    mut("sched-isolated-initial-unsorted",
        lambda d: (d["initial"].reverse(), d["final"].reverse()), doc=z)
    def unknown_label(d):
        t = V.parse_term("<k>_zz")
        for part in ("initial", "final"):
            d[part][0].update(term=t.text(), digest=t.hexdigest())
    mut("sched-isolated-initial-unknown-label", unknown_label, doc=z)
    mut("sched-isolated-initial-digest-wrong",
        lambda d: (d["initial"][0].update(digest="8" * 64), d["final"][0].update(digest="8" * 64)), doc=z)
    mut("sched-isolated-initial-term-syntax-error",
        lambda d: (d["initial"][0].update(term="K ("), d["final"][0].update(term="K (")), doc=z)
    mut("sched-isolated-initial-normal",
        lambda d: (d["initial"][0].update(normal=True), d["final"][0].update(normal=True)), doc=z)

    # A rearm whose term uses a label with no entry; term and digest agree.
    def rearm_unknown_label(d):
        t = V.parse_term("<k>_zz")
        d["events"][ev_re]["rearm"].update(term=t.text(), digest=t.hexdigest())
    ev_re = next(i for i, e in enumerate(base["events"]) if e.get("rearm") and e["rearm"].get("preempted"))
    mut("sched-rearm-unknown-label", rearm_unknown_label)

    # Rearm claims that no later check can see: the slice runs from the term
    # the digest names, so only the rearm's own consistency checks reject.
    re0 = mk_sched(SCHED_SPEC, "lo", 5, {"a": "I x"}, 2, rearms={(1, "a"): "I z"})
    def rearm_claims_other(d):
        d["events"][1]["rearm"]["term"] = "I y"
    mut("sched-rearm-digest-names-another-term", rearm_claims_other, doc=re0,
        note="the slice starts at I z, the digest says I z, the text says I y")
    def rearm_unparsable(d):
        d["events"][1]["rearm"]["term"] = "K ("
    mut("sched-rearm-text-unparsable-digest-real", rearm_unparsable, doc=re0)
    add("sched-valid-rearm-isolated", "sched", re0, "valid")
    zz = mk_sched(SCHED_SPEC, "lo", 3, {"a": "K x y"}, 0)
    mut("sched-final-term-unparsable-digest-real", lambda d: d["final"][0].update(term="K ("), doc=zz)

    # A slice that starts at the wrong term but ends where the right one would.
    twin = mk_sched(SCHED_SPEC, "lo", 5, {"a": "I (I x)"}, 1)
    assert twin["final"][0].get("normal")
    wrong_start = mk_trace("I x", SCHED_SPEC, "lo", 5)
    assert wrong_start["final_digest"] == twin["events"][0]["slice"]["final_digest"]
    mut("sched-slice-wrong-start-same-end",
        lambda d: d["events"][0].update(slice=wrong_start), doc=twin,
        note="only the start-digest check can reject this; the final states agree")

    # Initial entities.
    mut("sched-initial-duplicate-id", lambda d: d["initial"].append(copy.deepcopy(d["initial"][0])))
    mut("sched-initial-empty-id", lambda d: d["initial"][0].update(id=""))
    mut("sched-initial-unsorted", lambda d: d["initial"].reverse())
    mut("sched-initial-digest-wrong", lambda d: d["initial"][0].update(digest="4" * 64))
    mut("sched-initial-term-changed", lambda d: d["initial"][0].update(term="K x z"),
        note="the digest no longer matches the term text")
    mut("sched-initial-term-syntax-error", lambda d: d["initial"][0].update(term="K ("))
    d = copy.deepcopy(base)
    t = V.parse_term("<k>_zz")
    d["initial"][0].update(term=t.text(), digest=t.hexdigest())
    add("sched-initial-unknown-label", "sched", d, "invalid", "log-rejected",
        note="term and digest agree; the label has no entry in the configuration")
    # Events.
    mut("sched-events-not-whole-ticks", lambda d: d["events"].pop())
    mut("sched-events-reordered", lambda d: d["events"].insert(0, d["events"].pop(1)))
    mut("sched-event-wrong-tick", lambda d: d["events"][3].update(tick=7))
    mut("sched-event-wrong-entity", lambda d: d["events"][1].update(entity="zzz"))
    mut("sched-event-skip-instead-of-run", lambda d: (d["events"][0].update(decision="skip"), d["events"][0].pop("slice"), d["events"][0].pop("budget")))
    mut("sched-run-without-slice", lambda d: d["events"][0].pop("slice"))
    mut("sched-run-without-budget", lambda d: d["events"][0].pop("budget"))
    mut("sched-run-with-wrong-budget", lambda d: d["events"][0].update(budget=d["events"][0]["budget"] + 1))
    mut("sched-decision-unknown", lambda d: d["events"][0].update(decision="wait"))
    ev_skip = next(i for i, e in enumerate(base["events"]) if e["decision"] == "skip")
    mut("sched-skip-with-budget", lambda d: d["events"][ev_skip].update(budget=3))
    mut("sched-skip-becomes-run",
        lambda d: d["events"][ev_skip].update(decision="run", budget=3, slice=copy.deepcopy(d["events"][0]["slice"])))
    # Slices.
    mut("sched-slice-from-another-entity",
        lambda d: d["events"][0].update(slice=copy.deepcopy(d["events"][1]["slice"])))
    mut("sched-slice-strategy-differs",
        lambda d: (d["events"][0]["slice"].update(strategy="ro"), reseal_all_slices(d)))
    mut("sched-slice-configuration-differs",
        lambda d: (d["events"][0]["slice"]["config"].update(absorb="u"), reseal_all_slices(d)))
    mut("sched-slice-config-array-reordered",
        lambda d: (d["events"][0]["slice"]["config"].update(root=["S", "C", "K", "W"]), reseal_all_slices(d)),
        note="canonical texts differ, so the configurations differ")
    mut("sched-slice-step-tampered", lambda d: d["events"][0]["slice"]["steps"][0].update(after="5" * 64))
    mut("sched-slice-step-resealed-wrong-rule",
        lambda d: (d["events"][0]["slice"]["steps"][0].update(rule="S"), reseal_all_slices(d)))
    mut("sched-slice-fuel-differs",
        lambda d: (d["events"][0]["slice"].update(fuel=d["events"][0]["slice"]["fuel"] + 1), reseal_all_slices(d)))
    mut("sched-slice-format-wrong", lambda d: d["events"][0]["slice"].update(format="x"))
    mut("sched-slice-exhausted-flag-flipped",
        lambda d: d["events"][0]["slice"].update(exhausted=not d["events"][0]["slice"]["exhausted"]))
    # Rearms.
    ev_re = next(i for i, e in enumerate(base["events"]) if e.get("rearm") and e["rearm"].get("preempted"))
    mut("sched-rearm-preempted-flag-dropped", lambda d: d["events"][ev_re]["rearm"].pop("preempted"))
    mut("sched-rearm-digest-wrong", lambda d: d["events"][ev_re]["rearm"].update(digest="6" * 64))
    mut("sched-rearm-term-syntax-error", lambda d: d["events"][ev_re]["rearm"].update(term="K ("))
    mut("sched-rearm-slice-not-from-replacement",
        lambda d: d["events"][ev_re].update(slice=copy.deepcopy(next(
            e["slice"] for e in d["events"] if "slice" in e and e["entity"] != d["events"][ev_re]["entity"]))))
    # Final states.
    mut("sched-final-missing-entity", lambda d: d["final"].pop())
    mut("sched-final-extra-entity", lambda d: d["final"].append(copy.deepcopy(d["final"][0])))
    mut("sched-final-digest-wrong", lambda d: d["final"][0].update(digest="7" * 64))
    mut("sched-final-term-changed", lambda d: d["final"][0].update(term="a"))
    mut("sched-final-normal-flag-flipped",
        lambda d: d["final"][0].update(normal=True) if not d["final"][0].get("normal") else d["final"][0].pop("normal"))
    mut("sched-final-id-wrong", lambda d: d["final"][0].update(id="q"))
    mut("sched-final-swapped", lambda d: d["final"].reverse())
    mut("sched-no-entities-but-final", lambda d: d["final"].append({"id": "a", "term": "a", "digest": V.parse_term("a").hexdigest()}),
        note="the Go implementation returned early here without comparing final", doc=empty)
    mut("sched-no-entities-but-events", lambda d: d["events"].append({"tick": 0, "entity": "a", "decision": "skip"}), doc=empty)


# ---------------------------------------------------------------- effects

GENESIS = V.GENESIS


def mk_effects(specs, start_seq=0, prev=None):
    out, prev = [], GENESIS if prev is None else prev
    for i, s in enumerate(specs):
        e = {"seq": start_seq + i, "tick": s.get("tick", 0), "subject": s.get("subject", "s1")}
        if s.get("ref"):
            e["ref"] = s["ref"]
        e["index"] = s.get("index", 0)
        if s.get("request"):
            e["request"] = s["request"]
        e["disposition"] = s["disposition"]
        if s.get("reason"):
            e["reason"] = s["reason"]
        e["prev"] = prev
        e["hash"] = V.entry_hash(e)
        prev = e["hash"]
        out.append(e)
    return out


def jl(entries, sep="\n"):
    return "".join(json.dumps(e, separators=(",", ":")) + sep for e in entries)


def reseal_entries(entries):
    prev = GENESIS
    for i, e in enumerate(entries):
        e["seq"], e["prev"] = i, prev
        e["hash"] = V.entry_hash(e)
        prev = e["hash"]
    return entries


def gen_effects():
    SCH = "seeds/schema_arena.json"
    ref = "ab" * 32
    specs = [
        {"tick": 0, "ref": ref, "index": 0, "request": "effect (door 1) open", "disposition": "committed"},
        {"tick": 0, "ref": ref, "index": 1, "request": "effect (ship s1) thrust east", "disposition": "committed"},
        {"tick": 0, "ref": ref, "index": 2, "request": "effect (ship s2) thrust west", "disposition": "denied", "reason": "not the subject's own ship"},
        {"tick": 1, "ref": ref, "index": 0, "request": "effect (door 7) open", "disposition": "failed", "reason": "no such door"},
        {"tick": 1, "ref": ref, "index": 1, "request": "effect (door 01) open", "disposition": "malformed", "reason": "leading zero"},
        {"tick": 1, "ref": ref, "index": 2, "request": "effect (door 1", "disposition": "malformed", "reason": "does not parse"},
        {"tick": 2, "index": -1, "disposition": "no-output"},
        {"tick": 3, "ref": ref, "index": -1, "disposition": "exhausted", "reason": "fuel"},
        {"tick": 4, "ref": ref, "index": -1, "disposition": "truncated", "reason": "cap of 8"},
        {"tick": -5, "subject": "ünï\"code\\\n", "index": 3, "request": "effect (ship s1) set 4294967295", "disposition": "committed", "reason": "tab\there <&>  "},
    ]
    base = mk_effects(specs)
    add("effects-valid-all-dispositions", "effects", jl(base), "valid", options={"schema": SCH},
        note="includes quotes, backslash, newline, tab, <, &, U+2028 and non-ASCII in strings")
    add("effects-valid-no-schema", "effects", jl(base), "valid")
    add("effects-valid-seed-arena", "effects", seed("go_arena_effects.jsonl"), "valid", options={"schema": SCH})
    add("effects-valid-empty-log", "effects", "", "valid", options={"schema": SCH})
    add("effects-valid-empty-log-genesis-anchor", "effects", "", "valid", options={"anchor": GENESIS})
    add("effects-empty-log-wrong-anchor", "effects", "", "invalid", "anchor", {"anchor": "0" * 64})
    head = base[-1]["hash"]
    add("effects-valid-anchor-matches", "effects", jl(base), "valid", options={"anchor": head, "schema": SCH})
    add("effects-anchor-wrong", "effects", jl(base), "invalid", "anchor", {"anchor": "0" * 64})
    add("effects-anchor-uppercase", "effects", jl(base), "invalid", "anchor", {"anchor": head.upper()})
    add("effects-truncated-tail-without-anchor", "effects", jl(base[:-2]), "valid", options={"schema": SCH},
        note="a bare chain cannot detect a removed tail")
    add("effects-truncated-tail-with-anchor", "effects", jl(base[:-2]), "invalid", "anchor", {"anchor": head})
    add("effects-truncated-head-entry-removed", "effects", jl(base[1:]), "invalid", "chain")
    add("effects-middle-entry-removed", "effects", jl(base[:3] + base[4:]), "invalid", "chain")
    add("effects-entries-swapped", "effects", jl([base[1], base[0]] + base[2:]), "invalid", "chain")
    add("effects-entry-duplicated", "effects", jl(base[:2] + [base[1]] + base[2:]), "invalid", "chain")
    add("effects-valid-blank-lines", "effects", "\n" + jl(base, "\n\n") + "  \t \n", "valid")
    add("effects-valid-crlf", "effects", jl(base, "\r\n"), "valid")
    add("effects-valid-no-final-newline", "effects", jl(base).rstrip("\n"), "valid")
    add("effects-valid-reordered-members", "effects",
        "".join(json.dumps(dict(reversed(list(e.items()))), separators=(",", ":")) + "\n" for e in base), "valid",
        note="member order in the JSON text does not matter")
    add("effects-valid-spaced-json", "effects", "".join(json.dumps(e) + "\n" for e in base), "valid")
    raw = jl(base).split("\n")
    # Line-level truncation inside an entry.
    add("effects-last-line-cut", "effects", "\n".join(raw[:-2]) + "\n" + raw[-2][: len(raw[-2]) // 2], "invalid", "chain")
    add("effects-not-json", "effects", "garbage\n", "invalid", "chain")

    def field(cid, fn, cls="chain", opts=None, note="", entries=None, reseal=False):
        es = copy.deepcopy(entries or base)
        fn(es)
        if reseal:
            reseal_entries(es)
        add(cid, "effects", jl(es), "invalid", cls, opts, note)

    SO = {"schema": SCH}
    for k, newv in (("tick", 99), ("subject", "other"), ("ref", "cd" * 32), ("index", 5),
                    ("request", "effect (door 2) open"), ("disposition", "denied"), ("reason", "changed"),
                    ("seq", 4), ("prev", "0" * 64), ("hash", "0" * 64)):
        field("effects-tampered-%s" % k, lambda es, k=k, v=newv: es[0].__setitem__(k, v))
    field("effects-hash-uppercase", lambda es: es[2].__setitem__("hash", es[2]["hash"].upper()))
    field("effects-last-entry-tampered", lambda es: es[-1].__setitem__("reason", "x"))
    field("effects-optional-ref-removed", lambda es: es[0].pop("ref"))
    field("effects-optional-reason-added", lambda es: es[0].__setitem__("reason", "added"))
    for k in ("seq", "tick", "subject", "index", "disposition", "prev", "hash"):
        field("effects-missing-%s" % k, lambda es, k=k: es[0].pop(k))
        field("effects-null-%s" % k, lambda es, k=k: es[0].__setitem__(k, None))
    field("effects-unknown-member", lambda es: es[0].__setitem__("extra", 1))
    field("effects-capitalised-member", lambda es: es[0].__setitem__("Seq", es[0].pop("seq")))
    field("effects-ref-empty-string-present", lambda es: es[0].__setitem__("ref", ""), reseal=True,
          note="optional member present with its zero value")
    field("effects-request-empty-string-present", lambda es: es[6].__setitem__("request", ""), reseal=True)
    field("effects-seq-is-float", lambda es: es[0].__setitem__("seq", 0.0))
    field("effects-seq-is-negative", lambda es: es[0].__setitem__("seq", -1))
    field("effects-seq-2pow64", lambda es: es[0].__setitem__("seq", 2**64))
    field("effects-tick-is-string", lambda es: es[0].__setitem__("tick", "0"))
    field("effects-index-below-minus-one", lambda es: es[6].__setitem__("index", -2), reseal=True)
    field("effects-tick-beyond-int64", lambda es: es[0].__setitem__("tick", 2**63))
    field("effects-gap-in-seq", lambda es: es[3].__setitem__("seq", 9), reseal=False)
    field("effects-prev-of-first-not-genesis", lambda es: es[0].__setitem__("prev", "0" * 64))
    es = copy.deepcopy(base)
    es[0]["tick"] = 2**63 - 1
    es[1]["tick"] = -(2**63)
    reseal_entries(es)
    add("effects-valid-extreme-ticks", "effects", jl(es), "valid")

    # Schema-level rejections: chain is valid (resealed), the entry is not.
    def entry(cid, mutate, cls="log-entry", note=""):
        es = copy.deepcopy(base)
        mutate(es)
        reseal_entries(es)
        add(cid, "effects", jl(es), "invalid", cls, SO, note)

    entry("effects-unknown-disposition", lambda es: es[0].__setitem__("disposition", "approved"))
    entry("effects-unknown-disposition-whole-output",
          lambda es: es[6].__setitem__("disposition", "ignored"),
          note="index -1 and no request, so only the disposition list can reject it")
    entry("effects-committed-flipped-to-malformed", lambda es: es[0].__setitem__("disposition", "malformed"))
    entry("effects-malformed-flipped-to-committed", lambda es: es[4].__setitem__("disposition", "committed"))
    entry("effects-denied-for-undecodable-request", lambda es: es[5].__setitem__("disposition", "denied"))
    entry("effects-committed-with-syntax-error", lambda es: es[5].__setitem__("disposition", "committed"))
    entry("effects-committed-without-request", lambda es: es[0].pop("request"))
    entry("effects-committed-with-index-minus-one", lambda es: es[0].__setitem__("index", -1))
    entry("effects-no-output-with-index", lambda es: es[6].__setitem__("index", 0))
    entry("effects-no-output-with-request", lambda es: es[6].__setitem__("request", "effect (door 1) open"))
    entry("effects-exhausted-with-index", lambda es: es[7].__setitem__("index", 2))
    entry("effects-truncated-with-request", lambda es: es[8].__setitem__("request", "effect (door 1) open"))
    entry("effects-request-noncanonical-text",
          lambda es: es[0].__setitem__("request", "effect  (door 1)   open"),
          note="parses and decodes, but is not the canonical text of what it parses to")
    entry("effects-request-redundant-parentheses",
          lambda es: es[0].__setitem__("request", "(effect (door 1) open)"))
    entry("effects-malformed-but-decodes", lambda es: es[4].__setitem__("request", "effect (door 1) open"))

    # Request decoding edge cases. Each is a single-entry log, chain valid.
    def one(cid, request, disposition, expect, cls=None, schema=SCH, note=""):
        es = mk_effects([{"index": 0, "request": request, "disposition": disposition}])
        add(cid, "effects", jl(es), expect, cls, {"schema": schema}, note)

    one("effects-decode-int-zero", "effect (door 0) open", "committed", "valid")
    one("effects-decode-int-max", "effect (ship s1) set 4294967295", "committed", "valid")
    one("effects-decode-int-above-max", "effect (ship s1) set 4294967296", "malformed", "valid")
    one("effects-decode-int-above-max-committed", "effect (ship s1) set 4294967296", "committed", "invalid", "log-entry")
    one("effects-decode-int-leading-zero-malformed", "effect (door 007) open", "malformed", "valid")
    one("effects-decode-int-leading-zero-committed", "effect (door 007) open", "committed", "invalid", "log-entry")
    one("effects-decode-sym-uppercase-malformed", "effect (ship S1) thrust east", "malformed", "valid")
    one("effects-decode-sym-uppercase-committed", "effect (ship S1) thrust east", "committed", "invalid", "log-entry",
        note="an uppercase identifier is a syntax error, so the request does not even parse")
    one("effects-decode-sym-with-digits-and-quote", "effect (ship s1_a'2) thrust east", "committed", "valid")
    one("effects-decode-sym-starting-with-digit-committed", "effect (ship 1s) thrust east", "committed", "invalid", "log-entry")
    one("effects-decode-sym-digits-only", "effect (ship 12) thrust east", "malformed", "valid",
        note="12 is an atom but not a symbol")
    one("effects-decode-int-given-sym", "effect (door x) open", "malformed", "valid")
    one("effects-decode-too-many-args", "effect (door 1) open now", "malformed", "valid")
    one("effects-decode-too-few-args", "effect (ship s1) thrust", "malformed", "valid")
    one("effects-decode-no-verb", "effect (door 1)", "malformed", "valid")
    one("effects-decode-unknown-kind", "effect (window 1) open", "malformed", "valid")
    one("effects-decode-unknown-verb", "effect (door 1) smash", "malformed", "valid")
    one("effects-decode-verb-of-other-kind", "effect (door 1) thrust east", "malformed", "valid")
    one("effects-decode-target-not-applied", "effect door open", "malformed", "valid")
    one("effects-decode-target-too-deep", "effect (door 1 2) open", "malformed", "valid")
    one("effects-decode-target-id-is-compound", "effect (door (K 1 2)) open", "malformed", "valid")
    one("effects-decode-head-not-effect", "effects (door 1) open", "malformed", "valid")
    one("effects-decode-head-is-constant", "K (door 1) open", "malformed", "valid")
    one("effects-decode-argument-is-constant", "effect (ship s1) thrust K", "malformed", "valid")
    one("effects-decode-argument-is-bracket", "effect (ship s1) thrust <x>_p", "malformed", "valid")
    one("effects-decode-argument-is-scope", "effect (ship s1) thrust sc_p", "malformed", "valid")
    one("effects-request-redundant-parentheses-malformed", "effect (door 1) (open)", "malformed", "invalid", "log-entry",
        note="the canonical text of '(open)' is 'open', so even a malformed request must be recorded canonically if it parses")
    one("effects-decode-effect-alone", "effect", "malformed", "valid")
    one("effects-decode-empty-schema-everything-malformed", "effect (door 1) open", "malformed", "valid",
        schema="seeds/schema_empty.json")
    one("effects-decode-empty-schema-committed", "effect (door 1) open", "committed", "invalid", "log-entry",
        schema="seeds/schema_empty.json")
    g1, g2 = "effect (door 1) open", "effect (ship s1) set 3"
    one("effects-group-committed", "atomic (%s) (%s)" % (g1, g2), "committed", "valid")
    one("effects-group-denied", "atomic (%s) (%s)" % (g1, g2), "denied", "valid")
    one("effects-group-failed", "atomic (%s) (%s)" % (g1, g2), "failed", "valid")
    one("effects-group-of-one", "atomic (%s)" % g1, "committed", "valid")
    one("effects-group-empty-malformed", "atomic", "malformed", "valid")
    one("effects-group-empty-committed", "atomic", "committed", "invalid", "log-entry")
    one("effects-group-member-undecodable-malformed", "atomic (%s) (effect (door x) open)" % g1, "malformed", "valid")
    one("effects-group-member-undecodable-committed", "atomic (%s) (effect (door x) open)" % g1, "committed", "invalid", "log-entry")
    one("effects-group-decodable-recorded-malformed", "atomic (%s)" % g1, "malformed", "invalid", "log-entry")
    one("effects-group-nested-malformed", "atomic (atomic (%s))" % g1, "malformed", "valid")
    one("effects-group-nested-committed", "atomic (atomic (%s))" % g1, "committed", "invalid", "log-entry")
    one("effects-group-member-not-request-malformed", "atomic (%s) junk" % g1, "malformed", "valid")
    one("effects-group-as-effect-argument-malformed", "effect (door 1) (atomic)", "malformed", "invalid", "log-entry",
        note="not canonical text: the parentheses around a single atom are redundant")
    one("effects-group-noncanonical-text", "atomic  (%s)" % g1, "committed", "invalid", "log-entry")
    one("effects-group-with-schema-less-kind", "atomic (effect (window 1) open)", "committed", "invalid", "log-entry")
    one("effects-decode-custom-schema", "effect (lamp x) dim 5 y", "committed", "valid", schema="seeds/schema_custom.json")


def write_schemas():
    with open(os.path.join(SEEDS, "schema_empty.json"), "w") as f:
        f.write('{\n  "targets": {}\n}\n')
    with open(os.path.join(SEEDS, "schema_custom.json"), "w") as f:
        f.write(dumps({"targets": {"lamp": {"id": "sym", "verbs": {"dim": ["int", "sym"], "on": []}}}}))


def main():
    if os.path.isdir(CASES):
        shutil.rmtree(CASES)
    os.makedirs(CASES)
    write_schemas()
    gen_traces()
    gen_sched()
    gen_effects()
    with open(os.path.join(HERE, "manifest.json"), "w") as f:
        json.dump({"format": "spherepop-conformance/1", "cases": manifest}, f, indent=1)
        f.write("\n")
    by = {}
    for c in manifest:
        by.setdefault((c["kind"], c["expect"]), 0)
        by[(c["kind"], c["expect"])] += 1
    print(len(manifest), "cases", dict(sorted(by.items())))


if __name__ == "__main__":
    main()
