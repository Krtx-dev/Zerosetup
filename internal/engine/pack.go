package engine

import (
	"archive/tar"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/zerosetup/zerosetup/internal/source"
	ps "github.com/zerosetup/zerosetup/pkg/planschema"
)

// Packing limits.
const (
	maxPackBytes = 1 << 30
	maxPackFiles = 200000
)

// PackResult summarises what was packed.
type PackResult struct {
	Files   int      `json:"files"`
	Bytes   int64    `json:"bytes"`
	Skipped []string `json:"skipped,omitempty"`
}

// isLocalEnvFile matches real env files that may hold the user's secrets.
// Templates (.env.example etc.) are allowed.
func isLocalEnvFile(name string) bool {
	if name != ".env" && !strings.HasPrefix(name, ".env.") {
		return false
	}
	for _, t := range []string{".env.example", ".env.sample", ".env.template", ".env.dist"} {
		if name == t {
			return false
		}
	}
	return true
}

// writeWorkspaceTar streams a tar of srcDir into w, laid out as the
// workspace volume expects (src/, home/, tools/), owned by the container
// UID. It reads through os.Root so nothing outside srcDir can be included.
func writeWorkspaceTar(w io.Writer, srcDir string) (*PackResult, error) {
	root, err := os.OpenRoot(srcDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	tw := tar.NewWriter(w)
	res := &PackResult{}
	uid := ps.ContainerUID
	dir := func(name string) error {
		return tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: name + "/", Mode: 0o755, Uid: uid, Gid: uid})
	}
	for _, d := range []string{"src", "home", "tools"} {
		if err := dir(d); err != nil {
			return nil, err
		}
	}
	walkErr := fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			res.Skipped = append(res.Skipped, p+" (unreadable)")
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if p == "." {
			return nil
		}
		name := "src/" + p
		switch {
		case d.IsDir():
			if source.SkipDir(d.Name()) {
				return fs.SkipDir
			}
			return dir(name)
		case d.Type()&fs.ModeSymlink != 0:
			target, err := root.Readlink(p)
			if err != nil || path.IsAbs(target) {
				res.Skipped = append(res.Skipped, p+" (absolute or unreadable symlink)")
				return nil
			}
			resolved := path.Clean(path.Join(path.Dir(p), target))
			if resolved == ".." || strings.HasPrefix(resolved, "../") {
				res.Skipped = append(res.Skipped, p+" (symlink escapes repository)")
				return nil
			}
			return tw.WriteHeader(&tar.Header{Typeflag: tar.TypeSymlink, Name: name, Linkname: target, Uid: uid, Gid: uid, Mode: 0o777})
		case d.Type().IsRegular():
			if isLocalEnvFile(d.Name()) {
				res.Skipped = append(res.Skipped, p+" (local env file: may contain secrets)")
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			res.Files++
			res.Bytes += info.Size()
			if res.Bytes > maxPackBytes || res.Files > maxPackFiles {
				return fmt.Errorf("source exceeds packing limits (%d files, %d bytes)", res.Files, res.Bytes)
			}
			mode := int64(0o644)
			if info.Mode()&0o111 != 0 {
				mode = 0o755
			}
			if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Size: info.Size(), Mode: mode, Uid: uid, Gid: uid, ModTime: info.ModTime()}); err != nil {
				return err
			}
			f, err := root.Open(p)
			if err != nil {
				return err
			}
			_, err = io.CopyN(tw, f, info.Size())
			f.Close()
			return err
		default:
			res.Skipped = append(res.Skipped, p+" (special file)")
			return nil
		}
	})
	if walkErr != nil {
		return res, walkErr
	}
	return res, tw.Close()
}
