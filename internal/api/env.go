package api

// Environment variables and secrets for apps.

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/abrardw/lps/internal/store"
)

var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

const (
	maxEnvVars  = 100
	maxEnvValue = 32 << 10
)

// envView is a variable as the API returns it: secret values are left out.
type envView struct {
	Name   string `json:"name"`
	Value  string `json:"value,omitempty"`
	Secret bool   `json:"secret"`
	// HasValue tells the UI a secret is set even though its value is hidden.
	HasValue bool `json:"hasValue"`
}

// envInput is a variable as the UI sends it. For a secret, an empty value
// with Keep set means "keep the stored value" (the UI never sees it).
type envInput struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
	Keep   bool   `json:"keep"`
}

// reservedEnv reports names LPS sets itself for apps that run in a VPS.
func reservedEnv(name string, inVPS bool) bool {
	if strings.HasPrefix(name, "LPS_") {
		return true
	}
	return inVPS && (name == "PORT" || name == "HOST" || name == "HOME")
}

// validateEnv checks the new variables and resolves "keep" secrets against
// the stored ones.
func validateEnv(in []envInput, old []store.EnvVar, inVPS bool) ([]store.EnvVar, error) {
	if len(in) > maxEnvVars {
		return nil, fmt.Errorf("at most %d environment variables", maxEnvVars)
	}
	prev := map[string]store.EnvVar{}
	for _, v := range old {
		prev[v.Name] = v
	}
	seen := map[string]bool{}
	out := make([]store.EnvVar, 0, len(in))
	for _, v := range in {
		name := strings.TrimSpace(v.Name)
		switch {
		case !envNameRe.MatchString(name):
			return nil, fmt.Errorf("%q is not a valid variable name: use letters, digits and _, not starting with a digit", name)
		case seen[name]:
			return nil, fmt.Errorf("%s is listed twice", name)
		case reservedEnv(name, inVPS):
			if strings.HasPrefix(name, "LPS_") {
				return nil, fmt.Errorf("%s: names starting with LPS_ are reserved for LPS", name)
			}
			return nil, fmt.Errorf("%s is set by LPS for apps in a VPS (PORT, HOST and HOME)", name)
		case len(v.Value) > maxEnvValue:
			return nil, fmt.Errorf("%s: value is too long (max %d KB)", name, maxEnvValue>>10)
		case strings.ContainsRune(v.Value, 0):
			return nil, fmt.Errorf("%s: value must not contain NUL characters", name)
		}
		seen[name] = true
		value := v.Value
		if v.Secret && v.Keep && value == "" {
			p, ok := prev[name]
			if !ok || !p.Secret {
				return nil, fmt.Errorf("%s: enter a value for this secret", name)
			}
			value = p.Value
		}
		out = append(out, store.EnvVar{Name: name, Value: value, Secret: v.Secret})
	}
	return out, nil
}

func envViews(vars []store.EnvVar) []envView {
	out := make([]envView, 0, len(vars))
	for _, v := range vars {
		ev := envView{Name: v.Name, Secret: v.Secret, HasValue: v.Value != ""}
		if !v.Secret {
			ev.Value = v.Value
		}
		out = append(out, ev)
	}
	return out
}

// envList turns variables into NAME=value pairs for Docker.
func envList(vars []store.EnvVar) []string {
	out := make([]string, 0, len(vars))
	for _, v := range vars {
		out = append(out, v.Name+"="+v.Value)
	}
	return out
}

func (s *Server) handleGetAppEnv(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	vars, err := s.store.AppEnv(r.Context(), a.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, envViews(vars))
}

// handleSetAppEnv replaces an app's variables, then redeploys so they apply.
func (s *Server) handleSetAppEnv(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	var req struct {
		Vars []envInput `json:"vars"`
	}
	if !decode(w, r, &req) {
		return
	}
	old, err := s.store.AppEnv(r.Context(), a.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	vars, err := validateEnv(req.Vars, old, a.InVPS())
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := s.store.SetAppEnv(r.Context(), a.ID, vars); err != nil {
		s.internalError(w, err)
		return
	}
	s.info(r, "app.env", subjApp, a.ID, a.Name, "Environment of %s changed: %s", a.Name, envChanges(old, vars))
	if _, err := s.startDeploy(a); errors.Is(err, errDeployInProgress) {
		writeError(w, http.StatusConflict, "saved, but a deployment is already running; redeploy when it finishes")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, envViews(vars))
}

// envChanges summarises what changed by name only: values (secret or not)
// never go into the activity feed.
func envChanges(old, cur []store.EnvVar) string {
	before := map[string]store.EnvVar{}
	for _, v := range old {
		before[v.Name] = v
	}
	var added, changed, removed []string
	for _, v := range cur {
		p, ok := before[v.Name]
		switch {
		case !ok:
			added = append(added, v.Name)
		case p.Value != v.Value || p.Secret != v.Secret:
			changed = append(changed, v.Name)
		}
		delete(before, v.Name)
	}
	for name := range before {
		removed = append(removed, name)
	}
	sort.Strings(removed)
	var parts []string
	if len(added) > 0 {
		parts = append(parts, "added "+strings.Join(added, ", "))
	}
	if len(changed) > 0 {
		parts = append(parts, "changed "+strings.Join(changed, ", "))
	}
	if len(removed) > 0 {
		parts = append(parts, "removed "+strings.Join(removed, ", "))
	}
	if len(parts) == 0 {
		return "no changes"
	}
	return strings.Join(parts, "; ")
}
