package engine

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestRedactor(t *testing.T) {
	var buf bytes.Buffer
	r := NewRedactor(&buf, []string{"s3cr3t-value", "abc"}) // "abc" too short to redact
	r.Write([]byte("token=s3cr"))
	r.Write([]byte("3t-value end\nabc ok\npartial s3cr3t-val"))
	r.Write([]byte("ue"))
	r.Flush()
	out := buf.String()
	if strings.Contains(out, "s3cr3t-value") {
		t.Fatalf("secret leaked: %q", out)
	}
	if strings.Count(out, redacted) != 2 || !strings.Contains(out, "abc ok") {
		t.Fatalf("unexpected output: %q", out)
	}
	if RedactString("x s3cr3t-value y", []string{"s3cr3t-value"}) != "x "+redacted+" y" {
		t.Fatal("RedactString failed")
	}
}

func TestWorkspaceTar(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "id_rsa"), []byte("PRIVATE"), 0o600)
	os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=1"), 0o600)
	os.WriteFile(filepath.Join(dir, ".env.local"), []byte("SECRET=1"), 0o600)
	os.WriteFile(filepath.Join(dir, ".env.example"), []byte("SECRET="), 0o600)
	os.MkdirAll(filepath.Join(dir, "node_modules", "x"), 0o755)
	os.WriteFile(filepath.Join(dir, "node_modules", "x", "i.js"), []byte(""), 0o644)
	os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh"), 0o755)
	os.Symlink(filepath.Join(outside, "id_rsa"), filepath.Join(dir, "abs-link"))
	os.Symlink("../../"+filepath.Base(outside)+"/id_rsa", filepath.Join(dir, "rel-escape"))
	os.Symlink("package.json", filepath.Join(dir, "ok-link"))

	var buf bytes.Buffer
	res, err := writeWorkspaceTar(&buf, dir)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(&buf)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Uid != 1000 || h.Gid != 1000 {
			t.Errorf("%s owned by %d:%d", h.Name, h.Uid, h.Gid)
		}
		if h.Name == "src/run.sh" && h.Mode != 0o755 {
			t.Errorf("executable bit lost")
		}
		if h.Typeflag == tar.TypeReg {
			b, _ := io.ReadAll(tr)
			if string(b) == "PRIVATE" {
				t.Fatal("content from outside the repository was packed")
			}
		}
		names = append(names, h.Name)
	}
	sort.Strings(names)
	got := strings.Join(names, ",")
	for _, must := range []string{"src/package.json", "src/.env.example", "src/ok-link", "src/run.sh", "home/", "tools/"} {
		if !strings.Contains(got, must) {
			t.Errorf("missing %s in %s", must, got)
		}
	}
	for _, mustNot := range []string{"src/.env,", "src/.env.local", "node_modules", ".git/", "abs-link", "rel-escape"} {
		if strings.Contains(got+",", mustNot) {
			t.Errorf("%s must not be packed: %s", mustNot, got)
		}
	}
	if len(res.Skipped) < 4 {
		t.Errorf("skips must be reported: %v", res.Skipped)
	}
}
