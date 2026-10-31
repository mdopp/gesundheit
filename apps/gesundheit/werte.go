package main

import (
	"fmt"
	"html"
	"math"
	"strings"
	"time"
)

// gesundheit#10: a row of the values page reads at first sight. The owner,
// 2026-10-30, on the page as it was: „Es sagt mir nichts. Warum stehen Werte
// weiter links, andere weiter rechts? … Was hilft mir zu verstehen, was da
// steht?“ So a row says in words where the latest value stands against the
// range its report prints, how it changed since the time before, and draws
// a chart only when there is a course to see. Nothing here judges a value:
// the range is the report's, the words only compare numbers.

// Lage is where a value stands against its range.
type Lage int

const (
	LageKein  Lage = iota // no range: the report prints none and none can be computed
	LageIn                // inside the range
	LageUeber             // above it
	LageUnter             // below it
	LageSoll              // the report prints only a predicted value: a percentage, no boundary
)

// Einordnung is a value's status in words and where its range comes from.
type Einordnung struct {
	Lage   Lage
	Status string // „im Normalbereich“, „über dem Bereich“, „unter dem Bereich“, „84 % des Sollwerts“, „kein Bereich angegeben“
	Woher  string // „Bereich laut Befund: 12–16 g/dl“; "" without a range
}

// Sollformel is a published reference equation for one value: the range
// it gives a person at an age, in its unit; ok is false outside the
// equation's validity (age, height) or for a profile it cannot use.
type Sollformel struct {
	Name    string // the equation as cited, said on the page
	Einheit string
	Bereich func(p Profil, alter float64) (lo, hi *float64, ok bool)
}

// sollFormeln are the equations, by the dictionary's name. Empty on
// purpose (#10, 2026-10-30): the equation lung-function reports use,
// GLI-2012, needs the published lookup splines per age and sex, which are
// not built in here faithfully; an approximation would print wrong medical
// numbers, which are worse than none. Without an equation a value whose
// report prints no range says „kein Bereich angegeben“.
var sollFormeln = map[string]Sollformel{}

// einordnen is a value's status: the report's range first, then the
// report's lower limit of normal (LLN), then its predicted value, and only
// when the report prints none of them a range computed from the person's
// profile, said as such.
func einordnen(m Messwert, p *Profil) Einordnung {
	soll := sollText(m)
	switch {
	case m.RefMin != nil || m.RefMax != nil:
		e := Einordnung{Lage: lage(m.Wert, m.RefMin, m.RefMax), Woher: "Bereich laut Befund: " + bereichText(m.RefMin, m.RefMax, m.Einheit)}
		e.Status = lageText(e.Lage)
		if soll != "" {
			e.Woher += " · " + soll
		}
		return e
	case m.LLN != nil:
		e := Einordnung{Lage: lage(m.Wert, m.LLN, nil), Woher: "unterer Grenzwert laut Befund (LLN): " + mitEinheit(*m.LLN, m.Einheit)}
		e.Status = lageText(e.Lage)
		if soll != "" {
			e.Woher += " · " + soll
		}
		return e
	case soll != "":
		w := "eine Grenze nennt der Befund nicht" // a sheet may print „% Soll“ without the predicted value
		if m.Soll != nil {
			w = "Sollwert laut Befund: " + mitEinheit(*m.Soll, m.Einheit) + "; " + w
		}
		return Einordnung{Lage: LageSoll, Status: soll, Woher: w}
	}
	if f, ok := sollFormeln[m.Name]; ok && p != nil && p.Geburtsjahr > 0 && falten(f.Einheit) == falten(m.Einheit) {
		if t, err := time.Parse("2006-01-02", m.Datum); err == nil {
			if lo, hi, ok := f.Bereich(*p, float64(t.Year()-p.Geburtsjahr)); ok && (lo != nil || hi != nil) {
				l := lage(m.Wert, lo, hi)
				return Einordnung{Lage: l, Status: lageText(l), Woher: fmt.Sprintf("Bereich berechnet aus Alter, Geschlecht und Größe nach %s, nicht vom Befund: %s", f.Name, bereichText(lo, hi, m.Einheit))}
			}
		}
	}
	return Einordnung{Lage: LageKein, Status: "kein Bereich angegeben"}
}

// sollText is „84 % des Sollwerts“ from the report's percentage, or its
// predicted value (a division, no judgement); "" when it prints neither.
func sollText(m Messwert) string {
	switch {
	case m.SollProzent != nil:
		return fmt.Sprintf("%s %% des Sollwerts", zahlText(math.Round(*m.SollProzent)))
	case m.Soll != nil && *m.Soll > 0:
		return fmt.Sprintf("%s %% des Sollwerts", zahlText(math.Round(100*m.Wert / *m.Soll)))
	}
	return ""
}

