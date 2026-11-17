// Package source resolves user input (a GitHub URL or a local directory)
// into an immutable, inspectable directory with recorded provenance.
//
// Remote repositories are fetched with git invoked as an argv array under a
// locked-down environment: no hooks, no global/system config, no credential
// helpers or prompts, no submodules, no LFS smudge. Nothing from the
// repository is executed.
package source

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/zerosetup/zerosetup/internal/zerr"
)

const stage = "source"

// Resolved is a fetched, pinned source ready for inspection.
type Resolved struct {
	Kind       string `json:"kind"` // github | local
	Repository string `json:"repository,omitempty"`
	URL        string `json:"url,omitempty"`
	Ref        string `json:"ref,omitempty"`
	Commit     string `json:"commit,omitempty"`
	Dir        string `json:"dir"`              // directory to inspect (includes Subdir)
	Subdir     string `json:"subdir,omitempty"` // within the repository
	CacheHit   bool   `json:"cacheHit"`
}

// Resolver fetches sources into an application-managed cache.
type Resolver struct {
	CacheDir string        // e.g. ~/.zerosetup/sources
	MaxBytes int64         // maximum checked-out size
	Timeout  time.Duration // overall fetch timeout
	GitPath  string        // resolved lazily from PATH if empty
}

var (
	ghNameRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	ghRepoRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	shaRe    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	refRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)
)

// GitHubRef is a parsed GitHub repository reference.
type GitHubRef struct {
	Owner, Repo, Ref, Subdir string
}

// Repository returns "github.com/owner/repo".
func (g GitHubRef) Repository() string { return "github.com/" + g.Owner + "/" + g.Repo }

// CloneURL returns the canonical HTTPS clone URL.
func (g GitHubRef) CloneURL() string { return "https://github.com/" + g.Owner + "/" + g.Repo + ".git" }

// IsRemote reports whether input looks like a remote URL rather than a path.
func IsRemote(input string) bool {
	return strings.Contains(input, "://") || strings.HasPrefix(input, "github.com/") ||
		strings.HasPrefix(input, "git@") || strings.HasPrefix(input, "www.github.com/")
}

// ParseGitHubURL validates and parses a GitHub HTTPS URL. Only github.com
// over HTTPS is accepted; embedded credentials are rejected.
func ParseGitHubURL(raw string) (GitHubRef, error) {
	in := strings.TrimSpace(raw)
	if strings.HasPrefix(in, "git@") || strings.HasPrefix(in, "ssh://") {
		return GitHubRef{}, zerr.New(zerr.CodeSourceUnsupported, stage, "SSH URLs are not supported").
			WithSuggestion("Use the HTTPS form: https://github.com/<owner>/<repo>")
	}
	if !strings.Contains(in, "://") {
		in = "https://" + in
	}
	u, err := url.Parse(in)
	if err != nil {
		return GitHubRef{}, zerr.Wrap(err, zerr.CodeSourceInvalid, stage, "not a valid URL")
	}
	if u.Scheme != "https" {
		return GitHubRef{}, zerr.New(zerr.CodeSourceUnsupported, stage, "only https:// URLs are supported").
			WithDetail("got scheme %q", u.Scheme)
	}
	if u.User != nil {
		return GitHubRef{}, zerr.New(zerr.CodeSourceInvalid, stage, "URLs containing credentials are refused").
			WithSuggestion("Remove the username/token from the URL. ZEROSETUP never sends credentials unless explicitly configured.")
	}
	host := strings.ToLower(u.Hostname())
	if host == "www.github.com" {
		host = "github.com"
	}
	if host != "github.com" || u.Port() != "" {
		return GitHubRef{}, zerr.New(zerr.CodeSourceUnsupported, stage, "only github.com repositories are supported in this version").
			WithDetail("host %q is not on the provider allowlist", u.Host)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		// Ignore harmless UI cruft but never interpret it.
		u.RawQuery, u.Fragment = "", ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return GitHubRef{}, zerr.New(zerr.CodeSourceInvalid, stage, "URL must name a repository: https://github.com/<owner>/<repo>")
	}
	g := GitHubRef{Owner: parts[0], Repo: strings.TrimSuffix(parts[1], ".git")}
	if !ghNameRe.MatchString(g.Owner) || !ghRepoRe.MatchString(g.Repo) || g.Repo == "." || g.Repo == ".." {
		return GitHubRef{}, zerr.New(zerr.CodeSourceInvalid, stage, "invalid GitHub owner or repository name")
	}
	rest := parts[2:]
	if len(rest) > 0 {
		if (rest[0] == "tree" || rest[0] == "commit") && len(rest) >= 2 {
			g.Ref = rest[1]
			if len(rest) > 2 {
				g.Subdir = strings.Join(rest[2:], "/")
			}
		} else {
			return GitHubRef{}, zerr.New(zerr.CodeSourceInvalid, stage, "unrecognised GitHub URL path").
				WithDetail("expected /<owner>/<repo>[/tree/<ref>[/<subdir>]]")
		}
	}
	if g.Ref != "" && !refRe.MatchString(g.Ref) {
		return GitHubRef{}, zerr.New(zerr.CodeSourceInvalid, stage, "invalid ref")
	}
	if g.Subdir != "" {
		if _, err := clean(g.Subdir); err != nil {
			return GitHubRef{}, zerr.Wrap(err, zerr.CodeSourceInvalid, stage, "invalid subdirectory")
		}
	}
	return g, nil
}

