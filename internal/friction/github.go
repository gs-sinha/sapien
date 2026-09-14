package friction

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os/exec"
	"strings"
	"time"

	"github.com/gs-sinha/sapien/internal/errs"
)

// defaultGHTimeout and defaultGHBin are GitHubDiscussions' defaults, used
// whenever the corresponding field is unset -- the same "zero value means
// default" convention internal/gitsrc.Options uses for its own Timeout/Git.
const (
	defaultGHTimeout = 60 * time.Second
	defaultGHBin     = "gh"
)

// Publisher is what `sapien friction send` posts a rendered report through.
// The only implementation today is GitHubDiscussions; the interface exists
// so a test (or a future destination) can stand in for it without shelling
// out.
type Publisher interface {
	Publish(ctx context.Context, title, body string) (url string, err error)
}

// GitHubDiscussions posts a friction report as a GitHub Discussion by
// shelling out to the `gh` CLI, exactly as internal/gitsrc shells out to
// `git`: `gh` already holds the user's auth (via `gh auth login`), so
// Sapien itself stores no credentials for this either.
type GitHubDiscussions struct {
	// Repo is "owner/name" to post the Discussion on.
	Repo string
	// Category is a GitHub Discussion category name or slug on Repo (e.g.
	// "General" or "general"), matched case-insensitively. Empty defaults
	// to "General".
	Category string
	// GH is the `gh` executable to run. Default: "gh" (resolved via PATH).
	GH string
	// Timeout bounds every individual `gh` invocation. Default: 60s.
	Timeout time.Duration
}

var _ Publisher = GitHubDiscussions{}

// ghRepoQuery asks for exactly what Publish needs to validate the
// destination before ever creating anything: whether Discussions are on at
// all, and the id of the category to post into (createDiscussion takes
// category/repository ids, not names).
const ghRepoQuery = `query($owner:String!,$name:String!){ repository(owner:$owner,name:$name){ id hasDiscussionsEnabled discussionCategories(first:50){ nodes{ id name slug } } } }`

// ghCreateDiscussionMutation creates the Discussion once Publish has
// resolved repositoryId/categoryId from ghRepoQuery.
const ghCreateDiscussionMutation = `mutation($repo:ID!,$cat:ID!,$title:String!,$body:String!){ createDiscussion(input:{repositoryId:$repo,categoryId:$cat,title:$title,body:$body}){ discussion{ url } } }`

// ghGraphQLError is one entry of a GraphQL response's top-level "errors"
// array, returned alongside (or instead of) "data" on a query/permission
// problem.
type ghGraphQLError struct {
	Message string `json:"message"`
}

// ghRepoResponse is `gh api graphql`'s response shape for ghRepoQuery.
type ghRepoResponse struct {
	Data struct {
		Repository *struct {
			ID                    string `json:"id"`
			HasDiscussionsEnabled bool   `json:"hasDiscussionsEnabled"`
			DiscussionCategories  struct {
				Nodes []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
					Slug string `json:"slug"`
				} `json:"nodes"`
			} `json:"discussionCategories"`
		} `json:"repository"`
	} `json:"data"`
	Errors []ghGraphQLError `json:"errors"`
}

// ghCreateDiscussionResponse is `gh api graphql`'s response shape for
// ghCreateDiscussionMutation.
type ghCreateDiscussionResponse struct {
	Data struct {
		CreateDiscussion struct {
			Discussion struct {
				URL string `json:"url"`
			} `json:"discussion"`
		} `json:"createDiscussion"`
	} `json:"data"`
	Errors []ghGraphQLError `json:"errors"`
}

