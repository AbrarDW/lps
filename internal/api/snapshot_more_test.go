package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/abrardw/lps/internal/docker"
	"github.com/abrardw/lps/internal/store"
)

// The fake's archives are just text naming what they hold.

func (f *fakeEngine) SaveImage(_ context.Context, ref string, w io.Writer) error {
	f.mu.Lock()
	ok := f.images[ref]
	f.mu.Unlock()
	if !ok {
		return fmt.Errorf("no such image %s", ref)
	}
	_, err := io.WriteString(w, "IMAGE "+ref)
	return err
}

func (f *fakeEngine) LoadImage(_ context.Context, r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	ref, ok := strings.CutPrefix(string(b), "IMAGE ")
	if !ok {
		return fmt.Errorf("load image: not an image archive")
	}
	f.mu.Lock()
	if f.images == nil {
		f.images = map[string]bool{}
	}
	f.images[ref] = true
	f.mu.Unlock()
	return nil
}

func (f *fakeEngine) ExportVolume(_ context.Context, vol string, w io.Writer) error {
	f.mu.Lock()
	ok := f.volumes[vol]
	f.mu.Unlock()
	if !ok {
		return fmt.Errorf("no such volume %s", vol)
	}
	_, err := io.WriteString(w, "VOLUME "+vol)
	return err
}

func (f *fakeEngine) ImportVolume(_ context.Context, vol string, r io.Reader, _ map[string]string) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(string(b), "VOLUME ") {
		return fmt.Errorf("not a volume archive")
	}
	f.mu.Lock()
	f.volumes[vol] = true
	f.mu.Unlock()
	return nil
}

