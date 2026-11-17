package source

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zerosetup/zerosetup/internal/zerr"
)

func TestParseGitHubURL(t *testing.T) {
	ok := map[string]GitHubRef{
		"https://github.com/vitejs/vite":                    {Owner: "vitejs", Repo: "vite"},
		"https://github.com/vitejs/vite.git":                {Owner: "vitejs", Repo: "vite"},
		"github.com/vercel/next.js":                         {Owner: "vercel", Repo: "next.js"},
		"https://www.github.com/a/b/":                       {Owner: "a", Repo: "b"},
		"https://github.com/a/b/tree/main":                  {Owner: "a", Repo: "b", Ref: "main"},
		"https://github.com/a/b/tree/v1.2.3/examples/basic": {Owner: "a", Repo: "b", Ref: "v1.2.3", Subdir: "examples/basic"},
		"https://github.com/a/b?tab=readme":                 {Owner: "a", Repo: "b"},
	}
	for in, want := range ok {
		got, err := ParseGitHubURL(in)
		if err != nil {
			t.Errorf("%s: unexpected error %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("%s: got %+v want %+v", in, got, want)
		}
	}
	bad := map[string]zerr.Code{
		"http://github.com/a/b":                    zerr.CodeSourceUnsupported,
		"git@github.com:a/b.git":                   zerr.CodeSourceUnsupported,
		"ssh://git@github.com/a/b":                 zerr.CodeSourceUnsupported,
		"https://gitlab.com/a/b":                   zerr.CodeSourceUnsupported,
		"https://github.com.evil.com/a/b":          zerr.CodeSourceUnsupported,
		"https://github.com:8443/a/b":              zerr.CodeSourceUnsupported,
		"https://user:token@github.com/a/b":        zerr.CodeSourceInvalid,
		"https://github.com/a":                     zerr.CodeSourceInvalid,
		"https://github.com/-bad/b":                zerr.CodeSourceInvalid,
		"https://github.com/a/..":                  zerr.CodeSourceInvalid,
		"https://github.com/a/b/blob/main/x":       zerr.CodeSourceInvalid,
		"https://github.com/a/b/tree/main/../../x": zerr.CodeSourceInvalid,
		"https://github.com/a/b/tree/$(id)":        zerr.CodeSourceInvalid,
	}
	for in, code := range bad {
		_, err := ParseGitHubURL(in)
		if !zerr.HasCode(err, code) {
			t.Errorf("%s: want %s, got %v", in, code, err)
		}
	}
}

func TestValidateRef(t *testing.T) {
	for _, r := range []string{"main", "v1.0.0", "feature/x", "0123456789abcdef0123456789abcdef01234567"} {
		if err := ValidateRef(r); err != nil {
			t.Errorf("%s rejected: %v", r, err)
		}
	}
	for _, r := range []string{"--upload-pack=x", "a..b", "-x", "x.lock", "a b", "$(id)"} {
		if err := ValidateRef(r); err == nil {
			t.Errorf("%s accepted", r)
		}
	}
}

func TestRepoFSRejectsSymlinkEscape(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	os.WriteFile(secret, []byte("top secret"), 0o600)
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "package.json"), []byte(`{}`), 0o600)
	if err := os.Symlink(secret, filepath.Join(repo, "leak.txt")); err != nil {
		t.Skip("symlinks unsupported")
	}
	os.Symlink(outside, filepath.Join(repo, "leakdir"))
	fs, err := OpenRepoFS(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	if _, err := fs.ReadFile("leak.txt", 0); err == nil {
		t.Fatal("read through an escaping symlink succeeded")
	}
	if _, err := fs.ReadFile("leakdir/secret.txt", 0); err == nil {
		t.Fatal("read through an escaping directory symlink succeeded")
	}
	if _, err := fs.ReadFile("../"+filepath.Base(outside)+"/secret.txt", 0); err == nil {
		t.Fatal("path traversal succeeded")
	}
	if fs.Exists("leak.txt") {
		t.Fatal("Exists followed an escaping symlink")
	}
	if b, err := fs.ReadFile("package.json", 0); err != nil || string(b) != "{}" {
		t.Fatalf("normal read failed: %v", err)
	}
}

func TestRepoFSSizeLimit(t *testing.T) {
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "big.json"), make([]byte, 2048), 0o600)
	fs, _ := OpenRepoFS(repo)
	defer fs.Close()
	if _, err := fs.ReadFile("big.json", 1024); err == nil {
		t.Fatal("size limit not enforced")
	}
}

func TestResolveLocal(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".git", "refs", "heads"), 0o700)
	sha := "0123456789abcdef0123456789abcdef01234567"
	os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600)
	os.WriteFile(filepath.Join(dir, ".git", "refs", "heads", "main"), []byte(sha+"\n"), 0o600)
	r := &Resolver{CacheDir: t.TempDir()}
	res, err := r.Resolve(context.Background(), dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "local" || res.Commit != sha {
		t.Fatalf("got %+v", res)
	}
	if _, err := r.Resolve(context.Background(), filepath.Join(dir, "missing"), ""); !zerr.HasCode(err, zerr.CodeSourceNotFound) {
		t.Fatalf("missing dir: %v", err)
	}
	if _, err := r.Resolve(context.Background(), dir, "main"); !zerr.HasCode(err, zerr.CodeSourceUnsupported) {
		t.Fatalf("ref on local dir should be unsupported: %v", err)
	}
}
