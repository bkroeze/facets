package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"facets.barnlab.dev/internal/cli"
	"facets.barnlab.dev/internal/project"
	"facets.barnlab.dev/internal/project/kata"
	"facets.barnlab.dev/internal/store"
	"facets.barnlab.dev/internal/web"
)

func main() {
	os.Exit(run())
}

func run() int {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	registry := project.NewRegistry()
	provider := kata.New(kata.Config{})
	if err := registry.Register(provider); err != nil {
		logger.Error("configure provider", "error", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	databasePath := os.Getenv("FACETS_DB")
	if databasePath == "" {
		var err error
		databasePath, err = store.DefaultPath()
		if err != nil {
			logger.Error("resolve project registry path", "error", err)
			return 1
		}
	}
	projectStore, err := store.Open(ctx, databasePath)
	if err != nil {
		logger.Error("open project registry", "error", err, "path", databasePath)
		return 1
	}
	defer func() {
		if err := projectStore.Close(); err != nil {
			logger.Error("close project registry", "error", err)
		}
	}()

	app := cli.App{
		Registry:     registry,
		ProjectStore: projectStore,
		Stdout:       os.Stdout,
		Stderr:       os.Stderr,
		Getenv:       os.Getenv,
		Serve: func(ctx context.Context, address string) error {
			return runServer(ctx, address, logger, provider, provider, projectStore)
		},
	}
	return app.Run(ctx, os.Args[1:])
}

func runServer(ctx context.Context, address string, logger *slog.Logger, projects web.ProjectSource, provider project.Provider, projectStore *store.Store) error {
	handler, err := web.NewWithRegistry(logger, projects, projectStore, provider)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("server started", "address", address)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		logger.Info("server shutting down")
		return server.Shutdown(shutdownCtx)
	}
}
