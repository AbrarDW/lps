//go:build darwin

package platform

import (
	"encoding/xml"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestLaunchAgent(t *testing.T) {
	home := t.TempDir() + "/A & B" // needs XML escaping
	a := LaunchAgent{Bin: MacBinary, Home: home}
	if on, err := a.Enabled(); on || err != nil {
		t.Fatalf("Enabled = %v, %v before Set", on, err)
	}
	if err := a.Set(true); err != nil {
		t.Fatal(err)
	}
	if on, _ := a.Enabled(); !on {
		t.Fatal("not enabled after Set(true)")
	}
	data, err := os.ReadFile(a.Path())
	if err != nil {
		t.Fatal(err)
	}
	if err := xml.Unmarshal(data, new(struct{})); err != nil {
		t.Fatalf("plist is not valid XML: %v", err)
	}
	for _, want := range []string{"<string>com.abrardw.lps</string>", "<string>/usr/local/bin/lpsd</string>", "A &amp; B/Library/Logs/LPS/lpsd.log"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("plist lacks %q", want)
		}
	}
	if err := a.Set(false); err != nil {
		t.Fatal(err)
	}
	if on, _ := a.Enabled(); on {
		t.Fatal("still enabled after Set(false)")
	}
	if err := a.Set(false); err != nil {
		t.Fatalf("second Set(false): %v", err)
	}
	if err := (LaunchAgent{Home: home}).Set(true); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("without the package: %v", err)
	}
}
