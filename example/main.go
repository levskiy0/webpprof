package main

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/levskiy0/webpprof"
	webpprofhttp "github.com/levskiy0/webpprof/profiler/http"
	webpprofslog "github.com/levskiy0/webpprof/profiler/slog"
	webpprofsql "github.com/levskiy0/webpprof/profiler/sql"
	modernsqlite "modernc.org/sqlite"
)

const (
	defaultAddress         = "127.0.0.1:3030"
	defaultApplicationDB   = "./var/webpprof/example.db"
	defaultProfilerStorage = "./var/webpprof/events.db"
)

func main() {
	if err := run(); err != nil {
		slog.Error("example stopped", "error", err)
		os.Exit(1)
	}
}

func run() (runErr error) {
	address := envOrDefault("WEBPPROF_ADDR", defaultAddress)
	applicationDB := envOrDefault("WEBPPROF_EXAMPLE_DB", defaultApplicationDB)
	profilerStorage := envOrDefault("WEBPPROF_STORAGE", defaultProfilerStorage)

	mux := http.NewServeMux()
	metrics := &demoMetrics{}
	baseLogHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
	baseSQLiteDriver := driver.Driver(&modernsqlite.Driver{})

	// CONNECT WEBPPROF
	//
	// The application remains ordinary net/http + database/sql + slog. Its
	// three boundaries are decorated once here; application code below keeps
	// using the standard APIs.
	profiler := webpprof.New(mux, profilerOptions(metrics, profilerStorage)...)
	defer func() { runErr = errors.Join(runErr, profiler.Close()) }()

	logHandler := webpprofslog.ProfileWith(profiler, baseLogHandler)
	logger := slog.New(logHandler).With("service", "webpprof-example")
	slog.SetDefault(logger)

	sqliteDriver := webpprofsql.ProfileDriverWith(profiler, baseSQLiteDriver, webpprofsql.Config{
		Connection:     "example",
		Driver:         "sqlite",
		Database:       filepath.Base(applicationDB),
		Explain:        true,
		ExplainTimeout: 500 * time.Millisecond,
		ExplainMaxRows: 50,
	})

	database, err := openPlayerDatabase(context.Background(), sqliteDriver, applicationDB)
	if err != nil {
		return fmt.Errorf("open example database: %w", err)
	}
	defer func() { runErr = errors.Join(runErr, database.Close()) }()

	webApplication := &application{
		profiler: profiler,
		players:  &playerRepository{database: database},
		logger:   logger,
		metrics:  metrics,
		manual:   newManualExamples(profiler),
	}
	appHandler := http.Handler(webApplication.routes())

	// The outer HTTP wrapper creates request correlation. SQL, slog, named
	// middleware, and optional handler measurements attach through context.
	appHandler = webpprofhttp.ProfileMiddlewareWith(profiler, "security-headers", securityHeaders)(
		webpprofhttp.ProfileMiddlewareWith(profiler, "request-log", requestLog(logger))(appHandler),
	)
	appHandler = requestTags(appHandler)
	appHandler = webpprofhttp.MiddlewareWith(profiler, appHandler)
	mux.Handle("/", recoverResponse(appHandler))
	// END WEBPPROF CONNECTION

	server := &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("server started",
			"application", "http://"+address+"/",
			"webpprof", "http://"+address+profiler.BasePath()+"/",
			"application_db", applicationDB,
			"profiler_db", profilerStorage,
		)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown server: %w", err)
		}
		return nil
	case err := <-serverErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("listen on %s: %w", address, err)
		}
		return nil
	}
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
