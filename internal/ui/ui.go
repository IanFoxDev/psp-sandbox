package ui

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ianfoxdev/psp-sandbox/internal/callback"
	"github.com/ianfoxdev/psp-sandbox/internal/clock"
	"github.com/ianfoxdev/psp-sandbox/internal/engine"
	"github.com/ianfoxdev/psp-sandbox/internal/payment"
	"github.com/ianfoxdev/psp-sandbox/internal/store"
)

//go:embed templates/*.html
var files embed.FS

// MaxPayments is how many of the newest payments the list shows.
const MaxPayments = 200

// UI serves the pages under /_sandbox.
type UI struct {
	engine     *engine.Engine
	dispatcher *callback.Dispatcher
	clock      clock.Clock
	log        *slog.Logger
	index      *template.Template
	payment    *template.Template
	threeDS    *template.Template
	checkout   *template.Template
	payer      CheckoutPayer
	// returnParams, if set, adds query parameters to return_url when the
	// customer leaves the 3DS page, as the provider would.
	returnParams func(payment.Payment) url.Values
}

// New returns the web UI.
func New(e *engine.Engine, d *callback.Dispatcher, clk clock.Clock, log *slog.Logger) *UI {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	page := func(name string) *template.Template {
		return template.Must(template.New("").Funcs(funcs).ParseFS(files, "templates/layout.html", "templates/"+name))
	}
	return &UI{engine: e, dispatcher: d, clock: clk, log: log, index: page("index.html"), payment: page("payment.html"),
		threeDS: page("3ds.html"), checkout: page("checkout.html")}
}

// CheckoutPayer pays a checkout session from its page. The profile that has
// sessions sets it.
type CheckoutPayer interface {
	PayFromPage(sessionID, paymentMethod string) (payment.Payment, error)
}

// SetCheckoutPayer turns on the checkout page.
func (u *UI) SetCheckoutPayer(p CheckoutPayer) {
	u.payer = p
}

// SetReturnParams sets the query parameters added to return_url after 3DS.
func (u *UI) SetReturnParams(f func(payment.Payment) url.Values) {
	u.returnParams = f
}

// Register adds the UI routes to mux.
func (u *UI) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /_sandbox/{$}", u.list)
	mux.HandleFunc("GET /_sandbox/ui/payments/{id}", u.show)
	mux.HandleFunc("POST /_sandbox/ui/deliveries/{id}/replay", u.replay)
	mux.HandleFunc("POST /_sandbox/ui/reset", u.reset)
	mux.HandleFunc("GET /_sandbox/ui/3ds/{id}", u.challenge)
	mux.HandleFunc("POST /_sandbox/ui/3ds/{id}", u.authenticate)
	mux.HandleFunc("GET /_sandbox/ui/checkout/{id}", u.checkoutPage)
	mux.HandleFunc("POST /_sandbox/ui/checkout/{id}", u.checkoutPay)
}

var funcs = template.FuncMap{
	"ts": func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format("2006-01-02 15:04:05.000")
	},
}

// page is what every template gets.
type page struct {
	Title   string
	Now     time.Time
	Manual  bool
	Refresh int
	// Path is the current page, for the auto-refresh switch.
	Path string
	Data any
}

// Link returns path with the current auto-refresh setting kept.
func (p page) Link(path string) template.URL {
	if p.Refresh > 0 {
		path += "?refresh=" + strconv.Itoa(p.Refresh)
	}
	return template.URL(path) //nolint:gosec // paths are built by the templates from sandbox ids
}

func (u *UI) page(r *http.Request, title string, data any) page {
	_, manual := u.clock.(*clock.Manual)
	refresh, _ := strconv.Atoi(r.URL.Query().Get("refresh"))
	refresh = min(max(refresh, 0), 60)
	return page{Title: title, Now: u.clock.Now(), Manual: manual, Refresh: refresh, Path: r.URL.Path, Data: data}
}

