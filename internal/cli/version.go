package cli

import (
	"runtime/debug"
	"strconv"
	"strings"
)

// rawVersion is set at build time via ldflags:
//
//	go build -ldflags "-X github.com/shellroute/shellroute-cli/internal/cli.rawVersion=v0.2.0"
var rawVersion = "dev"

// installMethod is set at build time via ldflags. Each distribution
// channel sets its own value: "npm", "brew", or "" (unknown).
//
//	go build -ldflags "-X github.com/shellroute/shellroute-cli/internal/cli.installMethod=npm"
var installMethod = ""

// UpdateCommand returns the appropriate update instruction.
func UpdateCommand() string {
	switch installMethod {
	case "npm":
		return "npm update -g shellroute"
	case "brew":
		return "brew upgrade shellroute"
	case "direct":
		return "Download the latest release from https://github.com/shellroute/shellroute-cli/releases"
	default:
		return "Visit https://github.com/shellroute/shellroute-cli/releases"
	}
}

// Version returns the clean version string (no "v" prefix, no git suffixes).
// "v0.1.0-3-gabcdef-dirty" → "0.1.0"
// "dev" → "dev"
// Falls back to debug.ReadBuildInfo for go install users.
func Version() string {
	v := cleanVersion(rawVersion)
	if v == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok {
			mv := info.Main.Version
			// Skip empty, (devel), and pseudo-versions (v0.0.0-timestamp-hash)
			if mv != "" && mv != "(devel)" && !strings.HasPrefix(mv, "v0.0.0-") {
				return cleanVersion(mv)
			}
		}
	}
	return v
}

// cleanVersion strips "v" prefix and git describe suffixes (-dirty, -N-gHASH).
func cleanVersion(v string) string {
	v = strings.TrimPrefix(v, "v")
	if idx := strings.IndexByte(v, '-'); idx >= 0 {
		// Keep only the semver part before the first dash
		v = v[:idx]
	}
	return v
}

// compareVersions compares two semver strings.
// Returns -1 if a < b, 0 if equal, 1 if a > b.
func compareVersions(a, b string) int {
	ap := parseSemver(a)
	bp := parseSemver(b)
	for i := 0; i < 3; i++ {
		if ap[i] < bp[i] {
			return -1
		}
		if ap[i] > bp[i] {
			return 1
		}
	}
	return 0
}

// parseSemver parses "v0.1.0", "0.1.0", "0.1.0-dirty" etc. into [major, minor, patch].
func parseSemver(v string) [3]int {
	v = cleanVersion(v)
	parts := strings.SplitN(v, ".", 3)
	var result [3]int
	for i, p := range parts {
		if i >= 3 {
			break
		}
		result[i], _ = strconv.Atoi(p)
	}
	return result
}
