// Package httpserve runs an embedded HTTP server during a wizard run to
// deliver Agama profiles, kernel/initrd/squashfs, and any other artifacts
// the target nodes fetch over the network during install.
//
// This is essential because Agama (openSUSE Leap 16+) does NOT support
// inst.auto=device://OEMDRV/... — only inst.auto=http://... — so a small
// local HTTP server inside the Windows exe is the only viable delivery
// mechanism for VMs that can reach the host's IP.
//
// Lifetime: bound to the run; started before terraform apply boots VMs,
// stopped after all nodes finish first-boot.
package httpserve

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"
)

type Server struct {
	Root string // directory served at /
	Bind string // e.g. "0.0.0.0:0" for ephemeral

	srv  *http.Server
	addr net.Addr
	mu   sync.Mutex
}

// Start binds the listener and serves until ctx is cancelled or Stop is called.
// Returns the actual TCP address that's listening (host:port). Pass Bind = ""
// to default to "0.0.0.0:0" (ephemeral port — caller reads URL() afterwards).
func (s *Server) Start(ctx context.Context) error {
	bind := s.Bind
	if bind == "" {
		bind = "0.0.0.0:0"
	}
	ln, err := net.Listen("tcp", bind)
	if err != nil {
		return fmt.Errorf("listen %s: %w", bind, err)
	}
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir(s.Root)))

	s.mu.Lock()
	s.srv = &http.Server{
		Handler:           withRequestLog(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	s.addr = ln.Addr()
	s.mu.Unlock()

	go func() {
		<-ctx.Done()
		_ = s.Stop()
	}()
	return s.srv.Serve(ln)
}

func (s *Server) Stop() error {
	s.mu.Lock()
	srv := s.srv
	s.mu.Unlock()
	if srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}

// URL returns the http://host:port URL that targets should fetch from.
// host should be the LAN IP that VMs can reach (the Windows host's IP).
func (s *Server) URL(host string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.addr == nil {
		return ""
	}
	_, port, _ := net.SplitHostPort(s.addr.String())
	return fmt.Sprintf("http://%s:%s", host, port)
}

// ServePath returns the URL of a specific file under Root (relative path).
func (s *Server) ServePath(host, rel string) string {
	return s.URL(host) + "/" + filepath.ToSlash(rel)
}

func withRequestLog(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Lightweight log; the run's logger captures full diagnostics.
		_ = r
		h.ServeHTTP(w, r)
	})
}
