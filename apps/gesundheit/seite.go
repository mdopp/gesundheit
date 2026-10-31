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
	mux.HandleFunc("GET /werte", w.werte)
	mux.HandleFunc("GET /befunde", w.befunde)
	mux.HandleFunc("GET /befinden", w.befinden)
	mux.HandleFunc("GET /arzt", w.arzt)
	mux.HandleFunc("POST /profil", w.profil)
	mux.HandleFunc("POST /erklaerung", w.erklaerungSpeichern)
	mux.HandleFunc("POST /tag", w.tag)
	mux.HandleFunc("GET /fragen", w.fragen)
	mux.HandleFunc("POST /fragen", w.fragen)
	mux.HandleFunc("POST /zuordnen", w.zuordnen)
	mux.HandleFunc("GET /export.csv", w.export)
	mux.HandleFunc("GET /abschrift", w.abschrift)
	mux.HandleFunc("POST /korrektur", w.korrektur)
	mux.HandleFunc("GET /schnipsel", w.schnipsel)
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

// seiten are the program's pages, in the nav's order (owner 2026-10-30:
// a small group of pages; Haus links to these paths, keep them stable).
var seiten = []struct{ Pfad, Name string }{
	{"/werte", "Werte"},
	{"/befunde", "Befunde"},
	{"/befinden", "Wie es mir ging"},
	{"/arzt", "Für den Arzt"},
}

// start sends to the values: the page most people open the program for.
func (w *Web) start(rw http.ResponseWriter, r *http.Request) {
	ziel := "/werte"
	if q := r.URL.RawQuery; q != "" {
		ziel += "?" + q
	}
	http.Redirect(rw, r, ziel, http.StatusSeeOther)
}

// rahmen writes a page with the nav of the four pages (the current one
// marked), the people the viewer may open, and the message of a form.
func (w *Web) rahmen(rw http.ResponseWriter, r *http.Request, p *Person, s sitzung, darf []Person, pfad, titel, body string) {
	var b strings.Builder
	q := ""
	if p.Key != s.Login {
		q = "?person=" + html.EscapeString(p.Key)
	}
	b.WriteString(`<nav class=seiten aria-label="Seiten">`)
	for _, x := range seiten {
		aktiv := ""
		if x.Pfad == pfad {
			aktiv = ` aria-current=page`
		}
		fmt.Fprintf(&b, `<a href="%s%s"%s>%s</a>`, x.Pfad, q, aktiv, x.Name)
	}
	b.WriteString(`</nav>`)
	if len(darf) > 1 {
		b.WriteString(`<nav class=personen aria-label="Wessen Seiten">`)
		for _, d := range darf {
			cls := ""
			if d.Key == p.Key {
				cls = ` class=aktiv aria-current=true`
			}
			fmt.Fprintf(&b, `<a%s href="%s?person=%s">%s</a>`, cls, pfad, html.EscapeString(d.Key), html.EscapeString(d.Anzeige))
		}
		b.WriteString(`</nav>`)
	}
	if meldung := r.URL.Query().Get("m"); meldung != "" {
		b.WriteString(`<p class=ok role=status>` + html.EscapeString(meldung) + `</p>`)
	}
	seite(rw, http.StatusOK, titel, b.String()+body)
}

// neuZuLesen is the line while a new reader reads a person's records again (#10).
func (w *Web) neuZuLesen(key string) string {
	rec, err := w.Dienst.Speicher.Record()
	if err != nil || rec.Lauf == nil || rec.Lauf.NeuZuLesenPro[key] == 0 {
		return ""
	}
	n := rec.Lauf.NeuZuLesenPro[key]
	befunde := "Befunde"
	if n == 1 {
		befunde = "Befund"
	}
	return fmt.Sprintf(`<p class=hinweis role=status>Gesundheit liest deine Befunde gerade noch einmal, genauer als beim ersten Mal: noch %d %s neu zu lesen, einige pro Stunde. Bis dahin kann ein Wert falsch gelesen sein.</p>`, n, befunde)
}