// ValidateRef checks a user-supplied branch/tag/SHA.
func ValidateRef(ref string) error {
	if ref == "" {
		return nil
	}
	if !refRe.MatchString(ref) || strings.Contains(ref, "..") || strings.HasSuffix(ref, ".lock") {
		return zerr.New(zerr.CodeSourceInvalid, stage, fmt.Sprintf("invalid ref %q", ref))
	}
	return nil
}

// Resolve resolves input to a pinned directory. ref overrides any ref in the URL.
func (r *Resolver) Resolve(ctx context.Context, input, ref string) (*Resolved, error) {
	if err := ValidateRef(ref); err != nil {
		return nil, err
	}
	if !IsRemote(input) {
		return r.resolveLocal(input, ref)
	}
	g, err := ParseGitHubURL(input)
	if err != nil {
		return nil, err
	}
	if ref != "" {
		g.Ref = ref
	}
	return r.resolveGitHub(ctx, g)
}

func (r *Resolver) resolveLocal(input, ref string) (*Resolved, error) {
	if ref != "" {
		return nil, zerr.New(zerr.CodeSourceUnsupported, stage, "--ref is not supported for local directories").
			WithSuggestion("Check out the ref yourself, then inspect the directory.")
	}
	abs, err := filepath.Abs(input)
	if err != nil {
		return nil, zerr.Wrap(err, zerr.CodeSourceInvalid, stage, "invalid path")
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, zerr.Wrap(err, zerr.CodeSourceNotFound, stage, "directory not found").WithDetail("%s", abs)
	}
	if !st.IsDir() {
		return nil, zerr.New(zerr.CodeSourceInvalid, stage, "source must be a directory").WithDetail("%s", abs)
	}
	res := &Resolved{Kind: "local", Dir: abs}
	res.Commit = readLocalHead(abs) // best effort, pure file reads (never runs git here)
	return res, nil
}

// readLocalHead reads .git/HEAD without invoking git, because repo-local git
// config (e.g. core.fsmonitor) can execute commands.
func readLocalHead(dir string) string {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return ""
	}
	defer root.Close()
	head, err := root.ReadFile(".git/HEAD")
	if err != nil {
		return ""
	}
	h := strings.TrimSpace(string(head))
	if shaRe.MatchString(h) {
		return h
	}
	refName, ok := strings.CutPrefix(h, "ref: ")
	if !ok || !strings.HasPrefix(refName, "refs/") || strings.Contains(refName, "..") {
		return ""
	}
	if b, err := root.ReadFile(".git/" + refName); err == nil {
		if s := strings.TrimSpace(string(b)); shaRe.MatchString(s) {
			return s
		}
	}
	if b, err := root.ReadFile(".git/packed-refs"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) == 2 && f[1] == refName && shaRe.MatchString(f[0]) {
				return f[0]
			}
		}
	}
	return ""
}

func (r *Resolver) git() (string, error) {
	if r.GitPath != "" {
		return r.GitPath, nil
	}
	p, err := exec.LookPath("git")
	if err != nil {
		return "", zerr.Wrap(err, zerr.CodeGitUnavailable, stage, "git is required to fetch remote repositories").
			WithSuggestion("Install git, or inspect a local directory instead.")
	}
	r.GitPath = p
	return p, nil
}

