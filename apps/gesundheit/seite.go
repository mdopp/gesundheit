package main

import (
	"crypto/subtle"
	"encoding/csv"
	"fmt"
	"html"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The pages, behind Authelia: Caddy sends Remote-User and Remote-Groups
// with the proxy secret (laib ADR 0012 §3); without the secret nobody is
// logged in. A person sees their own timeline; an admin also the children's.
// Every sentence is German and says what happens; every form answers on
// the page it came from.

const proxyHeader = "X-Gesundheit-Proxy"

const saysWerSieht = "Nur du siehst deinen Verlauf. Wer die Box verwaltet (Admin), sieht die Dokumente in Paperless trotzdem."
const saysKeinArzt = "Zahlen aus deinen Befunden, so wie sie dort stehen — keine ärztliche Einschätzung. Nimm den Verlauf mit zum Arzt."

// Web is the pages' handler.
type Web struct {
	Dienst *Dienst
	Secret string
	Admins string // the group whose members see the children's timelines
}

type sitzung struct {
	Login string
	Admin bool
}

func (w *Web) sitzung(r *http.Request) sitzung {
	got := r.Header.Get(proxyHeader)
	if w.Secret == "" || subtle.ConstantTimeCompare([]byte(got), []byte(w.Secret)) != 1 {
		return sitzung{}
	}
	s := sitzung{Login: r.Header.Get("Remote-User")}
	for _, g := range strings.Split(r.Header.Get("Remote-Groups"), ",") {
		if strings.TrimSpace(g) == w.Admins && w.Admins != "" {
			s.Admin = true
		}
	}
	return s
}

// Handler is the mux of the pages.
func (w *Web) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(rw http.ResponseWriter, r *http.Request) { fmt.Fprintln(rw, "ok") })
	mux.HandleFunc("GET /{$}", w.start)
	mux.HandleFunc("POST /tag", w.tag)
	mux.HandleFunc("GET /fragen", w.fragen)
	mux.HandleFunc("POST /fragen", w.fragen)
	mux.HandleFunc("POST /zuordnen", w.zuordnen)
	mux.HandleFunc("GET /export.csv", w.export)
	mux.HandleFunc("POST /loeschen", w.loeschen)
	return mux
}

// wer is the person a request is about: the viewer, or ?person= when the
// viewer may see them. nil with a page already written when not.
func (w *Web) wer(rw http.ResponseWriter, r *http.Request) (*Person, sitzung, []Person) {
	s := w.sitzung(r)
	if s.Login == "" {
		seite(rw, http.StatusUnauthorized, "Nicht angemeldet", `<p>Diese Seite gibt es nur nach der Anmeldung über die Startseite des Hauses.</p>`)
		return nil, s, nil
	}
	personen := w.Dienst.Personen(r.Context())
	darf := sichtbar(s.Login, s.Admin, personen)
	key := r.FormValue("person")
	if key == "" {
		key = s.Login
	}
	for i := range darf {
		if darf[i].Key == key {
			return &darf[i], s, darf
		}
	}
	if key == s.Login {
		// a login Paperless does not know yet: a person who has not opened Paperless once
		p := Person{Key: s.Login, Login: s.Login, Anzeige: s.Login}
		return &p, s, append(darf, p)
	}
	seite(rw, http.StatusForbidden, "Nicht deiner", `<p>Diesen Verlauf darfst du nicht sehen. `+saysWerSieht+`</p><p><a href="/">Zu deinem Verlauf</a></p>`)
	return nil, s, nil
}