// werte is the values page: one row per value that reads at first sight
// (#10), grouped, with „Was ist das?“; then the profile.
func (w *Web) werte(rw http.ResponseWriter, r *http.Request) {
	p, s, darf := w.wer(rw, r)
	if p == nil {
		return
	}
	v, _ := w.Dienst.Speicher.Verlauf(p.Key)
	pr, _ := w.Dienst.Speicher.Profil(p.Key)
	ex, _ := w.Dienst.Speicher.Erklaerungen()
	var b strings.Builder
	b.WriteString(`<p class=k>` + saysKeinArzt + `</p>`)
	b.WriteString(w.neuZuLesen(p.Key))
	if len(v.Werte) == 0 {
		b.WriteString(`<p>Noch keine Werte. Sobald ein Laborblatt oder ein Befund in Paperless liegt (Scanner „Nur für mich“ oder Mail an das Dokumenten-Postfach mit Betreff „Arzt: …“), lese ich die Werte und zeige sie hier.</p>`)
	} else {
		b.WriteString(`<p class=k>Jede Zeile zeigt den letzten Wert mit Datum, wo er zu dem Bereich steht, den sein Befund nennt, und wie er sich seit dem Mal davor verändert hat. Ab drei Messungen zeigt ein Verlauf die Jahre; hell hinterlegt ist der Bereich laut Befund.</p>`)
		b.WriteString(werteHTML(v, &pr, ex, s, w.Dienst.Public, p.Key, "/werte"+mitPerson(p, s)))
	}
	b.WriteString(profilHTML(p.Key, pr))
	fmt.Fprintf(&b, `<section class=k><p>%s</p><p><a href="/export.csv?person=%s">Als CSV herunterladen</a> · <form method=post action="/loeschen" class=inline onsubmit="return confirm('Alle Werte, Tagesnotizen und Angaben von %s löschen? Die Dokumente bleiben in Paperless.')"><input type=hidden name=person value="%s"><button class=k>Verlauf löschen</button></form></p></section>`,
		saysWerSieht, html.EscapeString(p.Key), html.EscapeString(p.Anzeige), html.EscapeString(p.Key))
	w.rahmen(rw, r, p, s, darf, "/werte", "Werte · "+p.Anzeige, b.String())
}

func mitPerson(p *Person, s sitzung) string {
	if p.Key == s.Login {
		return ""
	}
	return "?person=" + p.Key
}

// werteHTML draws the rows, by group.
func werteHTML(v Verlauf, pr *Profil, ex map[string]Erklaerung, s sitzung, public, person, zurueck string) string {
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
	var b strings.Builder
	gruppe := ""
	for _, n := range names {
		ms := byName[n]
		if g := ms[0].Gruppe; g != gruppe {
			if gruppe != "" {
				b.WriteString(`</section>`)
			}
			gruppe = g
			fmt.Fprintf(&b, `<section class=gruppe><h2>%s</h2>`, html.EscapeString(g))
		}
		b.WriteString(zeileHTML(n, ms, pr, ex, s, public, person, zurueck))
	}
	if gruppe != "" {
		b.WriteString(`</section>`)
	}
	return b.String()
}

