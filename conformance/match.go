package conformance

import (
	"fmt"
	"reflect"
	"regexp"
)

// Sentinels usable in expected frames for non-deterministic fields.
const (
	sentinelAny     = "$ANY"
	sentinelAnyHash = "$ANY_HASH"
)

var sha256Re = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// matchFrame compares an expected frame template (from the scenario)
// to an actual frame received over the wire. Non-deterministic fields
// in the template use $ANY / $ANY_HASH placeholders. Unknown fields in
// the actual frame are tolerated (forward-compat). Missing required
// fields fail.
func matchFrame(expected, actual map[string]any) error {
	for k, want := range expected {
		got, ok := actual[k]
		if !ok {
			return fmt.Errorf("missing field %q", k)
		}
		if err := matchValue(want, got, k); err != nil {
			return err
		}
	}
	return nil
}

// matchValue applies the matching rules recursively :
//   - $ANY  → matches any value (including missing? no — only present)
//   - $ANY_HASH → matches a sha256:<hex64> string
//   - map → recurse with matchFrame semantics on each key in template
//   - slice → length and element-wise match
//   - scalar → strict equality (with the YAML/JSON normalisation noise)
func matchValue(expected, actual any, path string) error {
	if s, ok := expected.(string); ok {
		switch s {
		case sentinelAny:
			return nil
		case sentinelAnyHash:
			str, ok := actual.(string)
			if !ok || !sha256Re.MatchString(str) {
				return fmt.Errorf("%s: not a sha256 hash : %v", path, actual)
			}
			return nil
		}
	}
	switch e := expected.(type) {
	case map[string]any:
		a, ok := actual.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: want map, got %T", path, actual)
		}
		for k, v := range e {
			av, ok := a[k]
			if !ok {
				return fmt.Errorf("%s.%s: missing", path, k)
			}
			if err := matchValue(v, av, path+"."+k); err != nil {
				return err
			}
		}
		return nil
	case []any:
		a, ok := actual.([]any)
		if !ok {
			return fmt.Errorf("%s: want array, got %T", path, actual)
		}
		if len(a) != len(e) {
			return fmt.Errorf("%s: length %d != %d", path, len(a), len(e))
		}
		for i := range e {
			if err := matchValue(e[i], a[i], fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
		return nil
	default:
		if !equalScalar(expected, actual) {
			return fmt.Errorf("%s: want %v (%T), got %v (%T)", path, expected, expected, actual, actual)
		}
		return nil
	}
}

// equalScalar normalises across numeric tower differences (YAML may
// produce int / float64 from the same value) before falling back to
// reflect.DeepEqual.
func equalScalar(want, got any) bool {
	wf, wOk := toFloat(want)
	gf, gOk := toFloat(got)
	if wOk && gOk {
		return wf == gf
	}
	return reflect.DeepEqual(want, got)
}

// toFloat reports whether v is a numeric type and returns its float
// representation.
func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case float32:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}
