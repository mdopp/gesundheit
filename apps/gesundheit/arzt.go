package main

import (
	"fmt"
	"html"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// „Für den Arzt“ (#10): a printable page per person with what is worth a
// question at the next visit, each as a question with its value, its date
// and the document it stands in. It lists, it never concludes: a value
// outside the range its report prints, a change of more than a set share
// or across that range, a value read unsure.

// deutlichSchwelle is the relative change the page asks about: 15 % of the
// value before. A page threshold, not a medical one; it is said on the page.
const deutlichSchwelle = 0.15

// ArztFrage is one question on the page.
type ArztFrage struct {
	Art      string // "ausser" | "aenderung" | "pruefen"
	Name     string
	Text     string // the facts, one sentence
	Frage    string // the question
	Dokument int
	Datum    string
}

// arztFragen collects the questions from a person's values; titel names the documents.
func arztFragen(v Verlauf, p *Profil) []ArztFrage {
	byName := map[string][]Messwert{}
	var namen []string
	for _, m := range v.Werte {
		if _, ok := byName[m.Name]; !ok {
			namen = append(namen, m.Name)
		}
		byName[m.Name] = append(byName[m.Name], m)
	}
	sort.Strings(namen)
	var out []ArztFrage
	for _, n := range namen {
		ms := byName[n]
		last := ms[len(ms)-1]
		e := einordnen(last, p)
		if e.Lage == LageUeber || e.Lage == LageUnter {
			out = append(out, ArztFrage{Art: "ausser", Name: n, Dokument: last.Dokument, Datum: last.Datum,
				Text:  fmt.Sprintf("%s: %s am %s – %s %s (%s).", n, mitEinheit(last.Wert, last.Einheit), datumText(last.Datum), woherKurz(e), e.Status, bereichVon(last, e)),
				Frage: "Was bedeutet dieser Wert für mich?"})
		}
		if vor := vorher(ms); vor != nil && falten(vor.Einheit) == falten(last.Einheit) {
			r, rel := richtung(vor.Wert, last.Wert)
			ev := einordnen(*vor, p)
			gekreuzt := (ev.Lage == LageIn) != (e.Lage == LageIn) && ev.Lage != LageKein && ev.Lage != LageSoll && e.Lage != LageKein && e.Lage != LageSoll
			if math.Abs(rel) > deutlichSchwelle || gekreuzt {
				t := fmt.Sprintf("%s: von %s (%s) auf %s (%s) %s", n, mitEinheit(vor.Wert, vor.Einheit), datumText(vor.Datum), mitEinheit(last.Wert, last.Einheit), datumText(last.Datum), r)
				if !math.IsInf(rel, 0) && r != "kaum verändert" {
					t += fmt.Sprintf(", um %s %%", zahlText(math.Round(math.Abs(rel)*100)))
				}
				if gekreuzt {
					t += "; vorher " + kurzStatus(ev) + ", jetzt " + kurzStatus(e)
				}
				out = append(out, ArztFrage{Art: "aenderung", Name: n, Dokument: last.Dokument, Datum: last.Datum, Text: t + ".",
					Frage: "Ist diese Veränderung von Bedeutung?"})
			}
		}
		for _, m := range ms {
			if !m.Pruefen {
				continue
			}
			t := fmt.Sprintf("%s am %s: gelesen als %s, beim Lesen unsicher", n, datumText(m.Datum), mitEinheit(m.Wert, m.Einheit))
			if m.PruefenGrund != "" {
				t += " (" + m.PruefenGrund + ")"
			}
			out = append(out, ArztFrage{Art: "pruefen", Name: n, Dokument: m.Dokument, Datum: m.Datum, Text: t + ".",
				Frage: "Steht der Wert so im Befund, und was heißt er?"})
		}
	}
	return out
}

// kurzStatus is a status as the doctor's page says it: no „normal“, only the range.
func kurzStatus(e Einordnung) string {
	if e.Lage == LageIn {
		return "im Bereich"
	}
	return e.Status
}

// the woher of a status, in the doctor page's short form
func woherKurz(e Einordnung) string {
	if strings.Contains(e.Woher, "berechnet") {
		return "nach einer berechneten Grenze (nicht vom Befund)"
	}
	return "laut Befund"
}

func bereichVon(m Messwert, e Einordnung) string {
	switch {
	case m.RefMin != nil || m.RefMax != nil:
		return bereichText(m.RefMin, m.RefMax, m.Einheit)
	case m.LLN != nil:
		return "unterer Grenzwert " + mitEinheit(*m.LLN, m.Einheit)
	}
	if i := strings.LastIndex(e.Woher, ": "); i >= 0 {
		return e.Woher[i+2:]
	}
	return e.Woher
}

func (w *Web) arzt(rw http.ResponseWriter, r *http.Request) {
	p, s, darf := w.wer(rw, r)
	if p == nil {
		return
	}
	v, _ := w.Dienst.Speicher.Verlauf(p.Key)
	pr, _ := w.Dienst.Speicher.Profil(p.Key)
	rec, _ := w.Dienst.Speicher.Record()
	titel := func(doc int) string {
		if rec != nil {
			if e := rec.Gelesen[strconv.Itoa(doc)]; e != nil && e.Gelesen != nil && e.Gelesen.Titel != "" {
				return e.Gelesen.Titel
			}
		}
		return "Dokument " + strconv.Itoa(doc)
	}
	fragen := arztFragen(v, &pr)
	var b strings.Builder
	fmt.Fprintf(&b, `<h2>Fragen an den Arzt <span class=k>· %s · Stand %s</span></h2>`, html.EscapeString(p.Anzeige), datumText(w.Dienst.now().Format("2006-01-02")))
	b.WriteString(`<p class=k>Aus deinen Befunden zusammengestellt, so wie die Werte dort stehen. Die Seite bewertet nichts; die Fragen sind Vorschläge für das Gespräch.</p>`)
	b.WriteString(`<p class=nicht-drucken><button type=button onclick="window.print()">Drucken</button></p>`)
	abschnitte := []struct{ art, titel, leer string }{
		{"ausser", "Werte außerhalb ihres Bereichs (letzte Messung)", ""},
		{"aenderung", fmt.Sprintf("Veränderungen um mehr als %s %% oder über den Bereich hinweg", zahlText(deutlichSchwelle*100)), ""},
		{"pruefen", "Unsicher gelesen", ""},
	}
	if len(fragen) == 0 {
		b.WriteString(`<p>Nichts außerhalb eines Bereichs, keine Veränderung um mehr als 15 %, nichts unsicher Gelesenes. Eigene Fragen kannst du unten aufschreiben.</p>`)
	}
	for _, a := range abschnitte {
		var teil []ArztFrage
		for _, f := range fragen {
			if f.Art == a.art {
				teil = append(teil, f)
			}
		}
		if len(teil) == 0 {
			continue
		}
		fmt.Fprintf(&b, `<section><h3>%s</h3><ol class=arzt>`, html.EscapeString(a.titel))
		for _, f := range teil {
			quelle := html.EscapeString(titel(f.Dokument))
			if u := dokURL(f.Dokument, w.Dienst.Public); u != "" {
				quelle = `<a href="` + u + `">` + quelle + `</a> <span class=url>` + html.EscapeString(u) + `</span>`
			}
			fmt.Fprintf(&b, `<li><p>%s</p><p class=frage-text>%s</p><p class=k>Quelle: %s</p><div class=antwort></div></li>`,
				html.EscapeString(f.Text), html.EscapeString(f.Frage), quelle)
		}
		b.WriteString(`</ol></section>`)
	}
	b.WriteString(`<section><h3>Eigene Fragen</h3><div class=antwort></div><div class=antwort></div><div class=antwort></div></section>`)
	w.rahmen(rw, r, p, s, darf, "/arzt", "Für den Arzt · "+p.Anzeige, b.String())
}
