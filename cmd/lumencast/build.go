package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Lumencast/lumencast-go/lsml"
)

func cmdBuild(args []string) int {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	out := fs.String("out", "dist", "output directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	src := "."
	if fs.NArg() >= 1 {
		src = fs.Arg(0)
	}
	bundlePath := filepath.Join(src, "scene.json")
	raw, err := os.ReadFile(bundlePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lumencast build: %v\n", err)
		return 1
	}
	var bundle lsml.Bundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		fmt.Fprintf(os.Stderr, "lumencast build: parse %s: %v\n", bundlePath, err)
		return 1
	}
	rep := lsml.Validate(&bundle)
	if len(rep.Errors) > 0 {
		fmt.Fprintln(os.Stderr, "lumencast build: validation failed")
		for _, e := range rep.Errors {
			fmt.Fprintf(os.Stderr, "  %s — %s\n", e.Path, e.Message)
		}
		return 1
	}

	hash, canon, err := lsml.HashBundle(&bundle)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lumencast build: hash: %v\n", err)
		return 1
	}
	bundle.SceneVersion = "sha256:" + hash
	withVersion, err := json.Marshal(&bundle)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lumencast build: encode: %v\n", err)
		return 1
	}

	outDir := filepath.Join(*out, bundle.SceneID, hash)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "lumencast build: %v\n", err)
		return 1
	}
	if err := os.WriteFile(filepath.Join(outDir, "scene.json"), withVersion, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "lumencast build: %v\n", err)
		return 1
	}

	fmt.Printf("Built %s\n", outDir)
	fmt.Printf("  scene_id      : %s\n", bundle.SceneID)
	fmt.Printf("  scene_version : sha256:%s\n", hash)
	fmt.Printf("  canonical     : %d bytes\n", len(canon))
	return 0
}

