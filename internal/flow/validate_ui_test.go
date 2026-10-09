package flow

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

func validateUISrc(t *testing.T, src string) (*domain.Flow, *domain.ValidationResult) {
	t.Helper()
	return NewValidator(newFakeCatalog()).ValidateSource(context.Background(), src)
}

func codes(res *domain.ValidationResult) []string {
	var out []string
	for _, d := range res.Diagnostics {
		out = append(out, d.Code)
	}
	return out
}

func TestUIStep_ParsesActions(t *testing.T) {
	f, res := validateUISrc(t, `
version: 1
id: ui
inputs:
  phone: {type: string, default: "8445544024"}
steps:
  - id: login
    ui:
      app: rider
      actions:
        - launch: {clear_state: true}
        - type: {hint: Enter mobile number, text: "${inputs.phone}"}
        - type: {into: {text: OTP}, text: "000000", clear: true}
        - tap: {text: Get OTP, index: 1}
        - wait_for: {id: home, timeout: 30s, gone: true}
        - read: {id: rider_name, as: name}
        - deeplink: kaptaan://home
        - screenshot: home
        - back
        - assert_text: {id: rider_name, contains: Ra}
    assert:
      - {path: out.name, exists: true}
      - "body.logs.api.exists(c, c.status == 200)"
`)
	require.True(t, res.Valid, "%+v", res.Diagnostics)
	acts := f.Steps[0].UI.Actions
	require.Len(t, acts, 10)

	assert.Equal(t, domain.UILaunch, acts[0].Kind)
	assert.True(t, acts[0].ClearState)

	assert.Equal(t, "${inputs.phone}", acts[1].Text)
	assert.Equal(t, "Enter mobile number", acts[1].Target.Hint)

	assert.Equal(t, "000000", acts[2].Text, "type's text is what to type")
	assert.Equal(t, "OTP", acts[2].Target.Text, "into: selects by text")
	assert.True(t, acts[2].Clear)

	assert.Equal(t, 1, acts[3].Target.Index)
	assert.True(t, acts[4].Gone)
	assert.Equal(t, "name", acts[5].As)
	assert.Equal(t, "kaptaan://home", acts[6].URL)
	assert.Equal(t, "home", acts[7].Name)
	assert.Equal(t, domain.UIBack, acts[8].Kind)
	assert.Equal(t, "Ra", acts[9].Contains)
	assert.Greater(t, acts[0].Line, 0)
}

func TestUIStep_Diagnostics(t *testing.T) {
	cases := map[string]struct {
		step string
		want string
	}{
		"ui with call": {`
  - id: s
    call: order-service.getOrder
    ui: {app: rider, actions: [back]}`, CodeUnknownKey},
		"ui with body": {`
  - id: s
    body: {a: 1}
    ui: {app: rider, actions: [back]}`, CodeUnknownKey},
		"two selectors": {`
  - id: s
    ui: {app: rider, actions: [{tap: {id: a, text: b}}]}`, CodeUIAction},
		"tap without selector": {`
  - id: s
    ui: {app: rider, actions: [{tap: {timeout: 2s}}]}`, CodeUIAction},
		"type without text": {`
  - id: s
    ui: {app: rider, actions: [{type: {id: phone}}]}`, CodeUIAction},
		"read without as": {`
  - id: s
    ui: {app: rider, actions: [{read: {id: name}}]}`, CodeUIAction},
		"assert_text two matchers": {`
  - id: s
    ui: {app: rider, actions: [{assert_text: {id: a, eq: x, contains: y}}]}`, CodeUIAction},
		"swipe without direction": {`
  - id: s
    ui: {app: rider, actions: [{swipe: {}}]}`, CodeUIAction},
		"selector on back": {`
  - id: s
    ui: {app: rider, actions: [{back: {id: x}}]}`, CodeUIAction},
		"unknown kind": {`
  - id: s
    ui: {app: rider, actions: [{pinch: {id: x}}]}`, CodeSchema},
		"bad timeout": {`
  - id: s
    ui: {app: rider, actions: [{wait_for: {id: x, timeout: soon}}]}`, CodeSchema},
		"unknown out": {`
  - id: s
    ui: {app: rider, actions: [{read: {id: x, as: name}}]}
    assert: ["out.nmae == 'a'"]`, CodeUnknownOut},
		"unknown step in template": {`
  - id: s
    ui: {app: rider, actions: [{tap: {id: "card_${steps.nope.out.id}"}}]}`, CodeUnknownStep},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, res := validateUISrc(t, "version: 1\nid: ui\nsteps:"+tc.step+"\n")
			assert.False(t, res.Valid, "%+v", res.Diagnostics)
			assert.Contains(t, codes(res), tc.want, "%+v", res.Diagnostics)
		})
	}
}

func TestUIStep_MixesWithCallSteps(t *testing.T) {
	_, res := validateUISrc(t, `
version: 1
id: mixed
steps:
  - id: login
    ui:
      app: rider
      actions:
        - read: {id: rider_id, as: rider}
  - id: order
    call: rider-service.getRider
    input: {riderId: "${steps.login.out.rider}"}
  - id: card
    ui:
      app: rider
      actions:
        - wait_for: {id: "task_card_${steps.order.body.riderId}"}
teardown:
  - id: home
    ui: {app: rider, actions: [stop]}
`)
	assert.True(t, res.Valid, "%+v", res.Diagnostics)
}

func TestUIReference_ExamplesValidate(t *testing.T) {
	blocks := fencedYAMLBlocks(UIReference())
	require.NotEmpty(t, blocks)
	for i, block := range blocks {
		_, res := validateUISrc(t, block)
		assert.True(t, res.Valid, "example #%d: %+v\n%s", i, res.Diagnostics, block)
	}
}

func TestReferencedSteps_SeesUIActionTemplates(t *testing.T) {
	st := domain.Step{ID: "card", UI: &domain.UIStep{App: "rider", Actions: []domain.UIAction{
		{Kind: domain.UITap, Target: &domain.Selector{ID: "task_card_${steps.assign.out.tripId}"}},
		{Kind: domain.UIType, Target: &domain.Selector{Hint: "x"}, Text: "${steps.login.out.otp}"},
	}}}
	assert.ElementsMatch(t, []string{"assign", "login"}, ReferencedSteps(st))
}

func TestUIStep_SwipeAcrossElementAndHideKeyboard(t *testing.T) {
	f, res := validateUISrc(t, `
version: 1
id: ui
steps:
  - id: s
    ui:
      app: rider
      actions:
        - type: {id: amount, text: "10"}
        - hide_keyboard
        - swipe: {id: early_salary_swipe_withdraw, direction: right}
        - swipe: {direction: up}
`)
	require.True(t, res.Valid, "%+v", res.Diagnostics)
	acts := f.Steps[0].UI.Actions
	assert.Equal(t, domain.UIHideKeyboard, acts[1].Kind)
	assert.Equal(t, "early_salary_swipe_withdraw", acts[2].Target.ID)
	assert.Nil(t, acts[3].Target)

	_, res = validateUISrc(t, "version: 1\nid: ui\nsteps:\n  - id: s\n    ui: {app: rider, actions: [{hide_keyboard: {id: x}}]}\n")
	assert.Contains(t, codes(res), CodeUIAction, "hide_keyboard takes no selector")
}
