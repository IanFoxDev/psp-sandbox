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
}

// New returns the web UI.
func New(e *engine.Engine, d *callback.Dispatcher, clk clock.Clock, log *slog.Logger) *UI {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	page := func(name string) *template.Template {
		return template.Must(template.New("").Funcs(funcs).ParseFS(files, "templates/layout.html", "templates/"+name))
	}
	return &UI{engine: e, dispatcher: d, clock: clk, log: log, index: page("index.html"), payment: page("payment.html")}
}

// Register adds the UI routes to mux.
func (u *UI) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /_sandbox/{$}", u.list)
	mux.HandleFunc("GET /_sandbox/ui/payments/{id}", u.show)
	mux.HandleFunc("POST /_sandbox/ui/deliveries/{id}/replay", u.replay)
	mux.HandleFunc("POST /_sandbox/ui/reset", u.reset)
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
