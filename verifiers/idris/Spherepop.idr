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

||| Balanced ternary trit (-1, 0, +1) for BFP-64 and SPQT quantization.
public export
data Trit : Type where
  TNeg  : Trit
  TZero : Trit
  TPos  : Trit

||| Kleene 3-valued negation.
public export
kleeneNot : Trit -> Trit
kleeneNot TNeg  = TPos
kleeneNot TZero = TZero
kleeneNot TPos  = TNeg

||| Kleene 3-valued AND (+AND=1).
public export
kleeneAnd : Trit -> Trit -> Trit
kleeneAnd TNeg  _     = TNeg
kleeneAnd _     TNeg  = TNeg
kleeneAnd TPos  TPos  = TPos
kleeneAnd _     _     = TZero

||| Kleene 3-valued NAND (~NAND=-1).
public export
kleeneNand : Trit -> Trit -> Trit
kleeneNand a b = kleeneNot (kleeneAnd a b)

||| Proof that Kleene negation is involutive.
public export
kleeneNotInvolutive : (t : Trit) -> kleeneNot (kleeneNot t) = t
kleeneNotInvolutive TNeg  = Refl
kleeneNotInvolutive TZero = Refl
kleeneNotInvolutive TPos  = Refl

||| Proof that NAND synthesizes AND across all 9 balanced ternary pairs.
public export
nandSynthesizesAnd : (a, b : Trit) -> kleeneNand (kleeneNand a b) (kleeneNand a b) = kleeneAnd a b
nandSynthesizesAnd TNeg  TNeg  = Refl
nandSynthesizesAnd TNeg  TZero = Refl
nandSynthesizesAnd TNeg  TPos  = Refl
nandSynthesizesAnd TZero TNeg  = Refl
nandSynthesizesAnd TZero TZero = Refl
nandSynthesizesAnd TZero TPos  = Refl
nandSynthesizesAnd TPos  TNeg  = Refl
nandSynthesizesAnd TPos  TZero = Refl
nandSynthesizesAnd TPos  TPos  = Refl

||| Dependent transition type for the 0x62 gate (-1 -> 0 -> +1).
public export
data Gate0x62Transition : Trit -> Trit -> Type where
  ClampJitterToZero : Gate0x62Transition TNeg TZero
  SealZeroToPos     : Gate0x62Transition TZero TPos
  ComposeGate0x62   : Gate0x62Transition a b -> Gate0x62Transition b c -> Gate0x62Transition a c

||| Witness of the composite transition from TNeg (-1) to TPos (+1) across the 0x62 gate.
public export
gate0x62SealWitness : Gate0x62Transition TNeg TPos
gate0x62SealWitness = ComposeGate0x62 ClampJitterToZero SealZeroToPos
