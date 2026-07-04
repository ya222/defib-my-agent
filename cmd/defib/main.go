// Command defib is the entry point for the defib CLI. It wires the cobra
// command tree from internal/cli and injects the daemon-run loop; all
// business logic lives in internal packages.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ya222/defib/internal/cli"
	"github.com/ya222/defib/internal/config"
	"github.com/ya222/defib/internal/daemon"
	"github.com/ya222/defib/internal/ipc"
	"github.com/ya222/defib/internal/logging"
	"github.com/ya222/defib/internal/paths"
	"github.com/ya222/defib/internal/provider"
	"github.com/ya222/defib/internal/provider/claude"
	"github.com/ya222/defib/internal/provider/copilot"
	"github.com/ya222/defib/internal/provider/fake"
)

func main() {
	// Hidden child mode: the fake provider re-executes this binary to
	// replay a script block (see internal/provider/fake).
	if len(os.Args) > 1 && os.Args[1] == fake.RunMode {
		os.Exit(fake.Main(os.Args[2:], os.Stdout, os.Stderr, time.Now))
	}
	registerProviders()
	os.Exit(cli.Execute(os.Args[1:], cli.Hooks{
		RunDaemon: runDaemon,
		Providers: provider.List,
	}))
}

// registerProviders wires the compiled-in providers into the default
// registry (docs/providers.md adding-a-new-provider checklist, step 5).
func registerProviders() {
	if err := provider.Register(fake.New()); err != nil {
		fmt.Fprintf(os.Stderr, "defib: register providers: %v\n", err)
		os.Exit(1)
	}
	if err := provider.Register(claude.New()); err != nil {
		fmt.Fprintf(os.Stderr, "defib: register providers: %v\n", err)
		os.Exit(1)
	}
	if err := provider.Register(copilot.New()); err != nil {
		fmt.Fprintf(os.Stderr, "defib: register providers: %v\n", err)
		os.Exit(1)
	}
}

// runDaemon is the foreground daemon loop injected into `defib daemon run`.
func runDaemon(ctx context.Context, dirs paths.Dirs) error {
	cfg, err := config.Resolve(config.Options{
		GlobalPath: filepath.Join(dirs.Config, "config.toml"),
	})
	if err != nil {
		return err
	}
	logger, closeLog, err := logging.Open(filepath.Join(dirs.State, "daemon.log"), cfg.Logging.Level)
	if err != nil {
		return err
	}
	defer func() { _ = closeLog() }()

	d, err := daemon.New(daemon.Options{Dirs: dirs, Logger: logger})
	if err != nil {
		return err
	}
	// Recover tasks from a previous daemon before accepting clients
	// (docs/architecture.md#recovery).
	if err := d.Reconcile(ctx); err != nil {
		_ = d.Close()
		return err
	}

	sock := filepath.Join(dirs.Runtime, "daemon.sock")
	l, err := ipc.Listen(sock)
	if err != nil {
		_ = d.Close()
		return err
	}
	srv := ipc.NewServer()
	d.RegisterMethods(srv)

	serveCtx, cancel := context.WithCancel(ctx)
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(serveCtx, l) }()
	logger.Info("daemon started", "socket", sock, "pid", os.Getpid())

	var serveErr error
	select {
	case params := <-d.ShutdownRequested():
		logger.Info("shutdown requested", "stop_children", params.StopChildren)
		if params.StopChildren {
			d.StopAllChildren()
		}
		cancel()
		<-serveDone
	case <-ctx.Done(): // SIGINT/SIGTERM
		logger.Info("signal received, shutting down")
		cancel()
		<-serveDone
	case serveErr = <-serveDone:
		cancel()
	}

	_ = os.Remove(sock)
	if err := d.Close(); err != nil {
		logger.Error("close daemon", "error", err)
	}
	logger.Info("daemon stopped")
	return serveErr
}
