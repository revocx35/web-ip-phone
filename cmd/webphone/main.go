// Command webphone is the Web IP Phone server: a SIP gateway that lets browsers and the
// Android app use extensions of internal PBXs over HTTPS/WebSocket.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/revocx35/web-ip-phone/internal/auth"
	"github.com/revocx35/web-ip-phone/internal/config"
	"github.com/revocx35/web-ip-phone/internal/httpapi"
	"github.com/revocx35/web-ip-phone/internal/media"
	"github.com/revocx35/web-ip-phone/internal/phone"
	"github.com/revocx35/web-ip-phone/internal/secretbox"
	"github.com/revocx35/web-ip-phone/internal/sipua"
	"github.com/revocx35/web-ip-phone/internal/store"
	"github.com/revocx35/web-ip-phone/internal/tlscert"
	"github.com/revocx35/web-ip-phone/web"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 {
		os.Exit(subcommand(os.Args[1], os.Args[2:]))
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "webphone:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)
	log.Info("starting Web IP Phone", "version", version)

	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(cfg.DataDir, 0o700); err != nil {
		log.Warn("cannot restrict the data directory to the server user", "dir", cfg.DataDir, "err", err)
	}
	key := cfg.SecretKey
	if key == nil {
		if key, err = secretbox.LoadOrCreateKey(filepath.Join(cfg.DataDir, "secret.key")); err != nil {
			return fmt.Errorf("secret key: %w", err)
		}
	}
	box, err := secretbox.New(key)
	if err != nil {
		return err
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, "webphone.db"))
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer st.Close()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := st.FinishDanglingCalls(ctx); err != nil {
		return err
	}

	setupToken := ""
	if n, err := st.CountUsers(ctx); err != nil {
		return err
	} else if n == 0 {
		setupToken = cfg.SetupToken
		if setupToken == "" {
			setupToken = auth.NewHumanCode(5)
		}
		log.Warn("no admin account yet: open the web UI and create it with this setup token", "setup_token", setupToken)
	}

	svc := phone.New(st, box, log, version)
	svc.MaxCalls = cfg.MaxCalls
	engine, err := sipua.New(sipua.Config{
		ListenPort: cfg.SIPPort, AdvertiseIP: cfg.AdvertiseIP, RTPPool: media.NewPortPool(cfg.RTPPortMin, cfg.RTPPortMax, nil),
		Trace: cfg.SIPTrace, UserAgent: "WebIPPhone/" + version, Logger: log, Handler: svc,
	})
	if err != nil {
		return err
	}
	svc.SetEngine(engine)
	if err := engine.Start(ctx); err != nil {
		return err
	}
	defer engine.Close()
	if err := svc.SyncPBXs(ctx); err != nil {
		return err
	}

	api := httpapi.New(httpapi.Options{Config: cfg, Store: st, Box: box, Service: svc, SIP: engine, Logger: log,
		Static: web.Dist(), Version: version, SetupToken: setupToken})
	handler := api.Handler()

	var servers []*http.Server
	errc := make(chan error, 2)
	if cfg.HTTPSListen != "" {
		var prov *tlscert.Provider
		if cfg.TLSCertFile != "" {
			prov, err = tlscert.FromFiles(cfg.TLSCertFile, cfg.TLSKeyFile)
		} else {
			prov, err = tlscert.SelfSigned(filepath.Join(cfg.DataDir, "tls"))
		}
		if err != nil {
			return fmt.Errorf("TLS certificate: %w", err)
		}
		api.CertFingerprint = prov.Fingerprint()
		srv := newHTTPServer(cfg.HTTPSListen, handler, log)
		srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: prov.GetCertificate}
		servers = append(servers, srv)
		log.Info("HTTPS listening", "addr", cfg.HTTPSListen, "cert_sha256", api.CertFingerprint)
		go func() { errc <- serve(srv, true) }()
	}
	if cfg.HTTPListen != "" {
		srv := newHTTPServer(cfg.HTTPListen, handler, log)
		servers = append(servers, srv)
		log.Info("HTTP listening (put a TLS reverse proxy in front)", "addr", cfg.HTTPListen)
		go func() { errc <- serve(srv, false) }()
	}

	go housekeeping(ctx, st, log)

	select {
	case <-ctx.Done():
		log.Info("shutting down")
	case err := <-errc:
		if err != nil {
			log.Error("server failed", "err", err)
		}
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, s := range servers {
		s.Shutdown(shutCtx)
	}
	return nil
}

