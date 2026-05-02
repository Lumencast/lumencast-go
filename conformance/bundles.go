package conformance

import (
	"encoding/json"

	"github.com/Lumencast/lumencast-go/lsml"
)

// hashInlineBundle marshals an inline bundle (as parsed from YAML
// into a map[string]any) to LSML and runs it through lsml.HashBundle
// to get the canonical sha256:<hex>. Used by ComputeBundleHashes.
func hashInlineBundle(inline map[string]any) (string, error) {
	raw, err := json.Marshal(inline)
	if err != nil {
		return "", err
	}
	var b lsml.Bundle
	if err := json.Unmarshal(raw, &b); err != nil {
		return "", err
	}
	hex, _, err := lsml.HashBundle(&b)
	if err != nil {
		return "", err
	}
	return "sha256:" + hex, nil
}