func lage(v float64, lo, hi *float64) Lage {
	switch {
	case lo != nil && v < *lo:
		return LageUnter
	case hi != nil && v > *hi:
		return LageUeber
	}
	return LageIn
}

func lageText(l Lage) string {
	switch l {
	case LageUeber:
		return "über dem Bereich"
	case LageUnter:
		return "unter dem Bereich"
	}
	return "im Normalbereich"
}

// bereichText is a range as the page says it: „12–16 g/dl“, „unter 5 mg/l“, „über 60 ml/min“.
func bereichText(lo, hi *float64, einheit string) string {
	switch {
	case lo != nil && hi != nil:
		return strings.TrimSpace(zahlText(*lo) + "–" + zahlText(*hi) + " " + einheit)
	case lo != nil:
		return "über " + mitEinheit(*lo, einheit)
	case hi != nil:
		return "unter " + mitEinheit(*hi, einheit)
	}
	return ""
}

func mitEinheit(v float64, einheit string) string {
	return strings.TrimSpace(zahlText(v) + " " + einheit)
}

var monate = []string{"Jan.", "Feb.", "März", "Apr.", "Mai", "Juni", "Juli", "Aug.", "Sept.", "Okt.", "Nov.", "Dez."}

// monatJahr is a day as „Dez. 2023“.
func monatJahr(d string) string {
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		return d
	}
	return monate[t.Month()-1] + " " + fmt.Sprint(t.Year())
}

// wertMitDatum is the latest value as the row leads with it: „5,29 l · Dez. 2023“.
func wertMitDatum(m Messwert) string {
	return mitEinheit(m.Wert, m.Einheit) + " · " + monatJahr(m.Datum)
}

// kaumSchwelle is the relative change below which a value is „kaum
// verändert“: 5 % of the value before. Not every decimal is a change; a
// lung-function value repeated the same day differs by about this much.
const kaumSchwelle = 0.05

// vorher is the value before the latest: the last one of an earlier day
// (several reports of one day are one day). nil when there is none.
func vorher(ms []Messwert) *Messwert {
	if len(ms) < 2 {
		return nil
	}
	last := ms[len(ms)-1]
	for i := len(ms) - 2; i >= 0; i-- {
		if ms[i].Datum < last.Datum {
			return &ms[i]
		}
	}
	return nil
}

// richtung is the change from a to b in words, and its relative size.
func richtung(a, b float64) (string, float64) {
	if a == 0 {
		switch {
		case b > 0:
			return "gestiegen", math.Inf(1)
		case b < 0:
			return "gesunken", math.Inf(1)
		}
		return "kaum verändert", 0
	}
	rel := (b - a) / math.Abs(a)
	switch {
	case math.Abs(rel) < kaumSchwelle:
		return "kaum verändert", rel
	case rel > 0:
		return "gestiegen", rel
	}
	return "gesunken", rel
}

// aenderung is the change against the value before, in words: „vorher
// 5,41 l (2021) – kaum verändert“.
func aenderung(ms []Messwert) string {
	if len(ms) == 0 {
		return ""
	}
	v := vorher(ms)
	if v == nil {
		return "noch kein früherer Wert"
	}
	last := ms[len(ms)-1]
	wann := v.Datum[:min(4, len(v.Datum))]
	if len(v.Datum) >= 4 && len(last.Datum) >= 4 && v.Datum[:4] == last.Datum[:4] {
		wann = monatJahr(v.Datum)
	}
	vor := fmt.Sprintf("vorher %s (%s)", mitEinheit(v.Wert, v.Einheit), wann)
	if falten(v.Einheit) != falten(last.Einheit) {
		return vor + ", in einer anderen Einheit – nicht vergleichbar"
	}
	r, _ := richtung(v.Wert, last.Wert)
	return vor + " – " + r
}

// --- the chart: only from three days on, its own time axis with the years, the report's band

const diaW, diaH, diaUnten = 320.0, 64.0, 14.0

