# Conformance corpus

One specification (`SPEC.md` section 18), two interpreters of its evidence (the Go packages and `tools/verify-py/spherepop_verify.py`), and this corpus of records that both must judge the same way.

    manifest.json        every case: kind, file, required verdict, required failure class, options
    cases/               the records
    seeds/               records written by the Go implementation, and the schemas
    gen_corpus.py        regenerates cases/ and manifest.json (Python only)
    make_seeds.sh        regenerates seeds/ from the Go implementation
    crosscheck.py        random differential test, Go against Python, both directions
    conformance_test.go  runs the Go verifiers, then the Python verifier, over the corpus

Run `go test ./conformance`. To regenerate, run `python3 gen_corpus.py` (and `./make_seeds.sh` if the Go output format changes).

How the invalid cases are built matters more than how many there are. Each is a single mutation. A record that is tampered with and not resealed tests the chain. The same tampering resealed, so that every hash verifies, tests the semantic check behind the chain. Several scheduler and effects cases are built so that only one check can reject them, because a check that is also covered by a later one cannot be told from a missing check.

What the corpus found while it was being built: a scheduler log with no entities skipped the final-state check; a label with an empty permission was written as `null` and then rejected by the reader that had just been made strict; the effects hash depended on how Go escapes strings; JSON decoding accepted case-variant names, missing members, nulls and duplicates; and SPEC section 5 contradicted the code on the scope-constructor clause of settledness and on the default scope mode.

What it cannot show: both verifiers and the specification have one author, and the specification was drafted from the behaviour of the Go code. An error shared by all three would pass. A second human implementer reading only section 18 is the missing test.
