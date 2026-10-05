package observability

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

// Listener failure is reported; it never invents business success. Binding is
// synchronous, and both serving and worker goroutines join before return.
func RunWorker(ctx context.Context, addr string, m *Metrics, run func(context.Context) error) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return errors.New("worker metrics listen failed")
	}
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", m.Handler())
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 8192}
	served := make(chan error, 1)
	go func() {
		e := server.Serve(listener)
		if !errors.Is(e, http.ErrServerClosed) {
			cancel()
		}
		served <- e
	}()
	err = run(work)
	cancel()
	stop, end := context.WithTimeout(context.Background(), 3*time.Second)
	defer end()
	_ = server.Shutdown(stop)
	_ = server.Close()
	serverErr := <-served
	if err != nil {
		return err
	}
	if serverErr != nil && !errors.Is(serverErr, http.ErrServerClosed) {
		return errors.New("worker metrics server failed")
	}
	return nil
}
