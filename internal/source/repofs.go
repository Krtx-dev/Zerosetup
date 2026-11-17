package source

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
)

// DefaultMaxFileSize bounds how much of any single file inspection will read.
const DefaultMaxFileSize = 1 << 20 // 1 MiB

// ErrTooLarge is returned when a file exceeds the read limit.
var ErrTooLarge = errors.New("file exceeds inspection size limit")

// skipDirs are never descended into during inspection or packaging.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, ".venv": true, "venv": true,
	"__pycache__": true, ".next": true, ".turbo": true, ".cache": true,
	".pnpm-store": true, ".mypy_cache": true, ".pytest_cache": true,
}

// SkipDir reports whether a directory (by base name) is excluded from
// inspection and from the build context.
func SkipDir(name string) bool { return skipDirs[name] }

// RepoFS is a read-only, root-confined view of a repository directory.
//
// All access goes through os.Root, which refuses any path — including via
// symlinks — that resolves outside the root. It never executes anything.
type RepoFS struct {
	dir   string
	root  *os.Root
	files []string // lazily populated, sorted, slash-separated
	trunc bool
}

// Walk limits keep inspection bounded on hostile or huge repositories.
const (
	maxWalkEntries = 20000
	maxWalkDepth   = 6
)

// OpenRepoFS opens dir for inspection.
func OpenRepoFS(dir string) (*RepoFS, error) {
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &RepoFS{dir: dir, root: r}, nil
}

// Dir returns the host directory.
func (r *RepoFS) Dir() string { return r.dir }

// Close releases the root handle.
func (r *RepoFS) Close() error { return r.root.Close() }

func clean(rel string) (string, error) {
	if rel == "" || strings.HasPrefix(rel, "/") || strings.Contains(rel, "\\") {
		return "", fmt.Errorf("invalid repository path %q", rel)
	}
	c := path.Clean(rel)
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("path %q escapes repository", rel)
	}
	return c, nil
}

// Exists reports whether rel exists as a regular file inside the root.
func (r *RepoFS) Exists(rel string) bool {
	c, err := clean(rel)
	if err != nil {
		return false
	}
	st, err := r.root.Stat(c)
	return err == nil && st.Mode().IsRegular()
}

// IsDir reports whether rel is a directory inside the root.
func (r *RepoFS) IsDir(rel string) bool {
	c, err := clean(rel)
	if err != nil {
		return false
	}
	st, err := r.root.Stat(c)
	return err == nil && st.IsDir()
}

// ReadFile reads a regular file, refusing anything above max bytes.
func (r *RepoFS) ReadFile(rel string, max int64) ([]byte, error) {
	if max <= 0 {
		max = DefaultMaxFileSize
	}
	c, err := clean(rel)
	if err != nil {
		return nil, err
	}
	f, err := r.root.Open(c)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", rel)
	}
	if st.Size() > max {
		return nil, fmt.Errorf("%s: %w (%d > %d bytes)", rel, ErrTooLarge, st.Size(), max)
	}
	return io.ReadAll(io.LimitReader(f, max+1))
}

// Files returns repository-relative file paths up to a bounded depth,
// skipping dependency/VCS directories. Symlinks are listed but not followed.
func (r *RepoFS) Files() []string {
	if r.files != nil {
		return r.files
	}
	out := []string{}
	_ = fs.WalkDir(r.root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		if p == "." {
			return nil
		}
		if d.IsDir() {
			if SkipDir(d.Name()) || strings.Count(p, "/") >= maxWalkDepth {
				return fs.SkipDir
			}
			return nil
		}
		if len(out) >= maxWalkEntries {
			r.trunc = true
			return fs.SkipAll
		}
		out = append(out, p)
		return nil
	})
	sort.Strings(out)
	r.files = out
	return out
}

// Truncated reports whether Files hit the entry limit.
func (r *RepoFS) Truncated() bool { r.Files(); return r.trunc }

// Glob returns files whose base name matches one of names, at depth <= maxDepth.
func (r *RepoFS) FindByName(maxDepth int, names ...string) []string {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	var out []string
	for _, f := range r.Files() {
		if strings.Count(f, "/") > maxDepth {
			continue
		}
		if want[path.Base(f)] {
			out = append(out, f)
		}
	}
	return out
}