func (w *Web) start(rw http.ResponseWriter, r *http.Request) {
	p, _, darf := w.wer(rw, r)
	if p == nil {
		return
	}
	v, _ := w.Dienst.Speicher.Verlauf(p.Key)
	tage, _ := w.Dienst.Speicher.Tage(p.Key)
	fragen, _ := w.Dienst.Speicher.Fragen(p.Key)
	heute := w.Dienst.now().Format("2006-01-02")
	var b strings.Builder
	b.WriteString(`<p class=k>` + saysKeinArzt + `</p>`)
	if len(darf) > 1 {
		b.WriteString(`<nav class=personen>`)
		for _, d := range darf {
			cls := ""
			if d.Key == p.Key {
				cls = ` class=aktiv`
			}
			fmt.Fprintf(&b, `<a%s href="/?person=%s">%s</a>`, cls, html.EscapeString(d.Key), html.EscapeString(d.Anzeige))
		}
		b.WriteString(`</nav>`)
	}
	if meldung := r.URL.Query().Get("m"); meldung != "" {
		b.WriteString(`<p class=ok>` + html.EscapeString(meldung) + `</p>`)
	}
	// today's note
	b.WriteString(`<section><h2>Heute</h2>`)
	var heutige *Tagesnotiz
	for i := range tage {
		if tage[i].Datum == heute {
			heutige = &tage[i]
		}
	}
	if heutige != nil {
		fmt.Fprintf(&b, `<p>Für heute notiert: %s. Du kannst es unten ändern.</p>`, html.EscapeString(notizZeile(*heutige)))
	} else {
		b.WriteString(`<p>Wie ging es dir heute? Vier kurze Fragen, unter einer Minute.</p>`)
	}
	fmt.Fprintf(&b, `<form method=post action="/tag"><input type=hidden name=person value="%s"><input type=hidden name=datum value="%s">`, html.EscapeString(p.Key), heute)
	for _, f := range fragen {
		fmt.Fprintf(&b, `<label class=frage><span>%s</span><span class=skala>`, html.EscapeString(f.Text))
		for i := f.Min; i <= f.Max; i++ {
			checked := ""
			if heutige != nil {
				if v, ok := heutige.Antworten[f.Key]; ok && v == i {
					checked = " checked"
				}
			}
			fmt.Fprintf(&b, `<label><input type=radio name="%s" value="%d"%s> %d</label>`, html.EscapeString(f.Key), i, checked, i)
		}
		b.WriteString(`</span></label>`)
	}
	text := ""
	if heutige != nil {
		text = heutige.Text
	}
	fmt.Fprintf(&b, `<label class=frage><span>Eine Zeile zum Tag (optional)</span><input name=text maxlength=500 value="%s"></label>`, html.EscapeString(text))
	b.WriteString(`<button>Notieren</button> <a class=k href="/fragen?person=` + html.EscapeString(p.Key) + `">Eigene Fragen</a></form></section>`)
	// what changed last
	b.WriteString(`<section><h2>Verlauf</h2>`)
	if len(v.Werte) == 0 {
		b.WriteString(`<p>Noch keine Werte. Sobald ein Laborblatt oder ein Befund in Paperless liegt (Scanner „Nur für mich“ oder Mail an das Dokumenten-Postfach mit Betreff „Arzt: …“), lese ich die Werte und zeichne sie hier ein.</p>`)
	} else {
		b.WriteString(zuletzt(v.Werte))
		b.WriteString(verlaufHTML(v, tage, w.Dienst.Public))
	}
	b.WriteString(`</section>`)
	// days
	if len(tage) > 0 {
		b.WriteString(`<section><h2>Deine Tage</h2>` + tageHTML(tage, fragen) + `</section>`)
	}
	// open documents: anyone may take one as their own, an admin also for a child
	{
		offene := w.Dienst.Offene()
		if len(offene) > 0 {
			b.WriteString(`<section><h2>Für wen?</h2><p>Diese Befunde habe ich gelesen, aber niemandem zuordnen können.</p>`)
			for _, e := range offene {
				g := e.Gelesen
				fmt.Fprintf(&b, `<form method=post action="/zuordnen" class=offen><input type=hidden name=dokument value="%d"><span>%s (%s, %d Werte, Patient „%s“)</span><select name=person>`, g.Dokument, html.EscapeString(g.Titel), g.Datum, len(g.Werte), html.EscapeString(g.Patient))
				for _, d := range darf {
					fmt.Fprintf(&b, `<option value="%s">%s</option>`, html.EscapeString(d.Key), html.EscapeString(d.Anzeige))
				}
				b.WriteString(`</select><button>Zuordnen</button></form>`)
			}
			b.WriteString(`</section>`)
		}
	}
	fmt.Fprintf(&b, `<section class=k><p>%s</p><p><a href="/export.csv?person=%s">Als CSV herunterladen</a> · <form method=post action="/loeschen" class=inline onsubmit="return confirm('Alle Werte und Tagesnotizen von %s löschen? Die Dokumente bleiben in Paperless.')"><input type=hidden name=person value="%s"><button class=k>Verlauf löschen</button></form></p></section>`,
		saysWerSieht, html.EscapeString(p.Key), html.EscapeString(p.Anzeige), html.EscapeString(p.Key))
	seite(rw, 200, "Gesundheit · "+p.Anzeige, b.String())
}

