package env

import (
	"os"
	"sort"
	"strings"
)

const envVarPrefix = "SAPIEN_SECRET_"

// envVarName maps a secret name to its environment variable name: upper-cased
// with every non-alphanumeric byte replaced by '_'.
func envVarName(name string) string {
	var b strings.Builder
	b.Grow(len(envVarPrefix) + len(name))
	b.WriteString(envVarPrefix)
	for _, r := range strings.ToUpper(name) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

type envVarStore struct{}

// NewEnvVarStore returns a read-only SecretStore backed by process
// environment variables: secret "name" is read from
// SAPIEN_SECRET_<NAME-upper-cased-non-alnum-to-underscore>. This is the
// first store CI environments hit (PLAN §20).
func NewEnvVarStore() SecretStore {
	return envVarStore{}
}

func (envVarStore) Get(name string) (string, error) {
	v, ok := os.LookupEnv(envVarName(name))
	if !ok {
		return "", secretMissing(name)
	}
	return v, nil
}

func (envVarStore) Set(name, value string) error {
	return readOnly("environment variable secret store")
}

func (envVarStore) Delete(name string) error {
	return readOnly("environment variable secret store")
}

func (envVarStore) List() ([]string, error) {
	var names []string
	for _, kv := range os.Environ() {
		k, _, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(k, envVarPrefix) {
			continue
		}
		names = append(names, strings.TrimPrefix(k, envVarPrefix))
	}
	sort.Strings(names)
	return names, nil
}
