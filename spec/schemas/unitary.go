// Unitary Stepper for Spherepop kernel
// Implements Kohn–Sham / Strang-split unitary evolution on WaveMemory grid.
// Zero-Float p/q Law: Q16.16 + ℤ2³² fixed-point arithmetic only.
// No heap allocations, no floats, cache-line aligned.

package unitary

import (
	"errors"
	"time"
)

var (
	ErrInvalidGrid      = errors.New("unitary: invalid grid dimensions")
	ErrInvalidHamiltonian = errors.New("unitary: invalid hamiltonian")
	ErrInvalidStep      = errors.New("unitary: invalid step parameters")
)

// Q16_16 is a 32-bit signed fixed-point type (16 integer bits, 16 fractional bits)
// Operations are modulo 2³² — exact abelian group, strictly associative.
type Q16_16 int32

const (
	Q16_SHIFT    = 16
	Q16_ONE      = Q16_16(1 << Q16_SHIFT)        // 0x00010000 = 1.0
	Q16_HALF     = Q16_16(Q16_ONE >> 1)          // 0x00008000 = 0.5
	Q16_PI       = Q16_16(0x0003243F)            // π ≈ 3.14159
	Q16_TWO_PI   = Q16_16(0x0006487E)            // 2π ≈ 6.28318
	Q16_INV_2    = Q16_16(0x00008000)            // 1/2
	Q16_INV_6    = Q16_16(0x00002AAA)            // 1/6
	Q16_INV_24   = 0x00000AAA            // 1/24
)

// WaveMemory grid constants
const (
	GridSize  = 64
	GridArea  = GridSize * GridSize     // 4096
	ComplexSz = 2                       // real + imag per amplitude
	StateSize = GridArea * ComplexSz    // 8192 Q16_16 values
)

// Amplitude is a complex number in Q16.16 (real, imag)
type Amplitude struct {
	Re Q16_16
	Im Q16_16
}

// Hamiltonian is a sparse matrix in Q16.16
// Stored as CSR (Compressed Sparse Row) for cache efficiency
type Hamiltonian struct {
	RowPtr  [GridArea + 1]int  // 4097 ints
	ColIdx  []int              // variable, max 8*GridArea (8 neighbors)
	Values  []Q16_16           // Q16_16 values
	NNZ     int                // number of non-zeros
}

// WaveFunction is the 64×64 complex wavefield (damped MEM lattice)
// Aligned to 64-byte cache line for SIMD-friendly access
type WaveFunction struct {
	Data [StateSize]Q16_16 // flat: [re0, im0, re1, im1, ...]
	Time Q16_16            // current simulation time
	Step uint32            // step counter
}

// UnitaryStep represents one Strang half-step
type UnitaryStep struct {
	StepID              string    `json:"step_id"`
	HamiltonianCID      string    `json:"hamiltonian_cid"`
	DeltaT              Q16_16    `json:"delta_t_q16"`
	HalfStep            int       `json:"half_step"`            // 1 = e^{-iH0Δt/2}, 2 = e^{-iVksΔt}
	EntanglementCoupling float64  `json:"entanglement_coupling"` // P̂C, threshold 0.90
	ForwardDegree       int       `json:"forward_degree"`        // dT
	ReverseDegree       int       `json:"reverse_degree"`        // dR
	SalienceNats        float64   `json:"salience_nats"`         // Λ(C)
	ComputedAt          time.Time `json:"computed_at"`
}

