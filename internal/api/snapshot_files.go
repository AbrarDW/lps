package api

// Snapshot files: a snapshot can be exported as one .lpsnap file (to keep it
// elsewhere or move the VPS to another PC) and a VPS can be created from
// such a file. The file is a gzip-compressed tar archive holding
//
//	lps-snapshot.json  what the snapshot is (format, VPS settings, label)
//	system.tar         the system files (docker save of the snapshot image)
//	home.tar           /home, with owners and permissions
//
// in that order, so an import can stream it without temporary files.

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/abrardw/lps/internal/docker"
	"github.com/abrardw/lps/internal/store"
)

const (
	snapFormat   = "lps-snapshot"
	snapVersion  = 1
	snapExt      = ".lpsnap"
	manifestName = "lps-snapshot.json"
)

type snapManifest struct {
	Format     string    `json:"format"`
	Version    int       `json:"version"`
	LPSVersion string    `json:"lpsVersion"`
	ExportedAt time.Time `json:"exportedAt"`
	VPS        struct {
		Name         string  `json:"name"`
		SSHUser      string  `json:"sshUser"`
		SSHPublicKey string  `json:"sshPublicKey"`
		CPUs         float64 `json:"cpus"`
		MemoryMB     int     `json:"memoryMb"`
	} `json:"vps"`
	Snapshot struct {
		Label       string    `json:"label"`
		CreatedAt   time.Time `json:"createdAt"`
		Image       string    `json:"image"`
		SystemBytes int64     `json:"systemBytes"`
		HomeBytes   int64     `json:"homeBytes"`
	} `json:"snapshot"`
}

// exportJob is an export in progress (or one that failed).
type exportJob struct {
	running bool
	err     string
	written atomic.Int64
}

// exportView is a snapshot's export file, as the API shows it.
type exportView struct {
	State   string `json:"state"` // none | exporting | ready | failed
	File    string `json:"file,omitempty"`
	Path    string `json:"path,omitempty"` // full path on this PC
	Bytes   int64  `json:"bytes,omitempty"`
	Written int64  `json:"written,omitempty"` // while exporting
	Error   string `json:"error,omitempty"`
}

func (s *Server) exportDir() string { return filepath.Join(s.dataDir(), "exports") }

func exportName(v *store.VPS, sn *store.Snapshot) string {
	return fmt.Sprintf("%s-snapshot-%d-%s%s", v.Name, sn.ID, sn.CreatedAt.UTC().Format("20060102-1504"), snapExt)
}

func (s *Server) exportPath(v *store.VPS, sn *store.Snapshot) string {
	return filepath.Join(s.exportDir(), exportName(v, sn))
}

func (s *Server) exportView(v *store.VPS, sn *store.Snapshot) exportView {
	// running and err are guarded by s.mu; written is atomic.
	s.mu.Lock()
	job := s.exports[sn.ID]
	running, jobErr := false, ""
	if job != nil {
		running, jobErr = job.running, job.err
	}
	s.mu.Unlock()
	if running {
		return exportView{State: "exporting", Written: job.written.Load()}
	}
	p := s.exportPath(v, sn)
	if st, err := os.Stat(p); err == nil {
		abs, _ := filepath.Abs(p)
		return exportView{State: "ready", File: filepath.Base(p), Path: abs, Bytes: st.Size()}
	}
	if jobErr != "" {
		return exportView{State: "failed", Error: jobErr}
	}
	return exportView{State: "none"}
}

// countingWriter counts bytes written through it.
type countingWriter struct {
	w io.Writer
	n *atomic.Int64
}

func (c countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n.Add(int64(n))
	return n, err
}

