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
	"facets.barnlab.dev/internal/web"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	registry := project.NewRegistry()
	if err := registry.Register(kata.New(kata.Config{})); err != nil {
		logger.Error("configure provider", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app := cli.App{
		Registry: registry,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		Getenv:   os.Getenv,
		Serve: func(ctx context.Context, address string) error {
			return runServer(ctx, address, logger)
		},
	}
	os.Exit(app.Run(ctx, os.Args[1:]))
}

func runServer(ctx context.Context, address string, logger *slog.Logger) error {
	handler, err := web.New(logger)
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