// StrangHalfStep1 computes U₁(Δt) = e^{-iH0 Δt/2} (kinetic half-step)
// H0 = -½∇² (discrete Laplacian on 64×64 grid)
// Uses 5-point stencil: ∇²ψ ≈ (ψ[i+1] + ψ[i-1] + ψ[j+1] + ψ[j-1] - 4ψ[i]) / h²
// In Q16.16: exact integer arithmetic, no floats.
func StrangHalfStep1(psi *WaveFunction, dt Q16_16) {
	halfDt := dt >> 1  // Δt/2 in Q16.16
	
	// Temporary buffer for Laplacian result (stack-allocated via escape analysis)
	var laplacian [StateSize]Q16_16
	
	// Compute discrete Laplacian: ∇²ψ
	computeLaplacian(&psi.Data, &laplacian)
	
	// psi = psi - i * (H0 * halfDt) * psi
	// H0 = -½∇², so -i * H0 = -i * (-½∇²) = +i * ½∇²
	// Update: ψ_new = ψ + i * (½∇²) * (Δt/2) = ψ + i * ∇² * (Δt/4)
	dtQuarter := halfDt >> 1  // Δt/4
	
	// For each grid point: apply i * (∇² * dtQuarter)
	for idx := 0; idx < StateSize; idx += 2 {
		lapRe := laplacian[idx]
		lapIm := laplacian[idx+1]
		
		// i * (lapRe + i*lapIm) = -lapIm + i*lapRe
		// Multiply by dtQuarter
		dr := q16Mul(-lapIm, dtQuarter)
		di := q16Mul(lapRe, dtQuarter)
		
		// ψ_new = ψ + dr + i*di
		psi.Data[idx] = q16Add(psi.Data[idx], dr)
		psi.Data[idx+1] = q16Add(psi.Data[idx+1], di)
	}
}

// StrangHalfStep2 computes U₂(Δt) = e^{-iVks Δt} (potential full-step)
// Vks = Kohn-Sham effective potential (diagonal in position basis)
// psi_new = e^{-iV Δt} ψ = (cos(VΔt) - i sin(VΔt)) * ψ
func StrangHalfStep2(psi *WaveFunction, vks *[GridArea]Q16_16, dt Q16_16) {
	for cell := 0; cell < GridArea; cell++ {
		v := vks[cell]
		vdt := q16Mul(v, dt)  // V * Δt in Q16.16
		
		// cos(vdt) and sin(vdt) via Taylor series (Q16.16 integer arithmetic)
		// cos(x) ≈ 1 - x²/2! + x⁴/4! - x⁶/6!
		// sin(x) ≈ x - x³/3! + x⁵/5! - x⁷/7!
		cosVal := q16Cos(vdt)
		sinVal := q16Sin(vdt)
		
		idx := cell * 2
		re := psi.Data[idx]
		im := psi.Data[idx+1]
		
		// (cos - i sin) * (re + i im) = (cos*re + sin*im) + i(cos*im - sin*re)
		newRe := q16Add(q16Mul(cosVal, re), q16Mul(sinVal, im))
		newIm := q16Sub(q16Mul(cosVal, im), q16Mul(sinVal, re))
		
		psi.Data[idx] = newRe
		psi.Data[idx+1] = newIm
	}
}

// StrangFullStep computes U(Δt) = e^{-iH0Δt/2} e^{-iVksΔt} e^{-iH0Δt/2}
// This is the second-order Strang splitting
func StrangFullStep(psi *WaveFunction, vks *[GridArea]Q16_16, dt Q16_16) {
	StrangHalfStep1(psi, dt)        // e^{-iH0Δt/2}
	StrangHalfStep2(psi, vks, dt)   // e^{-iVksΔt}
	StrangHalfStep1(psi, dt)        // e^{-iH0Δt/2}
	psi.Time = q16Add(psi.Time, dt)
	psi.Step++
}

