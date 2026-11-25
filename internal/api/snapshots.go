package api

// VPS snapshots: the VPS's system files (docker commit) plus a copy of its
// /home volume. A snapshot can restore the VPS, or become a new VPS.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abrardw/lps/internal/docker"
	"github.com/abrardw/lps/internal/store"
)

const stateRestoring = "restoring"

// snapshotView is a snapshot as the API returns it.
type snapshotView struct {
	*store.Snapshot
	Export exportView `json:"export"`
}

// loadSnapshot loads a VPS and one of its snapshots from the URL.
func (s *Server) loadSnapshot(w http.ResponseWriter, r *http.Request) (*store.VPS, *store.Snapshot, bool) {
	v, ok := s.loadVPS(w, r)
	if !ok {
		return nil, nil, false
	}
	sid, err := strconv.ParseInt(chi.URLParam(r, "sid"), 10, 64)
	if err != nil {
		writeError(w, 400, "invalid snapshot id")
		return nil, nil, false
	}
	sn, err := s.store.GetSnapshot(r.Context(), sid)
	if err != nil || sn.VPSID != v.ID {
		writeError(w, 404, "snapshot not found")
		return nil, nil, false
	}
	return v, sn, true
}

func (s *Server) handleListSnapshots(w http.ResponseWriter, r *http.Request) {
	v, ok := s.loadVPS(w, r)
	if !ok {
		return
	}
	list, err := s.store.ListSnapshots(r.Context(), v.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	out := make([]snapshotView, 0, len(list))
	for _, sn := range list {
		out = append(out, snapshotView{Snapshot: sn, Export: s.exportView(v, sn)})
	}
	writeJSON(w, 200, out)
}

// snapshotBusy reports whether a snapshot is being taken of the VPS.
func (s *Server) snapshotBusy(vpsID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapping[vpsID]
}

// takeSnapshot saves the VPS's system files and /home into sn and records
// the result. It is used by the API and before a restore.
func (s *Server) takeSnapshot(ctx context.Context, v *store.VPS, sn *store.Snapshot, log io.Writer) error {
	s.mu.Lock()
	s.snapping[v.ID] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.snapping, v.ID)
		s.mu.Unlock()
	}()
	err := func() error {
		img := docker.SnapshotImage(v.Name, sn.ID, sn.CreatedAt)
		fmt.Fprintf(log, "[lps] saving the system files of %s\n", v.Name)
		if err := s.engine.Commit(ctx, v.ContainerID, img, "LPS snapshot: "+sn.Label); err != nil {
			return fmt.Errorf("save system files: %w", err)
		}
		sn.Image = img
		sn.SystemBytes, _ = s.engine.ImageSize(ctx, img)
		vol := docker.SnapshotVolume(sn.ID, sn.CreatedAt)
		fmt.Fprintf(log, "[lps] copying /home\n")
		n, err := s.engine.CopyVolume(ctx, docker.VPSVolumeName(v.Name), vol, map[string]string{"lps.managed": "true", "lps.kind": "snapshot"})
		sn.Volume = vol // set even on failure, so a delete cleans it up
		if err != nil {
			return fmt.Errorf("copy /home: %w", err)
		}
		sn.HomeBytes = n
		return nil
	}()
	sn.Status = store.SnapshotReady
	if err != nil {
		sn.Status, sn.Error = store.SnapshotFailed, err.Error()
	}
	if ferr := s.store.FinishSnapshot(context.WithoutCancel(ctx), sn); ferr != nil && err == nil {
		err = ferr
	}
	return err
}

// quoted shows a snapshot label in curly quotes (no escaping, unlike %q).
func quoted(l string) string { return "“" + l + "”" }

// safetyLabel names the snapshot taken before restoring to label.
func safetyLabel(label string) string {
	if strings.HasPrefix(label, "Before restoring") || strings.HasPrefix(label, "Before undoing") {
		return "Before undoing a restore"
	}
	return cleanLabel("Before restoring " + quoted(label))
}

