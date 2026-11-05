package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sudatti624/AmuOS/internal/controlplane"
	"github.com/sudatti624/AmuOS/internal/maintenance"
	"github.com/sudatti624/AmuOS/internal/recovery"
	"github.com/sudatti624/AmuOS/internal/statelock"
	"github.com/sudatti624/AmuOS/internal/store"
)

// version is set by release builds with -ldflags "-X main.version=...".
var version = "dev"

func run() error {
	showVersion := flag.Bool("version", false, "print the version and exit")
	dbPath := flag.String("db", "data/amuos.db", "SQLite database path")
	addr := flag.String("addr", "127.0.0.1:8443", "HTTPS listen address")
	origin := flag.String("origin", "https://localhost:8443", "exact external HTTPS origin")
	cert := flag.String("tls-cert", "", "server certificate PEM")
	key := flag.String("tls-key", "", "server private key PEM")
	bootstrap := flag.String("bootstrap-file", "", "write initial setup token to a new local private file and exit")
	backup := flag.String("backup-out", "", "write a verified age-encrypted backup and exit (server must be stopped)")
	recipient := flag.String("recipient", "", "age X25519 backup recipient public key")
	restore := flag.String("restore-from", "", "restore a verified age backup and exit (server must be stopped)")
	recoveryKey := flag.String("recovery-key-file", "", "private file containing one age X25519 recovery identity")
	generateKey := flag.String("generate-recovery-key", "", "write a new private recovery key to a new file and print only its public recipient")
	checkDB := flag.Bool("check-db", false, "check without writing whether this binary can start with the existing database, then exit (server must be stopped)")
	flag.Parse()
	modes := 0
	for _, enabled := range []bool{*showVersion, *bootstrap != "", *backup != "", *restore != "", *generateKey != "", *checkDB} {
		if enabled {
			modes++
		}
	}
	if modes > 1 || (*recipient != "" && *backup == "") || (*recoveryKey != "" && *restore == "") || (*backup != "" && *recipient == "") || (*restore != "" && *recoveryKey == "") {
		return errors.New("choose one mode: server, version, database check, bootstrap, key generation, backup with recipient, or restore with recovery key file")
	}
	if flag.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *showVersion {
		fmt.Println("amuos-controlplane", version)
		return nil
	}
	if *generateKey != "" {
		recipient, err := recovery.GenerateRecoveryKey(*generateKey)
		if err != nil {
			return err
		}
		fmt.Println(recipient)
		return nil
	}
	if *checkDB {
		// Check before locking so a mistyped path does not create a state directory.
		if _, err := os.Lstat(*dbPath); err != nil {
			return err
		}
		stateLock, err := statelock.Acquire(*dbPath)
		if err != nil {
			return err
		}
		defer stateLock.Close()
		check, err := store.CheckUpdate(context.Background(), *dbPath)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(struct {
			ProductVersion string `json:"product_version"`
			store.UpdateCheck
		}{version, check})
	}
	if *backup != "" || *restore != "" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
		defer cancel()
		if *backup != "" {
			manifest, err := recovery.Backup(ctx, *dbPath, *backup, *recipient, version, recovery.Config{FormatVersion: 1, Address: *addr, Origin: *origin})
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(manifest)
		}
		result, err := recovery.Restore(ctx, *dbPath, *restore, *recoveryKey, version)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	if *bootstrap == "" && (*cert == "" || *key == "") {
		return errors.New("--tls-cert and --tls-key are required")
	}
	var certificate tls.Certificate
	if *bootstrap == "" {
		var err error
		certificate, err = tls.LoadX509KeyPair(*cert, *key)
		if err != nil {
			return err
		}
	}
	stateLock, err := statelock.Acquire(*dbPath)
	if err != nil {
		return err
	}
	defer stateLock.Close()
	if err := recovery.CleanupStages(*dbPath); err != nil {
		return err
	}
	db, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.RecoverLocalMaintenance(context.Background(), time.Now()); err != nil {
		return err
	}
	if *bootstrap != "" {
		// O_EXCL prevents overwriting an existing credential file.
		file, err := os.OpenFile(*bootstrap, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		token, err := db.PrepareBootstrap(context.Background(), time.Now())
		if err != nil {
			file.Close()
			os.Remove(*bootstrap)
			return err
		}
		if _, err = file.WriteString(token + "\n"); err != nil {
			file.Close()
			os.Remove(*bootstrap)
			return err
		}
		if err = file.Close(); err != nil {
			return err
		}
		fmt.Println("Setup token written to the specified file; valid for 10 minutes. Keep it private and remove after setup.")
		return nil
	}
	handler, err := controlplane.New(db, *origin)
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: *addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}}}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); _ = maintenance.Run(ctx, time.Minute, db.RunMaintenance) }()
	defer func() { stop(); <-workerDone }()
	done := make(chan error, 1)
	go func() { done <- srv.ServeTLS(listener, "", "") }()
	slog.Info("control plane starting", "address", *addr)
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	}
}

func main() {
	if err := run(); err != nil {
		slog.Error("control plane stopped", "error", err)
		os.Exit(1)
	}
}
