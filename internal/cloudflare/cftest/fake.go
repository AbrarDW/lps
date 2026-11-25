// Package cftest is an in-memory fake of the parts of the Cloudflare API LPS
// uses, for tests.
package cftest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/abrardw/lps/internal/cloudflare"
)

// Fake is a fake Cloudflare API. Serve it with httptest.NewServer(f).
type Fake struct {
	mu      sync.Mutex
	Token   string // the only accepted API token
	ZoneSet []cloudflare.Zone
	Tunnels map[string]string               // id -> name
	Configs map[string][]cloudflare.Ingress // tunnel id -> routes (without the catch-all)
	Records map[string]cloudflare.Record    // id -> record
	zoneOf  map[string]string               // record id -> zone id
	n       int
	// DeleteTunnelFails makes tunnel deletion fail this many times.
	DeleteTunnelFails int
	// PutConfigErr makes route updates fail.
	PutConfigErr string
}

// New returns a fake that accepts token and has zones example.com and
// example.org in account "acc1".
func New(token string) *Fake {
	z := func(id, name string) cloudflare.Zone {
		var zn cloudflare.Zone
		zn.ID, zn.Name, zn.Status = id, name, "active"
		zn.Account.ID, zn.Account.Name = "acc1", "Home"
		return zn
	}
	return &Fake{
		Token:   token,
		ZoneSet: []cloudflare.Zone{z("z1", "example.com"), z("z2", "example.org")},
		Tunnels: map[string]string{}, Configs: map[string][]cloudflare.Ingress{},
		Records: map[string]cloudflare.Record{}, zoneOf: map[string]string{},
	}
}

// AddRecord adds a record someone made by hand.
func (f *Fake) AddRecord(zone string, r cloudflare.Record) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	r.ID = fmt.Sprintf("rec%d", f.n)
	f.Records[r.ID], f.zoneOf[r.ID] = r, zone
	return r.ID
}

// Snapshot returns copies of the tunnels, routes and records.
func (f *Fake) Snapshot() (tunnels map[string]string, configs map[string][]cloudflare.Ingress, records map[string]cloudflare.Record) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tunnels, configs, records = map[string]string{}, map[string][]cloudflare.Ingress{}, map[string]cloudflare.Record{}
	for k, v := range f.Tunnels {
		tunnels[k] = v
	}
	for k, v := range f.Configs {
		configs[k] = append([]cloudflare.Ingress{}, v...)
	}
	for k, v := range f.Records {
		records[k] = v
	}
	return
}

func reply(w http.ResponseWriter, status int, result any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := map[string]any{"success": status < 400, "errors": []any{}, "result": result}
	if status >= 400 {
		body["errors"] = []any{map[string]any{"code": 1000 + status, "message": fmt.Sprint(result)}}
		body["result"] = nil
	}
	_ = json.NewEncoder(w).Encode(body)
}

func (f *Fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+f.Token {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":6003,"message":"Invalid request headers"}],"result":null}`))
		return
	}
	p := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/client/v4"), "/"), "/")
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	switch {
	case r.Method == "GET" && len(p) == 1 && p[0] == "zones":
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": f.ZoneSet,
			"result_info": map[string]int{"page": 1, "total_pages": 1}})
	case len(p) >= 3 && p[0] == "accounts" && p[2] == "cfd_tunnel":
		f.tunnel(w, r, p, body)
	case len(p) >= 3 && p[0] == "zones" && p[2] == "dns_records":
		f.dns(w, r, p, body)
	default:
		reply(w, 404, "no route "+r.Method+" "+r.URL.Path)
	}
}

func (f *Fake) tunnel(w http.ResponseWriter, r *http.Request, p []string, body map[string]any) {
	switch {
	case r.Method == "POST" && len(p) == 3:
		if body["config_src"] != "cloudflare" || body["name"] == "" {
			reply(w, 400, "bad tunnel request")
			return
		}
		f.n++
		id := fmt.Sprintf("tun-%d", f.n)
		f.Tunnels[id] = fmt.Sprint(body["name"])
		reply(w, 200, map[string]any{"id": id, "name": body["name"]})
	case len(p) >= 4 && f.Tunnels[p[3]] == "":
		reply(w, 404, "tunnel not found")
	case r.Method == "GET" && len(p) == 5 && p[4] == "token":
		reply(w, 200, "token-for-"+p[3])
	case r.Method == "PUT" && len(p) == 5 && p[4] == "configurations":
		if f.PutConfigErr != "" {
			reply(w, 400, f.PutConfigErr)
			return
		}
		cfg, _ := body["config"].(map[string]any)
		raw, _ := json.Marshal(cfg["ingress"])
		var ing []cloudflare.Ingress
		_ = json.Unmarshal(raw, &ing)
		if len(ing) == 0 || ing[len(ing)-1].Hostname != "" {
			reply(w, 400, "the last ingress rule must be a catch-all")
			return
		}
		f.Configs[p[3]] = ing[:len(ing)-1]
		reply(w, 200, map[string]any{})
	case r.Method == "DELETE" && len(p) == 4:
		if f.DeleteTunnelFails > 0 {
			f.DeleteTunnelFails--
			reply(w, 400, "Cannot delete tunnel because it has active connections")
			return
		}
		delete(f.Tunnels, p[3])
		delete(f.Configs, p[3])
		reply(w, 200, map[string]any{"id": p[3]})
	default:
		reply(w, 404, "no route")
	}
}

func (f *Fake) dns(w http.ResponseWriter, r *http.Request, p []string, body map[string]any) {
	zone := p[1]
	switch {
	case r.Method == "GET" && len(p) == 3:
		name := r.URL.Query().Get("name")
		out := []cloudflare.Record{}
		for id, rec := range f.Records {
			if f.zoneOf[id] == zone && rec.Name == name {
				out = append(out, rec)
			}
		}
		reply(w, 200, out)
	case r.Method == "POST" && len(p) == 3:
		name := fmt.Sprint(body["name"])
		for id, rec := range f.Records {
			if f.zoneOf[id] == zone && rec.Name == name {
				reply(w, 400, "An A, AAAA, or CNAME record with that host already exists.")
				return
			}
		}
		f.n++
		rec := cloudflare.Record{ID: fmt.Sprintf("rec%d", f.n), Type: fmt.Sprint(body["type"]), Name: name,
			Content: fmt.Sprint(body["content"]), Proxied: body["proxied"] == true, Comment: fmt.Sprint(body["comment"])}
		f.Records[rec.ID], f.zoneOf[rec.ID] = rec, zone
		reply(w, 200, rec)
	case r.Method == "DELETE" && len(p) == 4:
		if _, ok := f.Records[p[3]]; !ok {
			reply(w, 404, "record not found")
			return
		}
		delete(f.Records, p[3])
		reply(w, 200, map[string]any{"id": p[3]})
	default:
		reply(w, 404, "no route")
	}
}