// writeExport builds the .lpsnap file for a snapshot.
func (s *Server) writeExport(ctx context.Context, v *store.VPS, sn *store.Snapshot, job *exportJob) error {
	dir := s.exportDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	final := s.exportPath(v, sn)
	tmp, err := os.MkdirTemp(dir, ".export-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	// The archive needs each part's size up front, so the parts are spooled first.
	spool := func(name string, fill func(io.Writer) error) (string, error) {
		p := filepath.Join(tmp, name)
		f, err := os.Create(p)
		if err != nil {
			return "", err
		}
		if err := fill(f); err != nil {
			f.Close()
			return "", err
		}
		return p, f.Close()
	}
	sys, err := spool("system.tar", func(w io.Writer) error { return s.engine.SaveImage(ctx, sn.Image, w) })
	if err != nil {
		return fmt.Errorf("save the system files: %w", err)
	}
	home, err := spool("home.tar", func(w io.Writer) error { return s.engine.ExportVolume(ctx, sn.Volume, w) })
	if err != nil {
		return fmt.Errorf("save /home: %w", err)
	}

	var m snapManifest
	m.Format, m.Version, m.LPSVersion, m.ExportedAt = snapFormat, snapVersion, s.cfg.Version, time.Now().UTC()
	m.VPS.Name, m.VPS.SSHUser, m.VPS.SSHPublicKey, m.VPS.CPUs, m.VPS.MemoryMB = v.Name, v.SSHUser, v.SSHPublicKey, v.CPUs, v.MemoryMB
	m.Snapshot.Label, m.Snapshot.CreatedAt, m.Snapshot.Image = sn.Label, sn.CreatedAt, sn.Image
	m.Snapshot.SystemBytes, m.Snapshot.HomeBytes = sn.SystemBytes, sn.HomeBytes
	manifest, _ := json.MarshalIndent(m, "", "  ")

	part := final + ".part"
	f, err := os.Create(part)
	if err != nil {
		return err
	}
	err = func() error {
		gz, _ := gzip.NewWriterLevel(countingWriter{f, &job.written}, gzip.BestSpeed)
		tw := tar.NewWriter(gz)
		hdr := func(name string, size int64) error {
			return tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: size, ModTime: m.ExportedAt, Typeflag: tar.TypeReg})
		}
		if err := hdr(manifestName, int64(len(manifest))); err != nil {
			return err
		}
		if _, err := tw.Write(manifest); err != nil {
			return err
		}
		for _, p := range []string{sys, home} {
			src, err := os.Open(p)
			if err != nil {
				return err
			}
			st, _ := src.Stat()
			if err := hdr(filepath.Base(p), st.Size()); err != nil {
				src.Close()
				return err
			}
			_, err = io.Copy(tw, src)
			src.Close()
			if err != nil {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
		if err := tw.Close(); err != nil {
			return err
		}
		return gz.Close()
	}()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(part)
		return err
	}
	return os.Rename(part, final)
}

func (s *Server) handleExportSnapshot(w http.ResponseWriter, r *http.Request) {
	v, sn, ok := s.loadSnapshot(w, r)
	if !ok {
		return
	}
	if sn.Status != store.SnapshotReady {
		writeError(w, http.StatusConflict, "this snapshot isn't ready")
		return
	}
	s.mu.Lock()
	if j := s.exports[sn.ID]; j != nil && j.running {
		s.mu.Unlock()
		writeError(w, http.StatusConflict, "this snapshot is already being exported")
		return
	}
	job := &exportJob{running: true}
	s.exports[sn.ID] = job
	s.mu.Unlock()
	s.info(r, "vps.snapshot_export", subjVPS, v.ID, v.Name, "Exporting snapshot %s of %s to a file", quoted(sn.Label), v.Name)
	vc, snc := *v, *sn
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		err := s.writeExport(s.ctx, &vc, &snc, job)
		s.mu.Lock()
		job.running = false
		if err != nil {
			job.err = err.Error()
		}
		s.mu.Unlock()
		if err != nil {
			s.fail(nil, "vps.snapshot_export_failed", subjVPS, vc.ID, vc.Name, "Exporting snapshot %s of %s failed: %s", quoted(snc.Label), vc.Name, firstLine(err.Error()))
			return
		}
		st, _ := os.Stat(s.exportPath(&vc, &snc))
		size := int64(0)
		if st != nil {
			size = st.Size()
		}
		s.info(nil, "vps.snapshot_exported", subjVPS, vc.ID, vc.Name, "Snapshot %s of %s saved as %s (%s)", quoted(snc.Label), vc.Name, exportName(&vc, &snc), humanBytes(size))
	}()
	writeJSON(w, http.StatusAccepted, s.exportView(v, sn))
}

