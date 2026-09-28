package scenario

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/ianfoxdev/psp-sandbox/internal/payment"
)

// Rules pick a scenario for payments created without the X-Sandbox-Scenario
// header. They are checked top to bottom, the first match wins.
type Rules struct {
	rules []Rule
}

// Rule is one entry of the rules file.
type Rule struct {
	When When
	Spec Spec
}

// When is the condition of a rule. Every condition that is set must match.
type When struct {
	Amount          *int64
	Currency        string
	ReferencePrefix string
	Metadata        map[string]string
}

// Input is what a rule is matched against.
type Input struct {
	Amount    int64
	Currency  string
	Reference string
	Metadata  map[string]string
}

// file is the YAML layout of the rules file.
type file struct {
	Rules []struct {
		When     *fileWhen            `yaml:"when"`
		Scenario string               `yaml:"scenario"`
		Params   map[string]yaml.Node `yaml:"params"`
	} `yaml:"rules"`
}

type fileWhen struct {
	Amount          *int64            `yaml:"amount"`
	Currency        *string           `yaml:"currency"`
	ReferencePrefix *string           `yaml:"reference_prefix"`
	Metadata        map[string]string `yaml:"metadata"`
}

// LoadRules reads a rules file. See ParseRules.
func LoadRules(path string, c *Catalog) (*Rules, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseRules(data, c)
}

// ParseRules parses a rules file and checks every rule against the catalog, so
// a typo fails at startup instead of silently falling back to the default
// scenario. Unknown keys are errors too.
func ParseRules(data []byte, c *Catalog) (*Rules, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var f file
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}

	rs := &Rules{}
	var errs []error
	for i, raw := range f.Rules {
		r, err := buildRule(raw.When, raw.Scenario, raw.Params, c)
		if err != nil {
			errs = append(errs, fmt.Errorf("rule %d: %w", i+1, err))
			continue
		}
		rs.rules = append(rs.rules, r)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return rs, nil
}

func buildRule(w *fileWhen, name string, params map[string]yaml.Node, c *Catalog) (Rule, error) {
	if w == nil || (w.Amount == nil && w.Currency == nil && w.ReferencePrefix == nil && len(w.Metadata) == 0) {
		return Rule{}, errors.New("when is empty; use PSP_DEFAULT_SCENARIO for a catch-all")
	}
	r := Rule{When: When{Amount: w.Amount, Metadata: w.Metadata}}
	if w.Amount != nil && *w.Amount <= 0 {
		return Rule{}, fmt.Errorf("amount must be positive, got %d", *w.Amount)
	}
	if w.Currency != nil {
		if !payment.ValidCurrency(*w.Currency) {
			return Rule{}, fmt.Errorf("currency must be an uppercase code such as EUR, got %q", *w.Currency)
		}
		r.When.Currency = *w.Currency
	}
	if w.ReferencePrefix != nil {
		if *w.ReferencePrefix == "" {
			return Rule{}, errors.New("reference_prefix is empty")
		}
		r.When.ReferencePrefix = *w.ReferencePrefix
	}

	if name == "" {
		return Rule{}, errors.New("scenario is missing")
	}
	r.Spec = Spec{Name: name, Params: map[string]string{}}
	for key, node := range params {
		if node.Kind != yaml.ScalarNode {
			return Rule{}, fmt.Errorf("parameter %q must be a single value", key)
		}
		r.Spec.Params[key] = node.Value
	}
	if _, err := c.Build(r.Spec); err != nil {
		return Rule{}, err
	}
	return r, nil
}

// Len returns the number of rules.
func (rs *Rules) Len() int {
	if rs == nil {
		return 0
	}
	return len(rs.rules)
}

// Match returns the scenario of the first rule that matches in.
func (rs *Rules) Match(in Input) (Spec, bool) {
	if rs == nil {
		return Spec{}, false
	}
	for _, r := range rs.rules {
		if r.When.matches(in) {
			return r.Spec, true
		}
	}
	return Spec{}, false
}

func (w When) matches(in Input) bool {
	if w.Amount != nil && *w.Amount != in.Amount {
		return false
	}
	if w.Currency != "" && w.Currency != in.Currency {
		return false
	}
	if w.ReferencePrefix != "" && !strings.HasPrefix(in.Reference, w.ReferencePrefix) {
		return false
	}
	for k, v := range w.Metadata {
		if got, ok := in.Metadata[k]; !ok || got != v {
			return false
		}
	}
	return true
}
