package webpprof

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

func Start(addr string, options ...Option) (*Profiler, error) {
	defaultProfilerMu.Lock()
	if profiler := defaultProfiler.Load(); profiler != nil {
		defaultProfilerMu.Unlock()
		if profiler.URL() == "" {
			return nil, errors.New("webpprof: profiler already initialized without a server")
		}
		return profiler, nil
	}
	if strings.TrimSpace(addr) == "" {
		defaultProfilerMu.Unlock()
		return nil, errors.New("webpprof: listen address is required")
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		defaultProfilerMu.Unlock()
		return nil, fmt.Errorf("webpprof: listen on %s: %w", addr, err)
	}
	mux := http.NewServeMux()
	profiler := newProfiler(options...)
	profiler.register(mux)
	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      35 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	profiler.server = server
	profiler.serverAddr = listener.Addr().String()
	defaultProfiler.Store(profiler)
	defaultProfilerMu.Unlock()

	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("webpprof server error", "error", err)
		}
	}()
	return profiler, nil
}

func Shutdown(ctx context.Context) error {
	profiler := Default()
	if profiler == nil {
		return nil
	}
	return profiler.Shutdown(ctx)
}

func URL() string {
	profiler := Default()
	if profiler == nil {
		return ""
	}
	return profiler.URL()
}

func (p *Profiler) URL() string {
	if p == nil {
		return ""
	}
	p.serverMu.Lock()
	addr := p.serverAddr
	p.serverMu.Unlock()
	if addr == "" {
		return ""
	}
	return "http://" + addr + strings.TrimRight(p.BasePath(), "/") + "/"
}

func (p *Profiler) Shutdown(ctx context.Context) error {
	if p == nil {
		return nil
	}
	defaultProfilerMu.Lock()
	defer defaultProfilerMu.Unlock()
	p.serverMu.Lock()
	server := p.server
	p.server = nil
	p.serverAddr = ""
	p.serverMu.Unlock()
	var serverErr error
	if server != nil {
		serverErr = server.Shutdown(ctx)
	}
	p.closeOnce.Do(func() {
		p.store.close()
	})
	defaultProfiler.CompareAndSwap(p, nil)
	return serverErr
}