// handleDownloadExport sends the export file.
func (s *Server) handleDownloadExport(w http.ResponseWriter, r *http.Request) {
	v, sn, ok := s.loadSnapshot(w, r)
	if !ok {
		return
	}
	if s.exportView(v, sn).State != "ready" {
		writeError(w, http.StatusNotFound, "export this snapshot first")
		return
	}
	f, err := os.Open(s.exportPath(v, sn))
	if err != nil {
		writeError(w, http.StatusNotFound, "export file not found")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		s.internalError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, exportName(v, sn)))
	http.ServeContent(w, r, "", st.ModTime(), f)
}

func (s *Server) handleDeleteExport(w http.ResponseWriter, r *http.Request) {
	v, sn, ok := s.loadSnapshot(w, r)
	if !ok {
		return
	}
	if s.exportView(v, sn).State == "exporting" {
		writeError(w, http.StatusConflict, "wait for the export to finish")
		return
	}
	if err := os.Remove(s.exportPath(v, sn)); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.internalError(w, err)
		return
	}
	s.mu.Lock()
	delete(s.exports, sn.ID)
	s.mu.Unlock()
	writeJSON(w, 200, s.exportView(v, sn))
}

// removeExport deletes a snapshot's export file, if any.
func (s *Server) removeExport(v *store.VPS, sn *store.Snapshot) {
	_ = os.Remove(s.exportPath(v, sn))
	s.mu.Lock()
	delete(s.exports, sn.ID)
	s.mu.Unlock()
}

// ---- import ----

type importError struct {
	status int
	msg    string
}

func (e *importError) Error() string { return e.msg }

func badFile(format string, a ...any) error {
	return &importError{http.StatusBadRequest, "this isn't a usable LPS snapshot file: " + fmt.Sprintf(format, a...)}
}

// readManifest reads and checks the first entry of a snapshot file.
func readManifest(tr *tar.Reader) (*snapManifest, error) {
	h, err := tr.Next()
	if err != nil || h.Name != manifestName {
		return nil, badFile("it doesn't start with %s", manifestName)
	}
	var m snapManifest
	if err := json.NewDecoder(io.LimitReader(tr, 64<<10)).Decode(&m); err != nil {
		return nil, badFile("%s: %v", manifestName, err)
	}
	if m.Format != snapFormat {
		return nil, badFile("unknown format %q", m.Format)
	}
	if m.Version > snapVersion {
		return nil, badFile("it was made by a newer LPS (format %d); update LPS first", m.Version)
	}
	if !sshUserRe.MatchString(m.VPS.SSHUser) || reservedUsers[m.VPS.SSHUser] {
		return nil, badFile("invalid SSH user %q", m.VPS.SSHUser)
	}
	if err := validateSSHKey(strings.TrimSpace(m.VPS.SSHPublicKey)); err != nil { // "" = password only
		return nil, badFile("%v", err)
	}
	if err := validateResources(m.VPS.CPUs, m.VPS.MemoryMB); err != nil {
		return nil, badFile("%v", err)
	}
	if m.Snapshot.Image == "" {
		return nil, badFile("no image name")
	}
	return &m, nil
}

// nextPart moves to the next entry, which must be name.
func nextPart(tr *tar.Reader, name string) error {
	h, err := tr.Next()
	if err != nil {
		return badFile("%s is missing (%v)", name, err)
	}
	if h.Name != name {
		return badFile("expected %s, found %s", name, h.Name)
	}
	return nil
}

