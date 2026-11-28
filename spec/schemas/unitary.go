// Unitary Stepper for Spherepop kernel (UltraGraph §3.1).
// Implements Kohn–Sham / Strang-split unitary evolution on a 64x64 WaveMemory grid.
// Zero-Float p/q Law: Q16.16 + Z/(2^32 Z) fixed-point and reduced p/q rationals only.

package schemas

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"time"
)

var (
	ErrInvalidGrid        = errors.New("unitary: invalid grid dimensions")
	ErrInvalidHamiltonian = errors.New("unitary: invalid hamiltonian")
	ErrInvalidStep        = errors.New("unitary: invalid step parameters")
)

// Q16_16 is a 32-bit signed fixed-point scalar (16 integer bits, 16 fractional bits).
// Addition and subtraction wrap modulo 2^32, forming an exact abelian group.
type Q16_16 int32

const (
	Q16_SHIFT  = 16
	Q16_ONE    = Q16_16(1 << Q16_SHIFT) // 0x00010000 = 1
	Q16_HALF   = Q16_16(Q16_ONE >> 1)   // 0x00008000 = 1/2
	Q16_PI     = Q16_16(0x0003243F)     // pi in Q16.16
	Q16_TWO_PI = Q16_16(0x0006487E)     // 2*pi in Q16.16
	Q16_INV_2  = Q16_16(0x00008000)     // 1/2
	Q16_INV_6  = Q16_16(0x00002AAA)     // 1/6
	Q16_INV_24 = Q16_16(0x00000AAA)     // 1/24
	Q16_LN2    = Q16_16(0x0000B172)     // ln(2) in Q16.16
)

// WaveMemory grid dimensions.
const (
	GridSize  = 64
	GridArea  = GridSize * GridSize  // 4096 cells
	ComplexSz = 2                    // (Re, Im) per cell
	StateSize = GridArea * ComplexSz // 8192 Q16_16 scalars (32 KiB)
)

// Amplitude is a complex Q16.16 amplitude.
type Amplitude struct {
	Re Q16_16
	Im Q16_16
}

// Hamiltonian is a CSR sparse matrix in Q16.16.
type Hamiltonian struct {
	RowPtr [GridArea + 1]int
	ColIdx []int
	Values []Q16_16
	NNZ    int
}

// WaveFunction is the 64x64 complex wavefield lattice (32 KiB flat array).
type WaveFunction struct {
	Data [StateSize]Q16_16
	Time Q16_16
	Step uint32
}

// UnitaryStep records a content-addressed Strang half-step matching unitary.json.
type UnitaryStep struct {
	StepID            string    `json:"step_id"`
	HamiltonianCID    string    `json:"hamiltonian_cid"`
	DeltaTQ16         Q16_16    `json:"delta_t_q16"`
	HalfStep          int       `json:"half_step"` // 1 = kinetic half-step, 2 = Kohn-Sham potential step
	EntanglementRatio Ratio     `json:"entanglement_ratio"`
	EntanglementQ16   Q16_16    `json:"entanglement_q16"`
	ForwardDegree     int       `json:"forward_degree"`
	ReverseDegree     int       `json:"reverse_degree"`
	SalienceQ16       Q16_16    `json:"salience_q16"`
	CheckedAt         time.Time `json:"checked_at"`
}