func (u *UI) render(w http.ResponseWriter, t *template.Template, status int, p page) {
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", p); err != nil {
		u.log.Error("render ui page", "err", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

type row struct {
	Payment    payment.Payment
	Deliveries int
	Succeeded  int
	Failed     int
	Pending    int
	Dropped    int
}

type listData struct {
	Reference string
	Total     int
	Rows      []row
}

func (u *UI) list(w http.ResponseWriter, r *http.Request) {
	ref := r.URL.Query().Get("reference")
	payments := u.engine.Payments(ref)
	d := listData{Reference: ref, Total: len(payments)}
	slices.Reverse(payments)
	for _, p := range payments[:min(len(payments), MaxPayments)] {
		rw := row{Payment: p}
		for _, del := range u.dispatcher.Deliveries(p.ID) {
			rw.Deliveries++
			switch del.Status {
			case callback.StatusSucceeded:
				rw.Succeeded++
			case callback.StatusFailed:
				rw.Failed++
			case callback.StatusPending:
				rw.Pending++
			case callback.StatusDropped:
				rw.Dropped++
			}
		}
		d.Rows = append(d.Rows, rw)
	}
	u.render(w, u.index, http.StatusOK, u.page(r, "Payments", d))
}

type delivery struct {
	callback.Delivery
	PrettyBody string
}

type paymentData struct {
	Payment    payment.Payment
	Events     []event
	Deliveries []delivery
}

type event struct {
	payment.Event
	PrettyData string
}

func (u *UI) show(w http.ResponseWriter, r *http.Request) {
	p, err := u.engine.Payment(r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		u.render(w, u.payment, http.StatusNotFound, u.page(r, "Payment not found", nil))
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	events, err := u.engine.Events(p.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	d := paymentData{Payment: p}
	for _, e := range events {
		b, _ := json.MarshalIndent(e.Data, "", "  ")
		d.Events = append(d.Events, event{Event: e, PrettyData: string(b)})
	}
	for _, del := range u.dispatcher.Deliveries(p.ID) {
		d.Deliveries = append(d.Deliveries, delivery{Delivery: del, PrettyBody: indent(del.Body)})
	}
	u.render(w, u.payment, http.StatusOK, u.page(r, p.ID, d))
}

func indent(b []byte) string {
	var buf bytes.Buffer
	if json.Indent(&buf, b, "", "  ") != nil {
		return string(b)
	}
	return buf.String()
}

type challengeData struct {
	Payment payment.Payment
	Waiting bool
	Message string
}

// challenge is the 3DS page a payment's action_url points at.
func (u *UI) challenge(w http.ResponseWriter, r *http.Request) {
	p, err := u.engine.Payment(r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		u.render(w, u.threeDS, http.StatusNotFound, u.page(r, "Payment not found", nil))
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	d := challengeData{Payment: p, Waiting: p.Status == payment.RequiresAction}
	if !d.Waiting {
		d.Message = "Nothing to authenticate: the payment is " + string(p.Status) + "."
	}
	u.render(w, u.threeDS, http.StatusOK, u.page(r, "Authenticate "+p.ID, d))
}

// authenticate applies the button the customer pressed, then sends the
// browser back to the app's return_url, or to this page when there is none.
func (u *UI) authenticate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	result := r.FormValue("result")
	if result != "success" && result != "failure" {
		http.Error(w, "result must be success or failure", http.StatusBadRequest)
		return
	}
	p, err := u.engine.Authenticate(id, result == "success")
	switch {
	case errors.Is(err, store.ErrNotFound):
		u.render(w, u.threeDS, http.StatusNotFound, u.page(r, "Payment not found", nil))
		return
	case errors.Is(err, payment.ErrInvalidState):
		u.render(w, u.threeDS, http.StatusConflict, u.page(r, "Authenticate "+id,
			challengeData{Payment: p, Message: "Nothing to authenticate: the payment is " + string(p.Status) + "."}))
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if p.ReturnURL != "" {
		http.Redirect(w, r, u.returnURL(p), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/_sandbox/ui/3ds/"+url.PathEscape(id), http.StatusSeeOther)
}

type card struct{ ID, Label string }

// testCards are the cards the checkout page offers, Stripe's test ids.
var testCards = []card{
	{"pm_card_visa", "Visa: succeeds"},
	{"pm_card_visa_chargeDeclined", "Visa: declined"},
	{"pm_card_visa_chargeDeclinedInsufficientFunds", "Visa: declined, insufficient funds"},
	{"pm_card_threeDSecure2Required", "3D Secure: authentication required"},
	{"pm_card_threeDSecureRequiredChargeDeclined", "3D Secure, then declined"},
	{"pm_card_createDispute", "Succeeds, then disputed"},
}

type checkoutData struct {
	Session payment.Session
	Total   int64
	Open    bool
	Cards   []card
	Message string
}

// successURL fills in the session id where success_url asks for it.
func successURL(cs payment.Session) string {
	return strings.ReplaceAll(cs.SuccessURL, "{CHECKOUT_SESSION_ID}", cs.ID)
}

func (u *UI) renderCheckout(w http.ResponseWriter, r *http.Request, status int, cs payment.Session, msg string) {
	d := checkoutData{Session: cs, Total: cs.AmountTotal(), Open: cs.Status == payment.SessionOpen, Cards: testCards, Message: msg}
	if cs.Status == payment.SessionExpired && msg == "" {
		d.Message = "This checkout session has expired."
	}
	u.render(w, u.checkout, status, u.page(r, "Checkout "+cs.ID, d))
}

// checkoutPage is the page a session's url points at. A completed session
// sends the customer on to success_url, which is where 3DS returns too.
func (u *UI) checkoutPage(w http.ResponseWriter, r *http.Request) {
	cs, err := u.engine.Session(r.PathValue("id"))
	if err != nil || u.payer == nil {
		u.render(w, u.checkout, http.StatusNotFound, u.page(r, "Checkout session not found", nil))
		return
	}
	if cs.Status == payment.SessionComplete && cs.SuccessURL != "" {
		http.Redirect(w, r, successURL(cs), http.StatusSeeOther)
		return
	}
	msg := ""
	if cs.PaymentID != "" {
		if p, err := u.engine.Payment(cs.PaymentID); err == nil && p.Status == payment.Failed {
			msg = "The last attempt failed: " + p.FailureReason + ". Try another card."
		}
	}
	u.renderCheckout(w, r, http.StatusOK, cs, msg)
}

func (u *UI) checkoutPay(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cs, err := u.engine.Session(id)
	if err != nil || u.payer == nil {
		u.render(w, u.checkout, http.StatusNotFound, u.page(r, "Checkout session not found", nil))
		return
	}
	p, err := u.payer.PayFromPage(id, r.FormValue("payment_method"))
	if err != nil {
		u.renderCheckout(w, r, http.StatusConflict, cs, err.Error())
		return
	}
	switch p.Status {
	case payment.RequiresAction:
		http.Redirect(w, r, p.ActionURL, http.StatusSeeOther)
	case payment.Failed:
		u.renderCheckout(w, r, http.StatusPaymentRequired, cs, "The card was declined: "+p.FailureReason+". Try another card.")
	default:
		// Completed, or still processing: the page shows where it stands.
		http.Redirect(w, r, "/_sandbox/ui/checkout/"+url.PathEscape(id), http.StatusSeeOther)
	}
}

func (u *UI) returnURL(p payment.Payment) string {
	if u.returnParams == nil {
		return p.ReturnURL
	}
	target, err := url.Parse(p.ReturnURL)
	if err != nil {
		return p.ReturnURL
	}
	q := target.Query()
	for k, vs := range u.returnParams(p) {
		q[k] = vs
	}
	target.RawQuery = q.Encode()
	return target.String()
}

func (u *UI) replay(w http.ResponseWriter, r *http.Request) {
	del, err := u.dispatcher.Replay(r.PathValue("id"))
	if errors.Is(err, callback.ErrNotFound) {
		http.Error(w, "delivery not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirect(w, r, "/_sandbox/ui/payments/"+url.PathEscape(del.PaymentID))
}

func (u *UI) reset(w http.ResponseWriter, r *http.Request) {
	u.engine.Reset()
	redirect(w, r, "/_sandbox/")
}

// redirect answers a form post with 303, keeping the refresh setting.
func redirect(w http.ResponseWriter, r *http.Request, path string) {
	if q := r.URL.Query().Get("refresh"); q != "" {
		path += "?refresh=" + url.QueryEscape(q)
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
}