// zuletzt is the line at the top: the values that changed most recently.
func zuletzt(werte []Messwert) string {
	byName := map[string][]Messwert{}
	for _, m := range werte {
		byName[m.Name] = append(byName[m.Name], m)
	}
	type delta struct {
		name    string
		von, zu Messwert
	}
	var ds []delta
	for n, ms := range byName {
		if len(ms) >= 2 {
			ds = append(ds, delta{n, ms[len(ms)-2], ms[len(ms)-1]})
		}
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i].zu.Datum > ds[j].zu.Datum })
	if len(ds) == 0 {
		return ""
	}
	if len(ds) > 4 {
		ds = ds[:4]
	}
	var parts []string
	for _, d := range ds {
		parts = append(parts, fmt.Sprintf("%s %s → %s %s seit %s", html.EscapeString(d.name), zahlText(d.von.Wert), zahlText(d.zu.Wert), html.EscapeString(d.zu.Einheit), datumText(d.von.Datum)))
	}
	return `<p class=zuletzt>Zuletzt verändert: ` + strings.Join(parts, " · ") + `</p>`
}

// verlaufHTML draws one row per value: name, sparkline with the reference
// band, the last value; then the events.
func verlaufHTML(v Verlauf, tage []Tagesnotiz, public string) string {
	byName := map[string][]Messwert{}
	for _, m := range v.Werte {
		byName[m.Name] = append(byName[m.Name], m)
	}
	var names []string
	for n := range byName {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		gi, gj := gruppenRang(byName[names[i]][0].Gruppe), gruppenRang(byName[names[j]][0].Gruppe)
		if gi != gj {
			return gi < gj
		}
		return names[i] < names[j]
	})
	min, max := spanne(v, tage)
	var b strings.Builder
	b.WriteString(`<table class=werte>`)
	gruppe := ""
	for _, n := range names {
		ms := byName[n]
		if g := ms[0].Gruppe; g != gruppe {
			gruppe = g
			fmt.Fprintf(&b, `<tr class=gruppe><th colspan=3>%s</th></tr>`, html.EscapeString(g))
		}
		last := ms[len(ms)-1]
		neu := ""
		if last.Neu {
			neu = ` <span class=neu title="Dieser Name steht nicht im Wörterbuch; er wird so geführt, wie der Befund ihn schreibt.">neu</span>`
		}
		fmt.Fprintf(&b, `<tr><th>%s%s</th><td>%s</td><td class=wert>%s <span class=k>%s</span> %s</td></tr>`,
			html.EscapeString(n), neu, sparkline(ms, min, max, public), zahlText(last.Wert), html.EscapeString(last.Einheit), lage(last))
	}
	b.WriteString(`</table>`)
	if len(tage) > 0 {
		b.WriteString(`<p class=k>Befinden (Schlaf, Energie, Stimmung) auf derselben Zeitachse:</p>` + befindenBand(tage, min, max))
	}
	if len(v.Ereignisse) > 0 {
		b.WriteString(`<h3>Ereignisse</h3><ul class=ereignisse>`)
		for i := len(v.Ereignisse) - 1; i >= 0; i-- {
			e := v.Ereignisse[i]
			fmt.Fprintf(&b, `<li><time>%s</time> <b>%s</b> %s %s</li>`, datumText(e.Datum), html.EscapeString(e.Art), html.EscapeString(e.Text), dokLink(e.Dokument, public))
		}
		b.WriteString(`</ul>`)
	}
	return b.String()
}