// NewUnitaryStep constructs a schema-compliant UnitaryStep with exact p/q and Q16.16 metrics.
func NewUnitaryStep(hamiltonianCID string, dt Q16_16, halfStep, forwardDeg, reverseDeg int, now time.Time) (*UnitaryStep, error) {
	if (halfStep != 1 && halfStep != 2) || forwardDeg < 0 || reverseDeg < 0 {
		return nil, ErrInvalidStep
	}
	entRatio := ShadowEntanglementRatio(uint64(forwardDeg), uint64(reverseDeg))
	entQ16 := ShadowEntanglementCoupling(forwardDeg, reverseDeg)
	salienceQ16 := computeSalienceQ16(uint64(forwardDeg + reverseDeg))
	stepID := computeCID(
		"spherepop-unitary/1",
		[]byte(hamiltonianCID),
		[]byte(fmt.Sprintf("dt=%d;hs=%d;f=%d;r=%d", dt, halfStep, forwardDeg, reverseDeg)),
	)
	return &UnitaryStep{
		StepID:            stepID,
		HamiltonianCID:    hamiltonianCID,
		DeltaTQ16:         dt,
		HalfStep:          halfStep,
		EntanglementRatio: entRatio,
		EntanglementQ16:   entQ16,
		ForwardDegree:     forwardDeg,
		ReverseDegree:     reverseDeg,
		SalienceQ16:       salienceQ16,
		CheckedAt:         now.UTC(),
	}, nil
}

// StrangHalfStep1 applies the kinetic half-step U_1(dt) = exp(-i H_0 dt/2)
// with H_0 = -1/2 nabla^2 using a second-order symplectic Taylor update in Q16.16.
func StrangHalfStep1(psi *WaveFunction, dt Q16_16) {
	if psi == nil {
		return
	}
	dtQuarter := dt >> 2 // dt / 4 in Q16.16
	var lap1, lap2 [StateSize]Q16_16

	computeLaplacian(&psi.Data, &lap1)
	// Second-order term -(1/2)*(dt/4)^2 * nabla^4 psi stabilizes norm to O(dt^3).
	computeLaplacian(&lap1, &lap2)
	dtQ2Half := Q16Mul(dtQuarter, dtQuarter) >> 1

	for idx := 0; idx < StateSize; idx += 2 {
		lapRe := lap1[idx]
		lapIm := lap1[idx+1]

		// First order: +i * dtQuarter * (lapRe + i*lapIm) = -dtQuarter*lapIm + i*dtQuarter*lapRe
		dr1 := Q16Mul(-lapIm, dtQuarter)
		di1 := Q16Mul(lapRe, dtQuarter)

		// Second order: -(dtQuarter^2 / 2) * nabla^2(nabla^2 psi)
		dr2 := -Q16Mul(lap2[idx], dtQ2Half)
		di2 := -Q16Mul(lap2[idx+1], dtQ2Half)

		psi.Data[idx] = Q16Add(psi.Data[idx], Q16Add(dr1, dr2))
		psi.Data[idx+1] = Q16Add(psi.Data[idx+1], Q16Add(di1, di2))
	}
}

// StrangHalfStep2 applies the diagonal Kohn–Sham potential step U_2(dt) = exp(-i V_ks dt).
func StrangHalfStep2(psi *WaveFunction, vks *[GridArea]Q16_16, dt Q16_16) {
	if psi == nil || vks == nil {
		return
	}
	for cell := 0; cell < GridArea; cell++ {
		vdt := Q16Mul(vks[cell], dt)
		cosVal := Q16Cos(vdt)
		sinVal := Q16Sin(vdt)

		idx := cell * 2
		re := psi.Data[idx]
		im := psi.Data[idx+1]

		newRe := Q16Add(Q16Mul(cosVal, re), Q16Mul(sinVal, im))
		newIm := Q16Sub(Q16Mul(cosVal, im), Q16Mul(sinVal, re))

		psi.Data[idx] = newRe
		psi.Data[idx+1] = newIm
	}
}

// StrangFullStep applies second-order Strang splitting U(dt) = U_1(dt) U_2(dt) U_1(dt).
func StrangFullStep(psi *WaveFunction, vks *[GridArea]Q16_16, dt Q16_16) {
	StrangHalfStep1(psi, dt)
	StrangHalfStep2(psi, vks, dt)
	StrangHalfStep1(psi, dt)
	psi.Time = Q16Add(psi.Time, dt)
	psi.Step++
}