func cleanLabel(l string) string {
	l = strings.Join(strings.Fields(l), " ")
	if r := []rune(l); len(r) > 80 { // cut by characters, never inside one
		l = string(r[:80])
	}
	return l
}

// handleCreateSnapshot starts taking a snapshot in the background.
func (s *Server) handleCreateSnapshot(w http.ResponseWriter, r *http.Request) {
	v, ok := s.loadVPS(w, r)
	if !ok || s.vpsBusy(w, v.ID) {
		return
	}
	if v.ContainerID == "" {
		writeError(w, http.StatusConflict, "this VPS has no container to snapshot")
		return
	}
	if s.snapshotBusy(v.ID) {
		writeError(w, http.StatusConflict, "a snapshot of this VPS is already being taken")
		return
	}
	var req struct {
		Label string `json:"label"`
	}
	if !decode(w, r, &req) {
		return
	}
	label := cleanLabel(req.Label)
	if label == "" {
		label = "Snapshot " + time.Now().Format("2006-01-02 15:04")
	}
	sn, err := s.store.CreateSnapshot(r.Context(), v.ID, label)
	if err != nil {
		s.internalError(w, err)
		return
	}
	s.mu.Lock()
	s.snapping[v.ID] = true // reserved until the goroutine takes over
	s.mu.Unlock()
	s.info(r, "vps.snapshot", subjVPS, v.ID, v.Name, "Taking snapshot %s of %s", quoted(label), v.Name)
	// The goroutine works on copies: the response below still encodes sn.
	vc, snc := *v, *sn
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.takeSnapshot(s.ctx, &vc, &snc, io.Discard); err != nil {
			s.fail(nil, "vps.snapshot_failed", subjVPS, vc.ID, vc.Name, "Snapshot %s of %s failed: %s", quoted(label), vc.Name, firstLine(err.Error()))
			return
		}
		s.info(nil, "vps.snapshot_ready", subjVPS, vc.ID, vc.Name, "Snapshot %s of %s is ready (%s)", quoted(label), vc.Name, humanBytes(snc.SystemBytes+snc.HomeBytes))
	}()
	writeJSON(w, http.StatusAccepted, snapshotView{Snapshot: sn, Export: exportView{State: "none"}})
}

// runFromSnapshot creates and starts the VPS's container from a snapshot,
// with /home replaced by the snapshot's copy. The VPS's container must be gone.
func (s *Server) runFromSnapshot(ctx context.Context, v *store.VPS, sn *store.Snapshot, log io.Writer) error {
	fmt.Fprintf(log, "[lps] restoring /home\n")
	if _, err := s.engine.CopyVolume(ctx, sn.Volume, docker.VPSVolumeName(v.Name), map[string]string{"lps.managed": "true", "lps.kind": "vps", "lps.name": v.Name}); err != nil {
		return fmt.Errorf("restore /home: %w", err)
	}
	// The VPS gets its own tag of the snapshot image, so the snapshot can
	// still be deleted later.
	img := fmt.Sprintf("lps-vps-%s:from-snapshot-%d", v.Name, sn.ID)
	if err := s.engine.TagImage(ctx, sn.Image, img); err != nil {
		return fmt.Errorf("tag image: %w", err)
	}
	fmt.Fprintf(log, "[lps] starting %s from the snapshot\n", v.Name)
	cid, err := s.engine.CreateVPS(ctx, docker.VPSSpec{
		Name: v.Name, User: v.SSHUser, PublicKey: v.SSHPublicKey, SSHPort: v.SSHPort,
		CPUs: v.CPUs, MemoryMB: v.MemoryMB, Image: img,
	})
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}
	if err := s.store.SetVPSContainer(ctx, v.ID, cid); err != nil {
		return err
	}
	old := v.Image
	if err := s.store.SetVPSImage(ctx, v.ID, img); err != nil {
		return err
	}
	if old != "" && old != img {
		_ = s.engine.RemoveSnapshotImage(ctx, old) // the previous restore's tag
	}
	if err := s.engine.Start(ctx, cid); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	// Apps in this VPS reach it over the LPS network; reattach and restart them.
	if apps, _ := s.store.ListAppsByVPS(ctx, v.ID); len(apps) > 0 {
		if err := s.engine.EnsureNetwork(ctx, cid); err != nil {
			return fmt.Errorf("network: %w", err)
		}
		s.kickVPSApps()
	}
	return nil
}