// spanne is the time axis: the first and last day of anything drawn.
func spanne(v Verlauf, tage []Tagesnotiz) (time.Time, time.Time) {
	var min, max time.Time
	add := func(d string) {
		t, err := time.Parse("2006-01-02", d)
		if err != nil {
			return
		}
		if min.IsZero() || t.Before(min) {
			min = t
		}
		if max.IsZero() || t.After(max) {
			max = t
		}
	}
	for _, m := range v.Werte {
		add(m.Datum)
	}
	for _, t := range tage {
		add(t.Datum)
	}
	if min.IsZero() {
		min, max = time.Now().AddDate(0, -1, 0), time.Now()
	}
	if max.Sub(min) < 30*24*time.Hour {
		min = max.AddDate(0, -1, 0)
	}
	return min, max
}

const sparkW, sparkH = 320.0, 44.0

func x(t string, min, max time.Time) float64 {
	d, err := time.Parse("2006-01-02", t)
	if err != nil {
		return 0
	}
	return 6 + (sparkW-12)*d.Sub(min).Hours()/max.Sub(min).Hours()
}

// sparkline draws one value's points, the laboratory's reference band
// behind each point as the report gave it, and a line between the points.
func sparkline(ms []Messwert, min, max time.Time, public string) string {
	lo, hi := ms[0].Wert, ms[0].Wert
	for _, m := range ms {
		for _, v := range []*float64{&m.Wert, m.RefMin, m.RefMax} {
			if v != nil {
				if *v < lo {
					lo = *v
				}
				if *v > hi {
					hi = *v
				}
			}
		}
	}
	if hi == lo {
		hi, lo = hi+1, lo-1
	}
	pad := (hi - lo) * 0.15
	lo, hi = lo-pad, hi+pad
	y := func(v float64) float64 { return sparkH - 4 - (sparkH-8)*(v-lo)/(hi-lo) }
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class=spark viewBox="0 0 %g %g" width="%g" height="%g" role="img" aria-label="%d Punkte">`, sparkW, sparkH, sparkW, sparkH, len(ms))
	// the reference band, per point: labs differ
	for i, m := range ms {
		if m.RefMin == nil && m.RefMax == nil {
			continue
		}
		x0 := x(m.Datum, min, max) - 5
		x1 := x0 + 10
		if i+1 < len(ms) {
			x1 = x(ms[i+1].Datum, min, max)
		}
		top, bottom := y(hi), y(lo)
		if m.RefMax != nil {
			top = y(*m.RefMax)
		}
		if m.RefMin != nil {
			bottom = y(*m.RefMin)
		}
		fmt.Fprintf(&b, `<rect class=ref x="%.1f" y="%.1f" width="%.1f" height="%.1f"/>`, x0, top, x1-x0, bottom-top)
	}
	var pts []string
	for _, m := range ms {
		pts = append(pts, fmt.Sprintf("%.1f,%.1f", x(m.Datum, min, max), y(m.Wert)))
	}
	if len(pts) > 1 {
		fmt.Fprintf(&b, `<polyline points="%s"/>`, strings.Join(pts, " "))
	}
	for _, m := range ms {
		cls := "punkt"
		if (m.RefMin != nil && m.Wert < *m.RefMin) || (m.RefMax != nil && m.Wert > *m.RefMax) {
			cls += " ausser"
		}
		title := fmt.Sprintf("%s: %s %s", datumText(m.Datum), zahlText(m.Wert), m.Einheit)
		if m.RefMin != nil || m.RefMax != nil {
			title += " (Referenz " + refText(m.RefMin, m.RefMax) + ")"
		}
		href := dokURL(m.Dokument, public)
		if href != "" {
			fmt.Fprintf(&b, `<a href="%s"><circle class="%s" cx="%.1f" cy="%.1f" r="3.5"><title>%s</title></circle></a>`, href, cls, x(m.Datum, min, max), y(m.Wert), html.EscapeString(title))
		} else {
			fmt.Fprintf(&b, `<circle class="%s" cx="%.1f" cy="%.1f" r="3.5"><title>%s</title></circle>`, cls, x(m.Datum, min, max), y(m.Wert), html.EscapeString(title))
		}
	}
	b.WriteString(`</svg>`)
	return b.String()
}

// befindenBand is the daily note on the same axis: the mean of the 1–5
// scales as a dot a day.
func befindenBand(tage []Tagesnotiz, min, max time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="spark befinden" viewBox="0 0 %g %g" width="%g" height="%g" role="img" aria-label="Befinden">`, sparkW, sparkH, sparkW, sparkH)
	for _, t := range tage {
		n, sum := 0, 0
		for _, k := range []string{"schlaf", "energie", "stimmung"} {
			if v, ok := t.Antworten[k]; ok {
				n, sum = n+1, sum+v
			}
		}
		if n == 0 {
			continue
		}
		mean := float64(sum) / float64(n)
		y := sparkH - 4 - (sparkH-8)*(mean-1)/4
		title := datumText(t.Datum) + ": " + notizZeile(t)
		fmt.Fprintf(&b, `<circle class=punkt cx="%.1f" cy="%.1f" r="3"><title>%s</title></circle>`, x(t.Datum, min, max), y, html.EscapeString(title))
	}
	b.WriteString(`</svg>`)
	return b.String()
}