func computeLaplacian(psi *[StateSize]Q16_16, laplacian *[StateSize]Q16_16) {
	for y := 0; y < GridSize; y++ {
		uy := (y + 1) & (GridSize - 1)
		dy := (y - 1 + GridSize) & (GridSize - 1)
		rowOff := y * GridSize
		upOff := uy * GridSize
		dnOff := dy * GridSize
		for x := 0; x < GridSize; x++ {
			idx := (rowOff + x) * 2
			rx := (x + 1) & (GridSize - 1)
			lx := (x - 1 + GridSize) & (GridSize - 1)

			ridx := (rowOff + rx) * 2
			lidx := (rowOff + lx) * 2
			uidx := (upOff + x) * 2
			didx := (dnOff + x) * 2

			sumRe := Q16Add(Q16Add(Q16Add(psi[ridx], psi[lidx]), psi[uidx]), psi[didx])
			sumIm := Q16Add(Q16Add(Q16Add(psi[ridx+1], psi[lidx+1]), psi[uidx+1]), psi[didx+1])
			laplacian[idx] = Q16Sub(sumRe, psi[idx]<<2)
			laplacian[idx+1] = Q16Sub(sumIm, psi[idx+1]<<2)
		}
	}
}

// Q16Add computes exact wrap-around addition a + b in Z/(2^32 Z).
func Q16Add(a, b Q16_16) Q16_16 { return a + b }

// Q16Sub computes exact wrap-around subtraction a - b in Z/(2^32 Z).
func Q16Sub(a, b Q16_16) Q16_16 { return a - b }

// Q16Mul computes fixed-point product (a * b) >> 16 via 64-bit integer intermediate.
func Q16Mul(a, b Q16_16) Q16_16 {
	return Q16_16((int64(a) * int64(b)) >> Q16_SHIFT)
}

// Q16Cos evaluates an 8th-order fixed-point Taylor polynomial for cos(x).
func Q16Cos(x Q16_16) Q16_16 {
	x2 := Q16Mul(x, x)
	x4 := Q16Mul(x2, x2)
	x6 := Q16Mul(x4, x2)
	x8 := Q16Mul(x4, x4)

	term2 := Q16Mul(x2, Q16_INV_2)
	term4 := Q16Mul(x4, Q16_INV_24)
	term6 := Q16Mul(x6, Q16_16(0x0000005B)) // round(65536 / 720) = 91 = 0x5B
	term8 := Q16Mul(x8, Q16_16(0x00000002)) // round(65536 / 40320) = 2

	res := Q16Sub(Q16_ONE, term2)
	res = Q16Add(res, term4)
	res = Q16Sub(res, term6)
	return Q16Add(res, term8)
}

// Q16Sin evaluates a 7th-order fixed-point Taylor polynomial for sin(x).
func Q16Sin(x Q16_16) Q16_16 {
	x2 := Q16Mul(x, x)
	x3 := Q16Mul(x2, x)
	x5 := Q16Mul(x3, x2)
	x7 := Q16Mul(x5, x2)

	term3 := Q16Mul(x3, Q16_INV_6)
	term5 := Q16Mul(x5, Q16_16(0x00000222)) // round(65536 / 120) = 546 = 0x222
	term7 := Q16Mul(x7, Q16_16(0x0000000D)) // round(65536 / 5040) = 13 = 0x0D

	res := Q16Sub(x, term3)
	res = Q16Add(res, term5)
	return Q16Sub(res, term7)
}

// ShadowEntanglementRatio computes the exact reduced rational P_C = |dT - dR| / (dT + dR).
func ShadowEntanglementRatio(forwardDeg, reverseDeg uint64) Ratio {
	if forwardDeg == 0 && reverseDeg == 0 {
		return Ratio{P: 0, Q: 1}
	}
	var diff uint64
	if forwardDeg >= reverseDeg {
		diff = forwardDeg - reverseDeg
	} else {
		diff = reverseDeg - forwardDeg
	}
	return MustRatio(diff, forwardDeg+reverseDeg)
}

