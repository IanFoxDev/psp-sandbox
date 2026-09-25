package scenario

import (
	"fmt"
	"strings"
)

// Header is the request header that picks a scenario for a payment.
const Header = "X-Sandbox-Scenario"

// Spec is a scenario name with raw parameter values, as written in the header
// or in a rule.
type Spec struct {
	Name   string
	Params map[string]string
}

// ParseHeader parses "name; key=value; key=value".
func ParseHeader(s string) (Spec, error) {
	parts := strings.Split(s, ";")
	spec := Spec{Name: strings.TrimSpace(parts[0]), Params: map[string]string{}}
	if spec.Name == "" {
		return Spec{}, fmt.Errorf("%s: scenario name is empty", Header)
	}
	for _, part := range parts[1:] {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || key == "" {
			return Spec{}, fmt.Errorf("%s: want key=value, got %q", Header, part)
		}
		if _, dup := spec.Params[key]; dup {
			return Spec{}, fmt.Errorf("%s: parameter %q is set twice", Header, key)
		}
		spec.Params[key] = value
	}
	return spec, nil
}
