package api

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/abrardw/lps/internal/docker"
	"github.com/abrardw/lps/internal/store"
)

func (f *fakeEngine) Commit(_ context.Context, id, ref, comment string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.containers[id]; !ok {
		return fmt.Errorf("no such container %s", id)
	}
	if f.images == nil {
		f.images = map[string]bool{}
	}
	f.images[ref] = true
	return nil
}

func (f *fakeEngine) ImageSize(_ context.Context, ref string) (int64, error) { return 300 << 20, nil }

func (f *fakeEngine) CopyVolume(_ context.Context, from, to string, _ map[string]string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.volumes[from] {
		return 0, fmt.Errorf("no such volume %s", from)
	}
	f.volumes[to] = true
	f.copies = append(f.copies, from+">"+to)
	return 20 << 20, nil
}

func (f *fakeEngine) TagImage(_ context.Context, src, dst string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.images[src] {
		return fmt.Errorf("no such image %s", src)
	}
	f.images[dst] = true
	return nil
}

func (f *fakeEngine) RemoveSnapshotImage(_ context.Context, ref string) error {
	f.mu.Lock()
	delete(f.images, ref)
	f.mu.Unlock()
	return nil
}

func (h *harness) waitSnapshot(vpsID int64, label string) store.Snapshot {
	h.t.Helper()
	var found store.Snapshot
	waitFor(h.t, func() bool {
		var list []store.Snapshot
		h.do("GET", fmt.Sprintf("/api/vps/%d/snapshots", vpsID), nil, &list)
		for _, s := range list {
			if s.Label == label && s.Status != store.SnapshotCreating {
				found = s
				return true
			}
		}
		return false
	})
	return found
}

func TestSnapshotRestoreClone(t *testing.T) {
	h := setup(t)
	h.login()
	v := h.newRunningVPS()
	base := fmt.Sprintf("/api/vps/%d", v.ID)

	var sn store.Snapshot
	if code := h.do("POST", base+"/snapshots", map[string]string{"label": "  before   upgrade "}, &sn); code != 202 || sn.Status != store.SnapshotCreating {
		t.Fatalf("create: %d %+v", code, sn)
	}
	sn = h.waitSnapshot(v.ID, "before upgrade")
	if sn.Status != store.SnapshotReady || sn.SystemBytes != 300<<20 || sn.HomeBytes != 20<<20 {
		t.Fatalf("snapshot = %+v", sn)
	}
	h.eng.mu.Lock()
	hasImg := h.eng.images[docker.SnapshotImage("box", sn.ID, sn.CreatedAt)]
	hasVol := h.eng.volumes[docker.SnapshotVolume(sn.ID, sn.CreatedAt)]
	h.eng.mu.Unlock()
	if !hasImg || !hasVol {
		t.Fatal("snapshot image or /home copy missing")
	}
	// Default label.
	var unnamed store.Snapshot
	h.do("POST", base+"/snapshots", map[string]string{}, &unnamed)
	if !strings.HasPrefix(unnamed.Label, "Snapshot 20") {
		t.Fatalf("default label %q", unnamed.Label)
	}
	h.waitSnapshot(v.ID, unnamed.Label)

	// Restore: a safety snapshot first, then the VPS runs from the snapshot.
	oldContainer := v.ContainerID
	if code := h.do("POST", fmt.Sprintf("%s/snapshots/%d/restore", base, sn.ID), nil, nil); code != 202 {
		t.Fatalf("restore: %d", code)
	}
	waitFor(t, func() bool {
		var got vpsView
		h.do("GET", base, nil, &got)
		v = got
		return got.State == docker.StateRunning
	})
	if v.ContainerID == oldContainer {
		t.Fatal("restore kept the old container")
	}
	h.eng.mu.Lock()
	spec := h.eng.vpsSpecs[len(h.eng.vpsSpecs)-1]
	copies := strings.Join(h.eng.copies, ",")
	_, oldStill := h.eng.containers[oldContainer]
	h.eng.mu.Unlock()
	wantImg := fmt.Sprintf("lps-vps-box:from-snapshot-%d", sn.ID)
	if spec.Image != wantImg || spec.Name != "box" || spec.SSHPort != v.SSHPort {
		t.Fatalf("restored spec = %+v", spec)
	}
	if oldStill || !strings.HasSuffix(copies, docker.SnapshotVolume(sn.ID, sn.CreatedAt)+">"+docker.VPSVolumeName("box")) {
		t.Fatalf("copies = %s, old container left = %v", copies, oldStill)
	}
	var list []store.Snapshot
	h.do("GET", base+"/snapshots", nil, &list)
	if len(list) != 3 || list[0].Label != "Before restoring “before upgrade”" || list[0].Status != store.SnapshotReady {
		t.Fatalf("snapshots after restore = %+v", list)
	}

	// The snapshot the VPS came from can still be deleted (the VPS has its own tag).
	if code := h.do("DELETE", fmt.Sprintf("%s/snapshots/%d", base, sn.ID), nil, nil); code != 204 {
		t.Fatalf("delete snapshot: %d", code)
	}
	h.eng.mu.Lock()
	ownTag := h.eng.images[wantImg]
	gone := !h.eng.images[docker.SnapshotImage("box", sn.ID, sn.CreatedAt)] && !h.eng.volumes[docker.SnapshotVolume(sn.ID, sn.CreatedAt)]
	h.eng.mu.Unlock()
	if !ownTag || !gone {
		t.Fatalf("own tag kept=%v snapshot data gone=%v", ownTag, gone)
	}

	// New VPS from a snapshot.
	safety := list[0]
	if code := h.do("POST", fmt.Sprintf("%s/snapshots/%d/clone", base, safety.ID), map[string]string{"name": "Bad Name"}, nil); code != 400 {
		t.Fatalf("bad clone name: %d", code)
	}
	var clone vpsView
	if code := h.do("POST", fmt.Sprintf("%s/snapshots/%d/clone", base, safety.ID), map[string]string{"name": "box-copy"}, &clone); code != 202 {
		t.Fatalf("clone: %d", code)
	}
	waitFor(t, func() bool {
		h.do("GET", fmt.Sprintf("/api/vps/%d", clone.ID), nil, &clone)
		return clone.State == docker.StateRunning
	})
	if clone.SSHPort == v.SSHPort || clone.SSHUser != v.SSHUser {
		t.Fatalf("clone = %+v", clone)
	}
	h.eng.mu.Lock()
	cspec := h.eng.vpsSpecs[len(h.eng.vpsSpecs)-1]
	h.eng.mu.Unlock()
	if cspec.Name != "box-copy" || cspec.Image != fmt.Sprintf("lps-vps-box-copy:from-snapshot-%d", safety.ID) {
		t.Fatalf("clone spec = %+v", cspec)
	}

	// Deleting the VPS deletes its snapshots and its image tag.
	if code := h.do("DELETE", base, nil, nil); code != 204 {
		t.Fatalf("delete vps: %d", code)
	}
	h.eng.mu.Lock()
	leftover := []string{}
	for img := range h.eng.images {
		if strings.HasPrefix(img, "lps-snap-box:") || strings.HasPrefix(img, "lps-vps-box:") {
			leftover = append(leftover, img)
		}
	}
	h.eng.mu.Unlock()
	if len(leftover) != 0 {
		t.Fatalf("images left after VPS delete: %v", leftover)
	}

	// The activity feed tells the story.
	var evs []store.Event
	h.do("GET", "/api/events?subject=vps&limit=100", nil, &evs)
	all := ""
	for _, e := range evs {
		all += e.Message + "\n"
	}
	for _, want := range []string{"Taking snapshot “before upgrade” of box", "Snapshot “before upgrade” of box is ready (320 MB)", "Restoring box to snapshot “before upgrade”", "box is back to snapshot “before upgrade”", "Created VPS box-copy from snapshot"} {
		if !strings.Contains(all, want) {
			t.Fatalf("missing %q in:\n%s", want, all)
		}
	}
}

