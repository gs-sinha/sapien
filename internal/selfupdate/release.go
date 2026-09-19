package selfupdate

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gs-sinha/sapien/internal/errs"
)

// defaultReleasesBase is this repository's GitHub Releases root. Every URL
// this file builds is relative to it (or, in a test, to LatestOptions.
// BaseURL/ApplyOptions.BaseURL instead).
const defaultReleasesBase = "https://github.com/gs-sinha/sapien"

// LatestOptions configures Latest.
type LatestOptions struct {
	// BaseURL overrides defaultReleasesBase, so a test can point Latest at
	// an httptest.Server that plays GitHub's redirect.
	BaseURL string
	// Client overrides http.DefaultClient.
	Client *http.Client
}

func releasesBase(base string) string {
	if base == "" {
		return defaultReleasesBase
	}
	return strings.TrimSuffix(base, "/")
}

// Latest resolves the latest release's tag by following the redirect
// releases/latest sends to releases/tag/<tag> -- the same trick
// scripts/install.sh already uses to avoid api.github.com's unauthenticated
// rate limit. It makes no call to the GitHub API.
func Latest(ctx context.Context, opts LatestOptions) (string, error) {
	client := opts.Client
	if client == nil {
		client = http.DefaultClient
	}

	url := releasesBase(opts.BaseURL) + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", errs.Wrap(errs.Internal, err, "building request for %s", url)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", errs.Wrap(errs.Internal, err, "requesting %s", url)
	}
	defer resp.Body.Close()
	// The body (an HTML release page) is never read; only the final
	// redirected URL matters, and it is already known once headers arrive.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))

	if resp.Request == nil || resp.Request.URL == nil {
		return "", errs.New(errs.Internal, "no response URL to read a release tag from")
	}
	final := resp.Request.URL.String()
	tag, ok := tagFromReleaseURL(final)
	if !ok {
		return "", errs.New(errs.Internal, "could not find a release tag in %s", final)
	}
	return tag, nil
}

// tagFromReleaseURL extracts "v1.2.3" from ".../releases/tag/v1.2.3".
func tagFromReleaseURL(u string) (string, bool) {
	const marker = "/releases/tag/"
	i := strings.Index(u, marker)
	if i < 0 {
		return "", false
	}
	tag := u[i+len(marker):]
	if tag == "" {
		return "", false
	}
	return tag, true
}

// ReleaseURL returns the human-facing release page for tag.
func ReleaseURL(tag string) string {
	return fmt.Sprintf("%s/releases/tag/%s", defaultReleasesBase, tag)
}