// handleRestoreSnapshot puts the VPS back to a snapshot. A safety snapshot
// of the current state is taken first, so the restore can be undone.
func (s *Server) handleRestoreSnapshot(w http.ResponseWriter, r *http.Request) {
	v, sn, ok := s.loadSnapshot(w, r)
	if !ok || s.vpsBusy(w, v.ID) {
		return
	}
	if sn.Status != store.SnapshotReady {
		writeError(w, http.StatusConflict, "this snapshot isn't ready")
		return
	}
	if s.snapshotBusy(v.ID) {
		writeError(w, http.StatusConflict, "a snapshot of this VPS is being taken; wait for it to finish")
		return
	}
	task := &taskInfo{running: true, kind: stateRestoring, log: &syncBuffer{}}
	s.mu.Lock()
	s.vpsTasks[v.ID] = task
	s.mu.Unlock()
	s.warn(r, "vps.restore", subjVPS, v.ID, v.Name, "Restoring %s to snapshot %s", v.Name, quoted(sn.Label))
	vc, snc := *v, *sn
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		err := s.restore(&vc, &snc, task.log)
		s.mu.Lock()
		defer s.mu.Unlock()
		if err != nil {
			fmt.Fprintf(task.log, "\n[lps] restore failed: %v\n", err)
			s.vpsTasks[vc.ID] = &taskInfo{err: "restore failed: " + err.Error(), log: task.log}
			s.fail(nil, "vps.restore_failed", subjVPS, vc.ID, vc.Name, "Restoring %s failed: %s", vc.Name, firstLine(err.Error()))
			return
		}
		delete(s.vpsTasks, vc.ID)
		s.info(nil, "vps.restored", subjVPS, vc.ID, vc.Name, "%s is back to snapshot %s", vc.Name, quoted(snc.Label))
	}()
	writeJSON(w, http.StatusAccepted, s.vpsView(r, v))
}

func (s *Server) restore(v *store.VPS, sn *store.Snapshot, log io.Writer) error {
	ctx := s.ctx
	if st, _ := s.engine.State(ctx, v.ContainerID); st != docker.StateMissing {
		safety, err := s.store.CreateSnapshot(ctx, v.ID, safetyLabel(sn.Label))
		if err != nil {
			return err
		}
		fmt.Fprintf(log, "[lps] taking a safety snapshot of the current state first\n")
		if err := s.takeSnapshot(ctx, v, safety, log); err != nil {
			return fmt.Errorf("safety snapshot (nothing was changed): %w", err)
		}
	}
	fmt.Fprintf(log, "[lps] removing the current container\n")
	if err := s.engine.Remove(ctx, v.ContainerID); err != nil {
		return err
	}
	_ = s.engine.Remove(ctx, docker.VPSContainerName(v.Name))
	return s.runFromSnapshot(ctx, v, sn, log)
}