func newHTTPServer(addr string, h http.Handler, log *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    32 << 10,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelDebug),
	}
}

func serve(s *http.Server, tlsOn bool) error {
	var err error
	if tlsOn {
		err = s.ListenAndServeTLS("", "")
	} else {
		err = s.ListenAndServe()
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// housekeeping removes expired sessions and old history once an hour.
func housekeeping(ctx context.Context, st *store.Store, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		settings, err := st.GetSettings(ctx)
		if err == nil {
			err = st.PurgeExpired(ctx, time.Duration(settings.CallHistoryDays)*24*time.Hour, time.Duration(settings.AuditLogDays)*24*time.Hour)
		}
		if err != nil && ctx.Err() == nil {
			log.Error("housekeeping", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ---- subcommands -------------------------------------------------------------------

func subcommand(name string, args []string) int {
	switch name {
	case "healthcheck":
		return healthcheck()
	case "reset-password", "reset-2fa":
		if len(args) != 1 {
			fmt.Fprintf(os.Stderr, "usage: webphone %s <username>\n", name)
			return 2
		}
		if err := resetUser(name, args[0]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		return 0
	case "version", "--version", "-v":
		fmt.Println(version)
		return 0
	}
	fmt.Fprintln(os.Stderr, "usage: webphone [healthcheck | reset-password <user> | reset-2fa <user> | version]")
	return 2
}

// healthcheck probes the local server (Docker HEALTHCHECK; the image has no curl).
func healthcheck() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	url := ""
	client := &http.Client{Timeout: 4 * time.Second}
	if cfg.HTTPSListen != "" {
		url = "https://" + localAddr(cfg.HTTPSListen) + "/healthz"
		// Only the local server is contacted; its certificate may be self-signed.
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec
	} else {
		url = "http://" + localAddr(cfg.HTTPListen) + "/healthz"
	}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func localAddr(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return listen
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// resetUser is the recovery path for a locked-out admin; it needs shell access to the
// server (docker compose exec), which is the trust boundary.
func resetUser(cmd, username string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, "webphone.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()
	u, err := st.GetUserByUsername(ctx, username)
	if err != nil {
		return fmt.Errorf("user %q: %w", username, err)
	}
	if cmd == "reset-2fa" {
		if err := st.DisableTOTP(ctx, u.ID); err != nil {
			return err
		}
		st.Audit(ctx, store.AuditEntry{Username: u.Username, Action: "user.2fa_reset", Target: u.Username, Details: "command line", Success: true})
		fmt.Printf("Two-factor authentication of %s is off. They can enrol again after signing in.\n", u.Username)
		return nil
	}
	pw := strings.ToLower(auth.NewHumanCode(4))
	hash, err := auth.HashPassword(ctx, pw, auth.DefaultParams)
	if err != nil {
		return err
	}
	if err := st.SetPassword(ctx, u.ID, hash, true, 0); err != nil {
		return err
	}
	if u.Disabled {
		f := false
		st.UpdateUser(ctx, u.ID, store.UserUpdate{Disabled: &f})
	}
	st.Audit(ctx, store.AuditEntry{Username: u.Username, Action: "user.password_reset", Target: u.Username, Details: "command line", Success: true})
	fmt.Printf("Temporary password for %s: %s\nIt must be changed at the next sign-in. All sessions were signed out.\n", u.Username, pw)
	return nil
}
