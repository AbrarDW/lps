package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/abrardw/lps/internal/builder"
	"github.com/abrardw/lps/internal/docker"
	"github.com/abrardw/lps/internal/store"
)

type appView struct {
	*store.App
	State      string            `json:"state"`
	Error      string            `json:"error,omitempty"`
	Deploying  bool              `json:"deploying"`
	LastDeploy *store.Deployment `json:"lastDeploy,omitempty"`
	// Remote URLs, set when remote access is active for this app.
	RemoteURL string `json:"remoteUrl,omitempty"`
	PublicURL string `json:"publicUrl,omitempty"`
}

type createAppReq struct {
	Name          string  `json:"name"`
	SourcePath    string  `json:"sourcePath"`
	ContainerPort int     `json:"containerPort"`
	CPUs          float64 `json:"cpus"`
	MemoryMB      int     `json:"memoryMb"`
}

func (req *createAppReq) validate() error {
	if !nameRe.MatchString(req.Name) {
		return errors.New("name must be 2-31 chars: lowercase letters, digits and '-'")
	}
	if !filepath.IsAbs(req.SourcePath) {
		return errors.New("sourcePath must be an absolute path on the host PC (e.g. C:\\projects\\my-app)")
	}
	req.SourcePath = filepath.Clean(req.SourcePath)
	if err := builder.Validate(req.SourcePath); err != nil {
		return err
	}
	if req.ContainerPort < 1 || req.ContainerPort > 65535 {
		return errors.New("containerPort must be 1-65535 (the port your app listens on inside the container)")
	}
	return validateResources(req.CPUs, req.MemoryMB)
}

func (s *Server) appView(r *http.Request, a *store.App) appView {
	view := appView{App: a}
	if dns, active := s.remoteSnapshot(r.Context()); dns != "" {
		if e, ok := active[a.HostPort]; ok {
			view.RemoteURL = remoteURL(dns, toExposure(e))
		}
		if e, ok := active[a.PublicPort]; ok && a.PublicPort > 0 && e.Public {
			view.PublicURL = remoteURL(dns, toExposure(e))
		}
	}
	s.mu.Lock()
	view.Deploying = s.deploying[a.ID]
	s.mu.Unlock()
	if ds, err := s.store.ListDeployments(r.Context(), a.ID, 1); err == nil && len(ds) > 0 {
		d := ds[0]
		d.Log = "" // full logs via /deployments
		view.LastDeploy = d
	}
	st, err := s.engine.State(r.Context(), a.ContainerID)
	if err != nil {
		view.State, view.Error = stateError, err.Error()
	} else {
		view.State = st
	}
	return view
}

func (s *Server) handleListApps(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListApps(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	out := make([]appView, 0, len(list))
	for _, a := range list {
		out = append(out, s.appView(r, a))
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleCreateApp(w http.ResponseWriter, r *http.Request) {
	var req createAppReq
	if !decode(w, r, &req) {
		return
	}
	if err := req.validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	port, err := s.reservePort(r.Context(), s.cfg.AppPortMin, s.cfg.AppPortMax)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	defer s.releasePort(port)
	a := &store.App{
		Name: req.Name, SourcePath: req.SourcePath, ContainerPort: req.ContainerPort,
		HostPort: port, CPUs: req.CPUs, MemoryMB: req.MemoryMB,
	}
	if err := s.store.CreateApp(r.Context(), a); err != nil {
		storeError(s, w, err, "app")
		return
	}
	if _, err := s.startDeploy(a); err != nil {
		s.internalError(w, err)
		return
	}
	s.kickRemote()
	writeJSON(w, http.StatusAccepted, s.appView(r, a))
}

func (s *Server) loadApp(w http.ResponseWriter, r *http.Request) (*store.App, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, 400, "invalid id")
		return nil, false
	}
	a, err := s.store.GetApp(r.Context(), id)
	if err != nil {
		storeError(s, w, err, "app")
		return nil, false
	}
	return a, true
}

func (s *Server) handleGetApp(w http.ResponseWriter, r *http.Request) {
	if a, ok := s.loadApp(w, r); ok {
		writeJSON(w, 200, s.appView(r, a))
	}
}

var errDeployInProgress = errors.New("a deployment is already in progress")

// startDeploy records a deployment and builds+runs it in the background.
func (s *Server) startDeploy(a *store.App) (*store.Deployment, error) {
	s.mu.Lock()
	if s.deploying[a.ID] {
		s.mu.Unlock()
		return nil, errDeployInProgress
	}
	s.deploying[a.ID] = true
	s.mu.Unlock()

	d, err := s.store.CreateDeployment(s.ctx, a.ID)
	if err != nil {
		s.mu.Lock()
		delete(s.deploying, a.ID)
		s.mu.Unlock()
		return nil, err
	}
	buf := &syncBuffer{}
	s.mu.Lock()
	s.liveLogs[d.ID] = buf
	s.mu.Unlock()

	// The goroutine gets its own copy: the caller keeps using a (e.g. to
	// encode the HTTP response) while the deploy updates ContainerID.
	app := *a
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		a := &app
		image, err := s.runDeploy(a, d, buf)
		status := store.DeployRunning
		if err != nil {
			fmt.Fprintf(buf, "\n[lps] deployment failed: %v\n", err)
			status = store.DeployFailed
		} else {
			fmt.Fprintf(buf, "\n[lps] deployed; serving on %s:%d\n", s.cfg.BindIP, a.HostPort)
		}
		if ferr := s.store.FinishDeployment(s.ctx, d.ID, status, image, buf.String()); ferr != nil {
			s.log.Error("finish deployment", "err", ferr)
		}
		s.mu.Lock()
		delete(s.deploying, a.ID)
		delete(s.liveLogs, d.ID)
		s.mu.Unlock()
	}()
	return d, nil
}