// handleImportVPS creates a VPS from an uploaded .lpsnap file (the raw
// request body). The file is read as it arrives; then the VPS starts in the
// background like any other.
func (s *Server) handleImportVPS(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if !nameRe.MatchString(name) {
		writeError(w, 400, "name must be 2-31 chars: lowercase letters, digits and '-'")
		return
	}
	ctx := r.Context()
	gz, err := gzip.NewReader(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "this isn't an LPS snapshot file (.lpsnap)")
		return
	}
	tr := tar.NewReader(gz)
	m, err := readManifest(tr)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// The helper containers that copy /home use the VPS image.
	if err := s.engine.EnsureVPSImage(ctx, io.Discard); err != nil {
		writeError(w, http.StatusBadGateway, "prepare the VPS image: "+err.Error())
		return
	}
	port, err := s.reservePort(ctx, s.cfg.VPSPortMin, s.cfg.VPSPortMax)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	defer s.releasePort(port)
	v := &store.VPS{Name: name, SSHUser: m.VPS.SSHUser, SSHPort: port, SSHPublicKey: strings.TrimSpace(m.VPS.SSHPublicKey), CPUs: m.VPS.CPUs, MemoryMB: m.VPS.MemoryMB}
	if err := s.store.CreateVPS(ctx, v); err != nil {
		storeError(s, w, err, "VPS")
		return
	}
	task := &taskInfo{running: true, log: &syncBuffer{}}
	s.mu.Lock()
	s.vpsTasks[v.ID] = task
	s.mu.Unlock()
	fmt.Fprintf(task.log, "[lps] importing snapshot %s of %s\n", quoted(m.Snapshot.Label), m.VPS.Name)

	sn, err := s.importSnapshot(ctx, v, m, tr, task.log)
	if err != nil {
		// Leave nothing behind.
		if sn != nil {
			_ = s.deleteSnapshotData(context.WithoutCancel(ctx), sn)
		}
		_ = s.store.DeleteVPS(context.WithoutCancel(ctx), v.ID)
		s.mu.Lock()
		delete(s.vpsTasks, v.ID)
		s.mu.Unlock()
		var ie *importError
		if errors.As(err, &ie) {
			writeError(w, ie.status, ie.msg)
		} else {
			writeError(w, http.StatusBadGateway, "import failed: "+err.Error())
		}
		return
	}
	s.info(r, "vps.created", subjVPS, v.ID, v.Name, "Created VPS %s from a snapshot file (%s of %s, SSH port %d)", v.Name, quoted(m.Snapshot.Label), m.VPS.Name, v.SSHPort)
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

// importSnapshot loads the file's system files and /home as a snapshot of v.
func (s *Server) importSnapshot(ctx context.Context, v *store.VPS, m *snapManifest, tr *tar.Reader, log io.Writer) (*store.Snapshot, error) {
	label := cleanLabel("Imported: " + m.Snapshot.Label)
	sn, err := s.store.CreateSnapshot(ctx, v.ID, label)
	if err != nil {
		return nil, err
	}
	if err := nextPart(tr, "system.tar"); err != nil {
		return sn, err
	}
	// If this PC already has the image name (it was exported here), keep it.
	_, missing := s.engine.ImageSize(ctx, m.Snapshot.Image)
	fmt.Fprintf(log, "[lps] loading the system files\n")
	if err := s.engine.LoadImage(ctx, tr); err != nil {
		return sn, err
	}
	if _, err := s.engine.ImageSize(ctx, m.Snapshot.Image); err != nil {
		return sn, badFile("the system files don't contain %s", m.Snapshot.Image)
	}
	img := docker.SnapshotImage(v.Name, sn.ID, sn.CreatedAt)
	if err := s.engine.TagImage(ctx, m.Snapshot.Image, img); err != nil {
		return sn, err
	}
	sn.Image = img
	if missing != nil {
		_ = s.engine.RemoveSnapshotImage(ctx, m.Snapshot.Image) // only the name the file brought
	}
	sn.SystemBytes, _ = s.engine.ImageSize(ctx, img)

	if err := nextPart(tr, "home.tar"); err != nil {
		return sn, err
	}
	fmt.Fprintf(log, "[lps] restoring /home\n")
	vol := docker.SnapshotVolume(sn.ID, sn.CreatedAt)
	sn.Volume = vol
	if err := s.engine.ImportVolume(ctx, vol, tr, map[string]string{"lps.managed": "true", "lps.kind": "snapshot"}); err != nil {
		return sn, fmt.Errorf("restore /home: %w", err)
	}
	sn.HomeBytes = m.Snapshot.HomeBytes
	sn.Status = store.SnapshotReady
	if err := s.store.FinishSnapshot(ctx, sn); err != nil {
		return sn, err
	}
	return sn, nil
}
