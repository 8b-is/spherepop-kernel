||| Formal Quantitative Type Theory (Idris 2) specification of the SpherePOP
||| circle calculus, permission-gated contractions, and the Settled vs Safe
||| boundary confluence rules (SPEC.md §§2-5, §17).
module Spherepop

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

||| True when a term has a bracket or scope constructor as function child (SPEC.md §17).
public export
hasPendingBoundary : Term -> Bool
hasPendingBoundary (Atom _)        = False
hasPendingBoundary (Const _)       = False
hasPendingBoundary (Scope _)       = False
hasPendingBoundary (Bracket _ u)   = hasPendingBoundary u
hasPendingBoundary (App (Bracket _ _) _) = True
hasPendingBoundary (App (Scope _) _)     = True
hasPendingBoundary (App f x)       = hasPendingBoundary f || hasPendingBoundary x

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