// zeileHTML is one value: its name and marks, the latest value with its
// date, the status in words, the change, the chart from three days on,
// „Was ist das?“, and the form to correct the latest value (#13).
func zeileHTML(n string, ms []Messwert, pr *Profil, ex map[string]Erklaerung, s sitzung, public, person, zurueck string) string {
	last := ms[len(ms)-1]
	var b strings.Builder
	b.WriteString(`<article class=zeile id="w-` + html.EscapeString(last.ID) + `"><h3>` + html.EscapeString(n))
	if last.Neu {
		b.WriteString(` <span class=marke title="Dieser Name steht nicht im Wörterbuch; er wird so geführt, wie der Befund ihn schreibt.">neu</span>`)
	}
	if last.Quelle == "bild" {
		fmt.Fprintf(&b, ` <a class=marke href="/abschrift?dokument=%d&amp;person=%s" title="Der Text der Seite war zu schwach; der Wert ist aus dem Bild abgeschrieben.">aus dem Bild gelesen</a>`, last.Dokument, html.EscapeString(person))
	}
	if last.Pruefen {
		grund := last.PruefenGrund
		if grund == "" {
			grund = "Der Text der Seite nennt eine andere Zahl als das Bild"
		}
		b.WriteString(` <span class="marke pruefen" title="` + html.EscapeString(grund) + `. Bitte im Dokument nachsehen.">prüfen</span>`)
	}
	b.WriteString(korrekturMarke(last, s.Login))
	b.WriteString(`</h3>`)
	e := einordnen(last, pr)
	cls := "status"
	if e.Lage == LageUeber || e.Lage == LageUnter {
		cls += " ausser"
	}
	b.WriteString(`<p class=jetzt>` + html.EscapeString(wertMitDatum(last)) + `</p>`)
	fmt.Fprintf(&b, `<p class="%s">%s`, cls, html.EscapeString(e.Status))
	if e.Woher != "" {
		b.WriteString(` <span class=k>· ` + html.EscapeString(e.Woher) + `</span>`)
	}
	b.WriteString(`</p>`)
	if last.Pruefen && last.PruefenGrund != "" {
		b.WriteString(`<p class=k>Beim Lesen unsicher: ` + html.EscapeString(last.PruefenGrund) + `.</p>`)
	}
	b.WriteString(gelesenAls(last, s.Login))
	fmt.Fprintf(&b, `<p class=k>%s %s</p>`, html.EscapeString(aenderung(ms)), dokLink(last.Dokument, public))
	b.WriteString(diagramm(ms, public))
	// #15: each value of the row whose name or unit is assumed, to confirm with one tap
	for i, m := range ms {
		if m.Vermutung == nil {
			continue
		}
		b.WriteString(vermutungHTML(m, person, zurueck))
		if i < len(ms)-1 && m.ID != "" {
			b.WriteString(korrekturHTML(WertStand{Messwert: m}, person, zurueck))
		}
	}
	if last.ID != "" {
		b.WriteString(korrekturHTML(WertStand{Messwert: last}, person, zurueck))
	}
	b.WriteString(erklaerungHTML(n, ex, s.Admin, zurueck))
	b.WriteString(`</article>`)
	return b.String()
}

// befunde lists the person's medical documents, newest first: what each
// yielded, its transcript, what to check; and the ones waiting for a person.
func (w *Web) befunde(rw http.ResponseWriter, r *http.Request) {
	p, s, darf := w.wer(rw, r)
	if p == nil {
		return
	}
	rec, _ := w.Dienst.Speicher.Record()
	ws, _ := w.Dienst.Speicher.AlleWerte(p.Key)
	proDok := map[int][]WertStand{}
	for _, x := range ws {
		proDok[x.Dokument] = append(proDok[x.Dokument], x)
	}
	var b strings.Builder
	b.WriteString(w.neuZuLesen(p.Key))
	var eigene []*Eintrag
	if rec != nil {
		for _, e := range rec.Gelesen {
			if e.Person == p.Key {
				eigene = append(eigene, e)
			}
		}
	}
	datum := func(e *Eintrag) string {
		if e.Gelesen != nil {
			return e.Gelesen.Datum
		}
		return ""
	}
	sort.Slice(eigene, func(i, j int) bool { return datum(eigene[i]) > datum(eigene[j]) })
	if len(eigene) == 0 {
		b.WriteString(`<p>Noch keine Befunde. Ein Laborblatt oder Arztbrief, das in Paperless liegt (Scanner „Nur für mich“ oder Mail mit Betreff „Arzt: …“), steht hier, sobald ich es gelesen habe.</p>`)
	} else {
		b.WriteString(`<ul class=befunde>`)
		for _, e := range eigene {
			doc := 0
			if e.Gelesen != nil {
				doc = e.Gelesen.Dokument
			}
			b.WriteString(befundHTML(e, w.Dienst.Public, p.Key, proDok[doc], s.Login, "/befunde"+mitPerson(p, s)))
		}
		b.WriteString(`</ul>`)
	}
	// open documents: anyone may take one as their own, an admin also for a child
	if offene := w.Dienst.Offene(); len(offene) > 0 {
		b.WriteString(`<section><h2>Für wen?</h2><p>Diese Befunde habe ich gelesen, aber niemandem zuordnen können.</p>`)
		for _, e := range offene {
			g := e.Gelesen
			fmt.Fprintf(&b, `<form method=post action="/zuordnen" class=offen><input type=hidden name=dokument value="%d"><span>%s (%s, %d Werte, Patient „%s“)</span><select name=person>`, g.Dokument, html.EscapeString(g.Titel), datumText(g.Datum), len(g.Werte), html.EscapeString(g.Patient))
			for _, d := range darf {
				fmt.Fprintf(&b, `<option value="%s">%s</option>`, html.EscapeString(d.Key), html.EscapeString(d.Anzeige))
			}
			b.WriteString(`</select><button>Zuordnen</button></form>`)
		}
		b.WriteString(`</section>`)
	}
	w.rahmen(rw, r, p, s, darf, "/befunde", "Befunde · "+p.Anzeige, b.String())
}

