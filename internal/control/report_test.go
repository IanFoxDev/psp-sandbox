package control_test

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/config"
	"github.com/ianfoxdev/psp-sandbox/internal/sandboxtest"
)

// balanceRows fetches the report and returns its rows without the header,
// each cut to category, gross, currency, source and payment.
func balanceRows(t *testing.T, sb *sandboxtest.Sandbox, query string) [][]string {
	t.Helper()
	resp := sb.Do(http.MethodGet, "/_sandbox/reports/balance.csv"+query, nil)
	if resp.Status != http.StatusOK {
		t.Fatalf("%d %s", resp.Status, resp.Body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("Content-Type = %s", ct)
	}
	records, err := csv.NewReader(strings.NewReader(string(resp.Body))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(records[0], ",") != "balance_transaction_id,created,available_on,currency,gross,fee,net,reporting_category,source_id,payment_intent_id,charge_id" {
		t.Fatalf("header = %v", records[0])
	}
	var rows [][]string
	for _, r := range records[1:] {
		rows = append(rows, []string{r[7], r[4], r[3], r[8], r[9]})
	}
	return rows
}

func TestBalanceReport(t *testing.T) {
	sb := sandboxtest.New(t)
	scenario := func(s string) []string { return []string{"X-Sandbox-Scenario", s} }

	refunded := sb.CreatePayment(map[string]any{"reference": "shop-1"})["id"].(string)
	sb.WaitStatus(refunded, "captured")
	refund := sb.Do(http.MethodPost, "/v1/payments/"+refunded+"/refunds", map[string]any{"amount": 300})
	refundID := refund.JSON(t)["id"].(string)
	sb.WaitStatus(refunded, "partially_refunded")

	lost := sb.CreatePayment(map[string]any{"reference": "shop-2", "amount": 2500, "currency": "USD"}, scenario("lost_callback")...)["id"].(string)
	sb.WaitStatus(lost, "captured")

	duplicated := sb.CreatePayment(map[string]any{"reference": "shop-3", "amount": 500, "currency": "JPY"}, scenario("duplicate_callback; times=3")...)["id"].(string)
	sb.WaitStatus(duplicated, "captured")

	disputed := sb.CreatePayment(map[string]any{"reference": "shop-4", "amount": 2000})["id"].(string)
	sb.WaitStatus(disputed, "captured")
	for _, ev := range []map[string]any{{"type": "chargeback.opened"}, {"type": "chargeback.closed", "outcome": "won"}} {
		if resp := sb.Do(http.MethodPost, "/_sandbox/payments/"+disputed+"/events", ev); resp.Status != http.StatusCreated {
			t.Fatalf("%v: %d %s", ev, resp.Status, resp.Body)
		}
	}

	declined := sb.CreatePayment(map[string]any{"reference": "shop-5"}, scenario("declined")...)["id"].(string)
	sb.WaitStatus(declined, "failed")

	regressed := sb.CreatePayment(map[string]any{"reference": "shop-6", "amount": 700}, scenario("status_regression; delay=10ms")...)["id"].(string)
	waitEvents(t, sb, regressed, 2)

	other := sb.CreatePayment(map[string]any{"reference": "other-1"})["id"].(string)
	sb.WaitStatus(other, "captured")

	want := [][]string{
		{"charge", "10.00", "eur", refunded, refunded},
		{"refund", "-3.00", "eur", refundID, refunded},
		{"charge", "25.00", "usd", lost, lost},
		{"charge", "500", "jpy", duplicated, duplicated},
		{"charge", "20.00", "eur", disputed, disputed},
		{"dispute", "-20.00", "eur", disputed, disputed},
		{"dispute_reversal", "20.00", "eur", disputed, disputed},
		{"charge", "7.00", "eur", regressed, regressed},
	}
	got := balanceRows(t, sb, "?reference_prefix=shop-")
	if len(got) != len(want) {
		t.Fatalf("rows:\n%v\nwant:\n%v", got, want)
	}
	for i := range want {
		if strings.Join(got[i], " ") != strings.Join(want[i], " ") {
			t.Errorf("row %d = %v, want %v", i, got[i], want[i])
		}
	}

	if rows := balanceRows(t, sb, ""); len(rows) != len(want)+1 {
		t.Errorf("without a prefix: %d rows, want %d", len(rows), len(want)+1)
	}
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if rows := balanceRows(t, sb, "?to="+past); len(rows) != 0 {
		t.Errorf("before the payments: %v", rows)
	}
	if rows := balanceRows(t, sb, "?reference_prefix=shop-&from="+past); len(rows) != len(want) {
		t.Errorf("from an hour ago: %d rows", len(rows))
	}
	if resp := sb.Do(http.MethodGet, "/_sandbox/reports/balance.csv?from=yesterday", nil); resp.Status != http.StatusBadRequest {
		t.Errorf("bad from: %d %s", resp.Status, resp.Body)
	}
}

func TestBalanceReportInTheStripeProfile(t *testing.T) {
	sb := sandboxtest.New(t, func(c *config.Config) { c.Profile = config.ProfileStripe })
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, sb.Server.URL+"/v1/payment_intents",
		strings.NewReader("amount=1000&currency=eur&confirm=true&payment_method=pm_card_visa"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer sk_test_1")
	resp, err := sb.Server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var intent struct {
		ID string `json:"id"`
	}
	err = json.NewDecoder(resp.Body).Decode(&intent)
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", resp.StatusCode, err)
	}
	id := intent.ID
	deadline := time.Now().Add(sandboxtest.Wait)
	for len(balanceRows(t, sb, "")) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no charge in the report")
		}
		time.Sleep(5 * time.Millisecond)
	}
	got := balanceRows(t, sb, "")[0]
	charge := "ch_" + strings.TrimPrefix(id, "pi_")
	if strings.Join(got, " ") != strings.Join([]string{"charge", "10.00", "eur", charge, id}, " ") {
		t.Errorf("row = %v", got)
	}
}

// waitEvents waits until a payment has n events.
func waitEvents(t *testing.T, sb *sandboxtest.Sandbox, paymentID string, n int) {
	t.Helper()
	deadline := time.Now().Add(sandboxtest.Wait)
	for {
		// A payment with no events yet lists null.
		events, _ := sb.Do(http.MethodGet, "/_sandbox/payments/"+paymentID+"/events", nil).JSON(t)["data"].([]any)
		if len(events) >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("payment %s has fewer than %d events", paymentID, n)
		}
		time.Sleep(2 * time.Millisecond)
	}
}
