package logs

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fixtures reproduce, byte for byte, what each app's logging code
// prints through the `logger` package's PrettyPrinter (border, per-line
// emoji, ANSI colours) as `adb logcat -v brief` shows it: rider_app_fe's
// _logApiDebug / _DioLogInterceptor and overwatch's baseservice.dart
// interceptor.
func fixture(t *testing.T, name string) []string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	require.NoError(t, err)
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

func TestClean(t *testing.T) {
	assert.Equal(t, "║ API POST /auth", Clean("I/flutter ( 4242): \x1b[38;5;12m│ 💡 ║ API POST /auth\x1b[0m"))
	assert.Equal(t, "hello", Clean("10-09 12:00:00.123  4242  4250 I flutter : hello"))
	assert.Equal(t, "hello", Clean("  1696841234.123  4242  4250 I flutter : hello"))
	assert.Equal(t, "plain", Clean("plain"))
}

func TestParseRiderBox(t *testing.T) {
	calls := Parse(FormatRiderBox, fixture(t, "rider.logcat"))
	require.Len(t, calls, 2)

	auth := calls[0]
	assert.Equal(t, "POST", auth.Method)
	assert.Equal(t, "/v1/auth", auth.Path)
	assert.Equal(t, "https://0rqgkkpp8j.execute-api.us-east-2.amazonaws.com/v1/auth", auth.URL)
	assert.Equal(t, 200, auth.Status)
	assert.Equal(t, map[string]any{"Content-Type": "application/json"}, auth.RequestHeaders)
	assert.Equal(t, "create_auth", auth.RequestBody.(map[string]any)["request_type"])
	assert.Equal(t, map[string]any{"session": "abc"}, auth.ResponseBody)

	prof := calls[1]
	assert.Equal(t, "GET", prof.Method)
	assert.Equal(t, "/v1/rider/profile", prof.Path)
	assert.Equal(t, 401, prof.Status)
	assert.Nil(t, prof.RequestBody, "no Request body section")
	assert.Equal(t, "Unauthorized", prof.ResponseBody.(map[string]any)["message"])
}

func TestParseOverwatch_PairsRequestsAndResponses(t *testing.T) {
	calls := Parse(FormatOverwatchAPILogger, fixture(t, "overwatch.logcat"))
	require.Len(t, calls, 2)

	riders := calls[0]
	assert.Equal(t, "POST", riders.Method)
	assert.Equal(t, "/v1/dispatch/riders", riders.Path)
	assert.Equal(t, 200, riders.Status, "the response logged after another request still pairs by URL")
	assert.Equal(t, map[string]any{"hub": float64(12)}, riders.RequestBody)
	assert.Equal(t, "Ravi", riders.ResponseBody.(map[string]any)["riders"].([]any)[0].(map[string]any)["name"])

	cod := calls[1]
	assert.Equal(t, "GET", cod.Method)
	assert.Nil(t, cod.RequestBody)
	assert.Equal(t, 0, cod.Status)
	assert.Equal(t, "Http status error [500]", cod.Error)
	assert.Equal(t, map[string]any{"error": "boom"}, cod.ResponseBody)
}

func TestParse_RawAndUnknownFormatsYieldNoCalls(t *testing.T) {
	assert.Empty(t, Parse(FormatRaw, fixture(t, "rider.logcat")))
	assert.Empty(t, Parse("", fixture(t, "rider.logcat")))
}

func TestCleanLines_DropsFramesAndBlanks(t *testing.T) {
	lines := CleanLines(fixture(t, "rider.logcat"))
	assert.Equal(t, "app started", lines[0])
	for _, l := range lines {
		assert.False(t, strings.HasPrefix(l, "┌") || strings.HasPrefix(l, "└"), l)
	}
	assert.Contains(t, lines, "║ API POST /auth")
}

func TestAPICallMap(t *testing.T) {
	m := APICall{Method: "GET", Path: "/x", Status: 200}.Map()
	assert.Equal(t, map[string]any{}, m["request"].(map[string]any)["headers"])
	assert.NotContains(t, m, "error")
}

func TestBuffer_MarkSinceAndTrim(t *testing.T) {
	b := NewBuffer(3)
	b.Append("a")
	m := b.Mark()
	b.Append("b")
	b.Append("c")
	assert.Equal(t, []string{"b", "c"}, b.Since(m))
	b.Append("d")
	b.Append("e") // "a","b" trimmed
	assert.Equal(t, []string{"c", "d", "e"}, b.Since(m), "a mark older than the buffer returns what is left")
	assert.Nil(t, b.Since(b.Mark()))
}
