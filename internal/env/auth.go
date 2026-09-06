package env

import (
	"net/http"
	"sort"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// ApplyAuth applies an auth rule to an outgoing request. Every field of auth
// is passed through SubstituteSecrets first. It returns the distinct secret
// values that were substituted (sorted), so the runtime's redactor can scrub
// them from anything persisted about this request (PLAN §20, §28).
//
// A nil auth, or one of type domain.AuthNone, is a no-op.
func ApplyAuth(req *http.Request, auth *domain.Auth, store SecretStore) (usedValues []string, err error) {
	if auth == nil || auth.Type == "" || auth.Type == domain.AuthNone {
		return nil, nil
	}

	usedNames := map[string]struct{}{}
	sub := func(s string) (string, error) {
		out, names, err := SubstituteSecrets(s, store)
		if err != nil {
			return "", err
		}
		for _, name := range names {
			usedNames[name] = struct{}{}
		}
		return out, nil
	}

	switch auth.Type {
	case domain.AuthBearer:
		token, err := sub(auth.Token)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)

	case domain.AuthHeader:
		name, err := sub(auth.Name)
		if err != nil {
			return nil, err
		}
		value, err := sub(auth.Value)
		if err != nil {
			return nil, err
		}
		req.Header.Set(name, value)

	case domain.AuthBasic:
		username, err := sub(auth.Username)
		if err != nil {
			return nil, err
		}
		password, err := sub(auth.Password)
		if err != nil {
			return nil, err
		}
		req.SetBasicAuth(username, password)

	case domain.AuthQuery:
		name, err := sub(auth.Name)
		if err != nil {
			return nil, err
		}
		value, err := sub(auth.Value)
		if err != nil {
			return nil, err
		}
		q := req.URL.Query()
		q.Add(name, value)
		req.URL.RawQuery = q.Encode()

	default:
		return nil, errs.New(errs.Invalid, "unknown auth type %q", auth.Type)
	}

	for name := range usedNames {
		val, err := store.Get(name)
		if err != nil {
			return nil, err
		}
		usedValues = append(usedValues, val)
	}
	sort.Strings(usedValues)
	return usedValues, nil
}
