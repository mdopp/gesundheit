package main

import (
	"fmt"
	"hash/fnv"
	"html"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Correcting a value (#13, owner 2026-10-31: „„Unsicher gelesen“ ist gut,
// aber warum gibst du mir nicht die Gelegenheit, den Wert zu
// korrigieren?“). A person — and an admin for a child — corrects a value's
// number, unit and date, confirms it („stimmt so“) or drops it („gehört
// nicht dazu“). The word is the person's: it lives in their own file
// (personen/<key>/korrekturen.json), apart from what the reader wrote, and
// is applied whenever the timeline is read. So a reader of a later version
// (leserVersion) replaces its own values and never the person's.

// Lesung is a value as the reader read it, kept beside a correction.
type Lesung struct {
	Wert    float64 `json:"wert"`
	Einheit string  `json:"einheit"`
	Datum   string  `json:"datum"`
}

// Korrektur is the person's word on one value.
type Korrektur struct {
	Aktion  string   `json:"aktion"` // korrigiert | bestaetigt | weg
	Wert    *float64 `json:"wert,omitempty"`
	Einheit string   `json:"einheit,omitempty"`
	Datum   string   `json:"datum,omitempty"`
	// Wie is the value as read when the person spoke: a re-read that names
	// it otherwise still finds it by its number, and a re-read that no
	// longer has it keeps the person's value
	Wie Messwert  `json:"wie_gelesen"`
	Von string    `json:"von"`
	At  time.Time `json:"at"`
}

// WertStand is a value as shown, with the person's word applied; Weg is a
// value the person dropped (only the correction forms list it).
type WertStand struct {
	Messwert
	Weg bool
	roh Messwert // as the reader read it
}

// schluessel names each value the same way across re-reads: its document,
// its name, its day, and the how-many-th of that name and day on the sheet
// (a lung-function sheet prints a value per attempt).
func schluessel(werte []Messwert) []string {
	n := map[string]int{}
	out := make([]string, len(werte))
	for i, m := range werte {
		basis := fmt.Sprintf("%d|%s|%s", m.Dokument, falten(m.Name), m.Datum)
		out[i] = basis + "|" + strconv.Itoa(n[basis])
		n[basis]++
	}
	return out
}

// wertID is a value's name in a form or a link: short, no characters to escape.
func wertID(schl string) string {
	h := fnv.New64a()
	h.Write([]byte(schl))
	return fmt.Sprintf("%016x", h.Sum64())
}

// anwenden lays the person's words over what the reader read. A word finds
// its value by its key; else by its document, day, number and unit (a
// re-read that renamed it); else, when the document is still the person's,
// the person's value stands by itself (a re-read that lost it).
func anwenden(roh []Messwert, ks map[string]Korrektur, eigenes func(doc int) bool) []WertStand {
	keys := schluessel(roh)
	out := make([]WertStand, len(roh))
	frei := make([]bool, len(roh))
	benutzt := map[string]bool{}
	for i, m := range roh {
		m.Schluessel, m.ID = keys[i], wertID(keys[i])
		out[i] = WertStand{Messwert: m, roh: m}
		frei[i] = true
		if k, ok := ks[keys[i]]; ok {
			out[i] = mitKorrektur(m, k)
			frei[i], benutzt[keys[i]] = false, true
		}
	}
	var rest []string
	for key := range ks {
		if !benutzt[key] {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	for _, key := range rest {
		k := ks[key]
		gefunden := false
		for i, m := range roh {
			if !frei[i] || m.Dokument != k.Wie.Dokument || m.Datum != k.Wie.Datum || m.Wert != k.Wie.Wert || falten(m.Einheit) != falten(k.Wie.Einheit) {
				continue
			}
			// the correction keeps its key: no value of the re-read carries it
			m.Schluessel, m.ID = key, wertID(key)
			out[i] = mitKorrektur(m, k)
			frei[i], gefunden = false, true
			break
		}
		if gefunden || k.Aktion == "weg" || eigenes == nil || !eigenes(k.Wie.Dokument) {
			continue
		}
		m := k.Wie
		m.Schluessel, m.ID = key, wertID(key)
		out = append(out, mitKorrektur(m, k))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Datum < out[j].Datum })
	return out
}

func mitKorrektur(m Messwert, k Korrektur) WertStand {
	w := WertStand{Messwert: m, roh: m}
	w.KorrigiertVon = k.Von
	switch k.Aktion {
	case "weg":
		w.Weg = true
	case "bestaetigt":
		w.Korrektur, w.Pruefen, w.PruefenGrund = "bestaetigt", false, ""
	case "korrigiert":
		w.Gelesen = &Lesung{Wert: m.Wert, Einheit: m.Einheit, Datum: m.Datum}
		if k.Wert != nil {
			w.Wert = *k.Wert
		}
		w.Einheit = k.Einheit
		if k.Datum != "" {
			w.Datum = k.Datum
		}
		w.Korrektur, w.Pruefen, w.PruefenGrund = "korrigiert", false, ""
	}
	return w
}

func (s *Speicher) korrekturen(key string) (map[string]Korrektur, error) {
	ks := map[string]Korrektur{}
	err := s.lesen(s.person(key, "korrekturen.json"), &ks)
	if ks == nil {
		ks = map[string]Korrektur{}
	}
	return ks, err
}

// alleWerte is a person's timeline with their words applied; the caller holds the lock.
func (s *Speicher) alleWerte(key string) (Verlauf, []WertStand, error) {
	var v Verlauf
	if err := s.lesen(s.person(key, "verlauf.json"), &v); err != nil {
		return v, nil, err
	}
	ks, err := s.korrekturen(key)
	if err != nil {
		// the person's values are never hidden for their side file; the log says why the words are missing
		log.Printf("korrekturen %s: %v (die Werte stehen wie gelesen da)", key, err)
		ks = map[string]Korrektur{}
	}
	var rec *Record
	eigenes := func(doc int) bool {
		if rec == nil {
			r, err := s.record()
			if err != nil {
				return false
			}
			rec = r
		}
		e := rec.Gelesen[strconv.Itoa(doc)]
		return e != nil && e.Person == key
	}
	return v, anwenden(v.Werte, ks, eigenes), nil
}

// AlleWerte are a person's values as the correction forms list them: the
// dropped ones too.
func (s *Speicher) AlleWerte(key string) ([]WertStand, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ws, err := s.alleWerte(key)
	return ws, err
}

// KorrekturSetzen keeps the person's word on one value, as it was read when they spoke.
func (s *Speicher) KorrekturSetzen(key string, w WertStand, k Korrektur) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ks, err := s.korrekturen(key)
	if err != nil {
		return err
	}
	k.Wie = w.roh
	k.Wie.ID, k.Wie.Schluessel = "", ""
	if k.At.IsZero() {
		k.At = time.Now()
	}
	ks[w.Schluessel] = k
	return s.schreiben(s.person(key, "korrekturen.json"), ks)
}

// KorrekturLoeschen takes the person's word back: the value is as read again.
func (s *Speicher) KorrekturLoeschen(key string, w WertStand) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ks, err := s.korrekturen(key)
	if err != nil {
		return err
	}
	delete(ks, w.Schluessel)
	return s.schreiben(s.person(key, "korrekturen.json"), ks)
}

// --- the form

func (w *Web) korrektur(rw http.ResponseWriter, r *http.Request) {
	p, s, _ := w.wer(rw, r)
	if p == nil {
		return
	}
	ws, err := w.Dienst.Speicher.AlleWerte(p.Key)
	if err != nil {
		seite(rw, http.StatusInternalServerError, "Nicht gespeichert", `<p>Der Verlauf ist gerade nicht lesbar: `+html.EscapeString(err.Error())+`.</p>`)
		return
	}
	var wert *WertStand
	for i := range ws {
		if ws[i].ID == r.PostFormValue("id") {
			wert = &ws[i]
		}
	}
	zurueck := sichererPfad(r.PostFormValue("zurueck"), "/werte"+mitPerson(p, s))
	if wert == nil {
		seite(rw, http.StatusNotFound, "Kein solcher Wert", `<p>Diesen Wert gibt es in diesem Verlauf nicht (mehr). Vielleicht wurde der Befund gerade neu gelesen.</p><p><a href="`+html.EscapeString(zurueck)+`">Zurück</a></p>`)
		return
	}
	nein := func(grund string) {
		seite(rw, http.StatusBadRequest, "Nicht gespeichert", `<p>Nicht gespeichert: `+html.EscapeString(grund)+`.</p><p><a href="`+html.EscapeString(zurueck)+`">Zurück</a></p>`)
	}
	roh := wert.roh
	k := Korrektur{Von: s.Login, At: w.Dienst.now()}
	var meldung string
	switch r.PostFormValue("aktion") {
	case "korrigieren":
		eingabe := strings.TrimSpace(r.PostFormValue("wert"))
		v, ok := zahl(eingabe)
		if !ok {
			nein(fmt.Sprintf("„%s“ ist keine Zahl (etwa 12,8)", eingabe))
			return
		}
		einheit := strings.TrimSpace(r.PostFormValue("einheit"))
		if len([]rune(einheit)) > 20 {
			nein("die Einheit ist länger als 20 Zeichen")
			return
		}
		datum := roh.Datum
		if e := strings.TrimSpace(r.PostFormValue("datum")); e != "" {
			if datum = tag(e); datum == "" {
				nein(fmt.Sprintf("„%s“ ist kein Datum (TT.MM.JJJJ)", e))
				return
			}
		}
		if v == roh.Wert && einheit == roh.Einheit && datum == roh.Datum {
			k.Aktion = "bestaetigt"
			meldung = fmt.Sprintf("Gemerkt: %s stimmt so.", roh.Name)
			break
		}
		k.Aktion, k.Wert, k.Einheit, k.Datum = "korrigiert", &v, einheit, datum
		meldung = fmt.Sprintf("Korrigiert: %s %s am %s. Das bleibt so, auch wenn Gesundheit den Befund neu liest.", roh.Name, mitEinheit(v, einheit), datumText(datum))
	case "bestaetigen":
		k.Aktion = "bestaetigt"
		meldung = fmt.Sprintf("Gemerkt: %s stimmt so.", roh.Name)
	case "weg":
		k.Aktion = "weg"
		meldung = fmt.Sprintf("Gemerkt: %s (%s) gehört nicht dazu und steht nirgends mehr. Unter Befunde kannst du das zurücknehmen.", roh.Name, mitEinheit(roh.Wert, roh.Einheit))
	case "zuruecknehmen":
		if err := w.Dienst.Speicher.KorrekturLoeschen(p.Key, *wert); err != nil {
			nein(err.Error())
			return
		}
		http.Redirect(rw, r, mitMeldung(zurueck, fmt.Sprintf("Zurückgenommen: %s steht wieder so da, wie Gesundheit ihn gelesen hat.", roh.Name))+"#w-"+wert.ID, http.StatusSeeOther)
		return
	default:
		nein("unbekannte Aktion")
		return
	}
	if err := w.Dienst.Speicher.KorrekturSetzen(p.Key, *wert, k); err != nil {
		nein(err.Error())
		return
	}
	http.Redirect(rw, r, mitMeldung(zurueck, meldung)+"#w-"+wert.ID, http.StatusSeeOther)
}

// wessenWort is whose word a value carries, as the viewer reads it: „von dir“, „von ben“.
func wessenWort(m Messwert, viewer string) string {
	if m.KorrigiertVon == "" || m.KorrigiertVon == viewer {
		return "von dir"
	}
	return "von " + m.KorrigiertVon
}

// korrekturMarke is the mark of a value the person spoke on.
func korrekturMarke(m Messwert, viewer string) string {
	switch m.Korrektur {
	case "korrigiert":
		titel := ""
		if m.Gelesen != nil {
			titel = "Gelesen als " + mitEinheit(m.Gelesen.Wert, m.Gelesen.Einheit) + " am " + datumText(m.Gelesen.Datum)
		}
		return ` <span class="marke ok" title="` + html.EscapeString(titel) + `">` + wessenWort(m, viewer) + ` korrigiert</span>`
	case "bestaetigt":
		return ` <span class="marke ok">` + wessenWort(m, viewer) + ` bestätigt</span>`
	}
	return ""
}

// gelesenAls is the line under a corrected value: what the reader had.
func gelesenAls(m Messwert, viewer string) string {
	if m.Korrektur != "korrigiert" || m.Gelesen == nil {
		return ""
	}
	g := m.Gelesen
	wann := ""
	if g.Datum != "" {
		wann = " am " + datumText(g.Datum)
	}
	return `<p class=k>` + strings.ToUpper(wessenWort(m, viewer)[:1]) + wessenWort(m, viewer)[1:] + ` korrigiert – gelesen als ` + html.EscapeString(mitEinheit(g.Wert, g.Einheit)+wann) + `.</p>`
}

// korrekturHTML is the form beside a value: the snippet of its page (loaded
// when the form is opened), the words it was read from with the number
// marked, the fields, and the three answers.
func korrekturHTML(w WertStand, person, zurueck string) string {
	var b strings.Builder
	id := html.EscapeString(w.ID)
	if w.Weg {
		fmt.Fprintf(&b, `<form method=post action="/korrektur" class=inline><input type=hidden name=person value="%s"><input type=hidden name=id value="%s"><input type=hidden name=zurueck value="%s"><button name=aktion value=zuruecknehmen class=k>zurücknehmen</button></form>`,
			html.EscapeString(person), id, html.EscapeString(zurueck))
		return b.String()
	}
	titel := "Korrigieren"
	if w.Pruefen {
		titel = "Prüfen und korrigieren"
	}
	fmt.Fprintf(&b, `<details class=korrektur><summary>%s</summary>`, titel)
	fmt.Fprintf(&b, `<figure class=schnipsel><img src="/schnipsel?person=%s&amp;id=%s" loading=lazy alt="Ausschnitt der Seite, auf der der Wert steht"><figcaption>Gelesen aus: „%s“</figcaption></figure>`,
		html.EscapeString(url.QueryEscape(person)), id, belegHTML(w.Messwert))
	fmt.Fprintf(&b, `<form method=post action="/korrektur" class=korrektur-form><input type=hidden name=person value="%s"><input type=hidden name=id value="%s"><input type=hidden name=zurueck value="%s">`,
		html.EscapeString(person), id, html.EscapeString(zurueck))
	fmt.Fprintf(&b, `<label><span>Wert</span><input name=wert inputmode=decimal value="%s"></label>`, html.EscapeString(zahlText(w.Wert)))
	fmt.Fprintf(&b, `<label><span>Einheit</span><input name=einheit maxlength=20 value="%s"></label>`, html.EscapeString(w.Einheit))
	fmt.Fprintf(&b, `<label><span>Datum</span><input type=date name=datum value="%s"></label>`, html.EscapeString(w.Datum))
	b.WriteString(`<p class=knoepfe><button name=aktion value=korrigieren>Korrektur speichern</button> <button name=aktion value=bestaetigen class=k>Stimmt so</button> <button name=aktion value=weg class=k>Gehört nicht dazu</button>`)
	if w.Korrektur != "" {
		b.WriteString(` <button name=aktion value=zuruecknehmen class=k>Wie gelesen</button>`)
	}
	b.WriteString(`</p></form><p class=k>Deine Korrektur gilt, auch wenn Gesundheit den Befund später neu liest. Das Bild der Seite holt Gesundheit nur für dich aus Paperless und speichert es nicht.</p></details>`)
	return b.String()
}