// tageHTML lists the last days' notes, newest first.
func tageHTML(tage []Tagesnotiz, fragen []Frage) string {
	var b strings.Builder
	b.WriteString(`<table class=tage><tr><th>Tag</th>`)
	for _, f := range fragen {
		fmt.Fprintf(&b, `<th title="%s">%s</th>`, html.EscapeString(f.Text), html.EscapeString(kurz(f.Key, 12)))
	}
	b.WriteString(`<th></th></tr>`)
	n := 0
	for i := len(tage) - 1; i >= 0 && n < 30; i-- {
		t := tage[i]
		n++
		nach := ""
		if t.Nachgetragen {
			nach = ` <span class=k title="nachgetragen">↺</span>`
		}
		fmt.Fprintf(&b, `<tr><td>%s%s</td>`, datumText(t.Datum), nach)
		for _, f := range fragen {
			if v, ok := t.Antworten[f.Key]; ok {
				fmt.Fprintf(&b, `<td>%d</td>`, v)
			} else {
				b.WriteString(`<td class=k>–</td>`)
			}
		}
		fmt.Fprintf(&b, `<td class=k>%s</td></tr>`, html.EscapeString(t.Text))
	}
	b.WriteString(`</table>`)
	return b.String()
}

func (w *Web) tag(rw http.ResponseWriter, r *http.Request) {
	p, _, _ := w.wer(rw, r)
	if p == nil {
		return
	}
	fragen, _ := w.Dienst.Speicher.Fragen(p.Key)
	antworten := map[string]int{}
	for _, f := range fragen {
		if v := r.PostFormValue(f.Key); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				antworten[f.Key] = n
			}
		}
	}
	n, err := tagesnotiz(w.Dienst, p.Key, r.PostFormValue("datum"), antworten, r.PostFormValue("text"), "seite")
	if err != nil {
		seite(rw, 400, "Nicht notiert", `<p>Das ging nicht: `+html.EscapeString(err.Error())+`.</p><p><a href="/?person=`+html.EscapeString(p.Key)+`">Zurück</a></p>`)
		return
	}
	http.Redirect(rw, r, "/?person="+p.Key+"&m="+urlQ("Notiert für "+datumText(n.Datum)+"."), http.StatusSeeOther)
}