// ShadowEntanglementCoupling computes P_C = |dT - dR| / (dT + dR) in Q16.16 without int32 overflow.
func ShadowEntanglementCoupling(forwardDeg, reverseDeg int) Q16_16 {
	if forwardDeg <= 0 && reverseDeg <= 0 {
		return 0
	}
	diff := int64(forwardDeg) - int64(reverseDeg)
	if diff < 0 {
		diff = -diff
	}
	sum := int64(forwardDeg) + int64(reverseDeg)
	if sum <= 0 {
		return 0
	}
	return Q16_16((diff << Q16_SHIFT) / sum)
}

// computeSalienceQ16 computes an integer-exact log(1 + totalDeg) approximation in Q16.16 nats.
func computeSalienceQ16(totalDeg uint64) Q16_16 {
	v := totalDeg + 1
	log2Int := bits.Len64(v) - 1
	// Linear mantissa interpolation in Q16.16 multiplied by ln(2).
	base := uint64(1) << log2Int
	rem := ((v - base) << Q16_SHIFT) / base
	log2Q16 := Q16_16((int64(log2Int) << Q16_SHIFT) + int64(rem))
	return Q16Mul(log2Q16, Q16_LN2)
}

// InitializeWaveFunction constructs a radially decaying Q16.16 wave packet centered at (32, 32).
// Uses exact integer Padé Gaussian envelope 1 / (1 + ((dx^2 + dy^2) / 2*sigma^2)) with sigma = 5.
func InitializeWaveFunction() *WaveFunction {
	var psi WaveFunction
	const center = GridSize / 2
	const twoSigma2 = int64(2 * 5 * 5) // 2 * sigma^2 = 50 in grid-cell^2 units

	for y := 0; y < GridSize; y++ {
		dy := int64(y - center)
		for x := 0; x < GridSize; x++ {
			dx := int64(x - center)
			r2 := dx*dx + dy*dy
			// Q16_ONE * twoSigma2 / (twoSigma2 + r2) stays strictly positive and radially smooth.
			amp := Q16_16((int64(Q16_ONE) * twoSigma2) / (twoSigma2 + r2))
			idx := (y*GridSize + x) * 2
			psi.Data[idx] = amp
			psi.Data[idx+1] = 0
		}
	}
	return &psi
}

// BuildKohnShamPotential computes a diagonal Q16.16 potential V_ks = |psi|^2 / 2.
func BuildKohnShamPotential(psi *WaveFunction) *[GridArea]Q16_16 {
	var vks [GridArea]Q16_16
	if psi == nil {
		return &vks
	}
	for cell := 0; cell < GridArea; cell++ {
		idx := cell * 2
		re := psi.Data[idx]
		im := psi.Data[idx+1]
		rho := Q16Add(Q16Mul(re, re), Q16Mul(im, im))
		vks[cell] = rho >> 1
	}
	return &vks
}

// VerifyUnitarity returns ||psiAfter||^2 - ||psiBefore||^2 absolute drift scaled to Q16.16.
func VerifyUnitarity(psiBefore, psiAfter *WaveFunction) Q16_16 {
	if psiBefore == nil || psiAfter == nil {
		return 0
	}
	var normBefore, normAfter int64
	for i := 0; i < StateSize; i += 2 {
		reB := int64(psiBefore.Data[i])
		imB := int64(psiBefore.Data[i+1])
		normBefore += (reB*reB + imB*imB) >> Q16_SHIFT

		reA := int64(psiAfter.Data[i])
		imA := int64(psiAfter.Data[i+1])
		normAfter += (reA*reA + imA*imA) >> Q16_SHIFT
	}
	diff := normAfter - normBefore
	if diff < 0 {
		diff = -diff
	}
	if diff > int64(^uint32(0)>>1) {
		return Q16_16(^uint32(0) >> 1)
	}
	return Q16_16(diff)
}

// ExportState serializes the 32 KiB wavefunction in little-endian binary format.
func (wf *WaveFunction) ExportState() []byte {
	buf := make([]byte, StateSize*4)
	for i, v := range wf.Data {
		binary.LittleEndian.PutUint32(buf[4*i:4*i+4], uint32(v))
	}
	return buf
}