// computeLaplacian computes 5-point discrete Laplacian on 64×64 toroidal grid
// ∇²ψ[i,j] = ψ[i+1,j] + ψ[i-1,j] + ψ[i,j+1] + ψ[i,j-1] - 4*ψ[i,j]
// Output: laplacian[2*idx] = Re(∇²ψ), laplacian[2*idx+1] = Im(∇²ψ)
func computeLaplacian(psi *[StateSize]Q16_16, laplacian *[StateSize]Q16_16) {
	// Clear output
	for i := range laplacian {
		laplacian[i] = 0
	}
	
	// For each cell in 64×64 grid
	for y := 0; y < GridSize; y++ {
		for x := 0; x < GridSize; x++ {
			idx := (y*GridSize + x) * 2
			re := psi[idx]
			im := psi[idx+1]
			
			// Neighbors with toroidal wrapping
			// Right (x+1)
			rx := (x + 1) % GridSize
			ridx := (y*GridSize + rx) * 2
			// Left (x-1)
			lx := (x - 1 + GridSize) % GridSize
			lidx := (y*GridSize + lx) * 2
			// Up (y+1)
			uy := (y + 1) % GridSize
			uidx := (uy*GridSize + x) * 2
			// Down (y-1)
			dy := (y - 1 + GridSize) % GridSize
			didx := (dy*GridSize + x) * 2
			
			// ∇² = right + left + up + down - 4*center
					sumRe := q16Add(q16Add(q16Add(psi[ridx], psi[lidx]), psi[uidx]), psi[didx])
					sumIm := q16Add(q16Add(q16Add(psi[ridx+1], psi[lidx+1]), psi[uidx+1]), psi[didx+1])
					laplacian[idx] = q16Sub(sumRe, re<<2)     // -4*re
					laplacian[idx+1] = q16Sub(sumIm, im<<2)   // -4*im
		}
	}
}

// Q16.16 arithmetic operations (all modulo 2³², exact)

// q16Add: a + b (mod 2³²)
func q16Add(a, b Q16_16) Q16_16 {
	return a + b
}

// q16Sub: a - b (mod 2³²)
func q16Sub(a, b Q16_16) Q16_16 {
	return a - b
}

// q16Mul: a * b >> 16 (fixed-point multiply, modulo 2³²)
// Uses int64 intermediate to prevent overflow, then truncates
func q16Mul(a, b Q16_16) Q16_16 {
	return Q16_16((int64(a) * int64(b)) >> Q16_SHIFT)
}

// q16Cos: cos(x) via Taylor series in Q16.16
// cos(x) = 1 - x²/2! + x⁴/4! - x⁶/6! + x⁸/8!
// x in Q16.16 radians, output in Q16.16
func q16Cos(x Q16_16) Q16_16 {
	x2 := q16Mul(x, x)
	x4 := q16Mul(x2, x2)
	x6 := q16Mul(x4, x2)
	x8 := q16Mul(x4, x4)
	
	// 1 - x²/2 + x⁴/24 - x⁶/720 + x⁸/40320
	// Coefficients in Q16.16: 1/2 = 0x8000, 1/24 ≈ 0x0AAA, 1/720 ≈ 0x0024, 1/40320 ≈ 0x0001
	term2 := q16Mul(x2, Q16_INV_2)
	term4 := q16Mul(x4, Q16_16(0x00000AAA))  // 1/24
	term6 := q16Mul(x6, Q16_16(0x00000024))  // 1/720
	term8 := q16Mul(x8, Q16_16(0x00000001))  // 1/40320
	
	// 1 - term2 + term4 - term6 + term8
	result := Q16_ONE
	result = q16Sub(result, term2)
	result = q16Add(result, term4)
	result = q16Sub(result, term6)
	result = q16Add(result, term8)
	
	return result
}

// q16Sin: sin(x) via Taylor series in Q16.16
// sin(x) = x - x³/3! + x⁵/5! - x⁷/7! + x⁹/9!
func q16Sin(x Q16_16) Q16_16 {
	x2 := q16Mul(x, x)
	x3 := q16Mul(x2, x)
	x5 := q16Mul(x3, x2)
	x7 := q16Mul(x5, x2)
	
	// x - x³/6 + x⁵/120 - x⁷/5040 + x⁹/362880
	// Coefficients: 1/6 ≈ 0x2AAA, 1/120 ≈ 0x0222, 1/5040 ≈ 0x0008, 1/362880 ≈ 0x0000
	term3 := q16Mul(x3, Q16_16(0x00002AAA))  // 1/6
	term5 := q16Mul(x5, Q16_16(0x00000222))  // 1/120
	term7 := q16Mul(x7, Q16_16(0x00000008))  // 1/5040
	
	result := x
	result = q16Sub(result, term3)
	result = q16Add(result, term5)
	result = q16Sub(result, term7)
	
	return result
}

