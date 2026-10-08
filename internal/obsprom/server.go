package obsprom

import (
	"context"
	stderrors "errors"
	"net"
	"net/http"
	"time"
)

// Path is the URL path the Server serves the exposition on.
const Path = "/metrics"

// Server is a running metrics listener: GET /metrics renders the
// Registry; every other path is 404.
type Server struct {
	srv *http.Server
	ln  net.Listener
}

// Listen binds addr (host:port; ":0" picks a free port) synchronously —
// so a bad or busy address fails here, before anything else starts —
// then serves r on Path in a background goroutine until Close or
// Shutdown.
func Listen(addr string, r *Registry) (*Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle(Path, r.Handler())
	s := &Server{
		srv: &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second},
		ln:  ln,
	}
	go func() {
		if err := s.srv.Serve(ln); err != nil && !stderrors.Is(err, http.ErrServerClosed) {
			_ = ln.Close()
		}
	}()
	return s, nil
}

// Addr is the bound address (the resolved port when addr was ":0").
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Shutdown stops accepting scrapes and waits for in-flight ones until
// ctx is done.
func (s *Server) Shutdown(ctx context.Context) error { return s.srv.Shutdown(ctx) }

// Close stops the listener immediately.
func (s *Server) Close() error { return s.srv.Close() }