// befundHTML is one document: date, title, links, what was read, what to
// check, and its values, each with its correction form (#13).
func befundHTML(e *Eintrag, public, person string, werte []WertStand, viewer, zurueck string) string {
	var b strings.Builder
	g := e.Gelesen
	if g == nil {
		fmt.Fprintf(&b, `<li><p><b>Nicht lesbar</b> <span class=k>%s</span></p></li>`, html.EscapeString(e.Fehler))
		return b.String()
	}
	// what still says „prüfen“: what was not taken, and the values the person has not spoken on yet
	pruefen, vermutet := len(g.Weg), 0
	for _, x := range werte {
		if x.Pruefen && !x.Weg {
			pruefen++
		}
		if x.Vermutung != nil && !x.Weg {
			vermutet++
		}
	}
	fmt.Fprintf(&b, `<li><p><time>%s</time> <b>%s</b>`, datumText(g.Datum), html.EscapeString(g.Titel))
	if g.Art != "" {
		b.WriteString(` <span class=k>· ` + html.EscapeString(g.Art) + `</span>`)
	}
	b.WriteString(`</p><p class=k>`)
	switch len(g.Werte) {
	case 0:
		b.WriteString(`keine Werte gelesen`)
	case 1:
		b.WriteString(`1 Wert gelesen`)
	default:
		fmt.Fprintf(&b, `%d Werte gelesen`, len(g.Werte))
	}
	if pruefen > 0 {
		fmt.Fprintf(&b, ` · <span class="marke pruefen">prüfen</span> %d %s`, pruefen, map[bool]string{true: "Stelle", false: "Stellen"}[pruefen == 1])
	}
	if vermutet > 0 {
		fmt.Fprintf(&b, ` · <span class="marke vermutet">%d vermutet, bitte bestätigen</span>`, vermutet)
	}
	if g.Quelle == "bild" {
		b.WriteString(` · aus dem Bild gelesen`)
	}
	if u := dokURL(g.Dokument, public); u != "" {
		b.WriteString(` · <a href="` + u + `" title="Nur zu Hause im WLAN">in Paperless</a>`)
	}
	if g.Abschrift != "" {
		fmt.Fprintf(&b, ` · <a href="/abschrift?dokument=%d&amp;person=%s">Abschrift</a>`, g.Dokument, html.EscapeString(person))
	}
	b.WriteString(`</p>`)
	if len(g.Ereignisse) > 0 {
		b.WriteString(`<ul class=ereignisse>`)
		for _, ev := range g.Ereignisse {
			fmt.Fprintf(&b, `<li><b>%s</b> %s</li>`, html.EscapeString(ev.Art), html.EscapeString(ev.Text))
		}
		b.WriteString(`</ul>`)
	}
	if len(werte) > 0 {
		offen := ""
		if pruefen > 0 || vermutet > 0 {
			offen = " open"
		}
		fmt.Fprintf(&b, `<details class=werte%s><summary>Die Werte ansehen und korrigieren</summary><ul class=werteliste>`, offen)
		for _, x := range werte {
			b.WriteString(wertZeileHTML(x, person, viewer, zurueck))
		}
		b.WriteString(`</ul></details>`)
	}
	b.WriteString(`</li>`)
	return b.String()
}

