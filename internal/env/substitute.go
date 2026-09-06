package env

import "regexp"

// secretRef matches ${secret.NAME} references.
var secretRef = regexp.MustCompile(`\$\{secret\.([A-Za-z0-9_]+)\}`)

// HasSecretRef reports whether s contains at least one ${secret.NAME}
// reference.
func HasSecretRef(s string) bool {
	return secretRef.MatchString(s)
}

// SecretNames returns the distinct secret names referenced in s, in the
// order they first appear.
func SecretNames(s string) []string {
	matches := secretRef.FindAllStringSubmatch(s, -1)
	if matches == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	var names []string
	for _, m := range matches {
		name := m[1]
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

// SubstituteSecrets replaces every ${secret.NAME} reference in s with the
// named secret's value from store. used lists the distinct names that were
// substituted (in order of first appearance), so callers can scrub those
// values from anything they persist (PLAN §20, §28). If any referenced
// secret is missing, it returns the store's errs.SecretMissing error
// unchanged and s is not substituted at all.
func SubstituteSecrets(s string, store SecretStore) (out string, used []string, err error) {
	if !HasSecretRef(s) {
		return s, nil, nil
	}

	seen := map[string]struct{}{}
	var firstErr error
	replaced := secretRef.ReplaceAllStringFunc(s, func(match string) string {
		if firstErr != nil {
			return match
		}
		sub := secretRef.FindStringSubmatch(match)
		name := sub[1]
		val, getErr := store.Get(name)
		if getErr != nil {
			firstErr = getErr
			return match
		}
		if _, ok := seen[name]; !ok {
			seen[name] = struct{}{}
			used = append(used, name)
		}
		return val
	})
	if firstErr != nil {
		return "", nil, firstErr
	}
	return replaced, used, nil
}
