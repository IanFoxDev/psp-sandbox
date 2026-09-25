package scenario

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Definition describes a scenario in the catalog.
type Definition struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Params      []Param `json:"params"`

	build func(v *Values) Scenario
}

// Param is one parameter of a scenario.
type Param struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"` // string, int, bool or duration
	Default     string   `json:"default"`
	Allowed     []string `json:"allowed,omitempty"`
	Description string   `json:"description"`
}

// Catalog is the set of known scenarios.
type Catalog struct {
	defs map[string]Definition
}

// NewCatalog returns a catalog with the given definitions.
func NewCatalog(defs ...Definition) *Catalog {
	c := &Catalog{defs: map[string]Definition{}}
	for _, d := range defs {
		c.defs[d.Name] = d
	}
	return c
}

// Builtin returns the catalog of scenarios shipped with the sandbox.
func Builtin() *Catalog {
	return NewCatalog(builtin...)
}

// builtin is filled by the init functions of the scenario files.
var builtin []Definition

func register(d Definition) {
	builtin = append(builtin, d)
}

// List returns all definitions sorted by name.
func (c *Catalog) List() []Definition {
	out := make([]Definition, 0, len(c.defs))
	for _, d := range c.defs {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Build returns the scenario for spec, with parameters checked against the
// definition and defaults filled in.
func (c *Catalog) Build(spec Spec) (Scenario, error) {
	d, ok := c.defs[spec.Name]
	if !ok {
		return nil, fmt.Errorf("unknown scenario %q", spec.Name)
	}
	for name := range spec.Params {
		if !slices.ContainsFunc(d.Params, func(p Param) bool { return p.Name == name }) {
			return nil, fmt.Errorf("scenario %s has no parameter %q", d.Name, name)
		}
	}
	v := &Values{def: d, raw: spec.Params}
	s := d.build(v)
	if err := errors.Join(v.errs...); err != nil {
		return nil, fmt.Errorf("scenario %s: %w", d.Name, err)
	}
	return s, nil
}

// Values reads typed parameters for one build. Errors are collected and
// reported by Build, so build functions stay short.
type Values struct {
	def  Definition
	raw  map[string]string
	errs []error
}

func (v *Values) get(name string) string {
	var p *Param
	for i := range v.def.Params {
		if v.def.Params[i].Name == name {
			p = &v.def.Params[i]
		}
	}
	if p == nil {
		panic("scenario " + v.def.Name + " reads undeclared parameter " + name)
	}
	s, ok := v.raw[name]
	if !ok {
		return p.Default
	}
	if len(p.Allowed) > 0 && !slices.Contains(p.Allowed, s) {
		v.errs = append(v.errs, fmt.Errorf("%s must be one of %s, got %q", name, strings.Join(p.Allowed, ", "), s))
	}
	return s
}

// String returns a string parameter.
func (v *Values) String(name string) string {
	return v.get(name)
}

// Int returns an integer parameter.
func (v *Values) Int(name string, lo, hi int) int {
	s := v.get(name)
	n, err := strconv.Atoi(s)
	if err != nil || n < lo || n > hi {
		v.errs = append(v.errs, fmt.Errorf("%s must be an integer from %d to %d, got %q", name, lo, hi, s))
	}
	return n
}

// Bool returns a boolean parameter.
func (v *Values) Bool(name string) bool {
	s := v.get(name)
	b, err := strconv.ParseBool(s)
	if err != nil {
		v.errs = append(v.errs, fmt.Errorf("%s must be true or false, got %q", name, s))
	}
	return b
}

// Duration returns a duration parameter in Go syntax (500ms, 35s, 2m).
func (v *Values) Duration(name string) time.Duration {
	s := v.get(name)
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		v.errs = append(v.errs, fmt.Errorf("%s must be a duration such as 500ms or 35s, got %q", name, s))
	}
	return d
}
