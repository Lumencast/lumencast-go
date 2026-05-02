package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/Lumencast/lumencast-go/server"
)

func cmdServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", ":8080", "listen address")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "lumencast serve: bundle directory required")
		return 2
	}
	dir := fs.Arg(0)
	if _, err := os.Stat(dir); err != nil {
		fmt.Fprintf(os.Stderr, "lumencast serve: %v\n", err)
		return 1
	}
	handler := server.StaticBundles(dir)
	fmt.Printf("lumencast serve — %s on %s\n", dir, *addr)
	if err := http.ListenAndServe(*addr, handler); err != nil { //nolint:gosec
		fmt.Fprintf(os.Stderr, "lumencast serve: %v\n", err)
		return 1
	}
	return 0
}
