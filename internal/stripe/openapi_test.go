package stripe_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

// The Stripe OpenAPI spec the profile follows (ADR 0005). make stripe-spec
// downloads it from github.com/stripe/openapi at this commit; the hash makes
// sure the file is that one.
const (
	stripeSpecCommit = "6f855712dfc6a235a407136e630bf36a01c069a3"
	stripeSpecSHA256 = "7cff4cc46d0654101a36a3302f7544640f5f73e4f3cad6f264f0e23c19fa1776"
)

// stripeSpec is the part of the spec the contract test reads.
type stripeSpec struct {
	Paths map[string]map[string]struct {
		Responses map[string]struct {
			Content map[string]struct {
				Schema *schema `json:"schema"`
			} `json:"content"`
		} `json:"responses"`
	} `json:"paths"`
	Components struct {
		Schemas map[string]*schema `json:"schemas"`
	} `json:"components"`

	patterns map[string]*regexp.Regexp
}

// schema holds the OpenAPI 3.0 keywords the Stripe spec uses for responses.
// Others (format, description, x-*) do not constrain values here.
type schema struct {
	Ref                  string             `json:"$ref"`
	Type                 string             `json:"type"`
	Nullable             bool               `json:"nullable"`
	Enum                 []any              `json:"enum"`
	AnyOf                []*schema          `json:"anyOf"`
	Properties           map[string]*schema `json:"properties"`
	Required             []string           `json:"required"`
	Items                *schema            `json:"items"`
	AdditionalProperties json.RawMessage    `json:"additionalProperties"`
	MaxLength            *int               `json:"maxLength"`
	Pattern              string             `json:"pattern"`
}

// loadStripeSpec reads the spec from STRIPE_OPENAPI_SPEC, or skips the test
// when it is not set (make contract sets it).
func loadStripeSpec(t *testing.T) *stripeSpec {
	t.Helper()
	path := os.Getenv("STRIPE_OPENAPI_SPEC")
	if path == "" {
		t.Skip("STRIPE_OPENAPI_SPEC is not set; run make contract")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != stripeSpecSHA256 {
		t.Fatalf("%s has sha256 %s, want %s (stripe/openapi at %s)", path, got, stripeSpecSHA256, stripeSpecCommit)
	}
	var sp stripeSpec
	if err := json.Unmarshal(raw, &sp); err != nil {
		t.Fatal(err)
	}
	sp.patterns = map[string]*regexp.Regexp{}
	return &sp
}

// responseSchema is the schema of a 200 answer of an operation, by the path
// template as the spec writes it.
func (sp *stripeSpec) responseSchema(t *testing.T, method, path string) *schema {
	t.Helper()
	op, ok := sp.Paths[path][strings.ToLower(method)]
	if !ok {
		t.Fatalf("the Stripe spec has no %s %s", method, path)
	}
	return op.Responses["200"].Content["application/json"].Schema
}

func (sp *stripeSpec) component(name string) *schema {
	return &schema{Ref: "#/components/schemas/" + name}
}

// check validates a JSON document against s and returns the problems found,
// sorted. On top of the schema it reports fields the schema does not have,
// so a misspelled or invented field fails too.
func (sp *stripeSpec) check(doc []byte, s *schema) []string {
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return []string{"not JSON: " + err.Error()}
	}
	var problems []string
	sp.validate("$", v, s, &problems)
	sort.Strings(problems)
	return problems
}

func (sp *stripeSpec) resolve(s *schema) *schema {
	for s != nil && s.Ref != "" {
		s = sp.Components.Schemas[strings.TrimPrefix(s.Ref, "#/components/schemas/")]
	}
	return s
}

