package scenario_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ianfoxdev/psp-sandbox/internal/app"
	"github.com/ianfoxdev/psp-sandbox/internal/config"
	"github.com/ianfoxdev/psp-sandbox/internal/sandboxtest"
	"github.com/ianfoxdev/psp-sandbox/internal/scenario"
)

const rulesYAML = `
rules:
  - when: { amount: 1313 }
    scenario: declined
    params: { reason: do_not_honor }
  - when: { reference_prefix: "dup-", currency: EUR }
    scenario: duplicate_callback
    params: { times: 3, parallel: true, interval: 10ms }
  - when: { metadata: { flow: slow, tier: gold } }
    scenario: delayed_callback
  - when: { reference_prefix: "dup-" }
    scenario: lost_callback
`

func TestRulesMatch(t *testing.T) {
	rs, err := scenario.ParseRules([]byte(rulesYAML), scenario.Builtin())
	if err != nil {
		t.Fatal(err)
	}
	if rs.Len() != 4 {
		t.Fatalf("Len = %d", rs.Len())
	}
	tests := []struct {
		name string
		in   scenario.Input
		want string
	}{
		{"amount", scenario.Input{Amount: 1313, Currency: "USD", Reference: "dup-1"}, "declined"},
		{"all keys of when", scenario.Input{Amount: 500, Currency: "EUR", Reference: "dup-1"}, "duplicate_callback"},
		{"falls through to next rule", scenario.Input{Amount: 500, Currency: "USD", Reference: "dup-1"}, "lost_callback"},
		{"all metadata keys", scenario.Input{Amount: 500, Metadata: map[string]string{"flow": "slow", "tier": "gold", "x": "y"}}, "delayed_callback"},
		{"missing metadata key", scenario.Input{Amount: 500, Metadata: map[string]string{"flow": "slow"}}, ""},
		{"no match", scenario.Input{Amount: 500, Currency: "EUR", Reference: "order-1"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, ok := rs.Match(tt.in)
			if spec.Name != tt.want || ok != (tt.want != "") {
				t.Fatalf("Match = %q, %v; want %q", spec.Name, ok, tt.want)
			}
		})
	}

	spec, _ := rs.Match(scenario.Input{Amount: 1, Currency: "EUR", Reference: "dup-1"})
	if spec.Params["times"] != "3" || spec.Params["parallel"] != "true" || spec.Params["interval"] != "10ms" {
		t.Fatalf("params = %v", spec.Params)
	}
}

func TestRulesNilAndEmpty(t *testing.T) {
	var rs *scenario.Rules
	if _, ok := rs.Match(scenario.Input{Amount: 1}); ok {
		t.Fatal("nil rules matched")
	}
	for _, src := range []string{"", "rules: []\n", "# nothing yet\n"} {
		rs, err := scenario.ParseRules([]byte(src), scenario.Builtin())
		if err != nil || rs.Len() != 0 {
			t.Fatalf("%q: %v, %d rules", src, err, rs.Len())
		}
	}
}

func TestRulesErrors(t *testing.T) {
	tests := []struct {
		name, src, want string
	}{
		{"unknown scenario", "rules:\n  - when: { amount: 1 }\n    scenario: happy\n", `rule 1: unknown scenario "happy"`},
		{"unknown param", "rules:\n  - when: { amount: 1 }\n    scenario: declined\n    params: { code: 51 }\n", `no parameter "code"`},
		{"bad param value", "rules:\n  - when: { amount: 1 }\n    scenario: duplicate_callback\n    params: { times: 100 }\n", "times must be an integer"},
		{"list param", "rules:\n  - when: { amount: 1 }\n    scenario: declined\n    params: { reason: [a, b] }\n", "must be a single value"},
		{"missing scenario", "rules:\n  - when: { amount: 1 }\n", "scenario is missing"},
		{"empty when", "rules:\n  - scenario: declined\n", "when is empty"},
		{"typo in when", "rules:\n  - when: { amounts: 1 }\n    scenario: declined\n", "field amounts not found"},
		{"typo at top", "rule:\n  - when: { amount: 1 }\n    scenario: declined\n", "field rule not found"},
		{"zero amount", "rules:\n  - when: { amount: 0 }\n    scenario: declined\n", "amount must be positive"},
		{"lowercase currency", "rules:\n  - when: { currency: eur }\n    scenario: declined\n", `currency must be an uppercase code such as EUR, got "eur"`},
		{"empty prefix", "rules:\n  - when: { reference_prefix: \"\" }\n    scenario: declined\n", "reference_prefix is empty"},
		{"second rule", "rules:\n  - when: { amount: 1 }\n    scenario: declined\n  - when: { amount: 2 }\n    scenario: nope\n", "rule 2:"},
		{"not yaml", "rules: [\n", "yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := scenario.ParseRules([]byte(tt.src), scenario.Builtin())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestExampleRulesFile(t *testing.T) {
	rs, err := scenario.LoadRules(filepath.Join("..", "..", "scenarios", "example.yaml"), scenario.Builtin())
	if err != nil {
		t.Fatal(err)
	}
	if rs.Len() == 0 {
		t.Fatal("example has no rules")
	}
}

func TestRulesOverHTTP(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scenarios.yaml")
	if err := os.WriteFile(path, []byte(rulesYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	sb := sandboxtest.New(t, func(c *config.Config) { c.ScenariosFile = path })

	p := sb.CreatePayment(map[string]any{"amount": 1313})
	if p["scenario"] != "declined" {
		t.Fatalf("rule not applied: %v", p)
	}
	if cb := sb.Receiver.Wait(1)[0]; cb.Type != "payment.failed" || cb.Data["failure_reason"] != "do_not_honor" {
		t.Fatalf("callback = %s", cb.Body)
	}

	// The header wins over the rules.
	p = sb.CreatePayment(map[string]any{"amount": 1313}, scenario.Header, "happy_path")
	if p["scenario"] != "happy_path" {
		t.Fatalf("header ignored: %v", p)
	}

	// No rule matches: the default scenario.
	p = sb.CreatePayment(map[string]any{"reference": "order-2"})
	if p["scenario"] != "happy_path" {
		t.Fatalf("default not applied: %v", p)
	}
}

func TestRulesFileErrorStopsStartup(t *testing.T) {
	cfg := sandboxtest.Config()
	for _, src := range []string{"", "rules:\n  - when: { amount: 1 }\n    scenario: nope\n"} {
		path := filepath.Join(t.TempDir(), "scenarios.yaml")
		if src != "" {
			if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		cfg.ScenariosFile = path
		a, err := app.New(cfg, nil, "test")
		if err == nil {
			a.Close()
			t.Fatalf("%q: app started", src)
		}
		if !strings.Contains(err.Error(), "PSP_SCENARIOS_FILE") {
			t.Fatalf("%q: err = %v", src, err)
		}
	}
}
