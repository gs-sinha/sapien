// Package appium is a minimal W3C WebDriver client for an Appium server:
// just the endpoints ui steps use, over plain HTTP, so Sapien needs no
// Appium SDK. Element lookups never wait server-side (implicit wait stays
// 0); callers poll, which keeps every timeout under the flow's control.
package appium

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// elementKey is the W3C element reference key in WebDriver responses.
const elementKey = "element-6066-11e4-a52e-4f735466cecf"

// ErrNoSuchElement is returned by FindElements' callers when nothing matched.
var ErrNoSuchElement = errors.New("no such element")

// Client talks to one Appium server.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// New returns a client for baseURL (e.g. http://127.0.0.1:4723).
func New(baseURL string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: 5 * time.Minute}}
}

// Error is a WebDriver error response.
type Error struct {
	Status  int
	Code    string // W3C error code, e.g. "no such element"
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("appium: %s (HTTP %d): %s", e.Code, e.Status, e.Message)
}

// Ready reports whether the server answers /status.
func (c *Client) Ready(ctx context.Context) error {
	var out struct {
		Ready bool `json:"ready"`
	}
	if err := c.do(ctx, http.MethodGet, "/status", nil, &out); err != nil {
		return err
	}
	return nil
}

// Session is one WebDriver session.
type Session struct {
	c  *Client
	ID string
}

// NewSession creates a session with the given capabilities (W3C
// alwaysMatch; keys other than platformName must carry the `appium:`
// vendor prefix).
func (c *Client) NewSession(ctx context.Context, caps map[string]any) (*Session, error) {
	var out struct {
		SessionID string `json:"sessionId"`
	}
	body := map[string]any{"capabilities": map[string]any{"alwaysMatch": caps, "firstMatch": []any{map[string]any{}}}}
	if err := c.do(ctx, http.MethodPost, "/session", body, &out); err != nil {
		return nil, err
	}
	if out.SessionID == "" {
		return nil, fmt.Errorf("appium: session created without an id")
	}
	return &Session{c: c, ID: out.SessionID}, nil
}

// Delete ends the session.
func (s *Session) Delete(ctx context.Context) error {
	return s.c.do(ctx, http.MethodDelete, "/session/"+s.ID, nil, nil)
}

func (s *Session) path(p string) string { return "/session/" + s.ID + p }

// FindElements returns the ids of every element matching (using, value);
// an empty slice (not an error) when nothing matches.
func (s *Session) FindElements(ctx context.Context, using, value string) ([]string, error) {
	var out []map[string]string
	if err := s.c.do(ctx, http.MethodPost, s.path("/elements"), map[string]string{"using": using, "value": value}, &out); err != nil {
		var we *Error
		if errors.As(err, &we) && we.Code == "no such element" {
			return nil, nil
		}
		return nil, err
	}
	ids := make([]string, 0, len(out))
	for _, m := range out {
		if id := m[elementKey]; id != "" {
			ids = append(ids, id)
		} else if id := m["ELEMENT"]; id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// FindChildElements returns the ids of el's descendants matching (using,
// value); an empty slice when none match.
func (s *Session) FindChildElements(ctx context.Context, el, using, value string) ([]string, error) {
	var out []map[string]string
	if err := s.c.do(ctx, http.MethodPost, s.path("/element/"+el+"/elements"), map[string]string{"using": using, "value": value}, &out); err != nil {
		var we *Error
		if errors.As(err, &we) && we.Code == "no such element" {
			return nil, nil
		}
		return nil, err
	}
	ids := make([]string, 0, len(out))
	for _, m := range out {
		if id := m[elementKey]; id != "" {
			ids = append(ids, id)
		} else if id := m["ELEMENT"]; id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// Click taps an element.
func (s *Session) Click(ctx context.Context, el string) error {
	return s.c.do(ctx, http.MethodPost, s.path("/element/"+el+"/click"), map[string]any{}, nil)
}

// SendKeys types text into an element.
func (s *Session) SendKeys(ctx context.Context, el, text string) error {
	return s.c.do(ctx, http.MethodPost, s.path("/element/"+el+"/value"), map[string]any{"text": text}, nil)
}

// Clear empties a text element.
func (s *Session) Clear(ctx context.Context, el string) error {
	return s.c.do(ctx, http.MethodPost, s.path("/element/"+el+"/clear"), map[string]any{}, nil)
}

// Text returns an element's visible text.
func (s *Session) Text(ctx context.Context, el string) (string, error) {
	var out string
	err := s.c.do(ctx, http.MethodGet, s.path("/element/"+el+"/text"), nil, &out)
	return out, err
}

// Attribute returns an element attribute ("" when absent).
func (s *Session) Attribute(ctx context.Context, el, name string) (string, error) {
	var out *string
	if err := s.c.do(ctx, http.MethodGet, s.path("/element/"+el+"/attribute/"+name), nil, &out); err != nil {
		return "", err
	}
	if out == nil {
		return "", nil
	}
	return *out, nil
}

// Source returns the current screen's UI hierarchy as XML.
func (s *Session) Source(ctx context.Context) (string, error) {
	var out string
	err := s.c.do(ctx, http.MethodGet, s.path("/source"), nil, &out)
	return out, err
}

// Screenshot returns the current screen as PNG.
func (s *Session) Screenshot(ctx context.Context) ([]byte, error) {
	var out string
	if err := s.c.do(ctx, http.MethodGet, s.path("/screenshot"), nil, &out); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(out)
}

// Back presses the platform back button.
func (s *Session) Back(ctx context.Context) error {
	return s.c.do(ctx, http.MethodPost, s.path("/back"), map[string]any{}, nil)
}

// Rect is a window or element rectangle.
type Rect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// WindowRect returns the screen size.
func (s *Session) WindowRect(ctx context.Context) (Rect, error) {
	var out Rect
	err := s.c.do(ctx, http.MethodGet, s.path("/window/rect"), nil, &out)
	return out, err
}

// Mobile runs an Appium `mobile: <cmd>` extension (activateApp,
// terminateApp, deepLink, swipeGesture, scrollGesture, ...), decoding its
// result into out when non-nil.
func (s *Session) Mobile(ctx context.Context, cmd string, args map[string]any, out any) error {
	body := map[string]any{"script": "mobile: " + cmd, "args": []any{args}}
	return s.c.do(ctx, http.MethodPost, s.path("/execute/sync"), body, out)
}

// do sends one WebDriver request and decodes its `value` into out.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("appium: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var env struct {
		Value json.RawMessage `json:"value"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &env); err != nil {
			return fmt.Errorf("appium: %s %s: HTTP %d, unreadable body: %.200s", method, path, resp.StatusCode, raw)
		}
	}
	if resp.StatusCode >= 400 {
		var we struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(env.Value, &we)
		if we.Error == "" {
			we.Error = "unknown error"
		}
		return &Error{Status: resp.StatusCode, Code: we.Error, Message: firstLine(we.Message)}
	}
	if out != nil && len(env.Value) > 0 && string(env.Value) != "null" {
		if err := json.Unmarshal(env.Value, out); err != nil {
			return fmt.Errorf("appium: decoding %s %s: %w", method, path, err)
		}
	}
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