func (w *Web) fragen(rw http.ResponseWriter, r *http.Request) {
	p, _, _ := w.wer(rw, r)
	if p == nil {
		return
	}
	if r.Method == http.MethodPost {
		var eigene []Frage
		for i := 1; i <= MaxEigeneFragen; i++ {
			t := strings.TrimSpace(r.PostFormValue("text" + strconv.Itoa(i)))
			if t == "" {
				continue
			}
			f := Frage{Text: kurz(t, 80), Min: 0, Max: 5, Key: r.PostFormValue("key" + strconv.Itoa(i))}
			if r.PostFormValue("skala"+strconv.Itoa(i)) == "10" {
				f.Max = 10
			}
			eigene = append(eigene, f)
		}
		if err := w.Dienst.Speicher.FragenSetzen(p.Key, eigene); err != nil {
			seite(rw, 400, "Nicht gespeichert", `<p>`+html.EscapeString(err.Error())+`</p>`)
			return
		}
		http.Redirect(rw, r, "/?person="+p.Key+"&m="+urlQ(fmt.Sprintf("%d eigene Fragen gespeichert.", len(eigene))), http.StatusSeeOther)
		return
	}
	fragen, _ := w.Dienst.Speicher.Fragen(p.Key)
	var b strings.Builder
	b.WriteString(`<p>Die vier Grundfragen stellt die Seite jeden Tag. Dazu kannst du bis zu drei eigene Fragen anlegen, die dich gerade beschäftigen — etwa „Atemnot“, „Kopfschmerz“ oder „Verdauung“. Jede wird auf einer Skala von 0 bis 5 (oder 0 bis 10) beantwortet. Weniger ist mehr: ein Katalog, den man jeden Tag beantwortet, muss kurz bleiben.</p>`)
	fmt.Fprintf(&b, `<form method=post action="/fragen"><input type=hidden name=person value="%s">`, html.EscapeString(p.Key))
	i := 0
	for _, f := range fragen {
		if !f.Eigen {
			continue
		}
		i++
		fmt.Fprintf(&b, `<label class=frage><span>Frage %d</span><input name="text%d" maxlength=80 value="%s"><input type=hidden name="key%d" value="%s"><select name="skala%d"><option value=5%s>0–5</option><option value=10%s>0–10</option></select></label>`,
			i, i, html.EscapeString(f.Text), i, html.EscapeString(f.Key), i, sel(f.Max == 5), sel(f.Max == 10))
	}
	for i++; i <= MaxEigeneFragen; i++ {
		fmt.Fprintf(&b, `<label class=frage><span>Frage %d</span><input name="text%d" maxlength=80 placeholder="leer lassen, wenn keine"><select name="skala%d"><option value=5>0–5</option><option value=10>0–10</option></select></label>`, i, i, i)
	}
	b.WriteString(`<button>Speichern</button> <a class=k href="/?person=` + html.EscapeString(p.Key) + `">Zurück</a></form>`)
	seite(rw, 200, "Eigene Fragen · "+p.Anzeige, b.String())
}

func sel(b bool) string {
	if b {
		return " selected"
	}
	return ""
}

func (w *Web) zuordnen(rw http.ResponseWriter, r *http.Request) {
	s := w.sitzung(r)
	if s.Login == "" {
		seite(rw, 401, "Nicht angemeldet", `<p>Bitte zuerst anmelden.</p>`)
		return
	}
	doc, _ := strconv.Atoi(r.PostFormValue("dokument"))
	key := r.PostFormValue("person")
	erlaubt := false
	for _, p := range sichtbar(s.Login, s.Admin, w.Dienst.Personen(r.Context())) {
		if p.Key == key {
			erlaubt = true
		}
	}
	if !erlaubt {
		seite(rw, 403, "Nicht erlaubt", `<p>Du kannst einen Befund nur dir selbst zuordnen`+map[bool]string{true: " oder einem Kind", false: ""}[s.Admin]+`.</p><p><a href="/">Zurück</a></p>`)
		return
	}
	if err := w.Dienst.Zuordnen(r.Context(), doc, key); err != nil {
		seite(rw, 400, "Nicht zugeordnet", `<p>`+html.EscapeString(err.Error())+`</p><p><a href="/">Zurück</a></p>`)
		return
	}
	http.Redirect(rw, r, "/?person="+key+"&m="+urlQ("Befund zugeordnet; seine Werte sind jetzt im Verlauf."), http.StatusSeeOther)
}

