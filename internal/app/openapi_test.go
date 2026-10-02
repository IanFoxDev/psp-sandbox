package app_test

import (
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/ianfoxdev/psp-sandbox/internal/sandboxtest"
	"github.com/ianfoxdev/psp-sandbox/internal/scenario"
)

type spec struct {
	Paths      map[string]map[string]any `yaml:"paths"`
	Components struct {
		Schemas map[string]schema `yaml:"schemas"`
	} `yaml:"components"`
}

type schema struct {
	Required   []string       `yaml:"required"`
	Properties map[string]any `yaml:"properties"`
}

func loadSpec(t *testing.T) spec {
	t.Helper()
	raw, err := os.ReadFile("../../docs/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var s spec
	if err := yaml.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// route matches a registration such as mux.HandleFunc("GET /v1/payments", ...).
var route = regexp.MustCompile(`\.Handle(?:Func)?\("([A-Z]+) (/[^"]*)"`)

// TestSpecRoutes keeps docs/openapi.yaml and the registered routes in step:
// every route is in the spec and every operation in the spec is served. The
// web UI is HTML for people and stays out of the spec. The Stripe profile is
// described by Stripe's own spec (ADR 0005), so internal/stripe is skipped.
func TestSpecRoutes(t *testing.T) {
	inCode := map[string]bool{}
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && filepath.Base(path) == "stripe" {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range route.FindAllStringSubmatch(string(src), -1) {
			if m[2] == "/_sandbox/{$}" || strings.HasPrefix(m[2], "/_sandbox/ui/") {
				continue
			}
			inCode[m[1]+" "+m[2]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	inSpec := map[string]bool{}
	for path, ops := range loadSpec(t).Paths {
		for method := range ops {
			inSpec[strings.ToUpper(method)+" "+path] = true
		}
	}

	for _, r := range slices.Sorted(maps.Keys(inCode)) {
		if !inSpec[r] {
			t.Errorf("route %s is not in docs/openapi.yaml", r)
		}
	}
	for _, r := range slices.Sorted(maps.Keys(inSpec)) {
		if !inCode[r] {
			t.Errorf("docs/openapi.yaml has %s, but no such route is registered", r)
		}
	}
}

// TestSpecSchemas checks real answers against the schemas: every field the
// server sends is described, and every required field is sent.
func TestSpecSchemas(t *testing.T) {
	s := loadSpec(t)
	sb := sandboxtest.New(t)

	p := sb.CreatePayment(map[string]any{"metadata": map[string]any{"cart": "7"}})
	id := p["id"].(string)
	checkSchema(t, s, "Payment", p)
	sb.Receiver.Wait(1)
	sb.WaitStatus(id, "captured")

	refund := sb.Do(http.MethodPost, "/v1/payments/"+id+"/refunds", map[string]any{"amount": 100, "reference": "r-1"}).JSON(t)
	checkSchema(t, s, "Refund", refund)

	failed := sb.CreatePayment(nil, scenario.Header, "declined")
	sb.Receiver.Wait(3)
	checkSchema(t, s, "Payment", sb.WaitStatus(failed["id"].(string), "failed"))

	dels := waitDeliveries(t, sb, id, 2)
	for _, d := range dels {
		checkSchema(t, s, "Delivery", d)
		checkSchema(t, s, "Event", d["body"].(map[string]any))
		for _, a := range d["attempts"].([]any) {
			checkSchema(t, s, "Attempt", a.(map[string]any))
		}
	}
	for _, e := range sb.Do(http.MethodGet, "/_sandbox/payments/"+id+"/events", nil).JSON(t)["data"].([]any) {
		checkSchema(t, s, "Event", e.(map[string]any))
	}
	for _, d := range sb.Do(http.MethodGet, "/_sandbox/scenarios", nil).JSON(t)["data"].([]any) {
		checkSchema(t, s, "ScenarioDefinition", d.(map[string]any))
	}
	checkSchema(t, s, "Clock", sb.Do(http.MethodGet, "/_sandbox/clock", nil).JSON(t))
	checkSchema(t, s, "Error", sb.Do(http.MethodGet, "/v1/payments/pay_none", nil).JSON(t))
}

func checkSchema(t *testing.T, s spec, name string, obj map[string]any) {
	t.Helper()
	sc, ok := s.Components.Schemas[name]
	if !ok {
		t.Fatalf("no schema %s in docs/openapi.yaml", name)
	}
	for _, key := range slices.Sorted(maps.Keys(obj)) {
		if _, ok := sc.Properties[key]; !ok {
			t.Errorf("%s: the server sends %q, the schema does not describe it", name, key)
		}
	}
	for _, key := range sc.Required {
		if _, ok := obj[key]; !ok {
			t.Errorf("%s: %q is required in the schema, the server did not send it", name, key)
		}
	}
}

// waitDeliveries waits until the payment has n finished deliveries.
func waitDeliveries(t *testing.T, sb *sandboxtest.Sandbox, id string, n int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(sandboxtest.Wait)
	for time.Now().Before(deadline) {
		var out []map[string]any
		for _, d := range sb.Do(http.MethodGet, "/_sandbox/payments/"+id+"/deliveries", nil).JSON(t)["data"].([]any) {
			if d := d.(map[string]any); d["status"] != "pending" {
				out = append(out, d)
			}
		}
		if len(out) >= n {
			return out
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("payment %s has fewer than %d finished deliveries", id, n)
	return nil
}
