package docker

import "testing"

func TestFindSocket(t *testing.T) {
	have := func(paths ...string) func(string) bool {
		return func(p string) bool {
			for _, q := range paths {
				if p == q {
					return true
				}
			}
			return false
		}
	}
	for _, tc := range []struct {
		name, goos string
		socks      []string
		want       string
	}{
		{"standard socket wins", "darwin", []string{"/var/run/docker.sock", "/Users/a/.colima/default/docker.sock"}, ""},
		{"docker desktop", "darwin", []string{"/Users/a/.docker/run/docker.sock", "/Users/a/.colima/default/docker.sock"}, "unix:///Users/a/.docker/run/docker.sock"},
		{"colima", "darwin", []string{"/Users/a/.colima/default/docker.sock"}, "unix:///Users/a/.colima/default/docker.sock"},
		{"nothing", "darwin", nil, ""},
		{"rootless linux", "linux", []string{"/run/user/1000/docker.sock"}, "unix:///run/user/1000/docker.sock"},
		{"windows keeps the pipe", "windows", []string{"/run/user/1000/docker.sock"}, ""},
	} {
		home := "/Users/a"
		if tc.goos == "linux" {
			home = "/home/a"
		}
		if got := findSocket(tc.goos, home, "/run/user/1000", have(tc.socks...)); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