// wertZeileHTML is one value in a document's list: as it stands, its marks, its form.
func wertZeileHTML(x WertStand, person, viewer, zurueck string) string {
	var b strings.Builder
	wann := ""
	if x.Datum != "" {
		wann = " · " + datumText(x.Datum)
	}
	if x.Weg {
		fmt.Fprintf(&b, `<li class=weg id="w-%s"><s>%s %s</s> · gehört nicht dazu (%s) %s</li>`, html.EscapeString(x.ID), html.EscapeString(x.Name), html.EscapeString(mitEinheit(x.Wert, x.Einheit)+wann), wessenWort(x.Messwert, viewer), korrekturHTML(x, person, zurueck))
		return b.String()
	}
	fmt.Fprintf(&b, `<li id="w-%s"><b>%s</b> %s`, html.EscapeString(x.ID), html.EscapeString(x.Name), html.EscapeString(mitEinheit(x.Wert, x.Einheit)+wann))
	if x.Pruefen {
		grund := x.PruefenGrund
		if grund == "" {
			grund = "beim Lesen unsicher"
		}
		b.WriteString(` <span class="marke pruefen" title="` + html.EscapeString(grund) + `">prüfen</span>`)
	}
	b.WriteString(korrekturMarke(x.Messwert, viewer))
	b.WriteString(gelesenAls(x.Messwert, viewer))
	b.WriteString(vermutungHTML(x.Messwert, person, zurueck))
	b.WriteString(korrekturHTML(x, person, zurueck))
	b.WriteString(`</li>`)
	return b.String()
}

// befinden is the daily note: today's form, then the days.
func (w *Web) befinden(rw http.ResponseWriter, r *http.Request) {
	p, s, darf := w.wer(rw, r)
	if p == nil {
		return
	}
	tage, _ := w.Dienst.Speicher.Tage(p.Key)
	fragen, _ := w.Dienst.Speicher.Fragen(p.Key)
	heute := w.Dienst.now().Format("2006-01-02")
	var b strings.Builder
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
	if len(tage) > 0 {
		min, max := spanne(Verlauf{}, tage)
		b.WriteString(`<section><h2>Deine Tage</h2><p class=k>Schlaf, Energie und Stimmung gemittelt, ein Punkt pro Tag, oben ist besser:</p>` + befindenBand(tage, min, max) + tageHTML(tage, fragen) + `</section>`)
	} else {
		b.WriteString(`<p class=k>Noch keine Tage notiert. Was du hier oder dem Boten sagst, steht dann als Verlauf hier.</p>`)
	}
	w.rahmen(rw, r, p, s, darf, "/befinden", "Wie es mir ging · "+p.Anzeige, b.String())
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
		seite(rw, 400, "Nicht notiert", `<p>Das ging nicht: `+html.EscapeString(err.Error())+`.</p><p><a href="/befinden?person=`+html.EscapeString(p.Key)+`">Zurück</a></p>`)
		return
	}
	http.Redirect(rw, r, "/befinden?person="+p.Key+"&m="+urlQ("Notiert für "+datumText(n.Datum)+"."), http.StatusSeeOther)
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
		http.Redirect(rw, r, "/befinden?person="+p.Key+"&m="+urlQ(fmt.Sprintf("%d eigene Fragen gespeichert.", len(eigene))), http.StatusSeeOther)
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
	b.WriteString(`<button>Speichern</button> <a class=k href="/befinden?person=` + html.EscapeString(p.Key) + `">Zurück</a></form>`)
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
	http.Redirect(rw, r, "/befunde?person="+key+"&m="+urlQ("Befund zugeordnet; seine Werte stehen jetzt unter Werte."), http.StatusSeeOther)
}

