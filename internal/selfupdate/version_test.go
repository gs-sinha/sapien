package selfupdate_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/gs-sinha/sapien/internal/selfupdate"
)

func TestIsNewer_Table(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v1.3.0", "v1.3.1", true},
		{"v1.3.1", "v1.3.1", false},
		{"v1.3.1", "v1.3.0", false},
		{"v1.9.0", "v2.0.0", true},
		{"1.3.0", "1.3.1", true}, // "v" prefix optional
		{"dev", "v99.0.0", false},
		{"v1.3.1", "", false},
		{"v1.3.1", "not-a-version", false},
		{"not-a-version", "v1.3.1", false},
		{"v1.3.1-5-gabc1234", "v1.3.1", true}, // a prerelease-shaped git-describe string sorts before the release
		{"v1.3.1-5-gabc1234", "v1.3.2", true},
		{"v1.3.1", "v1.3.1-5-gabc1234", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, selfupdate.IsNewer(tc.current, tc.latest), "current=%q latest=%q", tc.current, tc.latest)
	}
}
