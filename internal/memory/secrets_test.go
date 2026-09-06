package memory_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/gs-sinha/sapien/internal/memory"
)

func TestScanSecrets(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string // nil means "expect empty"
	}{
		{
			name: "bearer token",
			text: "Auth header was: Authorization: Bearer sk-live-ABCDEFGHIJ1234567890",
			want: []string{"bearer token"},
		},
		{
			name: "aws access key",
			text: "found AKIAIOSFODNN7EXAMPLE in the config",
			want: []string{"aws access key"},
		},
		{
			name: "aws sts key",
			text: "temporary key ASIAABCDEFGHIJKLMNOP was used",
			want: []string{"aws access key"},
		},
		{
			name: "private key header",
			text: "-----BEGIN RSA PRIVATE KEY-----\nMIIB...\n-----END RSA PRIVATE KEY-----",
			want: []string{"private key"},
		},
		{
			name: "long hex secret",
			text: "the api key is 9f3a7c1e2b4d5f60718293a4b5c6d7e8f9a0b1c2d3e4f5061728394a5b6c7d8",
			want: []string{"long hex/base64 secret"},
		},
		{
			name: "long base64 secret",
			text: "token=QWxhZGRpbjpvcGVuIHNlc2FtZQQWxhZGRpbjpvcGVuIHNlc2FtZQ==",
			want: []string{"long hex/base64 secret"},
		},
		{
			name: "multiple kinds at once",
			text: "Bearer abcdefghijklmnop and AKIAIOSFODNN7EXAMPLE both present",
			want: []string{"bearer token", "aws access key"},
		},
		{
			name: "plain prose has no secrets",
			text: "qcomSkill indicates whether the rider is eligible for quick-commerce orders.",
			want: nil,
		},
		{
			name: "short opaque-looking token is not flagged",
			text: "the id is abc123",
			want: nil,
		},
		{
			name: "empty text",
			text: "",
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := memory.ScanSecrets(tc.text)
			if tc.want == nil {
				assert.Empty(t, got)
				return
			}
			assert.ElementsMatch(t, tc.want, got)
		})
	}
}

func TestStore_ScanSecrets_DelegatesToPackageFunc(t *testing.T) {
	s, _ := newTestStore(t, nil)
	got := s.ScanSecrets("AKIAIOSFODNN7EXAMPLE")
	assert.Equal(t, []string{"aws access key"}, got)
}