func (s *Server) runDeploy(a *store.App, d *store.Deployment, log io.Writer) (string, error) {
	ctx := s.ctx
	if err := builder.Validate(a.SourcePath); err != nil {
		return "", err
	}
	image := fmt.Sprintf("lps-app-%s:%d", a.Name, d.ID)
	fmt.Fprintf(log, "[lps] building %s from %s\n", image, a.SourcePath)

	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(builder.TarDir(a.SourcePath, pw)) }()
	err := s.engine.BuildImage(ctx, pr, image, log)
	pr.CloseWithError(err) // unblock the tar writer if the build stopped early
	if err != nil {
		return "", fmt.Errorf("build: %w", err)
	}

	fmt.Fprintln(log, "[lps] replacing container")
	if err := s.engine.Remove(ctx, a.ContainerID); err != nil {
		return image, fmt.Errorf("remove old container: %w", err)
	}
	_ = s.engine.Remove(ctx, docker.AppContainerName(a.Name))
	cid, err := s.engine.CreateApp(ctx, docker.AppSpec{
		Name: a.Name, Image: image, ContainerPort: a.ContainerPort, HostPort: a.HostPort,
		CPUs: a.CPUs, MemoryMB: a.MemoryMB,
	})
	if err != nil {
		return image, fmt.Errorf("create container: %w", err)
	}
	if err := s.store.SetAppContainer(ctx, a.ID, cid); err != nil {
		return image, err
	}
	a.ContainerID = cid
	if err := s.engine.Start(ctx, cid); err != nil {
		return image, fmt.Errorf("start: %w", err)
	}
	return image, nil
}

func (s *Server) handleDeployApp(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	d, err := s.startDeploy(a)
	if errors.Is(err, errDeployInProgress) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, d)
}

func (s *Server) handleAppAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, ok := s.loadApp(w, r)
		if !ok {
			return
		}
		if a.ContainerID == "" {
			writeError(w, http.StatusConflict, "app has not been deployed successfully yet")
			return
		}
		var err error
		if action == "start" {
			err = s.engine.Start(r.Context(), a.ContainerID)
		} else {
			err = s.engine.Stop(r.Context(), a.ContainerID)
		}
		if err != nil {
			writeError(w, http.StatusBadGateway, action+" failed: "+err.Error())
			return
		}
		writeJSON(w, 200, s.appView(r, a))
	}
}

func (s *Server) handleDeleteApp(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	busy := s.deploying[a.ID]
	s.mu.Unlock()
	if busy {
		writeError(w, http.StatusConflict, errDeployInProgress.Error())
		return
	}
	if err := s.engine.Remove(r.Context(), a.ContainerID); err != nil {
		writeError(w, http.StatusBadGateway, "remove container: "+err.Error())
		return
	}
	_ = s.engine.Remove(r.Context(), docker.AppContainerName(a.Name))
	if err := s.store.DeleteApp(r.Context(), a.ID); err != nil {
		s.internalError(w, err)
		return
	}
	s.kickRemote()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAppLogs(w http.ResponseWriter, r *http.Request) {
	if a, ok := s.loadApp(w, r); ok {
		s.writeLogs(w, r, a.ContainerID)
	}
}

func (s *Server) handleListDeployments(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	ds, err := s.store.ListDeployments(r.Context(), a.ID, 20)
	if err != nil {
		s.internalError(w, err)
		return
	}
	s.mu.Lock()
	for _, d := range ds {
		if buf, ok := s.liveLogs[d.ID]; ok {
			d.Log = buf.String()
		}
	}
	s.mu.Unlock()
	writeJSON(w, 200, ds)
}
