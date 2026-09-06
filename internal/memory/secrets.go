package memory

import "regexp"

// Secret-pattern heuristics (PLAN.md §20, §28: "runs and memories are scanned
// for secret values on write"). These are deliberately loose and warn-only:
// ScanSecrets never blocks a write, it just names what it found so a caller
// (CLI/MCP) can surface a warning.
var (
	secretBearerTokenRe  = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9\-_.]{10,}`)
	secretAWSAccessKeyRe = regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`)
	secretPrivateKeyRe   = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)
	// secretLongOpaqueRe matches a long run of hex or base64-alphabet
	// characters (32+), the shape of an API key, token, or hash that
	// shouldn't be pasted verbatim into a memory. Word boundaries keep it
	// from matching inside a longer alphanumeric run that isn't itself
	// opaque-secret-shaped (e.g. concatenated identifiers), though in
	// practice any 32+ run of hex/base64 characters is exactly what a secret
	// looks like.
	secretLongOpaqueRe = regexp.MustCompile(`\b[A-Za-z0-9+/_-]{32,}={0,2}\b`)
)

// ScanSecrets is a warn-only heuristic scan of text for patterns that look
// like secrets: bearer tokens, AWS access keys, PEM private key headers, and
// long opaque hex/base64-looking runs. It returns the names of every kind of
// pattern found (deduplicated, in a stable order), or nil if none. It never
// blocks a write; callers (CLI/MCP) are expected to surface the result as a
// warning to the user.
func (s *Store) ScanSecrets(text string) []string {
	return ScanSecrets(text)
}

// ScanSecrets is the package-level implementation behind Store.ScanSecrets;
// exported directly so callers that don't have a *Store (e.g. validating
// free-standing text) can still use it.
func ScanSecrets(text string) []string {
	var found []string
	add := func(name string) {
		for _, f := range found {
			if f == name {
				return
			}
		}
		found = append(found, name)
	}

	if secretBearerTokenRe.MatchString(text) {
		add("bearer token")
	}
	if secretAWSAccessKeyRe.MatchString(text) {
		add("aws access key")
	}
	if secretPrivateKeyRe.MatchString(text) {
		add("private key")
	}
	if secretLongOpaqueRe.MatchString(text) {
		add("long hex/base64 secret")
	}
	return found
}
