// Package conformance is the LSDP/1 conformance harness.
//
// The package embeds the canonical conformance fixtures and scenarios
// (vendored from Lumencast/lumencast-protocol) and ships a Go API
// suitable for testing both the kit's own server (in-process) and any
// LSDP/1-conformant server reachable by URL.
//
// Usage in tests :
//
//	//go:build conformance
//
//	func TestConformance(t *testing.T) {
//	    conformance.Run(t, conformance.Config{
//	        ServerURL: "ws://localhost:4000/lsdp.v1",
//	        TagFilter: conformance.TagRequired,
//	    })
//	}
//
// Usage from the CLI :
//
//	lumencast conformance --server ws://localhost:4000/lsdp.v1
package conformance

import (
	"embed"
	"io/fs"
)

//go:embed all:v1
var fixturesFS embed.FS

// FS returns the embedded filesystem rooted at conformance/v1, with
// scenarios under scenarios/ and fixture frames under fixtures/.
func FS() fs.FS {
	sub, err := fs.Sub(fixturesFS, "v1")
	if err != nil {
		// embed.FS guarantees the path exists at compile time ; an
		// error here is a programmer error.
		panic("conformance: embed root v1/ missing: " + err.Error())
	}
	return sub
}

// ListScenarios returns the file names of every scenario YAML in the
// embedded suite. Names are without the .yaml extension.
func ListScenarios() ([]string, error) {
	entries, err := fs.ReadDir(FS(), "scenarios")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) > 5 && name[len(name)-5:] == ".yaml" {
			out = append(out, name[:len(name)-5])
		}
	}
	return out, nil
}

// ReadScenario returns the raw YAML for the named scenario.
func ReadScenario(name string) ([]byte, error) {
	return fs.ReadFile(FS(), "scenarios/"+name+".yaml")
}