func (w *Web) export(rw http.ResponseWriter, r *http.Request) {
	p, _, _ := w.wer(rw, r)
	if p == nil {
		return
	}
	v, _ := w.Dienst.Speicher.Verlauf(p.Key)
	tage, _ := w.Dienst.Speicher.Tage(p.Key)
	rw.Header().Set("Content-Type", "text/csv; charset=utf-8")
	rw.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="gesundheit-%s.csv"`, p.Key))
	c := csv.NewWriter(rw)
	c.Comma = ';'
	c.Write([]string{"datum", "art", "name", "wert", "einheit", "ref_min", "ref_max", "dokument", "text"})
	for _, m := range v.Werte {
		c.Write([]string{m.Datum, "wert", m.Name, zahlText(m.Wert), m.Einheit, optText(m.RefMin), optText(m.RefMax), strconv.Itoa(m.Dokument), m.Original})
	}
	for _, e := range v.Ereignisse {
		c.Write([]string{e.Datum, "ereignis", e.Art, "", "", "", "", strconv.Itoa(e.Dokument), e.Text})
	}
	for _, t := range tage {
		for k, val := range t.Antworten {
			c.Write([]string{t.Datum, "tag", k, strconv.Itoa(val), "", "", "", "", t.Text})
		}
	}
	c.Flush()
}

func (w *Web) loeschen(rw http.ResponseWriter, r *http.Request) {
	p, _, _ := w.wer(rw, r)
	if p == nil {
		return
	}
	if err := w.Dienst.Speicher.Loeschen(p.Key); err != nil {
		seite(rw, 500, "Nicht gelöscht", `<p>`+html.EscapeString(err.Error())+`</p>`)
		return
	}
	http.Redirect(rw, r, "/?person="+p.Key+"&m="+urlQ("Verlauf gelöscht. Die Dokumente liegen weiter in Paperless; ein neuer Lauf liest sie nicht noch einmal."), http.StatusSeeOther)
}

// --- small helpers

func zahlText(f float64) string {
	return strings.Replace(strconv.FormatFloat(f, 'f', -1, 64), ".", ",", 1)
}

func optText(f *float64) string {
	if f == nil {
		return ""
	}
	return zahlText(*f)
}

func refText(lo, hi *float64) string {
	switch {
	case lo != nil && hi != nil:
		return zahlText(*lo) + "–" + zahlText(*hi)
	case lo != nil:
		return "> " + zahlText(*lo)
	case hi != nil:
		return "< " + zahlText(*hi)
	}
	return ""
}

func lage(m Messwert) string {
	switch {
	case m.RefMin != nil && m.Wert < *m.RefMin:
		return `<span class=ausser title="unter dem Referenzbereich des Labors">▼</span>`
	case m.RefMax != nil && m.Wert > *m.RefMax:
		return `<span class=ausser title="über dem Referenzbereich des Labors">▲</span>`
	}
	return ""
}

func datumText(d string) string {
	if t, err := time.Parse("2006-01-02", d); err == nil {
		return t.Format("02.01.2006")
	}
	return d
}

func dokURL(id int, public string) string {
	if public == "" || id == 0 {
		return ""
	}
	return strings.TrimRight(public, "/") + "/documents/" + strconv.Itoa(id) + "/details"
}

func dokLink(id int, public string) string {
	if u := dokURL(id, public); u != "" {
		return `<a class=k href="` + u + `" title="Nur zu Hause im WLAN">Dokument</a>`
	}
	return ""
}

func urlQ(s string) string {
	r := strings.NewReplacer(" ", "+", "&", "%26", "#", "%23", "?", "%3F", "+", "%2B")
	return r.Replace(s)
}

// seite wraps a page: the same head, one small stylesheet, phone first.
func seite(w http.ResponseWriter, code int, titel, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	fmt.Fprintf(w, `<!doctype html><html lang=de><head><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1"><title>%s</title><style>%s</style></head><body><header><h1><a href="/">Gesundheit</a></h1></header><main>%s</main></body></html>`,
		html.EscapeString(titel), css, body)
}

