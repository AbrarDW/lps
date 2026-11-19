package builder

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func names(t *testing.T, r io.Reader) []string {
	t.Helper()
	var out []string
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag != tar.TypeDir {
			out = append(out, h.Name)
		}
	}
	sort.Strings(out)
	return out
}

func TestValidate(t *testing.T) {
	dir := t.TempDir()
	if err := Validate(dir); err != ErrNoDockerfile {
		t.Fatalf("want ErrNoDockerfile, got %v", err)
	}
	write(t, dir, "Dockerfile", "FROM scratch")
	if err := Validate(dir); err != nil {
		t.Fatal(err)
	}
	if err := Validate(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing dir should fail")
	}
}

func TestTarDirHonoursDockerignore(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Dockerfile", "FROM scratch")
	write(t, dir, ".dockerignore", "# comment\n*.log\n/secrets\n")
	write(t, dir, "src/app.js", "x")
	write(t, dir, "debug.log", "x")
	write(t, dir, "secrets/key", "x")
	write(t, dir, "node_modules/dep/index.js", "x")
	write(t, dir, ".git/HEAD", "x")

	var buf bytes.Buffer
	write(t, dir, ".venv/lib/x.py", "x")
	write(t, dir, "Dockerfile.lps", "stale")
	if err := TarDir(dir, &buf, map[string][]byte{GeneratedDockerfile: []byte("FROM x")}); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(names(t, &buf), ",")
	want := ".dockerignore,Dockerfile,Dockerfile.lps,src/app.js"
	if got != want {
		t.Fatalf("archive = %s, want %s", got, want)
	}
}

func TestTarFS(t *testing.T) {
	fsys := fstest.MapFS{
		"img/Dockerfile":    {Data: []byte("FROM x")},
		"img/entrypoint.sh": {Data: []byte("#!/bin/sh")},
	}
	var buf bytes.Buffer
	if err := TarFS(fsys, "img", &buf); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names(t, &buf), ","); got != "Dockerfile,entrypoint.sh" {
		t.Fatalf("archive = %s", got)
	}
}
