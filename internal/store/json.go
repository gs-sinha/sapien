package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// MarshalJSON serializes v for storage in a *_json TEXT column. A nil v (or one
// that marshals to the JSON literal null) is stored as "null"; callers that want
// an empty object/array should pass one explicitly.
func MarshalJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("store: marshal json: %w", err)
	}
	return string(b), nil
}

// UnmarshalJSON decodes s (as read from a *_json TEXT column) into v. An empty
// string is treated as "no value" and is a no-op, leaving v unchanged, since
// SQLite columns are frequently NULL/"" for not-yet-populated rows.
func UnmarshalJSON(s string, v any) error {
	if s == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(s), v); err != nil {
		return fmt.Errorf("store: unmarshal json: %w", err)
	}
	return nil
}

// NullString converts a Go string to a sql.NullString, treating "" as SQL NULL.
// Use it when a zero-value string should be stored as NULL rather than "".
func NullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// StringOrEmpty converts a sql.NullString back to a Go string, mapping NULL to "".
func StringOrEmpty(s sql.NullString) string {
	if !s.Valid {
		return ""
	}
	return s.String
}
