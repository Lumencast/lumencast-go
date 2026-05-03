package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/Lumencast/lumencast-go/interop/control"
	"github.com/Lumencast/lumencast-go/server"
)

// cmdServeScenario boots a lumencast-go server with the interop test
// control plane mounted on a separate port. Used by the cross-language
// interop matrix in `lumencast-protocol/interop/`.
//
// On startup the command prints exactly one JSON line on stdout :
//
//   {"control_url":"http://...","ws_url":"ws://.../lsdp.v1"}
//
// The driver waits for that line before invoking its conformance
// harness, so any output we write before this line risks being parsed
// as the discovery line. Keep startup quiet.
func cmdServeScenario(args []string) int {
	fs := flag.NewFlagSet("serve-scenario", flag.ExitOnError)
	wsPort := fs.Int("ws-port", 0, "TCP port for the LSDP/1 WebSocket endpoint (0 = OS-assigned)")
	controlPort := fs.Int("test-control-port", 0, "TCP port for the /test/* control plane (0 = OS-assigned)")
	host := fs.String("host", "127.0.0.1", "interface to bind both ports on")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Bind both ports up front so we can print their resolved values
	// in the discovery line. Net listeners stay around and are passed
	// into the http servers.
	wsLn, err := net.Listen("tcp", net.JoinHostPort(*host, strconv.Itoa(*wsPort)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve-scenario: bind ws: %v\n", err)
		return 1
	}
	controlLn, err := net.Listen("tcp", net.JoinHostPort(*host, strconv.Itoa(*controlPort)))
	if err != nil {
		_ = wsLn.Close()
		fmt.Fprintf(os.Stderr, "serve-scenario: bind control: %v\n", err)
		return 1
	}
	wsAddr := wsLn.Addr().(*net.TCPAddr)
	controlAddr := controlLn.Addr().(*net.TCPAddr)

	auth := server.NewStaticTokens(map[string]server.Identity{})
	srv, err := server.New(server.Config{
		ListenAddr: wsLn.Addr().String(),
		Auth:       auth,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve-scenario: build server: %v\n", err)
		return 1
	}

	wsURL := fmt.Sprintf("ws://%s/lsdp.v1", wsAddr.String())
	controlURL := fmt.Sprintf("http://%s", controlAddr.String())

	plane := control.New(srv, auth, wsURL)
	wsHTTP := &http.Server{
		Handler:           srv.Mux(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	controlHTTP := &http.Server{
		Handler:           plane.Mux(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	errCh := make(chan error, 2)
	go func() { errCh <- wsHTTP.Serve(wsLn) }()
	go func() { errCh <- controlHTTP.Serve(controlLn) }()

	// Discovery line — last thing we write before becoming silent so
	// the parent driver can consume stdout cleanly.
	enc := json.NewEncoder(os.Stdout)
	if err := enc.Encode(map[string]string{
		"control_url": controlURL,
		"ws_url":      wsURL,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "serve-scenario: emit discovery: %v\n", err)
	}

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "serve-scenario: serve: %v\n", err)
			cancel()
			_ = wsHTTP.Close()
			_ = controlHTTP.Close()
			return 1
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	_ = wsHTTP.Shutdown(shutdownCtx)
	_ = controlHTTP.Shutdown(shutdownCtx)
	return 0
}