func (sp *stripeSpec) validate(at string, v any, s *schema, problems *[]string) {
	s = sp.resolve(s)
	if s == nil {
		*problems = append(*problems, at+": schema not found")
		return
	}
	fail := func(format string, args ...any) {
		*problems = append(*problems, at+": "+fmt.Sprintf(format, args...))
	}
	if v == nil {
		if !s.Nullable {
			fail("null, but not nullable")
		}
		return
	}
	if len(s.AnyOf) > 0 {
		var all []string
		for _, alt := range s.AnyOf {
			var p []string
			sp.validate(at, v, alt, &p)
			if len(p) == 0 {
				return
			}
			all = append(all, p...)
		}
		fail("matches none of anyOf: %s", strings.Join(all, "; "))
		return
	}
	if len(s.Enum) > 0 && !slices.ContainsFunc(s.Enum, func(e any) bool { return fmt.Sprint(e) == fmt.Sprint(v) }) {
		fail("%v is not one of %v", v, s.Enum)
	}
	typ := s.Type
	if typ == "" && s.Properties != nil {
		typ = "object"
	}
	switch typ {
	case "string":
		str, ok := v.(string)
		if !ok {
			fail("want a string, got %T", v)
			return
		}
		if s.MaxLength != nil && utf8.RuneCountInString(str) > *s.MaxLength {
			fail("longer than %d", *s.MaxLength)
		}
		if s.Pattern != "" {
			re, ok := sp.patterns[s.Pattern]
			if !ok {
				re = regexp.MustCompile(s.Pattern)
				sp.patterns[s.Pattern] = re
			}
			if !re.MatchString(str) {
				fail("%q does not match %s", str, s.Pattern)
			}
		}
	case "integer":
		n, ok := v.(json.Number)
		if _, err := n.Int64(); !ok || err != nil {
			fail("want an integer, got %v", v)
		}
	case "number":
		if _, ok := v.(json.Number); !ok {
			fail("want a number, got %T", v)
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			fail("want a boolean, got %T", v)
		}
	case "array":
		arr, ok := v.([]any)
		if !ok {
			fail("want an array, got %T", v)
			return
		}
		for i, item := range arr {
			sp.validate(fmt.Sprintf("%s[%d]", at, i), item, s.Items, problems)
		}
	case "object":
		obj, ok := v.(map[string]any)
		if !ok {
			fail("want an object, got %T", v)
			return
		}
		for _, name := range s.Required {
			if _, ok := obj[name]; !ok {
				fail("required field %s is missing", name)
			}
		}
		extra := sp.additional(s)
		for name, fv := range obj {
			switch prop, ok := s.Properties[name]; {
			case ok:
				sp.validate(at+"."+name, fv, prop, problems)
			case extra != nil:
				sp.validate(at+"."+name, fv, extra, problems)
			case len(s.Properties) > 0 && string(s.AdditionalProperties) != "true":
				fail("field %s is not in the Stripe schema", name)
			}
		}
	}
}

// additional returns the schema of additionalProperties when it is one.
func (sp *stripeSpec) additional(s *schema) *schema {
	raw := bytes.TrimSpace(s.AdditionalProperties)
	if len(raw) == 0 || raw[0] != '{' {
		return nil
	}
	var extra schema
	if err := json.Unmarshal(raw, &extra); err != nil {
		return nil
	}
	return &extra
}

// TestValidatorCatchesProblems keeps the validator honest: each document
// breaks the payment_intent schema in one way.
func TestValidatorCatchesProblems(t *testing.T) {
	sp := loadStripeSpec(t)
	pi := sp.component("payment_intent")
	good := `{"id":"pi_1","object":"payment_intent","created":1,"livemode":false,"status":"succeeded","amount":1}`
	if p := sp.check([]byte(good), pi); len(p) != 0 {
		t.Fatalf("valid document rejected: %v", p)
	}
	for _, bad := range []string{
		`{"object":"payment_intent","created":1,"livemode":false,"status":"succeeded"}`,
		`{"id":"pi_1","object":"payment_intent","created":1,"livemode":false,"status":"done"}`,
		`{"id":"pi_1","object":"payment_intent","created":1.5,"livemode":false,"status":"succeeded"}`,
		`{"id":"pi_1","object":"payment_intent","created":1,"livemode":null,"status":"succeeded"}`,
		`{"id":"pi_1","object":"payment_intent","created":1,"livemode":false,"status":"succeeded","colour":"red"}`,
		`{"id":"pi_1","object":"payment_intent","created":1,"livemode":false,"status":"succeeded","metadata":{"a":1}}`,
		`{"id":"pi_1","object":"payment_intent","created":1,"livemode":false,"status":"succeeded","latest_charge":{"id":"ch_1"}}`,
	} {
		if p := sp.check([]byte(bad), pi); len(p) == 0 {
			t.Errorf("accepted %s", bad)
		}
	}
}
