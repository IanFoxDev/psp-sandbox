package stripe

import (
	"errors"
	"maps"
	"slices"
	"testing"
)

func parse(t *testing.T, raw string) *Params {
	t.Helper()
	p, err := ParseParams(raw)
	if err != nil {
		t.Fatalf("%q: %v", raw, err)
	}
	return p
}

// The encodings below are what stripe-php, stripe-go and stripe-node send.
func TestParamsAsSDKsSendThem(t *testing.T) {
	p := parse(t, "amount=1000&currency=eur&confirm=true&metadata%5Border_id%5D=42&metadata%5Bnote%5D=a+b"+
		"&expand%5B0%5D=latest_charge&expand%5B1%5D=customer&payment_method_types%5B0%5D=card")
	if v, ok, err := p.Int("amount"); v != 1000 || !ok || err != nil {
		t.Errorf("amount = %d %v %v", v, ok, err)
	}
	if v, ok, err := p.String("currency"); v != "eur" || !ok || err != nil {
		t.Errorf("currency = %q %v %v", v, ok, err)
	}
	if v, ok, err := p.Bool("confirm"); !v || !ok || err != nil {
		t.Errorf("confirm = %v %v %v", v, ok, err)
	}
	if m, _, err := p.Map("metadata"); err != nil || !maps.Equal(m, map[string]string{"order_id": "42", "note": "a b"}) {
		t.Errorf("metadata = %v %v", m, err)
	}
	if l, _, err := p.List("expand"); err != nil || !slices.Equal(l, []string{"latest_charge", "customer"}) {
		t.Errorf("expand = %v %v", l, err)
	}
	if got := p.Unread(); !slices.Equal(got, []string{"payment_method_types"}) {
		t.Errorf("Unread = %v", got)
	}
}

func TestParamsListIndexOrder(t *testing.T) {
	p := parse(t, "expand[10]=k&expand[2]=c&expand[0]=a")
	if l, _, _ := p.List("expand"); !slices.Equal(l, []string{"a", "c", "k"}) {
		t.Errorf("expand = %v", l)
	}
	p = parse(t, "expand[]=a&expand[]=b")
	if l, _, _ := p.List("expand"); !slices.Equal(l, []string{"a", "b"}) {
		t.Errorf("expand[] = %v", l)
	}
}

func TestParamsEmptyValues(t *testing.T) {
	p := parse(t, "metadata[order_id]=&description=&metadata2=")
	if m, ok, _ := p.Map("metadata"); !ok || m["order_id"] != "" || len(m) != 1 {
		t.Errorf("metadata[order_id]= should be kept to unset the key: %v", m)
	}
	if v, ok, _ := p.String("description"); !ok || v != "" {
		t.Errorf("description = %q %v", v, ok)
	}
	if m, ok, err := p.Map("metadata2"); !ok || err != nil || len(m) != 0 {
		t.Errorf("metadata2= should clear the hash: %v %v %v", m, ok, err)
	}
	if _, ok, _ := p.String("absent"); ok {
		t.Error("absent parameter reported as sent")
	}
}

func TestParamsErrors(t *testing.T) {
	cases := []struct {
		raw, name, code, param string
		get                    func(p *Params, name string) error
	}{
		{"amount=ten", "amount", "parameter_invalid_integer", "amount", func(p *Params, n string) error { _, _, err := p.Int(n); return err }},
		{"amount=1.5", "amount", "parameter_invalid_integer", "amount", func(p *Params, n string) error { _, _, err := p.Int(n); return err }},
		{"confirm=yes", "confirm", "", "confirm", func(p *Params, n string) error { _, _, err := p.Bool(n); return err }},
		{"metadata=x", "metadata", "parameter_invalid_empty", "metadata", func(p *Params, n string) error { _, _, err := p.Map(n); return err }},
		{"metadata[a][b]=x", "metadata", "", "metadata[a]", func(p *Params, n string) error { _, _, err := p.Map(n); return err }},
		{"currency[x]=eur", "currency", "parameter_invalid_string", "currency", func(p *Params, n string) error { _, _, err := p.String(n); return err }},
		{"expand[a]=x", "expand", "parameter_invalid_empty", "expand", func(p *Params, n string) error { _, _, err := p.List(n); return err }},
	}
	for _, c := range cases {
		err := c.get(parse(t, c.raw), c.name)
		var pe *ParamError
		if !errors.As(err, &pe) || pe.Code != c.code || pe.Param != c.param {
			t.Errorf("%q: got %#v", c.raw, err)
		}
	}
}

func TestParseParamsRejectsBadKeys(t *testing.T) {
	for _, raw := range []string{"a=1&a[b]=2", "a[b]=2&a=1", "[x]=1", "a[b=1", "a]b[=1", "%zz=1"} {
		if _, err := ParseParams(raw); err == nil {
			t.Errorf("%q accepted", raw)
		}
	}
}

func TestNestedParams(t *testing.T) {
	p := parse(t, "line_items[1][quantity]=2&line_items[0][quantity]=1&line_items[0][price_data][unit_amount]=500"+
		"&line_items[0][price_data][product_data][name]=Tea&payment_intent_data[metadata][reference]=r-1")
	items, ok, err := p.Items("line_items")
	if err != nil || !ok || len(items) != 2 {
		t.Fatalf("items: %v %v %v", items, ok, err)
	}
	if q, _, _ := items[0].Int("quantity"); q != 1 {
		t.Errorf("first quantity %d", q)
	}
	pd, ok, err := items[0].Sub("price_data")
	if err != nil || !ok {
		t.Fatalf("price_data: %v %v", ok, err)
	}
	prod, _, _ := pd.Sub("product_data")
	if name, _, _ := prod.String("name"); name != "Tea" {
		t.Errorf("name %q", name)
	}
	if _, ok, _ := items[1].Sub("price_data"); ok {
		t.Error("second item has no price_data")
	}
	pid, _, _ := p.Sub("payment_intent_data")
	if md, _, _ := pid.Map("metadata"); md["reference"] != "r-1" {
		t.Errorf("payment_intent_data[metadata] %v", md)
	}
	if got := p.Unread(); len(got) != 0 {
		t.Errorf("Unread %v", got)
	}
	for _, raw := range []string{"line_items=x", "line_items[a][quantity]=1", "line_items[0]=x", "payment_intent_data=x"} {
		q := parse(t, raw)
		_, _, err1 := q.Items("line_items")
		_, _, err2 := q.Sub("payment_intent_data")
		if err1 == nil && err2 == nil {
			t.Errorf("%q accepted", raw)
		}
	}
}
