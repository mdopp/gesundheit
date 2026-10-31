package main

import (
	"fmt"
	"html"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

// „Für den Arzt“, short (#13, owner 2026-10-31: „bitte fass es kürzer, z. B.
// als Werte zwischen zwei Tests … das Gesamtbild geht dann verloren, weil
// sie sich in Wirklichkeit gegenseitig beeinflussen“). The values are
// grouped by examination (lung function, spiroergometry, blood count,
// thyroid …), and each group compares its two latest examinations: a few
// values, the biggest moves first, and one or two neutral questions about
// the whole. Values of a group examined once go to a short „Außerdem“. The
// long list of #10 stays one link away („alle Einzelwerte“). Nothing here
// concludes: the words only compare numbers, the questions only ask.

// arztProGruppe caps the values a block compares: a printed page for all.
const arztProGruppe = 5

// arztAusserdem caps the lines under „Außerdem“.
const arztAusserdem = 6

// Untersuchung is a kind of examination the short page groups by.
type Untersuchung struct {
	Name    string
	Gruppen []string // the dictionary's groups whose values it holds
	Titel   []string // words of a document title that name it, folded
	Leit    []string // its leading values, first among equal moves
	// Dokument: the document says what it is, and each of its values belongs
	// to it (a spiroergometry's resting SpO2 is no lung-function value)
	Dokument bool
}

// untersuchungen are the kinds, the more specific first (a spiroergometry's
// title would otherwise read as a spirometry's).
var untersuchungen = []Untersuchung{
	{Name: "Belastungstest (Spiroergometrie)", Gruppen: []string{"Belastung"}, Titel: []string{"spiroergometrie", "ergospirometrie", "spiroergo", "ergometrie", "belastungstest", "belastungs-ekg"},
		Leit: []string{"Maximale Sauerstoffaufnahme", "Maximale Belastung", "Maximale Herzfrequenz", "Sauerstoffpuls"}, Dokument: true},
	{Name: "Lungenfunktion", Gruppen: []string{"Lunge"}, Titel: []string{"spirometrie", "lungenfunktion", "fvc-messung", "fluss-volumen", "bodyplethysmographie"},
		Leit: []string{"FEV1", "FVC", "FEV1/FVC", "Peak-Flow"}, Dokument: true},
	{Name: "Blutdruck und Puls", Gruppen: []string{"Kreislauf"}, Titel: []string{"blutdruck", "langzeit-rr", "rr-messung"},
		Leit: []string{"Blutdruck systolisch", "Blutdruck diastolisch", "Puls"}, Dokument: true},
	{Name: "Blutbild", Gruppen: []string{"Blutbild"}, Titel: []string{"blutbild"}, Leit: []string{"Hämoglobin", "Leukozyten", "Thrombozyten", "Erythrozyten", "Hämatokrit"}},
	{Name: "Entzündungswerte", Gruppen: []string{"Entzündung"}, Leit: []string{"CRP", "BSG"}},
	{Name: "Infektionen (Serologie)", Gruppen: []string{"Infektionen"}, Titel: []string{"hepatitis", "serologie", "hiv"}},
	{Name: "Stoffwechsel und Blutfette", Gruppen: []string{"Stoffwechsel"}, Leit: []string{"Glukose", "HbA1c", "Cholesterin", "LDL", "HDL"}},
	{Name: "Niere", Gruppen: []string{"Niere"}, Leit: []string{"Kreatinin", "GFR"}},
	{Name: "Leber", Gruppen: []string{"Leber"}, Leit: []string{"GOT", "GPT", "Gamma-GT"}},
	{Name: "Schilddrüse", Gruppen: []string{"Schilddrüse"}, Titel: []string{"schilddruese", "tsh"}, Leit: []string{"TSH", "fT4", "fT3"}},
	{Name: "Gerinnung", Gruppen: []string{"Gerinnung"}, Leit: []string{"Quick", "INR"}},
	{Name: "Vitamine", Gruppen: []string{"Vitamine"}},
	{Name: "Salze", Gruppen: []string{"Salze"}},
	{Name: "Körpermaße", Gruppen: []string{"Körper"}, Leit: []string{"Gewicht", "BMI"}},
}

// urteil are words the doctor's page never says (#13 extends the
// explanations' guard, erklaerung.go): an assessment, a direction that
// judges, an alarm, a therapy. A generated question that has one is
// replaced by a plain one.
var urteil = regexp.MustCompile(`(?i)\bsollte|\bsolltest|empfehl|\brat(e|en)\b|gefährlich|bedenklich|\bkrank|erhöht|erniedrigt|zu hoch|zu niedrig|normal|\bgesund\b|diagnos|behandl|auffällig|besorg|verschlechter|verbesser|schlechter|\bbesser|alarm|kritisch|harmlos|therapie|ursache`)

// ohneUrteil is the question, or ersatz when it judges.
func ohneUrteil(frage, ersatz string) string {
	if urteil.MatchString(frage) {
		return ersatz
	}
	return frage
}

// Vergleich is one value between the two examinations: the value of each
// day (the middle of the day's values, see tageswert), how many the day had,
// the change in words.
type Vergleich struct {
	Name             string
	A, B             Messwert // before, after
	AAnzahl, BAnzahl int
	AMin, AMax       float64
	BMin, BMax       float64
	Rel              float64
	Richtung         string
	Deutlich         bool   // more than deutlichSchwelle
	Gekreuzt         bool   // across the range its report prints
	Vorher, Nachher  string // where it stood against that range
	Leit             int
}

// Block is one examination kind: its two latest days compared.
type Block struct {
	Art      string
	Neu, Alt string // the two days
	Zeilen   []Vergleich
	Weitere  int // values compared but not shown
	// NurNeu: values of the later day only that stand outside the range their
	// report prints; the long list of #10 would have asked about them
	NurNeu            []string
	NichtVergleichbar []string // in different units on the two days
	Fragen            []string
}

// Titel is „Lungenfunktion: Dez. 2023 gegenüber Apr. 2021“.
func (b Block) Titel() string {
	n, a := monatJahr(b.Neu), monatJahr(b.Alt)
	if n == a {
		n, a = datumText(b.Neu), datumText(b.Alt)
	}
	return b.Art + ": " + n + " gegenüber " + a
}

// Kurzfassung is the short page.
type Kurzfassung struct {
	Bloecke   []Block
	Ausserdem []string
	Mehr      int // „Außerdem“ lines left out
	Unsicher  int // values read unsure, for the person's own note
	Vermutet  int // values whose name or unit is assumed, to confirm (#15)
	// Gesamtbild: per body system a rule of the Gesamtbild fits, its first
	// question (#17); at most four lines, the sheet stays one sheet
	Gesamtbild []string
}

// kanon is a value's name as compared across documents: the dictionary's,
// so a first reader's „SYS“ and a later one's „Blutdruck systolisch“ meet.
func kanon(m Messwert) string {
	if a := finde(m.Name); a != nil {
		return a.Name
	}
	return m.Name
}

func gruppeVon(m Messwert) string {
	if a := finde(m.Name); a != nil {
		return a.Gruppe
	}
	return m.Gruppe
}

func untersuchungDerGruppe(g string) int {
	for i, u := range untersuchungen {
		for _, x := range u.Gruppen {
			if x == g {
				return i
			}
		}
	}
	return -1
}

// dokumentArten is what each document examined: its title's words, else
// most of its values; -1 when neither says.
func dokumentArten(werte []Messwert, rec *Record) map[int]int {
	zaehl := map[int]map[int]int{}
	for _, m := range werte {
		if zaehl[m.Dokument] == nil {
			zaehl[m.Dokument] = map[int]int{}
		}
		if u := untersuchungDerGruppe(gruppeVon(m)); u >= 0 {
			zaehl[m.Dokument][u]++
		}
	}
	out := map[int]int{}
	for doc, z := range zaehl {
		out[doc] = -1
		titel := ""
		if rec != nil {
			if e := rec.Gelesen[fmt.Sprint(doc)]; e != nil && e.Gelesen != nil {
				titel = falten(e.Gelesen.Titel)
			}
		}
		for i, u := range untersuchungen {
			for _, w := range u.Titel {
				if titel != "" && strings.Contains(titel, w) && out[doc] < 0 {
					out[doc] = i
				}
			}
		}
		if out[doc] >= 0 {
			continue
		}
		best := 0
		for i := range untersuchungen {
			if z[i] > best {
				out[doc], best = i, z[i]
			}
		}
	}
	return out
}

// untersuchungVon is the kind a value belongs to: a lab value by its own
// group (one sheet holds blood count, kidney and thyroid), any other by
// what its document examined; -1 when nothing says.
func untersuchungVon(m Messwert, dok map[int]int) int {
	g := untersuchungDerGruppe(gruppeVon(m))
	if g >= 0 && !untersuchungen[g].Dokument {
		return g
	}
	if d, ok := dok[m.Dokument]; ok && d >= 0 {
		return d
	}
	return g
}

// tageswert is one value for a day that has several (a lung-function day:
// several sheets, a value per attempt): the values read without doubt
// when there are any, and of them the middle one — a value that was
// measured, not a mean; a misread at either end does not move it.
func tageswert(ms []Messwert) (Messwert, int, float64, float64) {
	var sicher []Messwert
	for _, m := range ms {
		if !m.Pruefen {
			sicher = append(sicher, m)
		}
	}
	if len(sicher) > 0 {
		ms = sicher
	}
	s := append([]Messwert{}, ms...)
	sort.SliceStable(s, func(i, j int) bool { return s[i].Wert < s[j].Wert })
	return s[(len(s)-1)/2], len(s), s[0].Wert, s[len(s)-1].Wert
}

// mitEinheitIn are the values in one unit (folded).
func mitEinheitIn(ms []Messwert, e string) []Messwert {
	var out []Messwert
	for _, m := range ms {
		if falten(m.Einheit) == e {
			out = append(out, m)
		}
	}
	return out
}

// gemeinsameEinheit is a unit both days give the value in: the
// dictionary's first, else the first of the later day's; false when none.
func gemeinsameEinheit(name string, a, b []Messwert) (string, bool) {
	in := func(ms []Messwert, e string) bool { return len(mitEinheitIn(ms, e)) > 0 }
	if an := finde(name); an != nil && in(a, falten(an.Einheit)) && in(b, falten(an.Einheit)) {
		return falten(an.Einheit), true
	}
	for _, m := range b {
		if e := falten(m.Einheit); in(a, e) {
			return e, true
		}
	}
	return "", false
}

func leitRang(u Untersuchung, name string) int {
	for i, l := range u.Leit {
		if l == name {
			return i
		}
	}
	return 99
}

func richtungVon(rel float64) string {
	switch {
	case math.Abs(rel) < kaumSchwelle:
		return "kaum verändert"
	case rel > 0:
		return "gestiegen"
	}
	return "gesunken"
}

// arztKurz builds the short page from a person's values (their corrections
// applied) and the record (the documents' titles).
func arztKurz(v Verlauf, rec *Record, p *Profil) Kurzfassung {
	var k Kurzfassung
	dok := dokumentArten(v.Werte, rec)
	// kind → day → name → values
	tage := map[int]map[string]map[string][]Messwert{}
	lose := map[string][]Messwert{}
	for _, m := range v.Werte {
		if m.Pruefen {
			k.Unsicher++
		}
		if m.Vermutung != nil {
			k.Vermutet++
		}
		if m.Datum == "" {
			continue // without a day there is nothing to compare; the long list has it
		}
		u := untersuchungVon(m, dok)
		if u < 0 {
			lose[kanon(m)] = append(lose[kanon(m)], m)
			continue
		}
		if tage[u] == nil {
			tage[u] = map[string]map[string][]Messwert{}
		}
		if tage[u][m.Datum] == nil {
			tage[u][m.Datum] = map[string][]Messwert{}
		}
		tage[u][m.Datum][kanon(m)] = append(tage[u][m.Datum][kanon(m)], m)
	}
	type zeile struct {
		datum, text string
	}
	var aus []zeile
	for u, proTag := range tage {
		var daten []string
		for d := range proTag {
			daten = append(daten, d)
		}
		sort.Sort(sort.Reverse(sort.StringSlice(daten)))
		art := untersuchungen[u]
		if len(daten) < 2 {
			aus = append(aus, zeile{daten[0], einmalZeile(art, daten[0], proTag[daten[0]], p)})
			continue
		}
		b := Block{Art: art.Name, Neu: daten[0], Alt: daten[1]}
		neu, alt := proTag[daten[0]], proTag[daten[1]]
		var namen []string
		for n := range neu {
			if _, ok := alt[n]; ok {
				namen = append(namen, n)
			}
		}
		sort.Strings(namen)
		var nurNeu []string
		for n, ms := range neu {
			if _, ok := alt[n]; ok {
				continue
			}
			m, anz, lo, hi := tageswert(ms)
			if e := einordnen(m, p); e.Lage == LageUeber || e.Lage == LageUnter {
				nurNeu = append(nurNeu, n+": "+wertText(m, anz, lo, hi)+" – nur in dieser Untersuchung, laut Befund "+e.Status)
			}
		}
		sort.Strings(nurNeu)
		if len(nurNeu) > 3 {
			nurNeu = nurNeu[:3]
		}
		b.NurNeu = nurNeu
		var alle []Vergleich
		for _, n := range namen {
			e, ok := gemeinsameEinheit(n, alt[n], neu[n])
			if !ok {
				a, _, _, _ := tageswert(alt[n])
				nb, _, _, _ := tageswert(neu[n])
				b.NichtVergleichbar = append(b.NichtVergleichbar, fmt.Sprintf("%s: %s (%s) und %s (%s)", n, mitEinheit(a.Wert, a.Einheit), monatJahr(a.Datum), mitEinheit(nb.Wert, nb.Einheit), monatJahr(nb.Datum)))
				continue
			}
			z := Vergleich{Name: n, Leit: leitRang(art, n)}
			z.A, z.AAnzahl, z.AMin, z.AMax = tageswert(mitEinheitIn(alt[n], e))
			z.B, z.BAnzahl, z.BMin, z.BMax = tageswert(mitEinheitIn(neu[n], e))
			z.Richtung, z.Rel = richtung(z.A.Wert, z.B.Wert)
			z.Deutlich = math.Abs(z.Rel) > deutlichSchwelle
			ea, eb := einordnen(z.A, p), einordnen(z.B, p)
			z.Gekreuzt = (ea.Lage == LageIn) != (eb.Lage == LageIn) && ea.Lage != LageKein && ea.Lage != LageSoll && eb.Lage != LageKein && eb.Lage != LageSoll
			z.Vorher, z.Nachher = kurzStatus(ea), kurzStatus(eb)
			alle = append(alle, z)
		}
		sort.SliceStable(alle, func(i, j int) bool {
			x, y := alle[i], alle[j]
			if x.Gekreuzt != y.Gekreuzt {
				return x.Gekreuzt
			}
			if x.Deutlich != y.Deutlich {
				return x.Deutlich
			}
			if x.Leit != y.Leit {
				return x.Leit < y.Leit
			}
			return math.Abs(x.Rel) > math.Abs(y.Rel)
		})
		if len(alle) > arztProGruppe {
			b.Weitere = len(alle) - arztProGruppe
			alle = alle[:arztProGruppe]
		}
		b.Zeilen = alle
		b.Fragen = blockFragen(b)
		k.Bloecke = append(k.Bloecke, b)
	}
	sort.SliceStable(k.Bloecke, func(i, j int) bool {
		if k.Bloecke[i].Neu != k.Bloecke[j].Neu {
			return k.Bloecke[i].Neu > k.Bloecke[j].Neu
		}
		return k.Bloecke[i].Art < k.Bloecke[j].Art
	})
	// a value no examination holds: its latest day
	for n, ms := range lose {
		letzter := ""
		for _, m := range ms {
			letzter = max(letzter, m.Datum)
		}
		var amTag []Messwert
		for _, m := range ms {
			if m.Datum == letzter {
				amTag = append(amTag, m)
			}
		}
		m, anz, lo, hi := tageswert(amTag)
		aus = append(aus, zeile{letzter, n + ": " + wertText(m, anz, lo, hi) + " (" + monatJahr(letzter) + ")"})
	}
	sort.SliceStable(aus, func(i, j int) bool {
		if aus[i].datum != aus[j].datum {
			return aus[i].datum > aus[j].datum
		}
		return aus[i].text < aus[j].text
	})
	for i, z := range aus {
		if i >= arztAusserdem {
			k.Mehr = len(aus) - arztAusserdem
			break
		}
		k.Ausserdem = append(k.Ausserdem, z.text)
	}
	return k
}

// wertText is a day's value as the short page says it: „3,04 l (2 Werte am Tag: 3,04–3,22)“.
func wertText(m Messwert, anzahl int, lo, hi float64) string {
	t := mitEinheit(m.Wert, m.Einheit)
	if anzahl > 1 && lo != hi {
		t += fmt.Sprintf(" (%d Werte am Tag: %s–%s)", anzahl, zahlText(lo), zahlText(hi))
	}
	if m.Pruefen {
		t += " (unsicher gelesen)"
	}
	if m.Korrektur == "korrigiert" {
		t += " (korrigiert)"
	}
	if v := vermutetKurz(m); v != "" {
		t += " (" + v + ")"
	}
	return t
}

// einmalZeile is a kind examined once: its day and a few of its values.
func einmalZeile(u Untersuchung, datum string, namen map[string][]Messwert, p *Profil) string {
	type eintrag struct {
		name   string
		text   string
		ausser bool
		leit   int
	}
	var es []eintrag
	for n, ms := range namen {
		m, anz, lo, hi := tageswert(ms)
		l := einordnen(m, p).Lage
		es = append(es, eintrag{n, n + " " + wertText(m, anz, lo, hi), l == LageUeber || l == LageUnter, leitRang(u, n)})
	}
	sort.SliceStable(es, func(i, j int) bool {
		if es[i].ausser != es[j].ausser {
			return es[i].ausser
		}
		if es[i].leit != es[j].leit {
			return es[i].leit < es[j].leit
		}
		return es[i].name < es[j].name
	})
	var teile []string
	for i, e := range es {
		if i == 3 {
			break
		}
		t := e.text
		if e.ausser {
			t += " – laut Befund außerhalb des Bereichs"
		}
		teile = append(teile, t)
	}
	t := u.Name + ", " + monatJahr(datum) + ": " + strings.Join(teile, ", ")
	if len(es) > 3 {
		t += fmt.Sprintf(" und %d weitere Werte", len(es)-3)
	}
	return t + " – nur diese eine Untersuchung."
}

// vergleichText is one row: „FEV1: 3,9 l → 3,04 l – gesunken um 22 % · 91 → 71 % des Sollwerts“.
func vergleichText(z Vergleich) string {
	t := fmt.Sprintf("%s: %s → %s – %s", z.Name, mitEinheit(z.A.Wert, z.A.Einheit), mitEinheit(z.B.Wert, z.B.Einheit), z.Richtung)
	if z.Richtung != "kaum verändert" && !math.IsInf(z.Rel, 0) {
		t += fmt.Sprintf(" um %s %%", zahlText(math.Round(math.Abs(z.Rel)*100)))
	}
	if z.A.SollProzent != nil && z.B.SollProzent != nil {
		t += fmt.Sprintf(" · %s → %s %% des Sollwerts", zahlText(math.Round(*z.A.SollProzent)), zahlText(math.Round(*z.B.SollProzent)))
	}
	if z.Gekreuzt {
		t += "; vorher " + z.Vorher + ", jetzt " + z.Nachher
	}
	var dazu []string
	if z.AAnzahl > 1 && z.AMin != z.AMax {
		dazu = append(dazu, fmt.Sprintf("vorher %d Werte am Tag: %s–%s", z.AAnzahl, zahlText(z.AMin), zahlText(z.AMax)))
	}
	if z.BAnzahl > 1 && z.BMin != z.BMax {
		dazu = append(dazu, fmt.Sprintf("%d Werte am Tag: %s–%s", z.BAnzahl, zahlText(z.BMin), zahlText(z.BMax)))
	}
	if z.A.Pruefen || z.B.Pruefen {
		dazu = append(dazu, "unsicher gelesen")
	}
	if z.A.Korrektur == "korrigiert" || z.B.Korrektur == "korrigiert" {
		dazu = append(dazu, "korrigiert")
	}
	for _, m := range []Messwert{z.A, z.B} {
		if v := vermutetKurz(m); v != "" {
			dazu = append(dazu, v)
			break
		}
	}
	if len(dazu) > 0 {
		t += " (" + strings.Join(dazu, "; ") + ")"
	}
	return t
}

// blockFragen are one or two questions on the whole of a block: what moved
// together, what moved apart, what left its range. They ask; they never say
// what it means.
func blockFragen(b Block) []string {
	allgemein := "Was sagt der Vergleich der beiden Untersuchungen insgesamt?"
	var bewegt []Vergleich
	for _, z := range b.Zeilen {
		if z.Deutlich || z.Gekreuzt {
			bewegt = append(bewegt, z)
		}
	}
	var out []string
	switch {
	case len(b.Zeilen) == 0:
		out = append(out, "Lassen sich die beiden Untersuchungen miteinander vergleichen, und was zeigen sie zusammen?")
	case len(bewegt) == 0:
		out = append(out, "Die Werte haben sich zwischen den beiden Untersuchungen wenig verändert – was sagt dieser Vergleich insgesamt?")
	case len(bewegt) == 1 && len(b.Zeilen) == 1:
		out = append(out, fmt.Sprintf("%s ist %s – was bedeutet das für das Gesamtbild?", bewegt[0].Name, bewegt[0].Richtung))
	case len(bewegt) == 1:
		out = append(out, fmt.Sprintf("%s ist %s, die übrigen Werte kaum – wie ist das im Zusammenhang zu sehen?", bewegt[0].Name, bewegt[0].Richtung))
	default:
		x, y := bewegt[0], bewegt[1]
		if x.Richtung == y.Richtung {
			out = append(out, fmt.Sprintf("%s und %s sind beide %s – was bedeutet das zusammen?", x.Name, y.Name, x.Richtung))
		} else {
			out = append(out, fmt.Sprintf("%s ist %s, %s %s – wie passt das zusammen?", x.Name, x.Richtung, y.Name, y.Richtung))
		}
	}
	for _, z := range b.Zeilen {
		if !z.Gekreuzt {
			continue
		}
		q := fmt.Sprintf("%s liegt jetzt %s laut Befund", z.Name, z.Nachher)
		if z.Vorher != "" {
			q += ", vorher " + z.Vorher
		}
		out = append(out, q+" – wie ist das zusammen mit den anderen Werten zu sehen?")
		break
	}
	for i := range out {
		out[i] = ohneUrteil(out[i], allgemein)
	}
	if len(out) > 2 {
		out = out[:2]
	}
	return out
}

// arztKurzHTML is the short page: printable, one sheet for a household's records.
func arztKurzHTML(k Kurzfassung, p *Person, s sitzung, jetzt time.Time) string {
	var b strings.Builder
	alle := "/arzt?alle=1"
	befunde := "/befunde"
	if p.Key != s.Login {
		alle += "&person=" + p.Key
		befunde += "?person=" + p.Key
	}
	fmt.Fprintf(&b, `<h2>Für den Arzt <span class=k>· %s · Stand %s</span></h2>`, html.EscapeString(p.Anzeige), datumText(jetzt.Format("2006-01-02")))
	b.WriteString(`<p class=k>Je Untersuchung die beiden letzten im Vergleich, so wie die Werte in deinen Befunden stehen. Die Seite bewertet nichts; die Fragen sind Vorschläge für das Gespräch.</p>`)
	fmt.Fprintf(&b, `<p class=nicht-drucken><button type=button onclick="window.print()">Drucken</button> · <a href="%s">alle Einzelwerte</a></p>`, html.EscapeString(alle))
	if k.Unsicher > 0 {
		werte := "Werte sind"
		if k.Unsicher == 1 {
			werte = "Wert ist"
		}
		fmt.Fprintf(&b, `<p class="hinweis nicht-drucken">%d %s unsicher gelesen. Unter <a href="%s">Befunde</a> kannst du sie mit dem Ausschnitt der Seite prüfen und korrigieren; was du korrigierst, steht dann auch hier.</p>`, k.Unsicher, werte, html.EscapeString(befunde))
	}
	b.WriteString(vermutetHinweis(k.Vermutet, befunde))
	if len(k.Bloecke) == 0 && len(k.Ausserdem) == 0 {
		b.WriteString(`<p>Noch keine Werte mit Datum, die sich vergleichen lassen. Eigene Fragen kannst du unten aufschreiben.</p>`)
	}
	for _, bl := range k.Bloecke {
		fmt.Fprintf(&b, `<section class=block><h3>%s</h3>`, html.EscapeString(bl.Titel()))
		if len(bl.Zeilen)+len(bl.NurNeu) > 0 {
			b.WriteString(`<ul class=vergleich>`)
			for _, z := range bl.Zeilen {
				b.WriteString(`<li>` + html.EscapeString(vergleichText(z)) + `</li>`)
			}
			for _, z := range bl.NurNeu {
				b.WriteString(`<li>` + html.EscapeString(z) + `</li>`)
			}
			b.WriteString(`</ul>`)
		}
		var dazu []string
		if bl.Weitere > 0 {
			dazu = append(dazu, fmt.Sprintf("und %d weitere Werte im Vergleich", bl.Weitere))
		}
		if len(bl.NichtVergleichbar) > 0 {
			dazu = append(dazu, "in verschiedenen Einheiten, nicht verglichen: "+strings.Join(bl.NichtVergleichbar, "; "))
		}
		if len(dazu) > 0 {
			b.WriteString(`<p class=k>` + html.EscapeString(strings.Join(dazu, " · ")) + `</p>`)
		}
		for _, f := range bl.Fragen {
			b.WriteString(`<p class=frage-text>` + html.EscapeString(f) + `</p>`)
		}
		b.WriteString(`<div class=antwort></div></section>`)
	}
	if len(k.Ausserdem) > 0 {
		b.WriteString(`<section class=block><h3>Außerdem</h3><ul class=vergleich>`)
		for _, a := range k.Ausserdem {
			b.WriteString(`<li>` + html.EscapeString(a) + `</li>`)
		}
		b.WriteString(`</ul>`)
		if k.Mehr > 0 {
			fmt.Fprintf(&b, `<p class=k>und %d weitere unter „alle Einzelwerte“</p>`, k.Mehr)
		}
		b.WriteString(`</section>`)
	}
	if len(k.Gesamtbild) > 0 {
		gesamt := "/gesamtbild"
		if p.Key != s.Login {
			gesamt += "?person=" + p.Key
		}
		b.WriteString(`<section class=block><h3>Aus dem Gesamtbild</h3><ul class=vergleich>`)
		for _, f := range k.Gesamtbild {
			b.WriteString(`<li class=gesamtbild-frage>` + html.EscapeString(f) + `</li>`)
		}
		fmt.Fprintf(&b, `</ul><p class="k nicht-drucken">Was solche Muster allgemein bedeuten können, mit Quellen: <a href="%s">Gesamtbild</a></p><div class=antwort></div></section>`, html.EscapeString(gesamt))
	}
	fmt.Fprintf(&b, `<p class=k>Jeder einzelne Wert mit seinem Befund: <a href="%s">alle Einzelwerte</a>.</p>`, html.EscapeString(alle))
	b.WriteString(`<section class=block><h3>Eigene Fragen</h3><div class=antwort></div><div class=antwort></div></section>`)
	return b.String()
}