// gitEnv is a minimal environment that disables every git extension point
// that could run code or leak credentials.
func gitEnv(home string) []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"LANG=C", "LC_ALL=C",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_LFS_SKIP_SMUDGE=1",
		"GIT_PROTOCOL_FROM_USER=0",
	}
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "NO_PROXY", "no_proxy"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

var gitSafeConfig = []string{
	"-c", "core.hooksPath=" + os.DevNull,
	"-c", "protocol.file.allow=never",
	"-c", "protocol.ext.allow=never",
	"-c", "protocol.allow=never",
	"-c", "protocol.https.allow=always",
	"-c", "credential.helper=",
	"-c", "core.fsmonitor=false",
	"-c", "submodule.recurse=false",
	"-c", "core.symlinks=true",
}

func (r *Resolver) runGit(ctx context.Context, dir string, args ...string) (string, error) {
	gitBin, err := r.git()
	if err != nil {
		return "", err
	}
	full := append(append([]string{}, gitSafeConfig...), args...)
	cmd := exec.CommandContext(ctx, gitBin, full...)
	cmd.Dir = dir
	cmd.Env = gitEnv(r.CacheDir)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", &gitError{args: args, stderr: strings.TrimSpace(errb.String()), err: err}
	}
	return out.String(), nil
}

type gitError struct {
	args   []string
	stderr string
	err    error
}

func (e *gitError) Error() string { return fmt.Sprintf("git %s: %v: %s", e.args[0], e.err, e.stderr) }

func classifyGitError(err error, repo string) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return zerr.Wrap(err, zerr.CodeSourceFetch, stage, "timed out fetching "+repo)
	}
	if errors.Is(err, context.Canceled) {
		return zerr.Wrap(err, zerr.CodeCancelled, stage, "fetch cancelled")
	}
	var ge *gitError
	if errors.As(err, &ge) {
		s := strings.ToLower(ge.stderr)
		switch {
		case strings.Contains(s, "repository not found"), strings.Contains(s, "not found"):
			return zerr.Wrap(err, zerr.CodeSourceNotFound, stage, repo+" was not found, or it is private").
				WithSuggestion("Check the URL. Private repositories require explicitly configured authentication, which is not supported yet.")
		case strings.Contains(s, "could not read username"), strings.Contains(s, "authentication"):
			return zerr.Wrap(err, zerr.CodeSourceAuthRequired, stage, repo+" requires authentication").
				WithSuggestion("ZEROSETUP does not send credentials automatically. Clone it yourself and inspect the local directory.")
		case strings.Contains(s, "couldn't find remote ref"), strings.Contains(s, "not our ref"):
			return zerr.Wrap(err, zerr.CodeSourceNotFound, stage, "the requested ref does not exist in "+repo)
		case strings.Contains(s, "could not resolve host"), strings.Contains(s, "unable to access"):
			return zerr.Wrap(err, zerr.CodeSourceFetch, stage, "network error while contacting github.com").
				WithSuggestion("Check your internet connection and proxy settings.")
		}
	}
	return zerr.Wrap(err, zerr.CodeSourceFetch, stage, "failed to fetch "+repo)
}

// resolveCommit maps a ref (or HEAD) to a commit SHA via ls-remote.
func (r *Resolver) resolveCommit(ctx context.Context, g GitHubRef) (string, error) {
	if shaRe.MatchString(g.Ref) {
		return g.Ref, nil
	}
	var patterns []string
	if g.Ref == "" {
		patterns = []string{"HEAD"}
	} else {
		patterns = []string{"refs/heads/" + g.Ref, "refs/tags/" + g.Ref, "refs/tags/" + g.Ref + "^{}"}
	}
	out, err := r.runGit(ctx, r.CacheDir, append([]string{"ls-remote", "--", g.CloneURL()}, patterns...)...)
	if err != nil {
		return "", classifyGitError(err, g.Repository())
	}
	refs := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && shaRe.MatchString(f[0]) {
			refs[f[1]] = f[0]
		}
	}
	for _, p := range []string{"HEAD", "refs/tags/" + g.Ref + "^{}", "refs/heads/" + g.Ref, "refs/tags/" + g.Ref} {
		if sha, ok := refs[p]; ok {
			return sha, nil
		}
	}
	if g.Ref != "" && regexp.MustCompile(`^[0-9a-f]{7,39}$`).MatchString(g.Ref) {
		return "", zerr.New(zerr.CodeSourceUnsupported, stage, "abbreviated commit SHAs are not supported").
			WithSuggestion("Pass the full 40-character commit SHA.")
	}
	return "", zerr.New(zerr.CodeSourceNotFound, stage, fmt.Sprintf("ref %q not found in %s", g.Ref, g.Repository()))
}