// diagramm draws one value's course: the points on the row's own time axis
// (first to last day, the years labelled), the report's range as a band
// behind each point, and a value axis that keeps a small change small.
// "" for fewer than three days: two points are a change, said in words.
func diagramm(ms []Messwert, public string) string {
	tage := map[string]bool{}
	for _, m := range ms {
		tage[m.Datum] = true
	}
	if len(tage) < 3 {
		return ""
	}
	t0, _ := time.Parse("2006-01-02", ms[0].Datum)
	t1, _ := time.Parse("2006-01-02", ms[len(ms)-1].Datum)
	if !t1.After(t0) {
		return ""
	}
	xs := func(d string) float64 {
		t, err := time.Parse("2006-01-02", d)
		if err != nil {
			return 0
		}
		return 8 + (diaW-16)*t.Sub(t0).Hours()/t1.Sub(t0).Hours()
	}
	// the value axis: the points and the bands, at least ±10 % around the mean
	lo, hi, sum := ms[0].Wert, ms[0].Wert, 0.0
	for _, m := range ms {
		sum += m.Wert
		for _, v := range []*float64{&m.Wert, m.RefMin, m.RefMax, m.LLN} {
			if v != nil {
				lo, hi = math.Min(lo, *v), math.Max(hi, *v)
			}
		}
	}
	mitte := sum / float64(len(ms))
	if need := 0.2 * math.Abs(mitte); hi-lo < need {
		c := (hi + lo) / 2
		lo, hi = c-need/2, c+need/2
	}
	if hi == lo {
		lo, hi = lo-1, hi+1
	}
	pad := (hi - lo) * 0.12
	lo, hi = lo-pad, hi+pad
	plotH := diaH - diaUnten
	ys := func(v float64) float64 { return 3 + (plotH-6)*(hi-v)/(hi-lo) }
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class=spark viewBox="0 0 %g %g" width="%g" height="%g" role="img" aria-label="%d Werte von %d bis %d">`, diaW, diaH, diaW, diaH, len(ms), t0.Year(), t1.Year())
	// the band, per point: laboratories differ
	for i, m := range ms {
		low := m.RefMin
		if low == nil && m.RefMax == nil {
			low = m.LLN
		}
		if low == nil && m.RefMax == nil {
			continue
		}
		x0 := xs(m.Datum) - 5
		x1 := x0 + 10
		if i+1 < len(ms) && ms[i+1].Datum > m.Datum {
			x1 = xs(ms[i+1].Datum)
		}
		top, bottom := ys(hi), ys(lo)
		if m.RefMax != nil {
			top = ys(*m.RefMax)
		}
		if low != nil {
			bottom = ys(*low)
		}
		fmt.Fprintf(&b, `<rect class=ref x="%.1f" y="%.1f" width="%.1f" height="%.1f"/>`, x0, top, math.Max(x1-x0, 1), math.Max(bottom-top, 0))
	}
	// the years: a tick and a label at each new year, at most six labels
	jahre := t1.Year() - t0.Year()
	schritt := 1
	for jahre/schritt > 5 {
		schritt++
	}
	fmt.Fprintf(&b, `<line class=achse x1="0" y1="%.1f" x2="%g" y2="%.1f"/>`, plotH, diaW, plotH)
	beschriftet := false
	for y := t0.Year(); y <= t1.Year(); y++ {
		if (y-t0.Year())%schritt != 0 {
			continue
		}
		x := xs(fmt.Sprintf("%d-01-01", y))
		if y == t0.Year() {
			x = xs(ms[0].Datum)
		}
		anker := "middle"
		switch {
		case x < 16:
			anker = "start"
		case x > diaW-16:
			anker = "end"
		}
		fmt.Fprintf(&b, `<text class=jahr x="%.1f" y="%g" text-anchor="%s">%d</text>`, x, diaH-2, anker, y)
		beschriftet = true
	}
	if !beschriftet || (t1.Year()-t0.Year())%schritt != 0 {
		fmt.Fprintf(&b, `<text class=jahr x="%g" y="%g" text-anchor="end">%d</text>`, diaW, diaH-2, t1.Year())
	}
	var pts []string
	for _, m := range ms {
		pts = append(pts, fmt.Sprintf("%.1f,%.1f", xs(m.Datum), ys(m.Wert)))
	}
	fmt.Fprintf(&b, `<polyline points="%s"/>`, strings.Join(pts, " "))
	for _, m := range ms {
		cls := "punkt"
		if l := einordnen(m, nil).Lage; l == LageUeber || l == LageUnter {
			cls += " ausser"
		}
		title := fmt.Sprintf("%s: %s", datumText(m.Datum), mitEinheit(m.Wert, m.Einheit))
		if m.RefMin != nil || m.RefMax != nil {
			title += " (Bereich laut Befund " + bereichText(m.RefMin, m.RefMax, m.Einheit) + ")"
		}
		if m.Quelle == "bild" {
			title += " · aus dem Bild gelesen"
		}
		if m.Pruefen {
			title += " · prüfen"
		}
		kreis := fmt.Sprintf(`<circle class="%s" cx="%.1f" cy="%.1f" r="3.5"><title>%s</title></circle>`, cls, xs(m.Datum), ys(m.Wert), html.EscapeString(title))
		if href := dokURL(m.Dokument, public); href != "" {
			kreis = `<a href="` + href + `">` + kreis + `</a>`
		}
		b.WriteString(kreis)
	}
	b.WriteString(`</svg>`)
	return b.String()
}
