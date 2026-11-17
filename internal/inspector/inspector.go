// Package inspector turns a resolved source into a detection report plus
// content hashes. It never executes repository code.
package inspector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"time"

	"github.com/zerosetup/zerosetup/internal/detect"
	"github.com/zerosetup/zerosetup/internal/detect/node"
	"github.com/zerosetup/zerosetup/internal/detect/python"
	"github.com/zerosetup/zerosetup/internal/source"
	"github.com/zerosetup/zerosetup/internal/zerr"
	ps "github.com/zerosetup/zerosetup/pkg/planschema"
)

// ManifestFiles are hashed into the content hash and reported as lockfile
// hashes in passports. Order is irrelevant; the list is sorted on use.
var ManifestFiles = []string{
	"package.json", "package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml", "yarn.lock", "bun.lockb", "bun.lock",
	".nvmrc", ".node-version", ".yarnrc.yml",
	"pyproject.toml", "requirements.txt", "uv.lock", "poetry.lock", "Pipfile", "Pipfile.lock", ".python-version",
}

// lockfiles is the subset of ManifestFiles that pin dependency versions.
var lockfiles = map[string]bool{
	"package-lock.json": true, "npm-shrinkwrap.json": true, "pnpm-lock.yaml": true, "yarn.lock": true,
	"bun.lockb": true, "bun.lock": true, "uv.lock": true, "poetry.lock": true, "Pipfile.lock": true, "requirements.txt": true,
}

// FileHash is a manifest's SHA-256.
type FileHash struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
	Lock   bool   `json:"lockfile"`
}

// Inspection is the result of inspecting a source.
type Inspection struct {
	Source    ps.Source      `json:"source"`
	Dir       string         `json:"-"`
	Report    *detect.Report `json:"-"`
	Manifests []FileHash     `json:"manifests"`
	Duration  time.Duration  `json:"durationNs"`
}

// DefaultRegistry returns the built-in detectors.
func DefaultRegistry() *detect.Registry {
	return detect.NewRegistry(node.New(), python.New())
}

// Inspect analyses the resolved source.
func Inspect(ctx context.Context, res *source.Resolved, reg *detect.Registry, hints detect.Hints) (*Inspection, error) {
	start := time.Now()
	repo, err := source.OpenRepoFS(res.Dir)
	if err != nil {
		return nil, zerr.Wrap(err, zerr.CodeInspect, "inspect", "cannot open source directory")
	}
	defer repo.Close()
	report, err := reg.Run(ctx, repo, hints)
	if err != nil {
		return nil, zerr.Wrap(err, zerr.CodeInspect, "inspect", "detection failed")
	}
	manifests, content := hashManifests(repo)
	in := &Inspection{
		Source: ps.Source{Kind: res.Kind, Repository: res.Repository, URL: res.URL, Ref: res.Ref,
			Commit: res.Commit, Subdir: res.Subdir, ContentHash: content},
		Dir: res.Dir, Report: report, Manifests: manifests,
	}
	in.Duration = time.Since(start)
	return in, nil
}

func hashManifests(repo *source.RepoFS) ([]FileHash, string) {
	files := append([]string{}, ManifestFiles...)
	sort.Strings(files)
	all := sha256.New()
	var out []FileHash
	for _, f := range files {
		b, err := repo.ReadFile(f, 64<<20)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(b)
		h := hex.EncodeToString(sum[:])
		out = append(out, FileHash{File: f, SHA256: h, Lock: lockfiles[f]})
		all.Write([]byte(f + "\x00" + h + "\n"))
	}
	return out, "sha256:" + hex.EncodeToString(all.Sum(nil))
}
