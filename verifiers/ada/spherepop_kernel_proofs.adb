pragma SPARK_Mode (On);
pragma Restrictions (No_Allocators); -- No_Heap_Allocations
pragma Restrictions (No_Implicit_Heap_Allocations);
pragma Restrictions (No_Exception_Propagation);

package body Spherepop_Kernel_Proofs
  with SPARK_Mode => On
is

   function Extend_Chain
     (Current, Label_Perm : Perm_Mask) return Perm_Mask
   is
      Res : constant Unsigned_8 := Current and Label_Perm;
   begin
      pragma Assert ((Res and Current) = Res);
      pragma Assert ((Res and Label_Perm) = Res);
      pragma Assert (Res <= Current);
      return Perm_Mask (Res);
   end Extend_Chain;

   function Check_Percolation_Gate
     (Sum_K  : Degree_Sum;
      Sum_K2 : Degree_Sq_Sum) return Boolean
   is
   begin
      if Sum_K = 0 then
         return True;
      end if;
      return Sum_K2 <= 2 * Unsigned_64 (Sum_K);
   end Check_Percolation_Gate;

   function Classify_Percolation_Trit
     (Sum_K  : Degree_Sum;
      Sum_K2 : Degree_Sq_Sum) return Resonance_Trit
   is
   begin
      if Sum_K = 0 then
         return 0;
      elsif 2 * Sum_K2 = 3 * Unsigned_64 (Sum_K) then
         return 1;
      elsif Sum_K2 > 2 * Unsigned_64 (Sum_K) then
         return -1;
      else
         return 0;
      end if;
   end Classify_Percolation_Trit;

   function Check_Collapse_Budget
     (Allocated, Consumed, Cost, Upkeep : Budget_Units;
      Has_Valid_Waiver                  : Boolean) return Boolean
   is
      Projected : constant Integer_64 := Consumed + Cost + Upkeep;
   begin
      if Projected <= Allocated then
         return True;
      end if;
      return Has_Valid_Waiver;
   end Check_Collapse_Budget;

   function Q16_Mul
     (A, B : Safe_Q16_Factor) return Q16_Scalar
   is
      Prod   : constant Integer_64 := Integer_64 (A) * Integer_64 (B);
      Scaled : constant Integer_64 :=
        (if Prod >= 0 then Prod / 65_536 else (Prod - 65_535) / 65_536);
   begin
      return Q16_Scalar (Scaled);
   end Q16_Mul;

end Spherepop_Kernel_Proofs;
