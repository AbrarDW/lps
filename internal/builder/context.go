// Package builder turns a project directory into a Docker build context.
package builder

import (
	"archive/tar"
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/moby/patternmatcher"
)

// ErrNoDockerfile is returned when the project has no Dockerfile and LPS has
// no generated one for it.
var ErrNoDockerfile = errors.New("no Dockerfile found in project directory")

// GeneratedDockerfile is the name a Dockerfile stored in LPS gets inside the
// build context, so it never clashes with a Dockerfile in the project.
const GeneratedDockerfile = "Dockerfile.lps"

// alwaysIgnore keeps huge, generated or sensitive folders out of the build context.
var alwaysIgnore = []string{".git", "node_modules", ".venv", "venv", "__pycache__", ".next", GeneratedDockerfile}

// ValidateDir checks that dir exists and is a directory.
func ValidateDir(dir string) error {
	st, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("source path: %w", err)
	}
	if !st.IsDir() {
		return fmt.Errorf("source path %q is not a directory", dir)
	}
	return nil
}

// HasDockerfile reports whether dir contains a Dockerfile.
func HasDockerfile(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, "Dockerfile"))
	return err == nil && st.Mode().IsRegular()
}

// Validate checks that dir exists and contains a Dockerfile.
func Validate(dir string) error {
	if err := ValidateDir(dir); err != nil {
		return err
	}
	if !HasDockerfile(dir) {
		return ErrNoDockerfile
	}
	return nil
}

// TarDir writes dir as a tar stream to w, honouring .dockerignore. Files in
// extra (name -> content) are added to the archive root, e.g. a generated
// Dockerfile.lps. Paths inside the archive always use forward slashes (works
// from Windows hosts).
func TarDir(dir string, w io.Writer, extra map[string][]byte) error {
	patterns, err := readDockerignore(dir)
	if err != nil {
		return err
	}
	pm, err := patternmatcher.New(append(patterns, alwaysIgnore...))
	if err != nil {
		return fmt.Errorf(".dockerignore: %w", err)
	}
	tw := tar.NewWriter(w)
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil || rel == "." {
			return err
		}
		rel = filepath.ToSlash(rel)
		// Dockerfile and .dockerignore must always reach the daemon.
		if rel != "Dockerfile" && rel != ".dockerignore" {
			skip, err := pm.MatchesOrParentMatches(rel)
			if err != nil {
				return err
			}
			if skip {
				if d.IsDir() && !pm.Exclusions() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		link := ""
		if info.Mode()&fs.ModeSymlink != 0 {
			if link, err = os.Readlink(path); err != nil {
				return err
			}
		} else if !info.Mode().IsRegular() && !info.IsDir() {
			return nil // sockets, devices, etc.
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = rel
		if info.IsDir() {
			hdr.Name += "/"
		}
		// Normalise ownership; Windows has no uid/gid.
		hdr.Uid, hdr.Gid, hdr.Uname, hdr.Gname = 0, 0, "", ""
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			_, err = io.Copy(tw, f)
			f.Close()
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for name, data := range extra {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
	}
	return tw.Close()
}

// TarFS writes an fs.FS subtree (e.g. embedded image contexts) as a tar stream.
func TarFS(fsys fs.FS, root string, w io.Writer) error {
	tw := tar.NewWriter(w)
	err := fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(strings.TrimPrefix(path, root), "/")
		mode := int64(0o644)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err = tw.Write(data)
		return err
	})
	if err != nil {
		return err
	}
	return tw.Close()
}

func readDockerignore(dir string) ([]string, error) {
	f, err := os.Open(filepath.Join(dir, ".dockerignore"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		neg := strings.HasPrefix(line, "!")
		line = strings.TrimPrefix(line, "!")
		line = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(line)), "/")
		if neg {
			line = "!" + line
		}
		out = append(out, line)
	}
	return out, sc.Err()
}