func TestSafetyLabel(t *testing.T) {
	if got := safetyLabel("clean v1"); got != "Before restoring “clean v1”" {
		t.Fatal(got)
	}
	if got := safetyLabel("Before restoring “clean v1”"); got != "Before undoing a restore" {
		t.Fatal(got)
	}
	if got := safetyLabel(strings.Repeat("é", 100)); len([]rune(got)) > 80 || !utf8.ValidString(got) {
		t.Fatalf("bad truncation: %q", got)
	}
}

func TestSnapshotFailures(t *testing.T) {
	h := setup(t)
	h.login()
	v := h.newRunningVPS()
	base := fmt.Sprintf("/api/vps/%d", v.ID)

	// A /home copy that fails leaves a failed snapshot; restore refuses it.
	h.eng.mu.Lock()
	delete(h.eng.volumes, docker.VPSVolumeName("box"))
	h.eng.mu.Unlock()
	var sn store.Snapshot
	h.do("POST", base+"/snapshots", map[string]string{"label": "broken"}, &sn)
	sn = h.waitSnapshot(v.ID, "broken")
	if sn.Status != store.SnapshotFailed || !strings.Contains(sn.Error, "copy /home") {
		t.Fatalf("snapshot = %+v", sn)
	}
	if code := h.do("POST", fmt.Sprintf("%s/snapshots/%d/restore", base, sn.ID), nil, nil); code != 409 {
		t.Fatalf("restore failed snapshot: %d", code)
	}
	if code := h.do("POST", fmt.Sprintf("%s/snapshots/%d/clone", base, sn.ID), map[string]string{"name": "x2"}, nil); code != 409 {
		t.Fatalf("clone failed snapshot: %d", code)
	}
	// A failed snapshot can be deleted, and so can its partial data.
	if code := h.do("DELETE", fmt.Sprintf("%s/snapshots/%d", base, sn.ID), nil, nil); code != 204 {
		t.Fatalf("delete failed snapshot: %d", code)
	}
	// Snapshots belong to their VPS.
	if code := h.do("DELETE", fmt.Sprintf("/api/vps/%d/snapshots/%d", v.ID+100, sn.ID), nil, nil); code != 404 {
		t.Fatalf("other VPS: %d", code)
	}
}
