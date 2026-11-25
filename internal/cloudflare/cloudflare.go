// Package cloudflare is a small client for the parts of the Cloudflare API
// LPS uses to give apps addresses on the user's own domain: zones, Cloudflare
// Tunnels (remotely managed) and DNS records.
package cloudflare

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL is Cloudflare's API.
const DefaultBaseURL = "https://api.cloudflare.com/client/v4"

// RecordComment marks DNS records LPS created.
const RecordComment = "Managed by LPS"

// Client calls the Cloudflare API with an API token.
type Client struct {
	Token   string
	BaseURL string
	HTTP    *http.Client
}

// New returns a client for token; baseURL "" means DefaultBaseURL.
func New(token, baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{Token: strings.TrimSpace(token), BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// APIError is an error reported by Cloudflare.
type APIError struct {
	Status   int
	Code     int
	Message  string
	Endpoint string
}

func (e *APIError) Error() string {
	msg := e.Message
	switch {
	case e.Status == http.StatusUnauthorized || e.Code == 10000 || e.Code == 9109 || e.Code == 6003 || e.Code == 6111 || e.Code == 1000:
		msg = "Cloudflare rejected the API token (" + e.Message + "). Check that it was copied whole and has the permissions LPS lists"
	case e.Status == http.StatusForbidden:
		msg = "the API token is missing a permission for this (" + e.Message + "). Edit the token in Cloudflare and add the permissions LPS lists"
	}
	return "cloudflare: " + msg
}

type envelope struct {
	Success bool            `json:"success"`
	Errors  []apiMsg        `json:"errors"`
	Result  json.RawMessage `json:"result"`
	Info    *struct {
		Page       int `json:"page"`
		TotalPages int `json:"total_pages"`
	} `json:"result_info"`
}

type apiMsg struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) (*envelope, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cloudflare: %w", err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("cloudflare: %w", err)
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, &APIError{Status: res.StatusCode, Message: fmt.Sprintf("unexpected response (HTTP %d)", res.StatusCode), Endpoint: path}
	}
	if !env.Success || res.StatusCode >= 400 {
		e := &APIError{Status: res.StatusCode, Message: fmt.Sprintf("HTTP %d", res.StatusCode), Endpoint: path}
		if len(env.Errors) > 0 {
			e.Code, e.Message = env.Errors[0].Code, env.Errors[0].Message
		}
		return nil, e
	}
	if out != nil && len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return nil, fmt.Errorf("cloudflare: decode %s: %w", path, err)
		}
	}
	return &env, nil
}

// Zone is a domain in the user's Cloudflare account.
type Zone struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Account struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"account"`
}

// Zones lists the zones the token can see.
func (c *Client) Zones(ctx context.Context) ([]Zone, error) {
	var all []Zone
	for page := 1; page <= 20; page++ {
		var zs []Zone
		env, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/zones?per_page=50&page=%d", page), nil, &zs)
		if err != nil {
			return nil, err
		}
		all = append(all, zs...)
		if env.Info == nil || page >= env.Info.TotalPages {
			break
		}
	}
	return all, nil
}

// Tunnel is a Cloudflare Tunnel.
type Tunnel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// CreateTunnel creates a remotely managed tunnel (its routes are set with PutConfig).
func (c *Client) CreateTunnel(ctx context.Context, account, name string) (*Tunnel, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	var t Tunnel
	_, err := c.do(ctx, http.MethodPost, "/accounts/"+url.PathEscape(account)+"/cfd_tunnel", map[string]any{
		"name": name, "config_src": "cloudflare", "tunnel_secret": base64.StdEncoding.EncodeToString(secret),
	}, &t)
	if err != nil {
		return nil, err
	}
	if t.ID == "" {
		return nil, errors.New("cloudflare: the new tunnel has no id")
	}
	return &t, nil
}

// TunnelToken returns the token cloudflared runs the tunnel with.
func (c *Client) TunnelToken(ctx context.Context, account, tunnel string) (string, error) {
	var tok string
	_, err := c.do(ctx, http.MethodGet, "/accounts/"+url.PathEscape(account)+"/cfd_tunnel/"+url.PathEscape(tunnel)+"/token", nil, &tok)
	if err == nil && tok == "" {
		err = errors.New("cloudflare: empty tunnel token")
	}
	return tok, err
}

// DeleteTunnel deletes a tunnel; a missing one is not an error.
func (c *Client) DeleteTunnel(ctx context.Context, account, tunnel string) error {
	_, err := c.do(ctx, http.MethodDelete, "/accounts/"+url.PathEscape(account)+"/cfd_tunnel/"+url.PathEscape(tunnel), nil, nil)
	return ignoreNotFound(err)
}

// Ingress is one route of a tunnel: requests for Hostname go to Service.
type Ingress struct {
	Hostname string `json:"hostname,omitempty"`
	Service  string `json:"service"`
}

// PutConfig replaces the tunnel's routes. A final catch-all answering 404 is
// added.
func (c *Client) PutConfig(ctx context.Context, account, tunnel string, routes []Ingress) error {
	ingress := append(append([]Ingress{}, routes...), Ingress{Service: "http_status:404"})
	_, err := c.do(ctx, http.MethodPut, "/accounts/"+url.PathEscape(account)+"/cfd_tunnel/"+url.PathEscape(tunnel)+"/configurations",
		map[string]any{"config": map[string]any{"ingress": ingress}}, nil)
	return err
}

// Record is a DNS record.
type Record struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	Comment string `json:"comment"`
}

// Records lists the DNS records named name (any type) in a zone.
func (c *Client) Records(ctx context.Context, zone, name string) ([]Record, error) {
	var rs []Record
	_, err := c.do(ctx, http.MethodGet, "/zones/"+url.PathEscape(zone)+"/dns_records?per_page=100&name="+url.QueryEscape(name), nil, &rs)
	return rs, err
}

// CreateCNAME adds a proxied CNAME record and returns its id.
func (c *Client) CreateCNAME(ctx context.Context, zone, name, target string) (string, error) {
	var r Record
	_, err := c.do(ctx, http.MethodPost, "/zones/"+url.PathEscape(zone)+"/dns_records", map[string]any{
		"type": "CNAME", "name": name, "content": target, "proxied": true, "ttl": 1, "comment": RecordComment,
	}, &r)
	return r.ID, err
}

// DeleteRecord deletes a DNS record; a missing one is not an error.
func (c *Client) DeleteRecord(ctx context.Context, zone, id string) error {
	_, err := c.do(ctx, http.MethodDelete, "/zones/"+url.PathEscape(zone)+"/dns_records/"+url.PathEscape(id), nil, nil)
	return ignoreNotFound(err)
}

// TunnelTarget is the DNS name a hostname points to for a tunnel.
func TunnelTarget(tunnel string) string { return tunnel + ".cfargotunnel.com" }

func ignoreNotFound(err error) error {
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
		return nil
	}
	return err
}