// abschrift shows the transcript a document's values were read from, when
// its page's text was too weak (#5): only to whom the document belongs.
func (w *Web) abschrift(rw http.ResponseWriter, r *http.Request) {
	p, _, _ := w.wer(rw, r)
	if p == nil {
		return
	}
	doc, _ := strconv.Atoi(r.FormValue("dokument"))
	rec, err := w.Dienst.Speicher.Record()
	var e *Eintrag
	if err == nil {
		e = rec.Gelesen[strconv.Itoa(doc)]
	}
	if e == nil || e.Person != p.Key || e.Gelesen == nil || e.Gelesen.Abschrift == "" {
		seite(rw, http.StatusNotFound, "Keine Abschrift", `<p>Zu diesem Dokument gibt es in deinem Verlauf keine Abschrift.</p><p><a href="/">Zurück</a></p>`)
		return
	}
	g := e.Gelesen
	var b strings.Builder
	fmt.Fprintf(&b, `<p class=k>Abschrift von „%s“ (%s), aus dem Bild gelesen: Der Text der Seite war zu schwach. Nur abgeschrieben, nicht gedeutet. %s</p>`, html.EscapeString(g.Titel), datumText(g.Datum), dokLink(g.Dokument, w.Dienst.Public))
	if len(g.Abweichungen) > 0 {
		b.WriteString(`<p>Bitte prüfen: ` + html.EscapeString(strings.Join(g.Abweichungen, "; ")) + `.</p>`)
	}
	b.WriteString(`<pre class=abschrift>` + html.EscapeString(g.Abschrift) + `</pre><p><a href="/befunde?person=` + html.EscapeString(p.Key) + `">Zurück zu den Befunden</a></p>`)
	seite(rw, http.StatusOK, "Abschrift", b.String())
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
	http.Redirect(rw, r, "/werte?person="+p.Key+"&m="+urlQ("Verlauf gelöscht. Die Dokumente liegen weiter in Paperless; ein neuer Lauf liest sie nicht noch einmal."), http.StatusSeeOther)
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
:root{--fg:#1d2730;--bg:#fff;--k:#5f6b78;--line:#e3e8ee;--akzent:#2b6f8f;--ref:#dfeef4;--ausser:#b4432f;--ok:#2f7d4f;--flaeche:#f4f7f9}
@media(prefers-color-scheme:dark){:root{--fg:#e7ecf0;--bg:#141a1f;--k:#a3aeb9;--line:#2a333c;--akzent:#7fb6d2;--ref:#1f3340;--ausser:#e88a77;--ok:#6cc08b;--flaeche:#1b232a}}
body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.45 system-ui,sans-serif}
header,main{max-width:760px;margin:0 auto;padding:0 16px}
h1{font-size:1.3rem;margin:1rem 0 .5rem}h1 a{color:inherit;text-decoration:none}h2{font-size:1.1rem;margin:1.6rem 0 .5rem}h3{font-size:1rem;margin:0 0 .2rem}
a{color:var(--akzent)}
.k{color:var(--k);font-size:.92rem}.ok{color:var(--ok)}.ausser{color:var(--ausser)}
.seiten{display:flex;flex-wrap:wrap;gap:.3rem;margin:0 0 .8rem;padding-bottom:.6rem;border-bottom:1px solid var(--line)}
.seiten a{padding:.35rem .7rem;border-radius:8px;color:inherit;text-decoration:none;font-size:.95rem}.seiten a[aria-current]{background:var(--akzent);color:var(--bg);font-weight:600}
.personen a{display:inline-block;margin:0 .6rem .4rem 0;padding:.2rem .6rem;border:1px solid var(--line);border-radius:999px;color:inherit;text-decoration:none}.personen a.aktiv{border-color:var(--akzent);color:var(--akzent)}
.hinweis{padding:.6rem .8rem;border-left:3px solid var(--akzent);background:var(--flaeche);border-radius:0 8px 8px 0}
.frage{display:block;margin:.6rem 0}.frage>span:first-child{display:block;font-size:.95rem;margin-bottom:.2rem}.skala label{display:inline-block;margin-right:.5rem}
input[type=text],input:not([type]),select,textarea{font:inherit;padding:.3rem .4rem;border:1px solid var(--line);border-radius:6px;background:var(--bg);color:inherit;width:100%;max-width:420px;box-sizing:border-box}select{width:auto}
button{font:inherit;padding:.4rem .9rem;border:1px solid var(--akzent);border-radius:8px;background:var(--akzent);color:#fff}button.k{background:none;color:var(--k);border-color:var(--line)}
.inline{display:inline}
table{border-collapse:collapse;width:100%}td,th{text-align:left;padding:.35rem .4rem;border-bottom:1px solid var(--line);vertical-align:middle}
.gruppe h2{color:var(--k);font-size:.85rem;text-transform:uppercase;letter-spacing:.04em;margin:1.4rem 0 .2rem}
.zeile{padding:.7rem 0;border-bottom:1px solid var(--line)}.zeile p{margin:.15rem 0}
.jetzt{font-size:1.1rem;font-weight:600}.status{font-weight:500}.status.ausser{color:var(--ausser)}
.marke{font-size:.75rem;font-weight:400;color:var(--k);border:1px solid var(--line);border-radius:4px;padding:0 .3rem;text-decoration:none;white-space:nowrap}.marke.pruefen{color:var(--ausser);border-color:var(--ausser)}
.was summary{cursor:pointer;color:var(--akzent);font-size:.92rem;margin-top:.2rem}.was p{margin:.3rem 0}
.spark{display:block;width:100%;max-width:320px;height:auto;margin:.3rem 0}.spark .ref{fill:var(--ref)}.spark polyline{fill:none;stroke:var(--akzent);stroke-width:1.5}.spark .punkt{fill:var(--akzent)}.spark .punkt.ausser{fill:var(--ausser)}.spark .achse{stroke:var(--line)}.spark .jahr{fill:var(--k);font-size:10px}.befinden .punkt{fill:var(--ok)}
.profil{margin:1.6rem 0;padding:.6rem .8rem;border:1px solid var(--line);border-radius:8px}.profil summary{cursor:pointer;font-weight:500}
.abschrift{white-space:pre-wrap;font-size:.85rem;border:1px solid var(--line);border-radius:6px;padding:.75rem;overflow-x:auto}
.befunde{list-style:none;padding:0}.befunde>li{padding:.6rem 0;border-bottom:1px solid var(--line)}.befunde p{margin:.1rem 0}.befunde time{color:var(--k);margin-right:.3rem}
.ereignisse{padding-left:1rem;margin:.3rem 0}
.offen{display:flex;flex-wrap:wrap;gap:.5rem;align-items:center;margin:.5rem 0}
.tage td,.tage th{font-size:.9rem}.tage{display:block;overflow-x:auto}
.korrektur summary{cursor:pointer;color:var(--akzent);font-size:.92rem;margin-top:.2rem}.korrektur{margin:.3rem 0}
.schnipsel{margin:.4rem 0}.schnipsel img{display:block;max-width:100%;height:auto;border:1px solid var(--line);border-radius:6px;min-height:2rem}.schnipsel figcaption{font-size:.85rem;color:var(--k)}
mark{background:#ffe58a;color:#1d2730;padding:0 .1rem}
.korrektur-form label{display:inline-block;margin:.2rem .6rem .2rem 0}.korrektur-form label span{display:block;font-size:.85rem;color:var(--k)}.korrektur-form input{max-width:9rem}.knoepfe{margin:.4rem 0}
.marke.ok{color:var(--ok);border-color:var(--ok)}
.marke.vermutet{color:var(--akzent);border-color:var(--akzent);white-space:normal}.vermutung{margin:.3rem 0}.vermutung button{padding:.15rem .7rem;margin-left:.3rem}
.werte summary{cursor:pointer;color:var(--akzent);font-size:.92rem}.werteliste{list-style:none;padding:0}.werteliste>li{padding:.3rem 0;border-bottom:1px dashed var(--line)}.werteliste .weg{color:var(--k)}
.block{break-inside:avoid;margin:.8rem 0}.vergleich{padding-left:1.1rem;margin:.3rem 0}.vergleich li{margin:.1rem 0}
.arzt>li{margin:.6rem 0 1rem;break-inside:avoid}.arzt p{margin:.15rem 0}.frage-text{font-weight:600}.url{display:none}
.antwort{border-bottom:1px solid var(--line);height:1.8rem}
@media print{
 :root{--fg:#000;--bg:#fff;--k:#333;--line:#999;--akzent:#000;--ausser:#000}
 body{font-size:10.5pt}header,.seiten,.personen,.nicht-drucken,p.ok{display:none}main{max-width:none;padding:0}h2{margin:.2rem 0 .3rem}.block{margin:.5rem 0}.antwort{height:1.4rem}
 a{color:inherit;text-decoration:none}.url{display:inline;font-size:8pt;color:#333}
}
`
