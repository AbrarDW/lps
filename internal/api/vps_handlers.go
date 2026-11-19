package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/abrardw/lps/internal/docker"
	"github.com/abrardw/lps/internal/store"
)

var (
	nameRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,30}$`)
	sshUserRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,30}$`)
	keyTypes  = []string{"ssh-ed25519 ", "ssh-rsa ", "ecdsa-sha2-", "sk-ssh-ed25519@openssh.com ", "sk-ecdsa-sha2-"}
	// Names that would clash with system accounts inside Ubuntu.
	reservedUsers = map[string]bool{"root": true, "daemon": true, "bin": true, "sys": true, "sync": true, "nobody": true, "sshd": true, "www-data": true}
)

// Extra states reported in API responses besides docker.State*.
const (
	stateProvisioning = "provisioning"
	stateError        = "error"
)

type vpsView struct {
	*store.VPS
	State string `json:"state"`
	Error string `json:"error,omitempty"`
	// ProvisionLog is the tail of the provisioning output while provisioning or after a failure.
	ProvisionLog string `json:"provisionLog,omitempty"`
	// RemoteSSH is the SSH command over the tailnet, when remote access is active.
	RemoteSSH string `json:"remoteSsh,omitempty"`
	// Apps lists the apps deployed into this VPS.
	Apps []vpsAppRef `json:"apps"`
}

type vpsAppRef struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	HostPort int    `json:"hostPort"`
}

type createVPSReq struct {
	Name         string  `json:"name"`
	SSHUser      string  `json:"sshUser"`
	SSHPublicKey string  `json:"sshPublicKey"`
	Password     string  `json:"password"`
	CPUs         float64 `json:"cpus"`
	MemoryMB     int     `json:"memoryMb"`
}

func validateResources(cpus float64, mem int) error {
	if cpus < 0 || cpus > 64 {
		return errors.New("cpus must be between 0 (unlimited) and 64")
	}
	if mem != 0 && (mem < 128 || mem > 1<<20) {
		return errors.New("memoryMb must be 0 (unlimited) or at least 128")
	}
	return nil
}

func (req *createVPSReq) validate() error {
	if !nameRe.MatchString(req.Name) {
		return errors.New("name must be 2-31 chars: lowercase letters, digits and '-'")
	}
	if !sshUserRe.MatchString(req.SSHUser) || reservedUsers[req.SSHUser] {
		return errors.New("sshUser must be a lowercase Linux username and not a system account")
	}
	req.SSHPublicKey = strings.TrimSpace(req.SSHPublicKey)
	if req.SSHPublicKey == "" && req.Password == "" {
		return errors.New("provide an SSH public key, a password, or both")
	}
	if req.SSHPublicKey != "" {
		if strings.ContainsAny(req.SSHPublicKey, "\r\n") {
			return errors.New("sshPublicKey must be a single line (one key)")
		}
		ok := false
		for _, t := range keyTypes {
			if strings.HasPrefix(req.SSHPublicKey, t) {
				ok = true
			}
		}
		if !ok {
			return errors.New("sshPublicKey must be an OpenSSH public key (e.g. the contents of id_ed25519.pub)")
		}
	}
	if req.Password != "" && (len(req.Password) < 8 || strings.ContainsAny(req.Password, "\r\n:")) {
		return errors.New("password must be at least 8 characters and contain no ':' or newlines")
	}
	return validateResources(req.CPUs, req.MemoryMB)
}

func (s *Server) vpsView(r *http.Request, v *store.VPS) vpsView {
	view := vpsView{VPS: v, Apps: []vpsAppRef{}}
	if apps, err := s.store.ListAppsByVPS(r.Context(), v.ID); err == nil {
		for _, a := range apps {
			view.Apps = append(view.Apps, vpsAppRef{ID: a.ID, Name: a.Name, HostPort: a.HostPort})
		}
	}
	if dns, active := s.remoteSnapshot(r.Context()); dns != "" {
		if _, ok := active[v.SSHPort]; ok {
			view.RemoteSSH = fmt.Sprintf("ssh %s@%s -p %d", v.SSHUser, dns, v.SSHPort)
		}
	}
	s.mu.Lock()
	t := s.vpsTasks[v.ID]
	s.mu.Unlock()
	if t != nil && t.log != nil {
		view.ProvisionLog = t.log.tail(8 << 10)
	}
	switch {
	case t != nil && t.running:
		view.State = stateProvisioning
	case t != nil && t.err != "":
		view.State, view.Error = stateError, t.err
	default:
		st, err := s.engine.State(r.Context(), v.ContainerID)
		if err != nil {
			view.State, view.Error = stateError, err.Error()
		} else {
			view.State = st
		}
	}
	return view
}