// Publish posts title/body as a GitHub Discussion on g.Repo, in g.Category
// (or "General"). It always makes two `gh api graphql` calls: the first
// resolves g.Repo's node id and g.Category's id (and checks Discussions are
// actually enabled), the second creates the Discussion.
func (g GitHubDiscussions) Publish(ctx context.Context, title, body string) (string, error) {
	owner, name, err := splitRepo(g.Repo)
	if err != nil {
		return "", err
	}

	out, err := g.run(ctx, "api", "graphql",
		"-f", "query="+ghRepoQuery,
		"-F", "owner="+owner,
		"-F", "name="+name,
	)
	if err != nil {
		return "", err
	}

	var resp ghRepoResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return "", errs.Wrap(errs.Internal, err, "friction: parsing gh api graphql response").WithDetail("stdout", out)
	}
	if len(resp.Errors) > 0 {
		return "", errs.New(errs.Invalid, "friction: %s", resp.Errors[0].Message).
			WithHint("check the repository name and your `gh` auth/access")
	}
	if resp.Data.Repository == nil {
		return "", errs.New(errs.Invalid, "friction: repository %s not found", g.Repo).
			WithHint("check friction.repo and that `gh` has access to it")
	}
	if !resp.Data.Repository.HasDiscussionsEnabled {
		return "", errs.New(errs.Invalid, "Discussions are not enabled on %s", g.Repo).
			WithHint("enable them under Settings > General > Features on GitHub, or set friction.repo to a repository that has them")
	}

	wantCategory := g.Category
	if wantCategory == "" {
		wantCategory = "General"
	}
	var categoryID string
	var available []string
	for _, c := range resp.Data.Repository.DiscussionCategories.Nodes {
		available = append(available, c.Name)
		if strings.EqualFold(c.Name, wantCategory) || strings.EqualFold(c.Slug, wantCategory) {
			categoryID = c.ID
		}
	}
	if categoryID == "" {
		return "", errs.New(errs.Invalid, "friction: no discussion category %q on %s", wantCategory, g.Repo).
			WithHint("available categories: " + strings.Join(available, ", "))
	}

	out, err = g.run(ctx, "api", "graphql",
		"-f", "query="+ghCreateDiscussionMutation,
		"-F", "repo="+resp.Data.Repository.ID,
		"-F", "cat="+categoryID,
		"-F", "title="+title,
		"-F", "body="+body,
	)
	if err != nil {
		return "", err
	}

	var created ghCreateDiscussionResponse
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		return "", errs.Wrap(errs.Internal, err, "friction: parsing gh api graphql response").WithDetail("stdout", out)
	}
	if len(created.Errors) > 0 {
		return "", errs.New(errs.Invalid, "friction: %s", created.Errors[0].Message)
	}
	url := created.Data.CreateDiscussion.Discussion.URL
	if url == "" {
		return "", errs.Wrap(errs.Internal, errors.New("no discussion url in response"), "friction: gh api graphql returned no discussion url").
			WithDetail("stdout", out)
	}
	return url, nil
}

// splitRepo splits "owner/name" into its two parts.
func splitRepo(repo string) (owner, name string, err error) {
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", errs.New(errs.Invalid, "friction: repo %q must be \"owner/name\"", repo)
	}
	return parts[0], parts[1], nil
}

// run executes `gh` with args, applying g.Timeout, capturing stdout/stderr,
// and forcing GH_PROMPT_DISABLED=1 so a missing credential fails instead of
// blocking on an interactive prompt -- the same GIT_TERMINAL_PROMPT=0
// discipline internal/gitsrc.Manager.run applies to `git`. An already-done
// ctx is rejected immediately, without starting a process.
func (g GitHubDiscussions) run(ctx context.Context, args ...string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", errs.Wrap(errs.Cancelled, err, "gh %s: cancelled", strings.Join(args, " "))
	}

	bin := g.GH
	if bin == "" {
		bin = defaultGHBin
	}
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = defaultGHTimeout
	}

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, bin, args...) //nolint:gosec // bin/args are operator-controlled config, not user input.
	cmd.Env = append(cmd.Environ(), "GH_PROMPT_DISABLED=1")
	// cmd.Stdin left nil: Go connects the child's stdin to the null
	// device, so `gh` never has a TTY to prompt on even if
	// GH_PROMPT_DISABLED were somehow ignored.

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return stdout.String(), ghError(cctx, bin, args, stderr.String(), err)
	}
	return stdout.String(), nil
}

// ghError classifies a failed `gh` invocation: a cancelled/timed-out
// context, a missing binary, an auth failure recognizable from stderr, or
// (the fallback) an internal error with stderr attached as a detail --
// mirroring internal/gitsrc.gitError's shape and reasoning.
func ghError(cctx context.Context, bin string, args []string, stderrOutput string, cause error) error {
	if cctx.Err() != nil {
		return errs.Wrap(errs.Cancelled, cctx.Err(), "gh %s: cancelled", strings.Join(args, " "))
	}

	// A bare "gh" that PATH can't resolve fails at Command-construction
	// time with *exec.Error; an explicit path (as GitHubDiscussions.GH or
	// a test's stub-binary path can be) instead fails at Start with a
	// wrapped fs.ErrNotExist from the syscall exec itself. Both mean the
	// same thing to a caller: no `gh` there.
	var execErr *exec.Error
	if errors.As(cause, &execErr) || errors.Is(cause, exec.ErrNotFound) || errors.Is(cause, fs.ErrNotExist) {
		return errs.Wrap(errs.Invalid, cause, "gh: %q not found", bin).
			WithHint("install the GitHub CLI (https://cli.github.com) and run `gh auth login`")
	}

	s := strings.ToLower(stderrOutput)
	if strings.Contains(s, "auth") || strings.Contains(s, "login") {
		return errs.Wrap(errs.Invalid, cause, "gh %s failed", strings.Join(args, " ")).
			WithDetail("stderr", strings.TrimSpace(stderrOutput)).
			WithHint("install the GitHub CLI (https://cli.github.com) and run `gh auth login`")
	}

	return errs.Wrap(errs.Internal, cause, "gh %s failed", strings.Join(args, " ")).
		WithDetail("stderr", strings.TrimSpace(stderrOutput))
}
