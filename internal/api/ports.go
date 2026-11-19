package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
)

// reservePort picks a free host port in [min,max] that is not used by any
// VPS/app record, not held by an in-flight request and not bound on the host.
// Call releasePort once the record is stored (or creation failed).
func (s *Server) reservePort(ctx context.Context, min, max int) (int, error) {
	used, err := s.store.UsedPorts(ctx)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for p := min; p <= max; p++ {
		if used[p] || s.portsHeld[p] || !hostPortFree(s.cfg.BindIP, p) {
			continue
		}
		s.portsHeld[p] = true
		return p, nil
	}
	return 0, errors.New(fmt.Sprintf("no free port in range %d-%d", min, max))
}

func (s *Server) releasePort(p int) {
	s.mu.Lock()
	delete(s.portsHeld, p)
	s.mu.Unlock()
}

func hostPortFree(ip string, port int) bool {
	l, err := net.Listen("tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	l.Close()
	return true
}