func (s *Server) handleListVPS(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListVPS(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	out := make([]vpsView, 0, len(list))
	for _, v := range list {
		out = append(out, s.vpsView(r, v))
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleCreateVPS(w http.ResponseWriter, r *http.Request) {
	var req createVPSReq
	if !decode(w, r, &req) {
		return
	}
	if err := req.validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	port, err := s.reservePort(r.Context(), s.cfg.VPSPortMin, s.cfg.VPSPortMax)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	defer s.releasePort(port)
	v := &store.VPS{
		Name: req.Name, SSHUser: req.SSHUser, SSHPort: port, SSHPublicKey: req.SSHPublicKey,
		CPUs: req.CPUs, MemoryMB: req.MemoryMB,
	}
	if err := s.store.CreateVPS(r.Context(), v); err != nil {
		storeError(s, w, err, "VPS")
		return
	}

	s.info(r, "vps.created", subjVPS, v.ID, v.Name, "Created VPS %s (SSH port %d)", v.Name, v.SSHPort)

	// Provision in the background: the first run builds the Ubuntu image,
	// which can take a few minutes.
	task := &taskInfo{running: true, log: &syncBuffer{}}
	s.mu.Lock()
	s.vpsTasks[v.ID] = task
	s.mu.Unlock()
	spec := docker.VPSSpec{
		Name: v.Name, User: v.SSHUser, PublicKey: v.SSHPublicKey, Password: req.Password,
		SSHPort: v.SSHPort, CPUs: v.CPUs, MemoryMB: v.MemoryMB,
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		err := s.provisionVPS(v.ID, spec, task.log)
		s.mu.Lock()
		defer s.mu.Unlock()
		if err != nil {
			s.log.Error("provision VPS", "name", v.Name, "err", err)
			fmt.Fprintf(task.log, "\n[lps] provisioning failed: %v\n", err)
			s.vpsTasks[v.ID] = &taskInfo{err: err.Error(), log: task.log}
			s.fail(nil, "vps.provision_failed", subjVPS, v.ID, v.Name, "VPS %s could not be created: %v", v.Name, err)
		} else {
			delete(s.vpsTasks, v.ID)
			s.info(nil, "vps.ready", subjVPS, v.ID, v.Name, "VPS %s is ready", v.Name)
		}
	}()
	s.kickRemote()
	writeJSON(w, http.StatusAccepted, s.vpsView(r, v))
}

func (s *Server) provisionVPS(id int64, spec docker.VPSSpec, log io.Writer) error {
	ctx := s.ctx
	fmt.Fprintln(log, "[lps] preparing Ubuntu VPS image (first run builds it; this can take a few minutes)")
	if err := s.engine.EnsureVPSImage(ctx, log); err != nil {
		return fmt.Errorf("build VPS image: %w", err)
	}
	cid, err := s.engine.CreateVPS(ctx, spec)
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}
	if err := s.store.SetVPSContainer(ctx, id, cid); err != nil {
		return err
	}
	if err := s.engine.Start(ctx, cid); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	fmt.Fprintf(log, "[lps] VPS started; SSH on port %d\n", spec.SSHPort)
	return nil
}

func (s *Server) loadVPS(w http.ResponseWriter, r *http.Request) (*store.VPS, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, 400, "invalid id")
		return nil, false
	}
	v, err := s.store.GetVPS(r.Context(), id)
	if err != nil {
		storeError(s, w, err, "VPS")
		return nil, false
	}
	return v, true
}

