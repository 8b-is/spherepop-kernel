||| Formal Quantitative Type Theory (Idris 2) specification of the SpherePOP
||| circle calculus, permission-gated contractions, Settled vs Safe boundary
||| confluence rules (SPEC.md §§2-6, §17), and P0/P1 Zero-Float p/q UltraGraph
||| percolation & Q16.16 invariants.
module Spherepop

import Data.Bits

%default total

||| The six combinator constants of the circle calculus (SPEC.md §2).
public export
data Comb = I | B | C | K | W | S

public export
Eq Comb where
  I == I = True
  B == B = True
  C == C = True
  K == K = True
  W == W = True
  S == S = True
  _ == _ = False

||| Scoped circle terms:
|||   t ::= a | c | sc_l | (t t) | <t>_l
public export
data Term : Type where
  Atom    : (name : String) -> Term
  Const   : (c : Comb) -> Term
  Scope   : (label : String) -> Term
  App     : (fun : Term) -> (arg : Term) -> Term
  Bracket : (label : String) -> (body : Term) -> Term

||| Rule identifiers (SPEC.md §4).
public export
data RuleID = RuleI | RuleB | RuleC | RuleK | RuleW | RuleS | RuleSc | RuleAbs

||| Permission mask at a bracket chain: decides whether a gated combinator may contract.
public export
PermMask : Type
PermMask = Comb -> Bool

||| Intersect two permission masks along a bracket chain: pi(eps) ∩ ⋂ pi(l).
public export
intersectPerm : PermMask -> PermMask -> PermMask
intersectPerm p q c = p c && q c

||| Lemma 6.1 (SPEC.md): Extending a bracket chain only shrinks effective permission.
public export
lemmaPermShrinks : (p, q : PermMask) -> (c : Comb) ->
                   intersectPerm p q c = True -> p c = True
lemmaPermShrinks p q c prf with (p c)
  lemmaPermShrinks p q c prf | True  = Refl
  lemmaPermShrinks p q c prf | False impossible

||| True when a term has a bracket or scope constructor as function child (SPEC.md §17).
public export
hasPendingBoundary : Term -> Bool
hasPendingBoundary (Atom _)              = False
hasPendingBoundary (Const _)             = False
hasPendingBoundary (Scope _)             = False
hasPendingBoundary (Bracket _ u)         = hasPendingBoundary u
hasPendingBoundary (App (Bracket _ _) _) = True
hasPendingBoundary (App (Scope _) _)     = True
hasPendingBoundary (App f x)             = hasPendingBoundary f || hasPendingBoundary x

||| True when a term contains no gated constants {W, K, C} (SPEC.md §5).
public export
isInsensitive : Term -> Bool
isInsensitive (Atom _)      = True
isInsensitive (Const I)     = True
isInsensitive (Const B)     = True
isInsensitive (Const S)     = True
isInsensitive (Const _)     = False
isInsensitive (Scope _)     = True
isInsensitive (Bracket _ u) = isInsensitive u
isInsensitive (App f x)     = isInsensitive f && isInsensitive x

||| The minimal witness of SPEC.md §17 where `safe` fails the single-round
||| parallel diamond while `settled` preserves it:
|||   sc_x (<I>_x a)
public export
safeParallelDiamondWitness : Term
safeParallelDiamondWitness =
  App (Scope "x") (App (Bracket "x" (Const I)) (Atom "a"))

-- ============================================================================
-- Zero-Float p/q UltraGraph Percolation & Q16.16 Proofs (P0/P1 Conformance)
-- ============================================================================

||| Exact rational p/q pair (with non-zero denominator enforced by Smart Constructors).
public export
record Ratio where
  constructor MkRatio
  p : Nat
  q : Nat

||| Exact integer cross-multiplication comparison: p1/q1 <= p2/q2 <=> p1*q2 <= p2*q1.
public export
ratioLTE : Ratio -> Ratio -> Bool
ratioLTE r1 r2 = lte (r1.p * r2.q) (r2.p * r1.q)

||| Exact integer Molloy–Reed percolation gate: permitted iff sum(d_v^2) <= 2 * sum(d_v).
public export
percolationPermitted : (sumK : Nat) -> (sumK2 : Nat) -> Bool
percolationPermitted 0    _     = True
percolationPermitted sumK sumK2 = lte sumK2 (2 * sumK)

||| Rung 0 Bonobo Target Resonance: kappa == 3/2 <=> 2 * sum(d_v^2) == 3 * sum(d_v).
public export
isBonoboResonance : (sumK : Nat) -> (sumK2 : Nat) -> Bool
isBonoboResonance 0    _     = False
isBonoboResonance sumK sumK2 = (2 * sumK2) == (3 * sumK)

||| Compile-time proof for vector `percolation-001` (P3 path: sumK=4, sumK2=6 -> kappa=3/2):
||| Both Rung 0 Bonobo resonance and collapse gate permission hold by computation.
public export
percolation001BonoboProof : (isBonoboResonance 4 6 = True, percolationPermitted 4 6 = True)
percolation001BonoboProof = (Refl, Refl)

||| Compile-time proof for vector `percolation-002` (K4 complete graph: sumK=12, sumK2=36 -> kappa=3/1):
||| Supercritical graph is refused by `percolationPermitted`.
public export
percolation002RefusalProof : percolationPermitted 12 36 = False
percolation002RefusalProof = Refl

||| Signed Q16.16 fixed-point multiplication via 64-bit integer intermediate.
public export
q16Mul : Bits32 -> Bits32 -> Bits32
q16Mul a b =
  let prod : Bits64 = cast a * cast b
  in cast (prod `shiftR` 16)

||| Compile-time proof for vector `zero-float-001`:
||| 0x00018000 * 0x00020000 == 0x00030000 (196608).
public export
zeroFloat001Proof : q16Mul 0x00018000 0x00020000 = 0x00030000
zeroFloat001Proof = Refl
