package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
)

func TestRedactorHeaders(t *testing.T) {
	r := NewRedactor(nil, nil)

	t.Run("default headers redacted case-insensitively", func(t *testing.T) {
		req := Request{
			Method: "GET",
			URL:    "https://api.example.com/x",
			Headers: map[string]string{
				"Authorization": "Bearer abc123",
				"X-Api-Key":     "secretkey",
				"cookie":        "session=1",
				"X-Custom":      "keep-me",
			},
		}
		rec := r.RequestRecord(req)
		assert.Equal(t, "[REDACTED]", rec.Headers["Authorization"])
		assert.Equal(t, "[REDACTED]", rec.Headers["X-Api-Key"])
		assert.Equal(t, "[REDACTED]", rec.Headers["Cookie"])
		assert.Equal(t, "keep-me", rec.Headers["X-Custom"])
	})

	t.Run("custom configured header redacted", func(t *testing.T) {
		cfg := &domain.Redaction{Headers: []string{"X-Internal-Token"}}
		rc := NewRedactor(cfg, nil)
		req := Request{
			Method:  "GET",
			URL:     "https://api.example.com/x",
			Headers: map[string]string{"X-Internal-Token": "shh", "X-Public": "ok"},
		}
		rec := rc.RequestRecord(req)
		assert.Equal(t, "[REDACTED]", rec.Headers["X-Internal-Token"])
		assert.Equal(t, "ok", rec.Headers["X-Public"])
	})
}

func TestRedactorSecretValues(t *testing.T) {
	r := NewRedactor(nil, []string{"topsecret123", "sh0rt"})

	t.Run("in header value", func(t *testing.T) {
		req := Request{
			Method:  "GET",
			URL:     "https://api.example.com/x",
			Headers: map[string]string{"X-Trace": "value-topsecret123-here"},
		}
		rec := r.RequestRecord(req)
		assert.Equal(t, "value-[REDACTED]-here", rec.Headers["X-Trace"])
	})

	t.Run("in url query", func(t *testing.T) {
		req := Request{
			Method: "GET",
			URL:    "https://api.example.com/x?token=topsecret123&x=1",
		}
		rec := r.RequestRecord(req)
		assert.Equal(t, "https://api.example.com/x?token=[REDACTED]&x=1", rec.URL)
	})

	t.Run("in body string leaf", func(t *testing.T) {
		req := Request{
			Method: "POST",
			URL:    "https://api.example.com/x",
			Body:   map[string]any{"apiKey": "topsecret123", "n": 1},
		}
		rec := r.RequestRecord(req)
		body := rec.Body.(map[string]any)
		assert.Equal(t, "[REDACTED]", body["apiKey"])
		assert.EqualValues(t, 1, body["n"])
	})

	t.Run("secrets shorter than 4 chars ignored", func(t *testing.T) {
		r2 := NewRedactor(nil, []string{"ab"})
		req := Request{Method: "GET", URL: "https://api.example.com/x?x=ab"}
		rec := r2.RequestRecord(req)
		assert.Equal(t, "https://api.example.com/x?x=ab", rec.URL)
	})

	t.Run("String helper scrubs arbitrary text", func(t *testing.T) {
		assert.Equal(t, "run failed: [REDACTED] was rejected", r.String("run failed: topsecret123 was rejected"))
	})
}