// handleCloneSnapshot makes a new VPS from a snapshot.
func (s *Server) handleCloneSnapshot(w http.ResponseWriter, r *http.Request) {
	src, sn, ok := s.loadSnapshot(w, r)
	if !ok {
		return
	}
	if sn.Status != store.SnapshotReady {
		writeError(w, http.StatusConflict, "this snapshot isn't ready")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &req) {
		return
	}
	if !nameRe.MatchString(req.Name) {
		writeError(w, 400, "name must be 2-31 chars: lowercase letters, digits and '-'")
		return
	}
	port, err := s.reservePort(r.Context(), s.cfg.VPSPortMin, s.cfg.VPSPortMax)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	defer s.releasePort(port)
	v := &store.VPS{Name: req.Name, SSHUser: src.SSHUser, SSHPort: port, SSHPublicKey: src.SSHPublicKey, CPUs: src.CPUs, MemoryMB: src.MemoryMB}
	if err := s.store.CreateVPS(r.Context(), v); err != nil {
		storeError(s, w, err, "VPS")
		return
	}
	task := &taskInfo{running: true, log: &syncBuffer{}}
	s.mu.Lock()
	s.vpsTasks[v.ID] = task
	s.mu.Unlock()
	s.info(r, "vps.created", subjVPS, v.ID, v.Name, "Created VPS %s from snapshot %s of %s (SSH port %d)", v.Name, quoted(sn.Label), src.Name, v.SSHPort)
	vc, snc := *v, *sn
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		err := s.runFromSnapshot(s.ctx, &vc, &snc, task.log)
		s.mu.Lock()
		defer s.mu.Unlock()
		if err != nil {
			fmt.Fprintf(task.log, "\n[lps] failed: %v\n", err)
			s.vpsTasks[vc.ID] = &taskInfo{err: err.Error(), log: task.log}
			s.fail(nil, "vps.provision_failed", subjVPS, vc.ID, vc.Name, "VPS %s could not be created: %v", vc.Name, err)
			return
		}
		delete(s.vpsTasks, vc.ID)
		s.info(nil, "vps.ready", subjVPS, vc.ID, vc.Name, "VPS %s is ready", vc.Name)
	}()
	s.kickRemote()
	writeJSON(w, http.StatusAccepted, s.vpsView(r, v))
}

// deleteSnapshotData removes a snapshot's image and /home copy.
func (s *Server) deleteSnapshotData(ctx context.Context, sn *store.Snapshot) error {
	if sn.Image != "" {
		if err := s.engine.RemoveSnapshotImage(ctx, sn.Image); err != nil {
			return fmt.Errorf("remove image: %w", err)
		}
	}
	if sn.Volume != "" {
		if err := s.engine.RemoveVolume(ctx, sn.Volume); err != nil {
			return fmt.Errorf("remove /home copy: %w", err)
		}
	}
	return nil
}

func (s *Server) handleDeleteSnapshot(w http.ResponseWriter, r *http.Request) {
	v, sn, ok := s.loadSnapshot(w, r)
	if !ok {
		return
	}
	if sn.Status == store.SnapshotCreating {
		writeError(w, http.StatusConflict, "this snapshot is still being taken")
		return
	}
	s.mu.Lock()
	t := s.vpsTasks[v.ID]
	s.mu.Unlock()
	if t != nil && t.running {
		writeError(w, http.StatusConflict, "wait for the VPS to finish restoring")
		return
	}
	if err := s.deleteSnapshotData(r.Context(), sn); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if err := s.store.DeleteSnapshot(r.Context(), sn.ID); err != nil {
		s.internalError(w, err)
		return
	}
	s.removeExport(v, sn)
	s.info(r, "vps.snapshot_deleted", subjVPS, v.ID, v.Name, "Deleted snapshot %s of %s", quoted(sn.Label), v.Name)
	w.WriteHeader(http.StatusNoContent)
}

// deleteVPSSnapshots removes all of a VPS's snapshots (when the VPS is deleted).
func (s *Server) deleteVPSSnapshots(ctx context.Context, v *store.VPS) error {
	list, err := s.store.ListSnapshots(ctx, v.ID)
	if err != nil {
		return err
	}
	var errs []error
	for _, sn := range list {
		s.removeExport(v, sn)
		if err := s.deleteSnapshotData(ctx, sn); err != nil {
			errs = append(errs, fmt.Errorf("snapshot %s: %w", quoted(sn.Label), err))
		}
	}
	return errors.Join(errs...)
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%d MB", n>>20)
	default:
		return fmt.Sprintf("%d KB", n>>10)
	}
}