func (s *Server) vpsBusy(w http.ResponseWriter, id int64) bool {
	s.mu.Lock()
	t := s.vpsTasks[id]
	s.mu.Unlock()
	if t != nil && t.running {
		writeError(w, http.StatusConflict, "VPS is still being provisioned")
		return true
	}
	return false
}

func (s *Server) handleGetVPS(w http.ResponseWriter, r *http.Request) {
	if v, ok := s.loadVPS(w, r); ok {
		writeJSON(w, 200, s.vpsView(r, v))
	}
}

func (s *Server) handleVPSAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, ok := s.loadVPS(w, r)
		if !ok || s.vpsBusy(w, v.ID) {
			return
		}
		if v.ContainerID == "" {
			writeError(w, http.StatusConflict, "VPS has no container (provisioning failed); delete and recreate it")
			return
		}
		var err error
		if action == "start" {
			err = s.engine.Start(r.Context(), v.ContainerID)
		} else {
			err = s.engine.Stop(r.Context(), v.ContainerID)
		}
		if err != nil {
			writeError(w, http.StatusBadGateway, action+" failed: "+err.Error())
			return
		}
		if action == "start" {
			s.kickVPSApps() // bring its apps back up
			s.info(r, "vps.started", subjVPS, v.ID, v.Name, "Started VPS %s", v.Name)
		} else {
			s.info(r, "vps.stopped", subjVPS, v.ID, v.Name, "Stopped VPS %s", v.Name)
		}
		writeJSON(w, 200, s.vpsView(r, v))
	}
}

// handleDeleteVPS removes the container and, unless ?keepData=true, its /home volume.
func (s *Server) handleDeleteVPS(w http.ResponseWriter, r *http.Request) {
	v, ok := s.loadVPS(w, r)
	if !ok || s.vpsBusy(w, v.ID) {
		return
	}
	if apps, err := s.store.ListAppsByVPS(r.Context(), v.ID); err != nil {
		s.internalError(w, err)
		return
	} else if len(apps) > 0 {
		names := make([]string, len(apps))
		for i, a := range apps {
			names[i] = a.Name
		}
		writeError(w, http.StatusConflict, "this VPS runs apps; delete them first: "+strings.Join(names, ", "))
		return
	}
	if err := s.engine.Remove(r.Context(), v.ContainerID); err != nil {
		writeError(w, http.StatusBadGateway, "remove container: "+err.Error())
		return
	}
	// Containers may exist without an ID recorded if provisioning died midway.
	_ = s.engine.Remove(r.Context(), docker.VPSContainerName(v.Name))
	if r.URL.Query().Get("keepData") != "true" {
		if err := s.engine.RemoveVolume(r.Context(), docker.VPSVolumeName(v.Name)); err != nil {
			writeError(w, http.StatusBadGateway, "remove volume: "+err.Error())
			return
		}
	}
	if err := s.store.DeleteVPS(r.Context(), v.ID); err != nil {
		s.internalError(w, err)
		return
	}
	s.mu.Lock()
	delete(s.vpsTasks, v.ID)
	s.mu.Unlock()
	if r.URL.Query().Get("keepData") == "true" {
		s.warn(r, "vps.deleted", subjVPS, v.ID, v.Name, "Deleted VPS %s (kept its /home volume)", v.Name)
	} else {
		s.warn(r, "vps.deleted", subjVPS, v.ID, v.Name, "Deleted VPS %s and its /home volume", v.Name)
	}
	s.kickRemote()
	w.WriteHeader(http.StatusNoContent)
}

func tailParam(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("tail"))
	if err != nil || n <= 0 || n > 5000 {
		return 200
	}
	return n
}

func (s *Server) handleVPSLogs(w http.ResponseWriter, r *http.Request) {
	v, ok := s.loadVPS(w, r)
	if !ok {
		return
	}
	s.writeLogs(w, r, v.ContainerID)
}

func (s *Server) writeLogs(w http.ResponseWriter, r *http.Request, containerID string) {
	if containerID == "" {
		writeJSON(w, 200, map[string]string{"logs": ""})
		return
	}
	logs, err := s.engine.Logs(r.Context(), containerID, tailParam(r))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"logs": logs})
}