const css = `
:root{--fg:#1d2730;--bg:#fff;--k:#6b7785;--line:#e3e8ee;--akzent:#2b6f8f;--ref:#dfeef4;--ausser:#b4432f;--ok:#2f7d4f}
@media(prefers-color-scheme:dark){:root{--fg:#e7ecf0;--bg:#141a1f;--k:#97a3af;--line:#2a333c;--akzent:#7fb6d2;--ref:#1f3340;--ausser:#e0735f;--ok:#6cc08b}}
body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.45 system-ui,sans-serif}
header,main{max-width:760px;margin:0 auto;padding:0 16px}
h1{font-size:1.3rem;margin:1rem 0}h1 a{color:inherit;text-decoration:none}h2{font-size:1.1rem;margin:1.6rem 0 .5rem}h3{font-size:1rem;margin:1.2rem 0 .4rem}
.k{color:var(--k);font-size:.92rem}.ok{color:var(--ok)}.ausser{color:var(--ausser)}
.personen a{display:inline-block;margin:0 .6rem .4rem 0;padding:.2rem .6rem;border:1px solid var(--line);border-radius:999px;color:inherit;text-decoration:none}.personen a.aktiv{border-color:var(--akzent);color:var(--akzent)}
.frage{display:block;margin:.6rem 0}.frage>span:first-child{display:block;font-size:.95rem;margin-bottom:.2rem}.skala label{display:inline-block;margin-right:.5rem}
input[type=text],input:not([type]),select{font:inherit;padding:.3rem .4rem;border:1px solid var(--line);border-radius:6px;background:var(--bg);color:inherit;width:100%;max-width:420px}select{width:auto}
button{font:inherit;padding:.4rem .9rem;border:1px solid var(--akzent);border-radius:8px;background:var(--akzent);color:#fff}button.k{background:none;color:var(--k);border-color:var(--line)}
.inline{display:inline}
table{border-collapse:collapse;width:100%}td,th{text-align:left;padding:.35rem .4rem;border-bottom:1px solid var(--line);vertical-align:middle}
.werte th{width:30%;font-weight:500}.werte .gruppe th{color:var(--k);font-size:.85rem;text-transform:uppercase;letter-spacing:.04em;padding-top:1rem;border:0}.wert{white-space:nowrap}
.spark{display:block;max-width:100%;height:44px}.spark .ref{fill:var(--ref)}.spark polyline{fill:none;stroke:var(--akzent);stroke-width:1.5}.spark .punkt{fill:var(--akzent)}.spark .punkt.ausser{fill:var(--ausser)}.befinden .punkt{fill:var(--ok)}
.neu{font-size:.75rem;color:var(--k);border:1px solid var(--line);border-radius:4px;padding:0 .3rem}
.ereignisse{padding-left:1rem}.ereignisse time{color:var(--k);font-size:.9rem;margin-right:.3rem}
.offen{display:flex;flex-wrap:wrap;gap:.5rem;align-items:center;margin:.5rem 0}
.zuletzt{padding:.6rem .8rem;border-left:3px solid var(--akzent);background:var(--ref);border-radius:0 8px 8px 0}
.tage td,.tage th{font-size:.9rem}
@media(max-width:480px){.werte th{width:38%}.spark{height:36px}}
`
