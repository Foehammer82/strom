package updates

import (
	"fmt"
	"regexp"
	"strconv"
)

// stableVersionPattern matches only strict stable release tags of the form
// vMAJOR.MINOR.PATCH. Prereleases (e.g. "-rc1", "-beta1") deliberately do not
// match: standalone node updates only ever consider stable releases.
var stableVersionPattern = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

// stableVersion is a parsed vMAJOR.MINOR.PATCH version.
type stableVersion struct {
	major, minor, patch int
}

// ParseStableVersion parses a strict stable "vMAJOR.MINOR.PATCH" tag. It
// returns an error for prerelease tags, malformed tags, or the "dev" build
// placeholder so callers can distinguish "not a real release" from "older
// release" without guessing.
func ParseStableVersion(version string) (stableVersion, error) {
	matches := stableVersionPattern.FindStringSubmatch(version)
	if matches == nil {
		return stableVersion{}, fmt.Errorf("%q is not a stable vMAJOR.MINOR.PATCH version", version)
	}
	major, err := strconv.Atoi(matches[1])
	if err != nil {
		return stableVersion{}, fmt.Errorf("parse major version: %w", err)
	}
	minor, err := strconv.Atoi(matches[2])
	if err != nil {
		return stableVersion{}, fmt.Errorf("parse minor version: %w", err)
	}
	patch, err := strconv.Atoi(matches[3])
	if err != nil {
		return stableVersion{}, fmt.Errorf("parse patch version: %w", err)
	}
	return stableVersion{major: major, minor: minor, patch: patch}, nil
}

// IsStableVersion reports whether version is a strict stable release tag.
func IsStableVersion(version string) bool {
	return stableVersionPattern.MatchString(version)
}

// compare returns -1, 0, or 1 as v is less than, equal to, or greater than
// other.
func (v stableVersion) compare(other stableVersion) int {
	switch {
	case v.major != other.major:
		return compareInt(v.major, other.major)
	case v.minor != other.minor:
		return compareInt(v.minor, other.minor)
	default:
		return compareInt(v.patch, other.patch)
	}
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// IsNewerStableVersion reports whether candidate is a strictly newer stable
// release than installed. Both must be strict stable versions; malformed or
// prerelease/dev values are treated as non-comparable and return an error so
// callers never silently "upgrade" from an untrustworthy baseline.
func IsNewerStableVersion(candidate, installed string) (bool, error) {
	candidateVersion, err := ParseStableVersion(candidate)
	if err != nil {
		return false, fmt.Errorf("candidate version: %w", err)
	}
	installedVersion, err := ParseStableVersion(installed)
	if err != nil {
		return false, fmt.Errorf("installed version: %w", err)
	}
	return candidateVersion.compare(installedVersion) > 0, nil
}