const completeMarker = ".zerosetup-source.json"

func (r *Resolver) resolveGitHub(ctx context.Context, g GitHubRef) (*Resolved, error) {
	timeout := r.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := os.MkdirAll(r.CacheDir, 0o700); err != nil {
		return nil, zerr.Wrap(err, zerr.CodeSourceFetch, stage, "cannot create source cache")
	}
	sha, err := r.resolveCommit(ctx, g)
	if err != nil {
		return nil, err
	}
	final := filepath.Join(r.CacheDir, "github.com", g.Owner, g.Repo, sha)
	res := &Resolved{Kind: "github", Repository: g.Repository(), URL: "https://github.com/" + g.Owner + "/" + g.Repo,
		Ref: g.Ref, Commit: sha, Subdir: g.Subdir}

	if _, err := os.Stat(filepath.Join(final, completeMarker)); err == nil {
		res.CacheHit = true
	} else {
		if err := r.fetchInto(ctx, g, sha, final); err != nil {
			return nil, err
		}
	}
	res.Dir = filepath.Join(final, "src")
	if g.Subdir != "" {
		res.Dir = filepath.Join(res.Dir, filepath.FromSlash(g.Subdir))
		root, err := os.OpenRoot(filepath.Join(final, "src"))
		if err != nil {
			return nil, zerr.Wrap(err, zerr.CodeSourceFetch, stage, "cannot open fetched source")
		}
		st, err := root.Stat(g.Subdir)
		root.Close()
		if err != nil || !st.IsDir() {
			return nil, zerr.New(zerr.CodeSourceNotFound, stage, fmt.Sprintf("subdirectory %q not found at %s", g.Subdir, sha[:12]))
		}
	}
	return res, nil
}

func (r *Resolver) fetchInto(ctx context.Context, g GitHubRef, sha, final string) error {
	parent := filepath.Dir(final)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return zerr.Wrap(err, zerr.CodeSourceFetch, stage, "cannot create source cache")
	}
	tmp := filepath.Join(parent, ".tmp-"+sha[:12]+"-"+randHex(4))
	src := filepath.Join(tmp, "src")
	if err := os.MkdirAll(src, 0o700); err != nil {
		return zerr.Wrap(err, zerr.CodeSourceFetch, stage, "cannot create temp dir")
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(tmp)
		}
	}()
	steps := [][]string{
		{"init", "-q", "--template=", "."},
		{"fetch", "-q", "--depth=1", "--no-tags", "--no-recurse-submodules", "--", g.CloneURL(), sha},
		{"checkout", "-q", "--detach", "FETCH_HEAD"},
	}
	for _, s := range steps {
		if _, err := r.runGit(ctx, src, s...); err != nil {
			return classifyGitError(err, g.Repository())
		}
	}
	if err := os.RemoveAll(filepath.Join(src, ".git")); err != nil {
		return zerr.Wrap(err, zerr.CodeSourceFetch, stage, "cannot finalise checkout")
	}
	size, err := dirSize(src)
	if err != nil {
		return zerr.Wrap(err, zerr.CodeSourceFetch, stage, "cannot measure checkout")
	}
	max := r.MaxBytes
	if max == 0 {
		max = 1 << 30
	}
	if size > max {
		return zerr.New(zerr.CodeSourceTooLarge, stage, "repository is too large").
			WithDetail("%d bytes checked out, limit %d", size, max)
	}
	meta := fmt.Sprintf(`{"repository":%q,"commit":%q,"fetchedAt":%q}`+"\n", g.Repository(), sha, time.Now().UTC().Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(tmp, completeMarker), []byte(meta), 0o600); err != nil {
		return zerr.Wrap(err, zerr.CodeSourceFetch, stage, "cannot write source metadata")
	}
	if err := os.Rename(tmp, final); err != nil {
		if _, statErr := os.Stat(filepath.Join(final, completeMarker)); statErr == nil {
			return nil // a concurrent fetch won the race; its copy is equivalent
		}
		return zerr.Wrap(err, zerr.CodeSourceFetch, stage, "cannot finalise source cache")
	}
	ok = true
	return nil
}

func dirSize(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
