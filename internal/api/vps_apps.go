package api

// Apps that run inside an LPS VPS instead of their own container.
//
// LPS copies the code into the VPS (or uses a folder already there), installs
// packages and dependencies, and runs the start command under the lps-app
// helper as the VPS's SSH user. A small relay container publishes the app's
// port on the host, so local, tailnet and public links work as for any app.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/abrardw/lps/internal/builder"
	"github.com/abrardw/lps/internal/docker"
	"github.com/abrardw/lps/internal/store"
)

var pkgRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]*$`)

const (
	maxCommand  = 4 << 10
	maxPackages = 40
	// vpsAppsInterval is how often LPS restarts VPS apps that stopped
	// (crashed, or the VPS restarted).
	vpsAppsInterval = 30 * time.Second
	// listenWait is how long a deploy waits for the app to open its port.
	listenWait = 20 * time.Second
)

// installPackagesScript installs only the packages that are missing ("$@").
const installPackagesScript = `missing=""
for p in "$@"; do dpkg -s "$p" >/dev/null 2>&1 || missing="$missing $p"; done
if [ -z "$missing" ]; then echo "[lps] system packages already installed"; exit 0; fi
echo "[lps] installing:$missing"
apt-get update -q && apt-get install -y -q --no-install-recommends $missing`

// validateRunSetup normalises and checks a VPS app's packages and commands.
func validateRunSetup(packages, install, start string) (string, string, string, error) {
	pk := strings.Fields(packages)
	if len(pk) > maxPackages {
		return "", "", "", fmt.Errorf("at most %d system packages", maxPackages)
	}
	for _, p := range pk {
		if !pkgRe.MatchString(p) {
			return "", "", "", fmt.Errorf("%q is not a valid Ubuntu package name", p)
		}
	}
	install, start = strings.TrimSpace(install), strings.TrimSpace(start)
	if start == "" {
		return "", "", "", errors.New("startCmd is required: the command that runs your app (e.g. npm start)")
	}
	for _, c := range []string{install, start} {
		if len(c) > maxCommand {
			return "", "", "", fmt.Errorf("commands must be under %d KB", maxCommand>>10)
		}
		if strings.ContainsRune(c, 0) {
			return "", "", "", errors.New("commands must not contain NUL characters")
		}
	}
	return strings.Join(pk, " "), install, start, nil
}

// cleanVPSPath checks a folder path inside the VPS: absolute, or relative to
// the user's home with "~/".
func cleanVPSPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if strings.ContainsAny(p, "\x00\r\n") {
		return "", errors.New("vpsPath must be a single line")
	}
	rel := strings.HasPrefix(p, "~/")
	if !rel && !strings.HasPrefix(p, "/") {
		return "", errors.New("vpsPath must be absolute (/srv/site) or start with ~/ (~/my-site)")
	}
	if rel {
		p = "~/" + strings.TrimPrefix(path.Clean("/"+p[2:]), "/")
	} else {
		p = path.Clean(p)
	}
	if p == "/" || p == "~/" {
		return "", errors.New("vpsPath must be a project folder, not / or your home folder")
	}
	return p, nil
}

func (req *createAppReq) validateVPS() error {
	switch req.SourceKind {
	case "", store.SourcePC:
		req.SourceKind = store.SourcePC
		var err error
		if req.SourcePath, err = cleanSourcePath(req.SourcePath); err != nil {
			return err
		}
		req.VPSPath = ""
	case store.SourceVPS:
		var err error
		if req.VPSPath, err = cleanVPSPath(req.VPSPath); err != nil {
			return err
		}
		req.SourcePath = ""
	default:
		return errors.New(`sourceKind must be "pc" or "vps"`)
	}
	var err error
	if req.Packages, req.InstallCmd, req.StartCmd, err = validateRunSetup(req.Packages, req.InstallCmd, req.StartCmd); err != nil {
		return err
	}
	if req.ContainerPort < 1 || req.ContainerPort > 65535 {
		return errors.New("containerPort must be 1-65535 (the port your app listens on inside the VPS)")
	}
	if req.ContainerPort == 22 {
		return errors.New("port 22 is the VPS's SSH server; pick another port for your app")
	}
	// The VPS's own CPU/memory limits apply.
	req.CPUs, req.MemoryMB, req.Dockerfile, req.SaveToProject = 0, 0, "", false
	return nil
}

// vpsAppDir is the app's folder inside the VPS.
func vpsAppDir(a *store.App, v *store.VPS) string {
	home := "/home/" + v.SSHUser
	if a.SourceKind == store.SourceVPS {
		if strings.HasPrefix(a.VPSPath, "~/") {
			return home + a.VPSPath[1:]
		}
		return a.VPSPath
	}
	return home + "/apps/" + a.Name
}

func userEnv(v *store.VPS) []string {
	return []string{"HOME=/home/" + v.SSHUser, "USER=" + v.SSHUser, "LANG=C.UTF-8"}
}

// vpsExec runs a command in the VPS and turns a non-zero exit into an error.
func (s *Server) vpsExec(ctx context.Context, v *store.VPS, user, dir string, cmd, env []string, out io.Writer) error {
	code, err := s.engine.Exec(ctx, v.ContainerID, user, dir, cmd, env, out)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("exit status %d", code)
	}
	return nil
}

// helper runs lps-app as the VPS user and returns its output.
func (s *Server) helper(ctx context.Context, v *store.VPS, a *store.App, args ...string) (string, error) {
	var out bytes.Buffer
	var env []string
	if args[0] == "start" {
		// The app's own variables go first so LPS's (HOME, PORT, LPS_*) win;
		// validateEnv also refuses those names.
		vars, err := s.store.AppEnv(ctx, a.ID)
		if err != nil {
			return "", err
		}
		env = envList(vars)
	}
	env = append(env, userEnv(v)...)
	env = append(env,
		"LPS_DIR="+vpsAppDir(a, v),
		"LPS_PORT="+strconv.Itoa(a.ContainerPort),
		"LPS_START="+a.StartCmd)
	cmd := append([]string{docker.AppHelperPath, args[0], a.Name}, args[1:]...)
	err := s.vpsExec(ctx, v, v.SSHUser, "/", cmd, env, &out)
	if err != nil {
		return out.String(), fmt.Errorf("lps-app %s: %w: %s", args[0], err, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}

// runningVPS loads an app's VPS and checks that it is running.
func (s *Server) runningVPS(ctx context.Context, a *store.App) (*store.VPS, error) {
	v, err := s.store.GetVPS(ctx, a.VPSID)
	if err != nil {
		return nil, fmt.Errorf("the app's VPS: %w", err)
	}
	st, err := s.engine.State(ctx, v.ContainerID)
	if err != nil {
		return v, err
	}
	if st != docker.StateRunning {
		return v, fmt.Errorf("VPS %s is not running; start it first", v.Name)
	}
	return v, nil
}

// runVPSDeploy deploys an app into its VPS; the returned label is stored as
// the deployment's "image".
func (s *Server) runVPSDeploy(a *store.App, log io.Writer) (string, error) {
	ctx := s.ctx
	v, err := s.runningVPS(ctx, a)
	if err != nil {
		return "", err
	}
	label := "VPS " + v.Name
	dir, user := vpsAppDir(a, v), v.SSHUser
	fmt.Fprintf(log, "[lps] deploying into VPS %s (%s)\n", v.Name, dir)

	if err := s.engine.InstallAppHelper(ctx, v.ContainerID); err != nil {
		return label, fmt.Errorf("install lps-app helper: %w", err)
	}
	if pk := strings.Fields(a.Packages); len(pk) > 0 {
		fmt.Fprintf(log, "[lps] checking system packages: %s\n", a.Packages)
		cmd := append([]string{"sh", "-c", installPackagesScript, "sh"}, pk...)
		if err := s.vpsExec(ctx, v, "root", "/", cmd, []string{"DEBIAN_FRONTEND=noninteractive"}, log); err != nil {
			return label, fmt.Errorf("installing system packages failed (%w)", err)
		}
	}

	staging := ""
	if a.SourceKind == store.SourceVPS {
		if err := s.vpsExec(ctx, v, user, "/", []string{"test", "-d", dir}, nil, nil); err != nil {
			return label, fmt.Errorf("folder %s does not exist in VPS %s", dir, v.Name)
		}
		fmt.Fprintf(log, "[lps] using the code already in %s\n", dir)
	} else {
		if err := builder.ValidateDir(a.SourcePath); err != nil {
			return label, err
		}
		staging = dir + ".new"
		home := "/home/" + user
		if err := s.vpsExec(ctx, v, "root", "/", []string{"sh", "-c", `rm -rf "$1" && mkdir -p "$1" && chown "$2:$2" "$3"`,
			"sh", staging, user, home + "/apps"}, nil, log); err != nil {
			return label, fmt.Errorf("prepare %s: %w", staging, err)
		}
		fmt.Fprintf(log, "[lps] copying %s into the VPS\n", a.SourcePath)
		pr, pw := io.Pipe()
		go func() { pw.CloseWithError(builder.TarDir(a.SourcePath, pw, nil)) }()
		err := s.engine.CopyTo(ctx, v.ContainerID, staging, pr)
		pr.CloseWithError(err)
		if err != nil {
			return label, fmt.Errorf("copy project: %w", err)
		}
		if err := s.vpsExec(ctx, v, "root", "/", []string{"chown", "-R", user + ":" + user, staging}, nil, log); err != nil {
			return label, fmt.Errorf("chown: %w", err)
		}
	}

	fmt.Fprintln(log, "[lps] stopping the running version")
	if _, err := s.helper(ctx, v, a, "stop"); err != nil {
		return label, err
	}
	if addrs := s.listening(ctx, v, a.ContainerPort); len(addrs) > 0 {
		return label, fmt.Errorf("port %d is already in use inside VPS %s (%s) by a process LPS did not start. "+
			"Stop it (e.g. find it with: sudo ss -ltnp) or pick another port", a.ContainerPort, v.Name, strings.Join(addrs, ", "))
	}
	if staging != "" {
		if err := s.vpsExec(ctx, v, "root", "/", []string{"sh", "-c", `rm -rf "$1" && mv "$2" "$1"`, "sh", dir, staging}, nil, log); err != nil {
			return label, fmt.Errorf("replace %s: %w", dir, err)
		}
	}
	if a.InstallCmd != "" {
		fmt.Fprintf(log, "[lps] $ %s\n", a.InstallCmd)
		vars, err := s.store.AppEnv(ctx, a.ID)
		if err != nil {
			return label, err
		}
		env := append(envList(vars), userEnv(v)...)
		env = append(env, "PORT="+strconv.Itoa(a.ContainerPort))
		if err := s.vpsExec(ctx, v, user, dir, []string{"bash", "-lc", a.InstallCmd}, env, log); err != nil {
			return label, fmt.Errorf("install command failed (%w)", err)
		}
	}

	relay, err := s.ensureRelay(ctx, a, v)
	if err != nil {
		return label, fmt.Errorf("relay: %w", err)
	}
	fmt.Fprintf(log, "[lps] $ %s   (PORT=%d)\n", a.StartCmd, a.ContainerPort)
	if _, err := s.helper(ctx, v, a, "start"); err != nil {
		return label, err
	}
	if err := s.store.SetAppWantRunning(ctx, a.ID, true); err != nil {
		return label, err
	}
	if err := s.store.SetAppContainer(ctx, a.ID, relay); err != nil {
		return label, err
	}
	a.ContainerID, a.WantRunning = relay, true
	return label, s.waitListening(ctx, a, v, log)
}

// ensureRelay (re)creates and starts the container publishing the app's port.
func (s *Server) ensureRelay(ctx context.Context, a *store.App, v *store.VPS) (string, error) {
	if err := s.engine.EnsureNetwork(ctx, v.ContainerID); err != nil {
		return "", err
	}
	name := docker.RelayContainerName(a.Name)
	if err := s.engine.Remove(ctx, name); err != nil {
		return "", err
	}
	id, err := s.engine.CreateRelay(ctx, docker.RelaySpec{App: a.Name, VPS: v.Name, Port: a.ContainerPort, HostPort: a.HostPort})
	if err != nil {
		return "", err
	}
	return id, s.engine.Start(ctx, id)
}

// listening returns the addresses with a TCP listener on port inside the VPS.
func (s *Server) listening(ctx context.Context, v *store.VPS, port int) []string {
	var out bytes.Buffer
	if err := s.vpsExec(ctx, v, "root", "/", []string{"ss", "-ltnH"}, nil, &out); err != nil {
		return nil
	}
	var addrs []string
	suffix := ":" + strconv.Itoa(port)
	for _, line := range strings.Split(out.String(), "\n") {
		f := strings.Fields(line)
		if len(f) >= 4 && strings.HasSuffix(f[3], suffix) {
			addrs = append(addrs, f[3])
		}
	}
	return addrs
}

func loopbackOnly(addrs []string) bool {
	for _, a := range addrs {
		if !strings.HasPrefix(a, "127.") && !strings.HasPrefix(a, "[::1]") {
			return false
		}
	}
	return len(addrs) > 0
}

// waitListening waits for the app to open its port and reports common mistakes.
func (s *Server) waitListening(ctx context.Context, a *store.App, v *store.VPS, log io.Writer) error {
	deadline := time.Now().Add(listenWait)
	for {
		if addrs := s.listening(ctx, v, a.ContainerPort); len(addrs) > 0 {
			if loopbackOnly(addrs) {
				fmt.Fprintf(log, "[lps] warning: the app listens on %s only, which LPS cannot reach.\n"+
					"[lps] Make it listen on 0.0.0.0 (all interfaces), e.g. with --host 0.0.0.0.\n", strings.Join(addrs, ", "))
			} else {
				fmt.Fprintf(log, "[lps] the app is listening on port %d\n", a.ContainerPort)
			}
			return nil
		}
		if st, _ := s.helper(ctx, v, a, "status"); strings.TrimSpace(st) != "running" {
			logs, _ := s.helper(ctx, v, a, "logs", "40")
			fmt.Fprintf(log, "[lps] the app exited. Its last output:\n%s", logs)
			return errors.New("the start command exited; check the output above and the App logs tab")
		}
		if time.Now().After(deadline) {
			fmt.Fprintf(log, "[lps] warning: nothing is listening on port %d yet. If your app uses another port, change it,\n"+
				"[lps] or make it read the PORT environment variable.\n", a.ContainerPort)
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// vpsAppState reports a VPS app's state without touching the relay.
func (s *Server) vpsAppState(ctx context.Context, a *store.App) (string, error) {
	if a.ContainerID == "" {
		return docker.StateMissing, nil
	}
	v, err := s.store.GetVPS(ctx, a.VPSID)
	if err != nil {
		return "", err
	}
	if st, err := s.engine.State(ctx, v.ContainerID); err != nil || st != docker.StateRunning {
		return docker.StateStopped, err
	}
	out, err := s.helper(ctx, v, a, "status")
	if err != nil {
		return docker.StateStopped, nil // helper missing: never started in this VPS
	}
	if strings.TrimSpace(out) == "running" {
		return docker.StateRunning, nil
	}
	return docker.StateStopped, nil
}

// startVPSApp starts a deployed app (and its relay) inside its running VPS.
func (s *Server) startVPSApp(ctx context.Context, a *store.App) error {
	v, err := s.runningVPS(ctx, a)
	if err != nil {
		return err
	}
	if err := s.engine.InstallAppHelper(ctx, v.ContainerID); err != nil {
		return err
	}
	if st, _ := s.engine.State(ctx, a.ContainerID); st == docker.StateMissing {
		id, err := s.ensureRelay(ctx, a, v)
		if err != nil {
			return err
		}
		if err := s.store.SetAppContainer(ctx, a.ID, id); err != nil {
			return err
		}
		a.ContainerID = id
	} else if st != docker.StateRunning {
		if err := s.engine.Start(ctx, a.ContainerID); err != nil {
			return err
		}
	}
	_, err = s.helper(ctx, v, a, "start")
	return err
}

func (s *Server) vpsAppAction(ctx context.Context, a *store.App, action string) error {
	if action == "start" {
		if err := s.store.SetAppWantRunning(ctx, a.ID, true); err != nil {
			return err
		}
		a.WantRunning = true
		return s.startVPSApp(ctx, a)
	}
	if err := s.store.SetAppWantRunning(ctx, a.ID, false); err != nil {
		return err
	}
	a.WantRunning = false
	v, err := s.runningVPS(ctx, a)
	if err != nil {
		return nil // a stopped VPS runs nothing
	}
	_, err = s.helper(ctx, v, a, "stop")
	return err
}

// removeVPSApp stops the app and removes its relay. The code folder in the
// VPS is kept.
func (s *Server) removeVPSApp(ctx context.Context, a *store.App) error {
	if v, err := s.runningVPS(ctx, a); err == nil {
		_, _ = s.helper(ctx, v, a, "stop")
	}
	if err := s.engine.Remove(ctx, a.ContainerID); err != nil {
		return err
	}
	return s.engine.Remove(ctx, docker.RelayContainerName(a.Name))
}

func (s *Server) vpsAppLogs(ctx context.Context, a *store.App, tail int) (string, error) {
	v, err := s.runningVPS(ctx, a)
	if err != nil {
		return "", err
	}
	return s.helper(ctx, v, a, "logs", strconv.Itoa(tail))
}

// kickVPSApps schedules a pass that restarts stopped VPS apps (non-blocking).
func (s *Server) kickVPSApps() {
	select {
	case s.vpsAppsKick <- struct{}{}:
	default:
	}
}

func (s *Server) vpsAppsLoop() {
	defer s.wg.Done()
	t := time.NewTicker(vpsAppsInterval)
	defer t.Stop()
	for {
		s.reconcileVPSApps(s.ctx)
		select {
		case <-s.ctx.Done():
			return
		case <-s.vpsAppsKick:
		case <-t.C:
		}
	}
}

// reconcileVPSApps restarts VPS apps that should run but don't: after the
// VPS restarted, lpsd restarted, or the app crashed.
func (s *Server) reconcileVPSApps(ctx context.Context) {
	apps, err := s.store.ListApps(ctx)
	if err != nil {
		return
	}
	for _, a := range apps {
		if !a.InVPS() || !a.WantRunning || a.ContainerID == "" {
			continue
		}
		s.mu.Lock()
		busy := s.deploying[a.ID]
		s.mu.Unlock()
		if busy {
			continue
		}
		if _, err := s.runningVPS(ctx, a); err != nil {
			continue // the app comes back when its VPS starts
		}
		appState, err := s.vpsAppState(ctx, a)
		relayState, _ := s.engine.State(ctx, a.ContainerID)
		if err == nil && appState == docker.StateRunning && relayState == docker.StateRunning {
			continue
		}
		if err := s.startVPSApp(ctx, a); err != nil {
			s.log.Warn("restart VPS app", "app", a.Name, "err", err)
			s.fail(nil, "app.restart_failed", subjApp, a.ID, a.Name, "Could not restart %s: %s", a.Name, firstLine(err.Error()))
		} else {
			s.log.Info("restarted VPS app", "app", a.Name)
			s.warn(nil, "app.restarted", subjApp, a.ID, a.Name, "Restarted %s, which had stopped", a.Name)
		}
	}
}

// handleSetAppRunConfig replaces a VPS app's packages and commands, then redeploys.
func (s *Server) handleSetAppRunConfig(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	if !a.InVPS() {
		writeError(w, 400, "this app runs in its own container; edit its Dockerfile instead")
		return
	}
	var req struct {
		Packages   string `json:"packages"`
		InstallCmd string `json:"installCmd"`
		StartCmd   string `json:"startCmd"`
	}
	if !decode(w, r, &req) {
		return
	}
	pk, install, start, err := validateRunSetup(req.Packages, req.InstallCmd, req.StartCmd)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := s.store.SetAppRunConfig(r.Context(), a.ID, pk, install, start); err != nil {
		s.internalError(w, err)
		return
	}
	a.Packages, a.InstallCmd, a.StartCmd = pk, install, start
	s.info(r, "app.run_config", subjApp, a.ID, a.Name, "Run setup of %s changed", a.Name)
	if _, err := s.startDeploy(a); errors.Is(err, errDeployInProgress) {
		writeError(w, http.StatusConflict, "saved, but a deployment is already running; redeploy when it finishes")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, s.appView(r, a))
}

// handleAppTemplates lists the run setups for code that is already in a VPS
// (LPS cannot look inside that folder, so the user picks one).
func (s *Server) handleAppTemplates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, builder.Templates(""))
}
