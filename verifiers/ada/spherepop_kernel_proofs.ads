pragma SPARK_Mode (On);
pragma Restrictions (No_Allocators); -- No_Heap_Allocations
pragma Restrictions (No_Implicit_Heap_Allocations);
pragma Restrictions (No_Exception_Propagation);

with Interfaces; use Interfaces;

package Spherepop_Kernel_Proofs
  with Pure, SPARK_Mode => On
is
   pragma Elaborate_Body;

   -- 4-bit permission bitmask matching reduce.PermSet (W=1, K=2, C=4, S=8).
   subtype Perm_Mask is Unsigned_8 range 0 .. 15;
   Perm_W : constant Perm_Mask := 2#0001#;
   Perm_K : constant Perm_Mask := 2#0010#;
   Perm_C : constant Perm_Mask := 2#0100#;
   Perm_S : constant Perm_Mask := 2#1000#;

   -- Balanced ternary resonance trit {-1, 0, +1}.
   subtype Resonance_Trit is Integer_8 range -1 .. 1;

   -- Bounded degree sums for O(|V| + |E|) Molloy–Reed percolation checks.
   subtype Degree_Sum is Unsigned_64 range 0 .. 2**32 - 1;
   subtype Degree_Sq_Sum is Unsigned_64 range 0 .. 2**61 - 1;

   -- Bounded maintenance budget units on machine Integer_64.
   subtype Budget_Units is Integer_64 range 0 .. 1_000_000_000;

   -- Q16.16 fixed-point scalar on 32-bit signed integer.
   subtype Q16_Scalar is Integer_32;
   Q16_One : constant Q16_Scalar := 65_536;

   type Reduce_Summary is record
      Steps        : Unsigned_64;
      Exhausted    : Boolean;
      Step_Remains : Boolean;
   end record;

   -- 1. Permission Monotonicity (SPEC.md Lemma 6.1):
   -- Intersecting a bracket chain permission mask never grants new capabilities.
   function Extend_Chain
     (Current, Label_Perm : Perm_Mask) return Perm_Mask
   with Inline,
        Post => (Extend_Chain'Result and Current) = Extend_Chain'Result
                and then (Extend_Chain'Result and Label_Perm) = Extend_Chain'Result
                and then Extend_Chain'Result <= Current;

   -- 2. Fuel Accounting Exactness (SPEC.md §6 & trace/check.go):
   function Valid_Fuel_Accounting
     (Fuel : Unsigned_64; S : Reduce_Summary) return Boolean
   is
     (S.Steps <= Fuel
      and then (if S.Exhausted then S.Steps = Fuel and S.Step_Remains
                else not S.Step_Remains))
   with Inline;

   -- 3. Zero-Float p/q Molloy–Reed Percolation Gate (UltraGraph §1.2):
   -- Subcritical / critical collapse permitted iff sum(d_v^2) <= 2 * sum(d_v).
   function Check_Percolation_Gate
     (Sum_K  : Degree_Sum;
      Sum_K2 : Degree_Sq_Sum) return Boolean
   with Inline,
        Post => Check_Percolation_Gate'Result =
                  (Sum_K = 0 or else Sum_K2 <= 2 * Unsigned_64 (Sum_K));

   -- 4. Solar Phylogenetic Octad Resonance Trit:
   -- +1 at Rung 0 Bonobo Target Resonance (p/q = 3/2 <=> 2 * Sum_K2 = 3 * Sum_K),
   --  0 when subcritical/critical (Sum_K2 <= 2 * Sum_K),
   -- -1 when supercritical (Sum_K2 > 2 * Sum_K).
   function Classify_Percolation_Trit
     (Sum_K  : Degree_Sum;
      Sum_K2 : Degree_Sq_Sum) return Resonance_Trit
   with Inline,
        Post => (if Sum_K > 0 and then 2 * Sum_K2 = 3 * Unsigned_64 (Sum_K)
                 then Classify_Percolation_Trit'Result = 1
                 elsif Sum_K > 0 and then Sum_K2 > 2 * Unsigned_64 (Sum_K)
                 then Classify_Percolation_Trit'Result = -1
                 else Classify_Percolation_Trit'Result = 0);

   -- 5. Maintenance Budget Enforcement (Protocol §12):
   function Check_Collapse_Budget
     (Allocated, Consumed, Cost, Upkeep : Budget_Units;
      Has_Valid_Waiver                  : Boolean) return Boolean
   with Inline,
        Post => (if Check_Collapse_Budget'Result and not Has_Valid_Waiver
                 then Consumed + Cost + Upkeep <= Allocated);

   -- 6. Zero-Float Q16.16 Fixed-Point Multiplication:
   -- Bounded domain [-32768 .. 32767] in integer part -> safe 64-bit intermediate.
   subtype Safe_Q16_Factor is Integer_32 range -2**22 .. 2**22;

   function Q16_Mul
     (A, B : Safe_Q16_Factor) return Q16_Scalar
   with Inline,
        Post => Integer_64 (Q16_Mul'Result) =
                  (if Integer_64 (A) * Integer_64 (B) >= 0
                   then (Integer_64 (A) * Integer_64 (B)) / 65_536
                   else ((Integer_64 (A) * Integer_64 (B)) - 65_535) / 65_536);

end Spherepop_Kernel_Proofs;
