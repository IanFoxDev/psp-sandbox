package stripe

import (
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// Params are the parameters of one request, decoded from Stripe's form
// encoding: metadata[order_id]=42, expand[0]=latest_charge. Hand-written
// requests may also use expand[]=latest_charge.
//
// Accessors remember which top-level names were read, so that the rest can be
// reported as ignored.
type Params struct {
	root *node
	read map[string]bool
}

type node struct {
	value    *string
	children map[string]*node
}

// ParamError is a problem with one parameter, reported as Stripe does.
type ParamError struct {
	Code    string
	Param   string
	Message string
}

func (e *ParamError) Error() string { return e.Message }

// ParseParams decodes a form-encoded body or query string. Repeated keys keep
// the last value. A key used both as a value and as a hash is an error.
func ParseParams(raw string) (*Params, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return nil, &ParamError{Message: "Invalid form encoding: " + err.Error()}
	}
	p := &Params{root: &node{children: map[string]*node{}}, read: map[string]bool{}}
	// url.ParseQuery loses the order of keys; indexes of "[]" lists follow the
	// order inside one key, which is kept.
	for _, key := range slices.Sorted(maps.Keys(values)) {
		path, err := splitKey(key)
		if err != nil {
			return nil, err
		}
		for _, v := range values[key] {
			if err := p.root.set(path, v, key); err != nil {
				return nil, err
			}
		}
	}
	return p, nil
}

// splitKey turns "a[b][0]" into ["a", "b", "0"].
func splitKey(key string) ([]string, error) {
	name, rest, nested := strings.Cut(key, "[")
	if name == "" {
		return nil, &ParamError{Code: "parameter_unknown", Param: key, Message: "Invalid parameter name: " + key}
	}
	path := []string{name}
	if !nested {
		return path, nil
	}
	rest = "[" + rest
	for rest != "" {
		end := strings.IndexByte(rest, ']')
		if rest[0] != '[' || end < 0 {
			return nil, &ParamError{Code: "parameter_unknown", Param: key, Message: "Invalid parameter name: " + key}
		}
		path = append(path, rest[1:end])
		rest = rest[end+1:]
	}
	return path, nil
}

func (n *node) set(path []string, v, key string) error {
	if len(path) == 0 {
		if n.children != nil {
			return mixed(key)
		}
		n.value = &v
		return nil
	}
	if n.value != nil {
		return mixed(key)
	}
	if n.children == nil {
		n.children = map[string]*node{}
	}
	seg := path[0]
	if seg == "" {
		// expand[]=a&expand[]=b: each value is the next element.
		seg = strconv.Itoa(len(n.children))
	}
	child, ok := n.children[seg]
	if !ok {
		child = &node{}
		n.children[seg] = child
	}
	return child.set(path[1:], v, key)
}

func mixed(key string) error {
	return &ParamError{Code: "parameter_invalid_empty", Param: key,
		Message: "Invalid parameter " + key + ": it is sent both as a value and as a hash."}
}

func (p *Params) get(name string) *node {
	p.read[name] = true
	return p.root.children[name]
}

// Has reports whether the parameter was sent.
func (p *Params) Has(name string) bool {
	return p.get(name) != nil
}

// String returns a plain value.
func (p *Params) String(name string) (string, bool, error) {
	n := p.get(name)
	if n == nil {
		return "", false, nil
	}
	if n.value == nil {
		return "", false, &ParamError{Code: "parameter_invalid_string", Param: name,
			Message: "Invalid " + name + ": must be a string"}
	}
	return *n.value, true, nil
}

// Int returns an integer value.
func (p *Params) Int(name string) (int64, bool, error) {
	s, ok, err := p.String(name)
	if !ok || err != nil {
		return 0, ok, err
	}
	i, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, true, &ParamError{Code: "parameter_invalid_integer", Param: name,
			Message: "Invalid integer: " + s}
	}
	return i, true, nil
}

// Bool returns true or false.
func (p *Params) Bool(name string) (bool, bool, error) {
	s, ok, err := p.String(name)
	if !ok || err != nil {
		return false, ok, err
	}
	switch s {
	case "true":
		return true, true, nil
	case "false":
		return false, true, nil
	}
	return false, true, &ParamError{Param: name, Message: "Invalid boolean: " + s}
}

// Map returns a hash of plain values, such as metadata. An empty string
// clears the whole hash, as in metadata=.
func (p *Params) Map(name string) (map[string]string, bool, error) {
	n := p.get(name)
	if n == nil {
		return nil, false, nil
	}
	if n.value != nil {
		if *n.value == "" {
			return map[string]string{}, true, nil
		}
		return nil, true, &ParamError{Code: "parameter_invalid_empty", Param: name,
			Message: "Invalid " + name + ": must be a hash"}
	}
	out := make(map[string]string, len(n.children))
	for k, c := range n.children {
		if c.value == nil {
			return nil, true, &ParamError{Param: name + "[" + k + "]",
				Message: fmt.Sprintf("Invalid %s[%s]: must be a string", name, k)}
		}
		out[k] = *c.value
	}
	return out, true, nil
}

// List returns an array of plain values in index order.
func (p *Params) List(name string) ([]string, bool, error) {
	n := p.get(name)
	if n == nil {
		return nil, false, nil
	}
	invalid := &ParamError{Code: "parameter_invalid_empty", Param: name, Message: "Invalid array: " + name}
	if n.value != nil {
		if *n.value == "" {
			return []string{}, true, nil
		}
		return nil, true, invalid
	}
	type item struct {
		i int
		v string
	}
	items := make([]item, 0, len(n.children))
	for k, c := range n.children {
		i, err := strconv.Atoi(k)
		if err != nil || i < 0 || c.value == nil {
			return nil, true, invalid
		}
		items = append(items, item{i, *c.value})
	}
	slices.SortFunc(items, func(a, b item) int { return a.i - b.i })
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.v
	}
	return out, true, nil
}

// Unread returns the top-level names that no accessor asked for, sorted.
func (p *Params) Unread() []string {
	var out []string
	for name := range p.root.children {
		if !p.read[name] {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}