func TestScheduleSlots(t *testing.T) {
	defer func(l *time.Location) { scheduleLoc = l }(scheduleLoc)
	scheduleLoc = time.UTC
	at := func(s string) time.Time {
		tm, err := time.Parse("2006-01-02 15:04 Mon", s)
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	daily := &store.SnapshotSchedule{Frequency: store.ScheduleDaily, Hour: 3}
	// 2026-11-25 is a Wednesday.
	if got := lastSlot(daily, at("2026-11-25 10:00 Wed")); !got.Equal(at("2026-11-25 03:00 Wed")) {
		t.Fatalf("daily last = %v", got)
	}
	if got := lastSlot(daily, at("2026-11-25 02:59 Wed")); !got.Equal(at("2026-11-24 03:00 Tue")) {
		t.Fatalf("daily last before the hour = %v", got)
	}
	if got := nextSlot(daily, at("2026-11-25 10:00 Wed")); !got.Equal(at("2026-11-26 03:00 Thu")) {
		t.Fatalf("daily next = %v", got)
	}
	weekly := &store.SnapshotSchedule{Frequency: store.ScheduleWeekly, Hour: 4, Weekday: int(time.Monday)}
	if got := lastSlot(weekly, at("2026-11-25 10:00 Wed")); !got.Equal(at("2026-11-23 04:00 Mon")) {
		t.Fatalf("weekly last = %v", got)
	}
	if got := lastSlot(weekly, at("2026-11-23 03:00 Mon")); !got.Equal(at("2026-11-16 04:00 Mon")) {
		t.Fatalf("weekly same day before the hour = %v", got)
	}
	if got := nextSlot(weekly, at("2026-11-25 10:00 Wed")); !got.Equal(at("2026-11-30 04:00 Mon")) {
		t.Fatalf("weekly next = %v", got)
	}

	// A new schedule waits for its first slot; one missed while the PC was
	// off is taken once.
	daily.CreatedAt = at("2026-11-25 10:00 Wed")
	if _, due := scheduleDue(daily, at("2026-11-25 23:00 Wed")); due {
		t.Fatal("due before its first slot")
	}
	slot, due := scheduleDue(daily, at("2026-11-27 09:00 Fri"))
	if !due || !slot.Equal(at("2026-11-27 03:00 Fri")) {
		t.Fatalf("catch-up: %v %v", slot, due)
	}
	daily.LastRun = &slot
	if _, due := scheduleDue(daily, at("2026-11-27 12:00 Fri")); due {
		t.Fatal("due twice for one slot")
	}
}

func TestScheduledSnapshots(t *testing.T) {
	h := setup(t)
	h.login()
	defer func(f func() time.Time) { scheduleNow = f }(scheduleNow)
	now := time.Now()
	scheduleNow = func() time.Time { return now }

	v := h.newRunningVPS()
	base := fmt.Sprintf("/api/vps/%d", v.ID)
	for _, bad := range []map[string]any{
		{"frequency": "hourly", "hour": 3, "keep": 3},
		{"frequency": "daily", "hour": 24, "keep": 3},
		{"frequency": "daily", "hour": 3, "keep": 0},
		{"frequency": "weekly", "hour": 3, "weekday": 7, "keep": 3},
	} {
		if code := h.do("PUT", base+"/snapshot-schedule", bad, nil); code != 400 {
			t.Fatalf("%v: %d", bad, code)
		}
	}
	var sv scheduleView
	if code := h.do("PUT", base+"/snapshot-schedule", map[string]any{"frequency": "daily", "hour": 3, "keep": 2}, &sv); code != 200 {
		t.Fatalf("set: %d", code)
	}
	if sv.Schedule == nil || sv.Next == nil || sv.Next.Hour() != 3 && sv.Next.In(scheduleLoc).Hour() != 3 {
		t.Fatalf("view = %+v", sv)
	}
	// A manual snapshot is never deleted by the schedule.
	h.do("POST", base+"/snapshots", map[string]string{"label": "mine"}, nil)
	h.waitSnapshot(v.ID, "mine")

	h.srv.runSchedules(t.Context()) // not due yet
	autos := func() []store.Snapshot {
		var list []store.Snapshot
		h.do("GET", base+"/snapshots", nil, &list)
		var out []store.Snapshot
		for _, s := range list {
			if s.Auto {
				out = append(out, s)
			}
		}
		return out
	}
	if n := len(autos()); n != 0 {
		t.Fatalf("%d automatic snapshots before the first slot", n)
	}
	for day := 1; day <= 4; day++ {
		now = now.Add(24 * time.Hour)
		h.srv.runSchedules(t.Context())
		h.srv.runSchedules(t.Context()) // once per slot
	}
	got := autos()
	if len(got) != 2 {
		t.Fatalf("kept %d automatic snapshots, want 2: %+v", len(got), got)
	}
	if !strings.HasPrefix(got[0].Label, "Daily snapshot ") || got[0].Status != store.SnapshotReady {
		t.Fatalf("newest = %+v", got[0])
	}
	var list []store.Snapshot
	h.do("GET", base+"/snapshots", nil, &list)
	if len(list) != 3 {
		t.Fatalf("snapshots = %d, want 2 automatic + 1 manual", len(list))
	}
	// Keeping fewer applies at once; turning it off stops it.
	h.do("PUT", base+"/snapshot-schedule", map[string]any{"frequency": "weekly", "hour": 3, "weekday": 1, "keep": 1}, nil)
	if n := len(autos()); n != 1 {
		t.Fatalf("after keep=1: %d", n)
	}
	h.do("PUT", base+"/snapshot-schedule", map[string]any{"frequency": "off"}, &sv)
	if sv.Schedule != nil {
		t.Fatal("schedule still on")
	}
	now = now.Add(8 * 24 * time.Hour)
	h.srv.runSchedules(t.Context())
	if n := len(autos()); n != 1 {
		t.Fatalf("ran while off: %d", n)
	}
}

func (h *harness) raw(method, path string, body io.Reader, out any) int {
	h.t.Helper()
	req, _ := http.NewRequest(method, h.ts.URL+path, body)
	req.Header.Set("X-LPS-Request", "1")
	res, err := h.c.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	if b, ok := out.(*[]byte); ok {
		*b, _ = io.ReadAll(res.Body)
	} else if out != nil {
		json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

func TestSnapshotExportImport(t *testing.T) {
	h := setup(t)
	h.login()
	v := h.newRunningVPS()
	base := fmt.Sprintf("/api/vps/%d", v.ID)
	h.do("POST", base+"/snapshots", map[string]string{"label": "golden"}, nil)
	sn := h.waitSnapshot(v.ID, "golden")
	snPath := fmt.Sprintf("%s/snapshots/%d", base, sn.ID)

	if code := h.raw("GET", snPath+"/export", nil, nil); code != 404 {
		t.Fatalf("download before export: %d", code)
	}
	var ev exportView
	if code := h.do("POST", snPath+"/export", nil, &ev); code != 202 {
		t.Fatalf("export: %d", code)
	}
	waitFor(t, func() bool {
		var list []snapshotView
		h.do("GET", base+"/snapshots", nil, &list)
		for _, s := range list {
			if s.ID == sn.ID {
				ev = s.Export
			}
		}
		return ev.State == "ready"
	})
	if !strings.HasPrefix(ev.File, "box-snapshot-") || !strings.HasSuffix(ev.File, ".lpsnap") || ev.Bytes == 0 {
		t.Fatalf("export = %+v", ev)
	}
	if _, err := os.Stat(ev.Path); err != nil {
		t.Fatalf("export file: %v", err)
	}

	var file []byte
	if code := h.raw("GET", snPath+"/export", nil, &file); code != 200 || int64(len(file)) != ev.Bytes {
		t.Fatalf("download: %d, %d bytes", code, len(file))
	}
	// The file: manifest, system files, /home, in that order.
	gz, err := gzip.NewReader(bytes.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var names []string
	var m snapManifest
	for {
		hd, err := tr.Next()
		if err != nil {
			break
		}
		names = append(names, hd.Name)
		if hd.Name == manifestName {
			json.NewDecoder(tr).Decode(&m)
		}
	}
	if strings.Join(names, ",") != "lps-snapshot.json,system.tar,home.tar" || m.VPS.SSHUser != "dev" || m.Snapshot.Label != "golden" {
		t.Fatalf("archive = %v, manifest = %+v", names, m)
	}

	// Not a snapshot file, and a bad name, are refused without leftovers.
	var e struct{ Error string }
	if code := h.raw("POST", "/api/vps/import?name=copy", strings.NewReader("hello"), &e); code != 400 {
		t.Fatalf("junk: %d %q", code, e.Error)
	}
	if code := h.raw("POST", "/api/vps/import?name=Bad_Name", bytes.NewReader(file), nil); code != 400 {
		t.Fatalf("bad name: %d", code)
	}
	if code := h.raw("POST", "/api/vps/import?name=box", bytes.NewReader(file), nil); code != 409 {
		t.Fatalf("existing name: %d", code)
	}
	var all []vpsView
	h.do("GET", "/api/vps", nil, &all)
	if len(all) != 1 {
		t.Fatalf("leftover VPS after failed imports: %d", len(all))
	}

	// Import makes a new VPS from the file.
	var nv vpsView
	if code := h.raw("POST", "/api/vps/import?name=copy", bytes.NewReader(file), &nv); code != 202 {
		t.Fatalf("import: %d", code)
	}
	waitFor(t, func() bool {
		h.do("GET", fmt.Sprintf("/api/vps/%d", nv.ID), nil, &nv)
		return nv.State == docker.StateRunning
	})
	if nv.SSHUser != "dev" || nv.SSHPort == v.SSHPort {
		t.Fatalf("imported VPS = %+v", nv.VPS)
	}
	var list []store.Snapshot
	h.do("GET", fmt.Sprintf("/api/vps/%d/snapshots", nv.ID), nil, &list)
	if len(list) != 1 || list[0].Label != "Imported: golden" || list[0].Status != store.SnapshotReady {
		t.Fatalf("imported snapshots = %+v", list)
	}
	h.eng.mu.Lock()
	spec := h.eng.vpsSpecs[len(h.eng.vpsSpecs)-1]
	origKept := h.eng.images[m.Snapshot.Image] // exported on this PC: the original stays
	h.eng.mu.Unlock()
	if spec.Name != "copy" || !strings.HasPrefix(spec.Image, "lps-vps-copy:from-snapshot-") || !origKept {
		t.Fatalf("spec = %+v, original image kept = %v", spec, origKept)
	}

	// Deleting the snapshot deletes its export file.
	if code := h.do("DELETE", snPath, nil, nil); code != 204 {
		t.Fatalf("delete snapshot: %d", code)
	}
	if _, err := os.Stat(ev.Path); !os.IsNotExist(err) {
		t.Fatalf("export file kept: %v", err)
	}
}
