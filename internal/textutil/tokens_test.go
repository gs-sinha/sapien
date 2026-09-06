package textutil

import (
	"reflect"
	"testing"
)

func TestSplitIdent(t *testing.T) {
	cases := map[string][]string{
		"getRiderById":                {"get", "rider", "by", "id"},
		"qcomSkill":                   {"qcom", "skill"},
		"upcoming_trips":              {"upcoming", "trips"},
		"order-service":               {"order", "service"},
		"v1":                          {"v1"},
		"HTTPServer":                  {"http", "server"},
		"riderId":                     {"rider", "id"},
		"allocation-service.allocate": {"allocation", "service", "allocate"},
		"":                            nil,
	}
	for in, want := range cases {
		if got := SplitIdent(in); !reflect.DeepEqual(got, want) {
			t.Errorf("SplitIdent(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestPathTokens(t *testing.T) {
	got := PathTokens("/v1/riders/{riderId}")
	want := []string{"v1", "riders", "riderid", "rider", "id"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestTokens(t *testing.T) {
	got := Tokens("Find riders around a pickup (qcomSkill=true)")
	want := []string{"find", "riders", "around", "a", "pickup", "qcomskill", "qcom", "skill", "true"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestParseMethodPath(t *testing.T) {
	m, p, ok := ParseMethodPath("post /v1/orders")
	if !ok || m != "POST" || p != "/v1/orders" {
		t.Errorf("got %q %q %v", m, p, ok)
	}
	m, p, ok = ParseMethodPath("/v1/orders")
	if !ok || m != "" || p != "/v1/orders" {
		t.Errorf("got %q %q %v", m, p, ok)
	}
	if _, _, ok := ParseMethodPath("create order"); ok {
		t.Error("free text must not parse as path")
	}
}

func TestPathMatches(t *testing.T) {
	if !PathMatches("/v1/riders/{riderId}", "/v1/riders/R123") {
		t.Error("templated should match concrete")
	}
	if PathMatches("/v1/riders/{riderId}", "/v1/riders") {
		t.Error("length mismatch should fail")
	}
	if !PathMatches("/v1/orders", "/V1/Orders") {
		t.Error("case-insensitive segments")
	}
}
