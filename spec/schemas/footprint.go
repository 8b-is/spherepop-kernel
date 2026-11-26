// Footprint subsumption for evidence freshness
// evidence.footprint ⊑ current_footprint (subsumption, not equality)
// Coarse footprints reject valid evidence; fine footprints admit stale evidence.

package footprint

import (
	"errors"
	"runtime"
	"strings"
)

var (
	ErrIncompatibleArch      = errors.New("footprint: incompatible architecture")
	ErrIncompatibleOS        = errors.New("footprint: incompatible OS")
	ErrIncompatibleLibc      = errors.New("footprint: incompatible libc")
	ErrRuntimeVersionMismatch = errors.New("footprint: runtime version mismatch")
	ErrMissingAccelerator    = errors.New("footprint: required accelerator not available")
	ErrManifestMismatch      = errors.New("footprint: environment manifest mismatch")
)

// Footprint represents an execution environment footprint
type Footprint struct {
	FootprintID       string            `json:"footprint_id"`
	Arch              string            `json:"arch"`
	OS                string            `json:"os"`
	Libc              string            `json:"libc"`
	RuntimeVersions   map[string]string `json:"runtime_versions"`
	Accelerator       []string          `json:"accelerator"`
	EnvironmentManifest string          `json:"environment_manifest"`
}

// Subsumes checks if evidenceFootprint ⊑ currentFootprint
// Returns nil if subsumption holds, error otherwise.
// Subsumption means: evidence was produced in an environment compatible with current.
// Rules:
// - Arch must match exactly (no cross-arch subsumption)
// - OS must match exactly (no cross-OS subsumption)
// - Libc must match or be compatible (musl ⊑ glibc for static binaries)
// - Runtime versions: evidence version ≤ current version (semver compatible)
// - Accelerators: evidence accelerators ⊆ current accelerators
// - Manifest: if both have manifest CID, they must match; if evidence has no manifest, allow
func (current *Footprint) Subsumes(evidence *Footprint) error {
	// Architecture: exact match required
	if current.Arch != evidence.Arch {
		return ErrIncompatibleArch
	}

	// OS: exact match required
	if current.OS != evidence.OS {
		return ErrIncompatibleOS
	}

	// Libc: current must be compatible with evidence
	if !libcCompatible(current.Libc, evidence.Libc) {
		return ErrIncompatibleLibc
	}

	// Runtime versions: evidence version must be ≤ current version (compatible)
	for name, evVer := range evidence.RuntimeVersions {
		curVer, ok := current.RuntimeVersions[name]
		if !ok {
			// Runtime not present in current - could be optional, warn but allow
			continue
		}
		if !semverCompatible(evVer, curVer) {
			return ErrRuntimeVersionMismatch
		}
	}

	// Accelerators: evidence accelerators must be subset of current
	for _, evAccel := range evidence.Accelerator {
		found := false
		for _, curAccel := range current.Accelerator {
			if evAccel == curAccel {
				found = true
				break
			}
		}
		if !found && evAccel != "none" {
			return ErrMissingAccelerator
		}
	}

	// Environment manifest: if both specified, must match
	if evidence.EnvironmentManifest != "" && current.EnvironmentManifest != "" {
		if evidence.EnvironmentManifest != current.EnvironmentManifest {
			return ErrManifestMismatch
		}
	}

	return nil
}

// libcCompatible returns true if currentLibc can run binaries built for evidenceLibc
func libcCompatible(current, evidence string) bool {
	if current == evidence {
		return true
	}
	// musl binaries can often run on glibc systems (static linking)
	if current == "glibc" && evidence == "musl" {
		return true
	}
	// none (bare metal/wasm) only compatible with none
	if current == "none" || evidence == "none" {
		return current == evidence
	}
	return false
}

// semverCompatible returns true if evidenceVer ≤ currentVer (compatible)
// Uses semantic versioning: MAJOR.MINOR.PATCH[-PRERELEASE]
// Compatible if: MAJOR matches and MINOR evidence ≤ current, or MAJOR=0 special case
func semverCompatible(evidence, current string) bool {
	// Strip prerelease suffixes for comparison
	ev := stripPrerelease(evidence)
	cu := stripPrerelease(current)

	evParts := strings.Split(ev, ".")
	cuParts := strings.Split(cu, ".")

	if len(evParts) == 0 || len(cuParts) == 0 {
		return false
	}

	// Major version must match (except 0.x.y where minor is breaking)
	evMajor := parseInt(evParts[0])
	cuMajor := parseInt(cuParts[0])

	if evMajor == 0 && cuMajor == 0 {
		// 0.x.y: minor is breaking
		evMinor := parseInt(safeGet(evParts, 1))
		cuMinor := parseInt(safeGet(cuParts, 1))
		return evMinor <= cuMinor
	}

	if evMajor != cuMajor {
		return false
	}

	// Minor: evidence ≤ current for compatibility
	evMinor := parseInt(safeGet(evParts, 1))
	cuMinor := parseInt(safeGet(cuParts, 1))
	return evMinor <= cuMinor
}

func stripPrerelease(v string) string {
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		return v[:i]
	}
	return v
}

func safeGet(parts []string, i int) string {
	if i < len(parts) {
		return parts[i]
	}
	return "0"
}

func parseInt(s string) int {
	n := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n = n*10 + int(r-'0')
		} else {
			break
		}
	}
	return n
}

// CurrentFootprint returns the footprint of the current execution environment
func CurrentFootprint() *Footprint {
	return &Footprint{
		Arch:              runtime.GOARCH,
		OS:                runtime.GOOS,
		Libc:              detectLibc(),
		RuntimeVersions:   detectRuntimes(),
		Accelerator:       detectAccelerators(),
		EnvironmentManifest: "", // Filled by host if available
	}
}

func detectLibc() string {
	switch runtime.GOOS {
	case "linux":
		return "glibc" // Simplified; could detect musl via /lib/libc.musl-*
	case "darwin":
		return "bionic" // macOS uses its own libc
	case "windows":
		return "none" // MSVC runtime
	default:
		return "none"
	}
}

func detectRuntimes() map[string]string {
	// In practice, this would query actual installed runtimes
	// For now, return the Go version as baseline
	return map[string]string{
		"go": runtime.Version()[2:], // Strip "go" prefix
	}
}

func detectAccelerators() []string {
	// In practice, would detect CUDA, ROCm, Metal, etc.
	return []string{"none"}
}