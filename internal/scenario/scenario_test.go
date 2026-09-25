package scenario

import (
	"strings"
	"testing"
	"time"

	"github.com/ghuser/psp-sandbox/internal/payment"
)

func TestParseHeader(t *testing.T) {
	spec, err := ParseHeader(" duplicate_callback ; times=3;parallel = true; ")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Name != "duplicate_callback" || spec.Params["times"] != "3" || spec.Params["parallel"] != "true" || len(spec.Params) != 2 {
		t.Fatalf("spec = %+v", spec)
	}
}

func TestParseHeaderErrors(t *testing.T) {
	for _, h := range []string{"", " ; times=3", "declined; reason", "declined; =x", "x; a=1; a=2"} {
		if _, err := ParseHeader(h); err == nil {
			t.Errorf("ParseHeader(%q) accepted invalid input", h)
		}
	}
}

func testCatalog() *Catalog {
	return NewCatalog(Definition{
		Name: "test",
		Params: []Param{
			{Name: "n", Type: "int", Default: "2"},
			{Name: "on", Type: "bool", Default: "false"},
			{Name: "wait", Type: "duration", Default: "1s"},
			{Name: "mode", Type: "string", Default: "a", Allowed: []string{"a", "b"}},
		},
		build: func(v *Values) Scenario {
			return testScenario{n: v.Int("n", 1, 10), on: v.Bool("on"), wait: v.Duration("wait"), mode: v.String("mode")}
		},
	})
}

type testScenario struct {
	base
	n    int
	on   bool
	wait time.Duration
	mode string
}

func TestBuildDefaults(t *testing.T) {
	s, err := testCatalog().Build(Spec{Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	got := s.(testScenario)
	if got.n != 2 || got.on || got.wait != time.Second || got.mode != "a" {
		t.Fatalf("got %+v", got)
	}
}

func TestBuildParams(t *testing.T) {
	s, err := testCatalog().Build(Spec{Name: "test", Params: map[string]string{"n": "5", "on": "true", "wait": "250ms", "mode": "b"}})
	if err != nil {
		t.Fatal(err)
	}
	got := s.(testScenario)
	if got.n != 5 || !got.on || got.wait != 250*time.Millisecond || got.mode != "b" {
		t.Fatalf("got %+v", got)
	}
}

func TestBuildErrors(t *testing.T) {
	cases := map[string]Spec{
		"unknown scenario": {Name: "nope"},
		"has no parameter": {Name: "test", Params: map[string]string{"x": "1"}},
		"n must be":        {Name: "test", Params: map[string]string{"n": "11"}},
		"on must be":       {Name: "test", Params: map[string]string{"on": "yes"}},
		"wait must be":     {Name: "test", Params: map[string]string{"wait": "5"}},
		"mode must be":     {Name: "test", Params: map[string]string{"mode": "c"}},
	}
	for want, spec := range cases {
		_, err := testCatalog().Build(spec)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%+v: err = %v, want it to mention %q", spec, err, want)
		}
	}
}

func TestHappyPath(t *testing.T) {
	s, err := Builtin().Build(Spec{Name: "happy_path"})
	if err != nil {
		t.Fatal(err)
	}
	auto := s.OnCreate(CreateContext{Payment: payment.Payment{Capture: payment.CaptureAuto}, ProcessingDelay: time.Second})
	if len(auto.Steps) != 1 || auto.Steps[0] != (Step{After: time.Second, Status: payment.Captured}) {
		t.Fatalf("auto capture plan = %+v", auto)
	}
	manual := s.OnCreate(CreateContext{Payment: payment.Payment{Capture: payment.CaptureManual}})
	if manual.Steps[0].Status != payment.Authorized {
		t.Fatalf("manual capture plan = %+v", manual)
	}
	if p := s.Deliver(payment.Event{}); p.Copies > 1 || p.Drop || p.Delay != 0 {
		t.Fatalf("delivery plan = %+v", p)
	}
}

// Every built-in scenario builds with its defaults, and its params are declared
// with a known type.
func TestBuiltinDefaults(t *testing.T) {
	for _, d := range Builtin().List() {
		if _, err := Builtin().Build(Spec{Name: d.Name}); err != nil {
			t.Errorf("%s: %v", d.Name, err)
		}
		for _, p := range d.Params {
			switch p.Type {
			case "string", "int", "bool", "duration":
			default:
				t.Errorf("%s.%s: unknown type %q", d.Name, p.Name, p.Type)
			}
		}
	}
}
