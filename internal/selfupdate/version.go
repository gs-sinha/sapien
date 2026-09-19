package selfupdate

import (
	"regexp"
	"strconv"
	"strings"
)

// semverRE matches a "vX[.Y[.Z]][-prerelease][+build]" version string. Y
// and Z default to 0 when absent so a bare "v1" or "v1.2" still compares
// sensibly. This is deliberately looser than strict semver 2.0: a `git
// describe --tags` string like "v1.3.1-5-gabc1234" (an unreleased dev
// build a few commits past a tag) parses too, with "5-gabc1234" as its
// "prerelease" part, which is exactly the ordering that string wants --
// see compareVersions.
var semverRE = regexp.MustCompile(`^v?(\d+)(?:\.(\d+))?(?:\.(\d+))?(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$`)

type semver struct {
	major, minor, patch int
	prerelease          string
}

func parseVersion(s string) (semver, bool) {
	m := semverRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return semver{}, false
	}
	var v semver
	v.major, _ = strconv.Atoi(m[1])
	if m[2] != "" {
		v.minor, _ = strconv.Atoi(m[2])
	}
	if m[3] != "" {
		v.patch, _ = strconv.Atoi(m[3])
	}
	v.prerelease = m[4]
	return v, true
}

// compareVersions returns -1, 0, or 1 as a is older than, equal to, or
// newer than b. A non-empty prerelease sorts before the same
// major.minor.patch with none (matching semver 2.0's own rule: 1.2.0-rc1 <
// 1.2.0); between two non-empty prereleases it is a plain string compare,
// which is not full semver 2.0 dot-identifier precedence but is enough to
// order "git describe" suffixes like "5-gabc1234" sensibly against a bare
// release tag.
func compareVersions(a, b semver) int {
	if a.major != b.major {
		return cmpInt(a.major, b.major)
	}
	if a.minor != b.minor {
		return cmpInt(a.minor, b.minor)
	}
	if a.patch != b.patch {
		return cmpInt(a.patch, b.patch)
	}
	switch {
	case a.prerelease == "" && b.prerelease == "":
		return 0
	case a.prerelease == "":
		return 1
	case b.prerelease == "":
		return -1
	default:
		return strings.Compare(a.prerelease, b.prerelease)
	}
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// IsNewer reports whether latest names a release newer than current. "dev"
// (the ldflags default for a build git could not describe with a tag) and
// an empty/unparseable latest never count as an update: there is nothing
// meaningful to compare a dev build's version string against, and an
// unparseable latest is treated as "no answer" rather than a false
// positive.
func IsNewer(current, latest string) bool {
	if current == "dev" || latest == "" {
		return false
	}
	c, ok := parseVersion(current)
	if !ok {
		return false
	}
	l, ok := parseVersion(latest)
	if !ok {
		return false
	}
	return compareVersions(l, c) > 0
}
