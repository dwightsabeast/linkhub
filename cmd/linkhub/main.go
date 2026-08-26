// Command linkhub is the LinkHub HTTP server.
//
// Configuration is entirely environment-driven. The install script
// (install/linkhub-install.sh) writes /etc/linkhub/linkhub.env and the OpenRC
// service file sources it before exec.
//
// Variables:
//
//	LINKHUB_DATA_DIR      mutable state dir   (default: /var/lib/linkhub)
//	                       config.json + assets/ live here
//	LINKHUB_STATIC_DIR    shipped chrome dir  (default: /opt/linkhub/static)
//	                       index.html.tmpl, admin.html, styles.css, …
//	LINKHUB_LISTEN        bind address        (default: 0.0.0.0:8080)
//	AUTH_MODE             trust_proxy | basic | form | none
//	                       (default: trust_proxy)
//	BASIC_AUTH_USER       admin username      (required when AUTH_MODE
//	                       is basic; bootstrap for form)
//	BASIC_AUTH_HASH       bcrypt hash from linkhub-hash
//	                       (required when AUTH_MODE is basic; bootstrap
//	                       for form)
//
// In form mode the admin can change its login from the UI; the new
// username + bcrypt hash are persisted to <data dir>/auth.json and take
// precedence over the BASIC_AUTH_* env bootstrap.
//
// On signal (SIGINT, SIGTERM) the server drains in-flight requests
// for up to 10s before exiting.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/dwightsabeast/linkhub/internal/auth"
	"github.com/dwightsabeast/linkhub/internal/config"
	"github.com/dwightsabeast/linkhub/internal/server"
)

// version is set at build time via -ldflags "-X main.version=…".
// Defaults to "dev" so unstamped local builds still report something.
var version = "dev"

// Defaults match the values written into /etc/linkhub/linkhub.env by
// the install script, so a fresh install Just Works with no overrides.
const (
	defaultDataDir   = "/var/lib/linkhub"
	defaultStaticDir = "/opt/linkhub/static"
	defaultListen    = "0.0.0.0:8080"

	// Shutdown grace period. Generous — public renders are
	// microseconds and the worst admin request is a 2 MB upload.
	shutdownTimeout = 10 * time.Second
)

func main() {
	// --version short-circuits before we do any other work, so
	// `linkhub --version` works even on a host with a broken env
	// file. Use a small flag set rather than the package-default
	// because we want to keep control over -h output too.
	fs := flag.NewFlagSet("linkhub", flag.ExitOnError)
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: linkhub [--version]\n\n"+
			"Configuration is via environment variables; see the package\n"+
			"docs or /etc/linkhub/linkhub.env on a default install.\n")
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if *showVersion {
		fmt.Println(version)
		return
	}

	// log.Lmsgprefix puts our prefix after the timestamp rather than
	// before, which reads better in journalctl-style output:
	//   2026/05/06 21:03:11 linkhub: listening on 0.0.0.0:8080
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("linkhub: ")

	dataDir := envOr("LINKHUB_DATA_DIR", defaultDataDir)
	staticDir := envOr("LINKHUB_STATIC_DIR", defaultStaticDir)
	listen := envOr("LINKHUB_LISTEN", defaultListen)

	// Auth comes first so a misconfigured AUTH_MODE doesn't have us
	// load config + parse templates before failing.
	mode, err := auth.ParseMode(os.Getenv("AUTH_MODE"))
	if err != nil {
		log.Fatalf("AUTH_MODE: %v", err)
	}
	// In form mode the admin can change its own login from the UI; the
	// new credential is persisted to auth.json in the data dir (the
	// daemon can't rewrite the root-owned env file). It overrides the
	// BASIC_AUTH_* env bootstrap once written.
	credPath := filepath.Join(dataDir, "auth.json")
	authCfg, err := auth.NewConfig(mode,
		os.Getenv("BASIC_AUTH_USER"),
		os.Getenv("BASIC_AUTH_HASH"),
		credPath)
	if err != nil {
		log.Fatalf("auth: %v", err)
	}

	// The install script copies config.default.json into place during
	// setup, so by the time the daemon runs the file should exist.
	// Missing-file is a hard error — better to fail loud than silently
	// invent state.
	configPath := filepath.Join(dataDir, "config.json")
	store, err := config.New(configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	srv, err := server.New(server.Options{
		Store:     store,
		Auth:      authCfg,
		StaticDir: staticDir,
		DataDir:   dataDir,
	})
	if err != nil {
		log.Fatalf("server: %v", err)
	}

	authCfg.LogStartup()
	log.Printf("version %s", version)
	log.Printf("listening on %s (data=%s, static=%s)", listen, dataDir, staticDir)

	httpSrv := &http.Server{
		Addr:    listen,
		Handler: srv.Handler(),

		// ReadHeaderTimeout is the Slowloris defense — if a client
		// stops sending headers we drop them. Short.
		ReadHeaderTimeout: 5 * time.Second,
		// ReadTimeout / WriteTimeout cover the body. 30s is generous
		// for a 2 MB avatar upload even on a poor connection.
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		// IdleTimeout limits how long an idle keep-alive connection
		// can sit before we close it. 2 minutes is the Go default-ish.
		IdleTimeout: 2 * time.Minute,
		// MaxHeaderBytes caps weirdo clients. 32 KB is plenty for any
		// legit browser; defends against memory-pressure attacks.
		MaxHeaderBytes: 32 << 10,
	}

	// Run ListenAndServe on its own goroutine so the main goroutine
	// can wait on signals. errCh carries either a startup failure
	// (e.g. address already in use) or nil after a clean shutdown.
	errCh := make(chan error, 1)
	go func() {
		err := httpSrv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// First wait: signal arrives, OR ListenAndServe fails before we
	// ever started serving. The former triggers shutdown; the latter
	// is fatal.
	select {
	case sig := <-sigCh:
		log.Printf("received %s, shutting down", sig)
	case err := <-errCh:
		if err != nil {
			log.Fatalf("listen: %v", err)
		}
		// Server returned http.ErrServerClosed without us asking,
		// somehow — exit cleanly.
		return
	}

	// Graceful shutdown. Use a fresh context so the signal that
	// triggered shutdown can't cancel its own cleanup.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
		os.Exit(1)
	}
	// Drain errCh so the goroutine isn't left blocked. Ignore the
	// value — we already know the server is closed.
	<-errCh
	log.Printf("clean shutdown")
}

// envOr returns os.Getenv(key) or def if the env var is unset or
// empty. Empty-string fallback matters here because the install
// script writes the env file with values present even when they're
// the defaults; treating "" as unset would mishandle a future
// "explicitly empty" case, but we don't have one today.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
