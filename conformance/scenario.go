package conformance

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Tag is the conformance importance level. Required scenarios MUST
// pass for an implementation to call itself LSDP/1 conformant ;
// recommended scenarios are quality signals ; extended scenarios
// cover corner cases.
type Tag string

const (
	TagRequired    Tag = "required"
	TagRecommended Tag = "recommended"
	TagExtended    Tag = "extended"
)

// Target is the implementation under test for a scenario.
type Target string

const (
	TargetServer  Target = "server"  // runner acts as a client driving the server
	TargetRuntime Target = "runtime" // runner acts as a server driving the runtime
	TargetAny     Target = "any"     // runner exercises both
)

// Scenario is the parsed YAML representation of a conformance scenario.
type Scenario struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Tag         Tag            `yaml:"tag"`
	Target      Target         `yaml:"target"`
	SpecRefs    []string       `yaml:"spec_refs"`
	Bundles     []BundleDecl   `yaml:"bundles,omitempty"`
	Steps       []Step         `yaml:"steps"`
}

// BundleDecl declares an LSML bundle that the scenario references via
// the $BUNDLE.<id>.hash placeholder. The harness hashes the inline
// document (per LSML 1.0 § 3 canonicalisation) and substitutes the
// placeholders before sending frames.
type BundleDecl struct {
	ID     string                 `yaml:"id"`
	Inline map[string]any         `yaml:"inline"`

	// computed at scenario load time — not part of the YAML.
	hash string
}

// Hash returns the computed sha256:<hex> identity of the inline
// bundle. Empty until ComputeHashes has run.
func (b *BundleDecl) Hash() string { return b.hash }

// Step is one entry in the scenario's `steps` list. Only the fields
// relevant to its kind are populated.
type Step struct {
	Kind StepKind `yaml:"kind"`

	// Used by client-sends, server-sends.
	Frame map[string]any `yaml:"frame,omitempty"`

	// Used by expect-runtime-state, expect-server-state.
	State map[string]any `yaml:"state,omitempty"`

	// Used by expect-no-frame-for.
	DurationMs int `yaml:"duration_ms,omitempty"`

	// Used by expect-client-action.
	Action ClientAction `yaml:"action,omitempty"`
	Reason string       `yaml:"reason,omitempty"`
}

// StepKind enumerates the supported step verbs.
type StepKind string

const (
	StepClientSends         StepKind = "client-sends"
	StepServerSends         StepKind = "server-sends"
	StepServerEmits         StepKind = "server-emits"
	StepExpectRuntimeState  StepKind = "expect-runtime-state"
	StepExpectServerState   StepKind = "expect-server-state"
	StepExpectNoFrameFor    StepKind = "expect-no-frame-for"
	StepExpectClientAction  StepKind = "expect-client-action"
)

// ClientAction is the verb checked by expect-client-action steps.
type ClientAction string

const (
	ActionCloseWithReason ClientAction = "close-with-reason"
	ActionReconnect       ClientAction = "reconnect"
)

// ParseScenario decodes one YAML document into a Scenario value.
// Bundle hashes are NOT computed here — call ComputeBundleHashes when
// the scenario is about to run.
func ParseScenario(raw []byte) (*Scenario, error) {
	var s Scenario
	if err := yaml.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("conformance: parse scenario: %w", err)
	}
	if s.Name == "" {
		return nil, fmt.Errorf("conformance: scenario missing name")
	}
	if s.Target == "" {
		s.Target = TargetAny
	}
	if s.Tag == "" {
		s.Tag = TagRequired
	}
	return &s, nil
}

// ComputeBundleHashes hashes every inline bundle declared on the
// scenario. The result populates BundleDecl.hash and is exposed via
// the BundleHashes map for substitution.
func (s *Scenario) ComputeBundleHashes() (map[string]string, error) {
	out := make(map[string]string, len(s.Bundles))
	for i := range s.Bundles {
		b := &s.Bundles[i]
		h, err := hashInlineBundle(b.Inline)
		if err != nil {
			return nil, fmt.Errorf("bundle %q: %w", b.ID, err)
		}
		b.hash = h
		out[b.ID] = h
	}
	return out, nil
}

// Load reads, parses, and returns every embedded scenario.
func Load() ([]*Scenario, error) {
	names, err := ListScenarios()
	if err != nil {
		return nil, err
	}
	out := make([]*Scenario, 0, len(names))
	for _, n := range names {
		raw, err := ReadScenario(n)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", n, err)
		}
		s, err := ParseScenario(raw)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", n, err)
		}
		out = append(out, s)
	}
	return out, nil
}
