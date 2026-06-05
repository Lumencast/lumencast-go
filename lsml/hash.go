package lsml

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// zeroHash is the placeholder injected into scene_version during
// canonicalisation, so the hash is computed over the bundle without
// being self-referential.
const zeroHash = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

// HashBundle computes the LSML 1.0 content hash for a bundle. Returns
// the lowercase hex sha256 plus the canonical JSON bytes. The bundle
// is not modified.
//
// Canonicalisation rules (spec § 3) :
//
//   - UTF-8
//   - Object keys sorted lexicographically at every level
//   - No insignificant whitespace
//   - scene_version replaced by zeroHash during the hash computation
func HashBundle(b *Bundle) (string, []byte, error) {
	raw, err := json.Marshal(b)
	if err != nil {
		return "", nil, err
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", nil, err
	}
	v = setSceneVersion(v, zeroHash)
	canon, err := canonicalize(v)
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:]), canon, nil
}

// setSceneVersion replaces the top-level scene_version field. v MUST
// be a map decoded from JSON.
func setSceneVersion(v any, replacement string) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	if _, exists := m["scene_version"]; exists {
		m["scene_version"] = replacement
	}
	return m
}

// canonicalize emits canonical JSON. Object keys are sorted ; numbers
// use Go's json shortest decimal representation (RFC 8259 compliant).
func canonicalize(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeCanonical(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonical(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if x {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case string:
		raw, err := marshalString(x)
		if err != nil {
			return err
		}
		buf.Write(raw)
	case json.Number:
		buf.WriteString(string(x))
	case float64:
		// json.Marshal already produces shortest form.
		raw, err := json.Marshal(x)
		if err != nil {
			return err
		}
		buf.Write(raw)
	case int:
		buf.WriteString(strconv.Itoa(x))
	case []any:
		buf.WriteByte('[')
		for i, item := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			raw, err := marshalString(k)
			if err != nil {
				return err
			}
			buf.Write(raw)
			buf.WriteByte(':')
			if err := writeCanonical(buf, x[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("canonical: unsupported type %T", v)
	}
	return nil
}

// marshalString JSON-encodes a string WITHOUT escaping &, <, > as
// &, <, >. Go's encoding/json escapes these by default
// (SetEscapeHTML(true)) as an anti-XSS measure for HTML embedding, but
// the TS reference serializer (@lumencast/compiler canonicalize.ts,
// JSON.stringify) does not. Disabling it keeps the canonical form
// byte-identical across SDKs, which the LSML content hash (spec § 3)
// requires — otherwise a string containing any of these characters
// produces a different scene_version in Go vs TS and the
// adopt-on-verify path (ADR 007 § C4) silently falls back to legacy.
//
// json.Encoder appends a trailing newline that json.Marshal does not,
// so it is trimmed.
func marshalString(s string) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}
