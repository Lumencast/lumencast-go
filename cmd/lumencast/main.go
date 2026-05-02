// Command lumencast is the developer tool that ships with the Go SDK.
//
// Subcommands :
//
//	lumencast init [name]       scaffold a new project
//	lumencast dev               run a mock server + local preview
//	lumencast validate <bundle> schema-validate an LSML bundle
//	lumencast conformance [...] run the LSDP/1 conformance suite
//	lumencast build [dir]       canonicalise + hash a bundle for prod
//	lumencast serve <dir>       static-serve a bundle for testing
//
// All subcommands use the standard flag package ; -h shows help.
package main

import (
	"fmt"
	"os"
)

// Version is stamped via -ldflags at release time.
var Version = "0.0.0-dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	switch cmd {
	case "init":
		os.Exit(cmdInit(args))
	case "dev":
		os.Exit(cmdDev(args))
	case "validate":
		os.Exit(cmdValidate(args))
	case "conformance":
		os.Exit(cmdConformance(args))
	case "build":
		os.Exit(cmdBuild(args))
	case "serve":
		os.Exit(cmdServe(args))
	case "version", "--version", "-v":
		fmt.Printf("lumencast %s\n", Version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "lumencast: unknown subcommand %q\n", cmd)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `lumencast — Lumencast CLI

Usage :
  lumencast <subcommand> [flags] [args...]

Subcommands :
  init       scaffold a new project
  dev        run a mock server + local preview
  validate   schema-validate an LSML bundle
  conformance run the LSDP/1 conformance suite
  build      canonicalise + hash a bundle for production
  serve      static-serve a bundle directory

Run "lumencast <subcommand> -h" for subcommand-specific help.`)
}
