#!/bin/sh
# Regenerates seeds/ from the Go implementation. The seeds are committed, so
# gen_corpus.py (pure Python) does not need Go to run.
set -e
cd "$(dirname "$0")/.."
BIN=$(mktemp -d)/spherepop
go build -o "$BIN" ./cmd/spherepop
S=conformance/seeds
"$BIN" reduce 'S K K x' --out $S/go_ski.json >/dev/null
"$BIN" reduce 'sc_p (K a b) d' --perm p=W,K,C,S --absorb s --sc settled --out $S/go_scoped.json >/dev/null
"$BIN" reduce '<K a b>_q (S I I x) (K y z)' --perm q=S --absorb u --sc safe --strategy ri --out $S/go_gated.json >/dev/null
"$BIN" reduce 'W W W' --fuel 6 --out $S/go_exhausted.json >/dev/null || true
"$BIN" arena-schema > $S/schema_arena.json
"$BIN" arena examples/cruise.lam --frames 12 --effects $S/go_arena_effects.jsonl --sched $S/go_arena_sched.json >/dev/null
"$BIN" sim --entity a='yield (K x y)' --entity b='I (yield (yield z))' --entity c='K p (yield q)' --budget 2 --ticks 5 --out $S/go_sim_yield.json >/dev/null
"$BIN" sim --entity a='K x y' --entity b='W W W' --entity c='I (I (I z))' --budget 2 --ticks 4 --out $S/go_sim_sched.json >/dev/null
