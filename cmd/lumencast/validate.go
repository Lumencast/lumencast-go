package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Lumencast/lumencast-go/lsml"
)

func cmdValidate(args []string) int {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "lumencast validate: bundle path required")
		return 2
	}
	path := fs.Arg(0)
	raw, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", path, err)
		return 1
	}
	var bundle lsml.Bundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		fmt.Fprintf(os.Stderr, "parse %s: %v\n", path, err)
		return 1
	}
	report := lsml.Validate(&bundle)
	if len(report.Errors) == 0 {
		fmt.Printf("OK — %s validates against LSML 1.0\n", path)
		if len(report.Warnings) > 0 {
			fmt.Println("Warnings :")
			for _, w := range report.Warnings {
				fmt.Printf("  %s — %s\n", w.Path, w.Message)
			}
		}
		return 0
	}
	fmt.Fprintf(os.Stderr, "FAIL — %s\n", path)
	for _, e := range report.Errors {
		fmt.Fprintf(os.Stderr, "  %s — %s\n", e.Path, e.Message)
	}
	if len(report.Warnings) > 0 {
		fmt.Fprintln(os.Stderr, "Warnings :")
		for _, w := range report.Warnings {
			fmt.Fprintf(os.Stderr, "  %s — %s\n", w.Path, w.Message)
		}
	}
	if !strings.HasSuffix(path, ".json") {
		fmt.Fprintln(os.Stderr, "Note: bundles are JSON files (.json).")
	}
	return 1
}
