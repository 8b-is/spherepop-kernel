// Footprint subsumption for evidence freshness (Protocol §14).
// Enforces evidence.footprint ⊑ current_footprint: evidence produced in an
// environment compatible with the current environment remains admissible.

package schemas

import (
	"encoding/json"
	"errors"
	"runtime"
	"strings"
)

var (
	ErrIncompatibleArch       = errors.New("footprint: incompatible architecture")
	ErrIncompatibleOS         = errors.New("footprint: incompatible OS")
	ErrIncompatibleLibc       = errors.New("footprint: incompatible libc")
	ErrRuntimeVersionMismatch = errors.New("footprint: runtime version mismatch")
	ErrMissingAccelerator     = errors.New("footprint: required accelerator not available")
	ErrManifestMismatch       = errors.New("footprint: environment manifest mismatch")
)

// Footprint represents a content-addressed execution environment footprint.
type Footprint struct {
	FootprintID         string            `json:"footprint_id"`
	Arch                string            `json:"arch"`
	OS                  string            `json:"os"`
	Libc                string            `json:"libc"`
	RuntimeVersions     map[string]string `json:"runtime_versions"`
	Accelerator         []string          `json:"accelerator"`
	EnvironmentManifest string            `json:"environment_manifest"`
}

// Subsumes verifies evidence ⊑ current:
//   - Arch and OS must match exactly.
//   - Libc must match or current="glibc" with static evidence="musl".
//   - Every runtime required by evidence must exist in current at a semver-compatible version.
//   - Every non-"none" accelerator required by evidence must exist in current.
//   - If both specify EnvironmentManifest, they must match.
func (current *Footprint) Subsumes(evidence *Footprint) error {
	if current == nil || evidence == nil {
		return ErrIncompatibleArch
	}
	if current.Arch != evidence.Arch {
		return ErrIncompatibleArch
	}
	if current.OS != evidence.OS {
		return ErrIncompatibleOS
	}
	if !libcCompatible(current.Libc, evidence.Libc) {
		return ErrIncompatibleLibc
	}

	for name, evVer := range evidence.RuntimeVersions {
		curVer, ok := current.RuntimeVersions[name]
		if !ok {
			return ErrRuntimeVersionMismatch
		}
		if !semverCompatible(evVer, curVer) {
			return ErrRuntimeVersionMismatch
		}
	}

	for _, evAccel := range evidence.Accelerator {
		if evAccel == "none" {
			continue
		}
		found := false
		for _, curAccel := range current.Accelerator {
			if evAccel == curAccel {
				found = true
				break
			}
		}
		if !found {
			return ErrMissingAccelerator
		}
	}

	if evidence.EnvironmentManifest != "" && current.EnvironmentManifest != "" {
		if evidence.EnvironmentManifest != current.EnvironmentManifest {
			return ErrManifestMismatch
		}
	}

	return nil
}

func libcCompatible(current, evidence string) bool {
	if current == evidence {
		return true
	}
	if current == "glibc" && evidence == "musl" {
		return true
	}
	if current == "none" || evidence == "none" {
		return current == evidence
	}
	return false
}

// semverCompatible checks whether evidenceVer <= currentVer within the same
// breaking-change epoch (same MAJOR for MAJOR >= 1; same MINOR for 0.MINOR.PATCH).
func semverCompatible(evidence, current string) bool {
	evMaj, evMin, evPat, okEv := parseSemver(evidence)
	cuMaj, cuMin, cuPat, okCu := parseSemver(current)
	if !okEv || !okCu {
		return false
	}
	if evMaj != cuMaj {
		return false
	}
	if evMaj == 0 {
		return evMin == cuMin && evPat <= cuPat
	}
	if evMin != cuMin {
		return evMin < cuMin
	}
	return evPat <= cuPat
}

func parseSemver(v string) (major, minor, patch int, ok bool) {
	if v == "" {
		return 0, 0, 0, false
	}
	core := v
	if idx := strings.IndexAny(core, "-+"); idx >= 0 {
		core = core[:idx]
	}
	parts := strings.Split(core, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return 0, 0, 0, false
	}
	vals := [3]int{0, 0, 0}
	for i, part := range parts {
		n, valid := parseStrictUint(part)
		if !valid {
			return 0, 0, 0, false
		}
		vals[i] = n
	}
	return vals[0], vals[1], vals[2], true
}

func parseStrictUint(s string) (int, bool) {
	if len(s) == 0 {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// ComputeID returns the content-addressed CID of the footprint's canonical fields.
func (f *Footprint) ComputeID() string {
	type canonical struct {
		Arch                string            `json:"arch"`
		OS                  string            `json:"os"`
		Libc                string            `json:"libc"`
		RuntimeVersions     map[string]string `json:"runtime_versions"`
		Accelerator         []string          `json:"accelerator"`
		EnvironmentManifest string            `json:"environment_manifest"`
	}
	raw, _ := json.Marshal(canonical{
		Arch:                f.Arch,
		OS:                  f.OS,
		Libc:                f.Libc,
		RuntimeVersions:     f.RuntimeVersions,
		Accelerator:         f.Accelerator,
		EnvironmentManifest: f.EnvironmentManifest,
	})
	return computeCID("spherepop-footprint/1", raw)
}

// CurrentFootprint returns a schema-compliant Footprint for the active Go host.
func CurrentFootprint() *Footprint {
	runtimes := detectRuntimes()
	manifestRaw, _ := json.Marshal(runtimes)
	manifestCID := computeCID("spherepop-env-manifest/1", []byte(runtime.GOOS), []byte(runtime.GOARCH), manifestRaw)
	fp := &Footprint{
		Arch:                normalizeArch(runtime.GOARCH),
		OS:                  normalizeOS(runtime.GOOS),
		Libc:                detectLibc(),
		RuntimeVersions:     runtimes,
		Accelerator:         []string{"none"},
		EnvironmentManifest: manifestCID,
	}
	fp.FootprintID = fp.ComputeID()
	return fp
}

func normalizeArch(goarch string) string {
	switch goarch {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	case "riscv64":
		return "riscv64"
	case "wasm":
		return "wasm32"
	default:
		return "x86_64"
	}
}

func normalizeOS(goos string) string {
	switch goos {
	case "linux", "darwin", "windows", "freebsd":
		return goos
	default:
		return "none"
	}
}

func detectLibc() string {
	switch runtime.GOOS {
	case "linux":
		return "glibc"
	case "darwin":
		return "libsystem"
	default:
		return "none"
	}
}

func detectRuntimes() map[string]string {
	v := strings.TrimPrefix(runtime.Version(), "go")
	if idx := strings.IndexFunc(v, func(r rune) bool {
		return (r < '0' || r > '9') && r != '.'
	}); idx > 0 {
		v = v[:idx]
	}
	if _, _, _, ok := parseSemver(v); !ok {
		v = "1.22.0"
	}
	return map[string]string{
		"go": v,
	}
}
