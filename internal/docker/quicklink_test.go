package docker

import (
	"strings"
	"testing"
)

func TestParseQuickLinkLog(t *testing.T) {
	ok := `2026-11-25T15:03:17.1Z 2026-11-25T15:03:17Z INF Thank you for trying Cloudflare Tunnel. ... (https://www.cloudflare.com/website-terms/) ...
2026-11-25T15:03:17Z INF Requesting new quick Tunnel on trycloudflare.com...
2026-11-25T15:03:19Z INF +--------------------------------------------------------------------------------------------+
2026-11-25T15:03:19Z INF |  Your quick Tunnel has been created! Visit it at (it may take some time to be reachable):  |
2026-11-25T15:03:19Z INF |  https://plain-river-words-tag.trycloudflare.com                                           |
2026-11-25T15:03:19Z INF +--------------------------------------------------------------------------------------------+
2026-11-25T15:03:20Z INF Registered tunnel connection connIndex=0
`
	url, errMsg := ParseQuickLinkLog(ok)
	if url != "https://plain-river-words-tag.trycloudflare.com" || errMsg != "" {
		t.Fatalf("url=%q err=%q", url, errMsg)
	}

	failed := `2026-11-25T15:03:17Z INF Requesting new quick Tunnel on trycloudflare.com...
failed to request quick Tunnel: Post "https://api.trycloudflare.com/tunnel": dial tcp: lookup api.trycloudflare.com on 8.8.8.8:53: no such host
`
	url, errMsg = ParseQuickLinkLog(failed)
	if url != "" || !strings.HasPrefix(errMsg, "failed to request quick Tunnel") {
		t.Fatalf("url=%q err=%q", url, errMsg)
	}
	if f := friendlyQuickErr(errMsg); !strings.Contains(f, "can't reach Cloudflare") {
		t.Fatalf("friendly = %q", f)
	}

	errLine := "2026-11-25T15:03:20.5Z 2026-11-25T15:03:20Z ERR Unable to reach the origin service error=\"dial tcp: lookup lps-app-x\"\n"
	if _, e := ParseQuickLinkLog(errLine); e != `Unable to reach the origin service error="dial tcp: lookup lps-app-x"` {
		t.Fatalf("err = %q", e)
	}
}

func TestParseConnectorLog(t *testing.T) {
	ok := `2026-11-25T15:03:17Z INF Starting tunnel tunnelID=abc
2026-11-25T15:03:18Z INF Registered tunnel connection connIndex=0 connection=x event=0 ip=198.41.200.33 location=fra08 protocol=quic
`
	if c, e := ParseConnectorLog(ok); !c || e != "" {
		t.Fatalf("connected=%v err=%q", c, e)
	}
	bad := `2026-11-25T15:03:17Z ERR Register tunnel error from server side error="Unauthorized: Invalid tunnel secret" connIndex=0
`
	if c, e := ParseConnectorLog(bad); c || !strings.Contains(e, "Invalid tunnel secret") {
		t.Fatalf("connected=%v err=%q", c, e)
	}
}
