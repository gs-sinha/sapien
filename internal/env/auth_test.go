package env

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/errs"
)

func newTestRequest(t *testing.T) *http.Request {
	t.Helper()
	u, err := url.Parse("https://api.example.com/orders")
	require.NoError(t, err)
	return &http.Request{Method: "GET", URL: u, Header: http.Header{}}
}

func TestApplyAuth_NoneOrNil(t *testing.T) {
	store := newTestStore(t, nil)

	req := newTestRequest(t)
	used, err := ApplyAuth(req, nil, store)
	require.NoError(t, err)
	assert.Nil(t, used)
	assert.Empty(t, req.Header)

	req2 := newTestRequest(t)
	used, err = ApplyAuth(req2, &domain.Auth{Type: domain.AuthNone}, store)
	require.NoError(t, err)
	assert.Nil(t, used)
	assert.Empty(t, req2.Header)
}

func TestApplyAuth_Bearer(t *testing.T) {
	store := newTestStore(t, map[string]string{"STAGING_TOKEN": "s3cr3t"})
	req := newTestRequest(t)

	used, err := ApplyAuth(req, &domain.Auth{Type: domain.AuthBearer, Token: "${secret.STAGING_TOKEN}"}, store)
	require.NoError(t, err)
	assert.Equal(t, "Bearer s3cr3t", req.Header.Get("Authorization"))
	assert.Equal(t, []string{"s3cr3t"}, used)
}

func TestApplyAuth_Header(t *testing.T) {
	store := newTestStore(t, map[string]string{"RIDER_KEY": "key-value"})
	req := newTestRequest(t)

	used, err := ApplyAuth(req, &domain.Auth{Type: domain.AuthHeader, Name: "X-Api-Key", Value: "${secret.RIDER_KEY}"}, store)
	require.NoError(t, err)
	assert.Equal(t, "key-value", req.Header.Get("X-Api-Key"))
	assert.Equal(t, []string{"key-value"}, used)
}

func TestApplyAuth_Header_NoSecretRef(t *testing.T) {
	store := newTestStore(t, nil)
	req := newTestRequest(t)

	used, err := ApplyAuth(req, &domain.Auth{Type: domain.AuthHeader, Name: "X-Api-Key", Value: "plain-value"}, store)
	require.NoError(t, err)
	assert.Equal(t, "plain-value", req.Header.Get("X-Api-Key"))
	assert.Nil(t, used)
}

func TestApplyAuth_Basic(t *testing.T) {
	store := newTestStore(t, map[string]string{"USER": "alice", "PASS": "hunter2"})
	req := newTestRequest(t)

	used, err := ApplyAuth(req, &domain.Auth{Type: domain.AuthBasic, Username: "${secret.USER}", Password: "${secret.PASS}"}, store)
	require.NoError(t, err)

	username, password, ok := req.BasicAuth()
	require.True(t, ok)
	assert.Equal(t, "alice", username)
	assert.Equal(t, "hunter2", password)
	assert.ElementsMatch(t, []string{"alice", "hunter2"}, used)
}

func TestApplyAuth_Query(t *testing.T) {
	store := newTestStore(t, map[string]string{"API_KEY": "qk"})
	req := newTestRequest(t)

	used, err := ApplyAuth(req, &domain.Auth{Type: domain.AuthQuery, Name: "api_key", Value: "${secret.API_KEY}"}, store)
	require.NoError(t, err)
	assert.Equal(t, "qk", req.URL.Query().Get("api_key"))
	assert.Equal(t, []string{"qk"}, used)
}

func TestApplyAuth_UsedValuesDedupedAcrossFields(t *testing.T) {
	store := newTestStore(t, map[string]string{"SAME": "dup-value"})
	req := newTestRequest(t)

	used, err := ApplyAuth(req, &domain.Auth{Type: domain.AuthHeader, Name: "X-Val", Value: "${secret.SAME}-${secret.SAME}"}, store)
	require.NoError(t, err)
	assert.Equal(t, "dup-value-dup-value", req.Header.Get("X-Val"))
	assert.Equal(t, []string{"dup-value"}, used, "the same secret used twice is only reported once")
}

func TestApplyAuth_MissingSecret(t *testing.T) {
	store := newTestStore(t, nil)
	req := newTestRequest(t)

	_, err := ApplyAuth(req, &domain.Auth{Type: domain.AuthBearer, Token: "${secret.MISSING}"}, store)
	require.Error(t, err)
	assert.True(t, errs.Is(err, errs.SecretMissing))
}

func TestApplyAuth_UnknownType(t *testing.T) {
	store := newTestStore(t, nil)
	req := newTestRequest(t)

	_, err := ApplyAuth(req, &domain.Auth{Type: domain.AuthType("weird")}, store)
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}