func TestRedactorJSONPaths(t *testing.T) {
	cfg := &domain.Redaction{JSONPaths: []string{"customer.phone", "items[].card.number"}}
	r := NewRedactor(cfg, nil)

	body := map[string]any{
		"customer": map[string]any{
			"phone": "555-1234",
			"name":  "Ada",
		},
		"items": []any{
			map[string]any{"card": map[string]any{"number": "4111111111111111", "brand": "visa"}},
			map[string]any{"card": map[string]any{"number": "4222222222222222", "brand": "mc"}},
		},
	}

	resp := &Response{Status: 200, Body: body}
	rec := r.ResponseRecord(resp)
	got := rec.Body.(map[string]any)

	assert.Equal(t, "[REDACTED]", got["customer"].(map[string]any)["phone"])
	assert.Equal(t, "Ada", got["customer"].(map[string]any)["name"])

	items := got["items"].([]any)
	assert.Equal(t, "[REDACTED]", items[0].(map[string]any)["card"].(map[string]any)["number"])
	assert.Equal(t, "visa", items[0].(map[string]any)["card"].(map[string]any)["brand"])
	assert.Equal(t, "[REDACTED]", items[1].(map[string]any)["card"].(map[string]any)["number"])
	assert.Equal(t, "mc", items[1].(map[string]any)["card"].(map[string]any)["brand"])

	// original response must be untouched (deep-copy safety).
	origPhone := body["customer"].(map[string]any)["phone"]
	assert.Equal(t, "555-1234", origPhone)
	origCard0 := body["items"].([]any)[0].(map[string]any)["card"].(map[string]any)["number"]
	assert.Equal(t, "4111111111111111", origCard0)
}

func TestRedactorResponseRecord(t *testing.T) {
	r := NewRedactor(nil, []string{"topsecret123"})

	t.Run("JSON body redacted, BodyRaw empty", func(t *testing.T) {
		resp := &Response{
			Status:  200,
			Headers: map[string]string{"Set-Cookie": "sid=abc"},
			Body:    map[string]any{"token": "topsecret123"},
			BodyRaw: []byte(`{"token":"topsecret123"}`),
			Size:    24,
		}
		rec := r.ResponseRecord(resp)
		assert.Equal(t, "[REDACTED]", rec.Headers["Set-Cookie"])
		body := rec.Body.(map[string]any)
		assert.Equal(t, "[REDACTED]", body["token"])
		assert.Empty(t, rec.BodyRaw)

		// original untouched.
		assert.Equal(t, "topsecret123", resp.Body.(map[string]any)["token"])
	})

	t.Run("non-JSON body uses BodyRaw, scrubbed", func(t *testing.T) {
		resp := &Response{
			Status:  200,
			BodyRaw: []byte("plain text with topsecret123 inside"),
			Size:    36,
		}
		rec := r.ResponseRecord(resp)
		assert.Nil(t, rec.Body)
		assert.Equal(t, "plain text with [REDACTED] inside", rec.BodyRaw)
	})

	t.Run("truncated body (Body nil) uses BodyRaw", func(t *testing.T) {
		resp := &Response{
			Status:    200,
			BodyRaw:   []byte(`{"a":`),
			Truncated: true,
			Size:      100,
		}
		rec := r.ResponseRecord(resp)
		assert.Nil(t, rec.Body)
		assert.True(t, rec.Truncated)
		assert.Equal(t, `{"a":`, rec.BodyRaw)
	})
}

func TestRedactorRequestRecordBodyRawOnlyWhenNotJSONEncoded(t *testing.T) {
	r := NewRedactor(nil, nil)

	t.Run("string body -> BodyRaw", func(t *testing.T) {
		req := Request{Method: "POST", URL: "https://x", Body: "raw text"}
		rec := r.RequestRecord(req)
		assert.Nil(t, rec.Body)
		assert.Equal(t, "raw text", rec.BodyRaw)
	})

	t.Run("bytes body -> BodyRaw", func(t *testing.T) {
		req := Request{Method: "POST", URL: "https://x", Body: []byte("raw bytes")}
		rec := r.RequestRecord(req)
		assert.Nil(t, rec.Body)
		assert.Equal(t, "raw bytes", rec.BodyRaw)
	})

	t.Run("nil body -> nothing", func(t *testing.T) {
		req := Request{Method: "GET", URL: "https://x"}
		rec := r.RequestRecord(req)
		assert.Nil(t, rec.Body)
		assert.Empty(t, rec.BodyRaw)
	})

	t.Run("struct body -> parsed/redacted Body", func(t *testing.T) {
		req := Request{Method: "POST", URL: "https://x", Body: map[string]any{"a": 1}}
		rec := r.RequestRecord(req)
		require.NotNil(t, rec.Body)
		assert.Empty(t, rec.BodyRaw)
	})
}
