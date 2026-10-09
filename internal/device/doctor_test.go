package device

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

type doctorExec struct {
	fakeExec
	answers map[string]string // command prefix -> output; missing -> error
}

func (d *doctorExec) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	c := d.record(name, args)
	for prefix, out := range d.answers {
		if strings.HasPrefix(c, prefix) {
			return out, nil
		}
	}
	return "", errors.New("not found")
}

func TestDoctor_ReportsFixes(t *testing.T) {
	t.Setenv("ANDROID_HOME", "")
	t.Setenv("ANDROID_SDK_ROOT", "")
	t.Setenv("JAVA_HOME", "")
	ex := &doctorExec{answers: map[string]string{
		"node --version":      "v18.17.0\n",
		"adb devices":         "List of devices attached\n\n",
		"emulator -list-avds": "INFO    | Storing crashdata\nPixel_7_API_29\n",
	}}
	checks := Doctor(context.Background(), LocalConfig{AndroidHome: "", AppiumURL: "http://127.0.0.1:1", AppiumBin: "appium"}, ex)
	byName := map[string]Check{}
	for _, c := range checks {
		byName[c.Name] = c
	}
	assert.False(t, byName["node"].OK, "Node 18 is too old for Appium 3")
	assert.Contains(t, byName["node"].Fix, "appium@2")
	assert.Equal(t, "npm install -g appium", byName["appium"].Fix)
	assert.False(t, byName["JAVA_HOME"].OK)
	assert.True(t, byName["adb"].OK)
	assert.False(t, byName["device"].OK)
	assert.Contains(t, byName["device"].Fix, "avd: Pixel_7_API_29", "points at the AVD that exists")
	assert.True(t, byName["appium server"].OK && byName["appium server"].Optional, "auto-start covers a stopped server")
}

func TestDoctor_DriverInstalled(t *testing.T) {
	ex := &doctorExec{answers: map[string]string{
		"node --version":   "v22.12.0",
		"appium --version": "3.0.1",
		"appium driver list": `- Listing installed drivers
{"uiautomator2":{"version":"4.2.0"}}`,
	}}
	checks := Doctor(context.Background(), LocalConfig{AppiumURL: "http://127.0.0.1:1"}, ex)
	for _, c := range checks {
		switch c.Name {
		case "node", "appium", "uiautomator2 driver":
			assert.True(t, c.OK, "%+v", c)
		}
	}
}

func TestParseSource(t *testing.T) {
	src := `<?xml version="1.0" encoding="UTF-8"?>
<hierarchy>
  <android.widget.FrameLayout class="android.widget.FrameLayout" bounds="[0,0][1080,2400]">
    <android.widget.EditText class="android.widget.EditText" resource-id="login_enter_mobile_number" text="Enter mobile number" hint="Enter mobile number" clickable="true" bounds="[48,600][1032,744]"/>
    <android.widget.Button class="android.widget.Button" content-desc="Get OTP" clickable="true" bounds="[48,800][1032,944]"/>
    <android.view.View class="android.view.View" bounds="[0,0][1,1]"/>
  </android.widget.FrameLayout>
</hierarchy>`
	els := ParseSource(src)
	assert.Len(t, els, 2)
	assert.Equal(t, "{id: login_enter_mobile_number}", els[0].Selector())
	assert.Empty(t, els[0].Hint, "an empty field's hint shown as its text is not repeated")
	assert.Equal(t, "{text: Get OTP}", els[1].Selector())
	assert.True(t, els[1].Clickable)
	assert.Equal(t, `{text: "a: b"}`, Element{Text: "a: b"}.Selector())
}

func TestDoctor_NodeBarFollowsAppiumMajor(t *testing.T) {
	ex := &doctorExec{answers: map[string]string{
		"node --version":   "v20.16.0",
		"appium --version": "2.19.0",
	}}
	for _, c := range Doctor(context.Background(), LocalConfig{AppiumURL: "http://127.0.0.1:1"}, ex) {
		switch c.Name {
		case "node":
			assert.True(t, c.OK, "Node 20.16 is fine for Appium 2: %+v", c)
		case "uiautomator2 driver":
			assert.Contains(t, c.Fix, "uiautomator2@3.10.0")
		}
	}
}