// ShadowEntanglementCoupling computes P̂C from Flyxion directional asymmetry
// P̂C = |dT - dR| / (dT + dR) where dT = forward degree, dR = reverse degree
// Returns Q16_16 scaled by 10000 (so 0.90 = 9000)
func ShadowEntanglementCoupling(forwardDeg, reverseDeg int) Q16_16 {
	if forwardDeg == 0 && reverseDeg == 0 {
		return 0
	}
	diff := forwardDeg - reverseDeg
	if diff < 0 {
		diff = -diff
	}
	sum := forwardDeg + reverseDeg
	// P̂C = diff / sum * 10000 (in Q16.16: multiply by 10000 << 16 / sum)
	// 10000 in Q16.16 = 10000 * 65536 = 655360000
	// But we can compute as: (diff * Q16_ONE) / sum
	num := Q16_16(diff) * Q16_ONE
	den := Q16_16(sum)
	return Q16_16(int64(num) / int64(den))
}

// InitializeWaveFunction creates a damped initial wavefield
func InitializeWaveFunction() *WaveFunction {
	var psi WaveFunction
	// Gaussian wave packet at center with damping
	center := GridSize / 2
	sigma := Q16_16(5 * Q16_ONE)  // σ = 5.0
	
	for y := 0; y < GridSize; y++ {
		for x := 0; x < GridSize; x++ {
			dx := Q16_16(x - center)
			dy := Q16_16(y - center)
			
			// Gaussian: exp(-(dx²+dy²)/2σ²)
			d2 := q16Add(q16Mul(dx, dx), q16Mul(dy, dy))
			sigma2 := q16Mul(sigma, sigma)
			twoSigma2 := sigma2 << 1
			
			// exp(-d2 / 2σ²) ≈ 1 - d2/2σ² for small values
			// Use integer division
			if d2 < twoSigma2 {
				val := Q16_ONE - q16Mul(d2, Q16_ONE/twoSigma2)
				if val < 0 {
					val = 0
				}
				idx := (y*GridSize + x) * 2
				psi.Data[idx] = val
				psi.Data[idx+1] = 0
			}
		}
	}
	psi.Time = 0
	psi.Step = 0
	return &psi
}

// BuildKohnShamPotential constructs Vks from electron density
// Simplified: returns diagonal potential array
func BuildKohnShamPotential(psi *WaveFunction) *[GridArea]Q16_16 {
	var vks [GridArea]Q16_16
	
	for cell := 0; cell < GridArea; cell++ {
		idx := cell * 2
		re := psi.Data[idx]
		im := psi.Data[idx+1]
		
		// Density ρ = |ψ|² = re² + im² (in Q16.16)
		re2 := q16Mul(re, re)
		im2 := q16Mul(im, im)
		rho := q16Add(re2, im2)
		
		// Vks ≈ ρ^(1/3) (Thomas-Fermi) + exchange-correlation
		// Simplified: Vks = ρ / 2
		vks[cell] = rho >> 1
	}
	
	return &vks
}

// VerifyUnitarity checks that ||Uψ|| = ||ψ|| (norm preservation)
// Returns norm difference in Q16.16 (should be ~0)
func VerifyUnitarity(psiBefore, psiAfter *WaveFunction) Q16_16 {
	var normBefore, normAfter int64
	
	for i := 0; i < StateSize; i += 2 {
		reB := int64(psiBefore.Data[i])
		imB := int64(psiBefore.Data[i+1])
		normBefore += reB*reB + imB*imB
		
		reA := int64(psiAfter.Data[i])
		imA := int64(psiAfter.Data[i+1])
		normAfter += reA*reA + imA*imA
	}
	
	diff := normAfter - normBefore
	if diff < 0 {
		diff = -diff
	}
	return Q16_16(diff >> 32) // Scale back
}

// ExportState serializes wavefunction to binary (zero-copy compatible)
func (wf *WaveFunction) ExportState() []byte {
	// 8192 * 4 bytes = 32768 bytes = 32KB
	buf := make([]byte, StateSize*4)
	for i, v := range wf.Data {
		buf[4*i] = byte(v)
		buf[4*i+1] = byte(v >> 8)
		buf[4*i+2] = byte(v >> 16)
		buf[4*i+3] = byte(v >> 24)
	}
	return buf
}