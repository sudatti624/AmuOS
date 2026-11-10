package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/sudatti624/AmuOS/internal/fileops"
	"github.com/sudatti624/AmuOS/internal/process"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sudatti624/AmuOS/internal/agent"
	"github.com/sudatti624/AmuOS/internal/statelock"
)

// version is set by release builds with -ldflags "-X main.version=...".
var version = "dev"

func run(ctx context.Context) error {
	showVersion := flag.Bool("version", false, "print the version and exit")
	stateDir := flag.String("state-dir", "/var/lib/amuos-agent", "private directory for this agent's key and credential")
	server := flag.String("server", "", "control plane HTTPS URL used for enrollment, e.g. https://cp.example:8443")
	agentServer := flag.String("agent-server", "", "control plane mTLS URL for enrolled agents, e.g. https://cp.example:9443")
	caFile := flag.String("ca-file", "", "PEM certificates that verify the control plane's TLS certificate")
	enrollFile := flag.String("enroll-code-file", "", "enroll with the one-time code in this private file, then exit")
	fileRootsFile := flag.String("file-roots", "", "JSON file declaring isolated directories published by this agent")
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *showVersion {
		fmt.Println("amuos-agent", version)
		return nil
	}
	pem, err := os.ReadFile(*caFile)
	if err != nil {
		return fmt.Errorf("--ca-file: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return errors.New("--ca-file contains no certificate")
	}
	if err := os.MkdirAll(*stateDir, 0700); err != nil {
		return err
	}
	// The lock file sits in the state directory, like the control plane's.
	lock, err := statelock.Acquire(filepath.Join(*stateDir, "agent"))
	if err != nil {
		return err
	}
	defer lock.Close()
	var fileRoots map[string]fileops.RootConfig
	if *fileRootsFile != "" {
		f, err := os.Open(*fileRootsFile)
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(io.LimitReader(f, 16385))
		f.Close()
		if readErr != nil || len(data) > 16384 {
			return errors.New("invalid file roots configuration")
		}
		decoder := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&fileRoots)
		tail := decoder.Decode(new(any))
		if err != nil || tail != io.EOF || len(fileRoots) > 16 {
			return errors.New("invalid file roots configuration")
		}
	}
	a, err := agent.New(agent.Config{FileRoots: fileRoots, ProtectedPaths: []string{*caFile, *fileRootsFile}, StateDir: *stateDir, Server: strings.TrimRight(*server, "/"), AgentServer: strings.TrimRight(*agentServer, "/"), Roots: roots, Version: version})
	if err != nil {
		return err
	}
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	if *enrollFile != "" {
		code, err := os.ReadFile(*enrollFile)
		if err != nil {
			return err
		}
		enrollCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		hostID, err := a.Enroll(enrollCtx, strings.TrimSpace(string(code)))
		if err != nil {
			return err
		}
		fmt.Println("Enrolled for host", hostID+". Remove the code file; it cannot be used again.")
		return nil
	}
	slog.Info("agent starting", "version", version, "agent_server", *agentServer)
	return a.Run(ctx)
}

func main() {
	if err := process.Run("AmuOSAgent", run); err != nil {
		slog.Error("agent stopped", "error", err)
		os.Exit(1)
	}
}
