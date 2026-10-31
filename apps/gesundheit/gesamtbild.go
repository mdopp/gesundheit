package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// „Gesamtbild“ (#17, owner 2026-10-31: „Und wie kriege ich jetzt eine
// Einschätzung über meinen Gesamtzustand? … zeitlicher Verlauf und
// gegenseitige Beeinflussung der Werte“). Per body system: the course in
// words, what moves together, what such patterns can mean in general, and
// questions for the doctor. The meanings come only from the fixed, sourced
// rule table gesamtbild_regeln.json — no free model text, nothing about the
// person, no diagnosis, no advice. A pattern is built only from values read
// without doubt, confirmed or corrected: a misread never becomes a pattern.

//go:embed gesamtbild_regeln.json
var regelnJSON []byte

// GBSystem is a body system: the dictionary's groups it holds, its leading
// values, the sets of values that move together, a general question.
type GBSystem struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Gruppen   []string   `json:"gruppen"`
	Leit      []string   `json:"leit"`
	Gemeinsam [][]string `json:"gemeinsam"`
	Frage     string     `json:"frage"`
}

// Bedingung is one condition of a rule (the table's head says what each field means).
type Bedingung struct {
	Wert                string   `json:"wert,omitempty"`
	EinerVon            []string `json:"einer_von,omitempty"`
	Lage                string   `json:"lage,omitempty"`
	Unter               *float64 `json:"unter,omitempty"`
	Ab                  *float64 `json:"ab,omitempty"`
	Einheiten           []string `json:"einheiten,omitempty"`
	SollUnter           *float64 `json:"soll_unter,omitempty"`
	Minus               string   `json:"minus,omitempty"`
	WennGemessen        bool     `json:"wenn_gemessen,omitempty"`
	Richtung            string   `json:"richtung,omitempty"`
	UmAb                *float64 `json:"um_ab,omitempty"`
	UmUnter             *float64 `json:"um_unter,omitempty"`
	GleichaufMit        string   `json:"gleichauf_mit,omitempty"`
	StaerkerGesunkenAls string   `json:"staerker_gesunken_als,omitempty"`
	Abstand             *float64 `json:"abstand,omitempty"`
}

// Fuer is the profile a rule needs.
type Fuer struct {
	Geschlecht string `json:"geschlecht,omitempty"`
	AlterAb    int    `json:"alter_ab,omitempty"`
}

// AlterText is a sentence a rule adds from an age on.
type AlterText struct {
	Ab   int    `json:"ab"`
	Text string `json:"text"`
}

// Regel is one row of the table.
type Regel struct {
	ID        string      `json:"id"`
	System    string      `json:"system"`
	Fuer      *Fuer       `json:"fuer,omitempty"`
	Wenn      []Bedingung `json:"wenn"`
	Oder      []Bedingung `json:"oder,omitempty"`
	Bedeutung string      `json:"bedeutung"`
	AbAlter   *AlterText  `json:"bedeutung_ab_alter,omitempty"`
	Frage     string      `json:"frage"`
	Quelle    string      `json:"quelle"`
}

// Plausibel is what a body can measure at all for one value, in its units.
type Plausibel struct {
	Wert      string   `json:"wert"`
	Einheiten []string `json:"einheiten"`
	Von       float64  `json:"von"`
	Bis       float64  `json:"bis"`
}

// Regelwerk is the table.
type Regelwerk struct {
	Plausibel []Plausibel `json:"plausibel"`
	Systeme   []GBSystem  `json:"systeme"`
	Regeln    []Regel     `json:"regeln"`
}

// persoenlich are words a general meaning never carries: it speaks of
// patterns, never to or of the person.
var persoenlich = regexp.MustCompile(`(?i)\b(du|dein\w*|dir|dich)\b`)

// regelwerk is the table as loaded; a rule that breaks the table's own
// rules is left out and named in regelFehler (the tests demand none).
var regelwerk, regelFehler = ladeRegeln(regelnJSON)

func ladeRegeln(b []byte) (Regelwerk, []string) {
	var r Regelwerk
	if err := json.Unmarshal(b, &r); err != nil {
		log.Printf("gesamtbild: Regeltabelle unlesbar: %v", err)
		return Regelwerk{}, []string{err.Error()}
	}
	var fehler []string
	var plaus []Plausibel
	for _, x := range r.Plausibel {
		if !bekannt(x.Wert) || len(x.Einheiten) == 0 || !(x.Von < x.Bis) {
			fehler = append(fehler, "plausibel "+x.Wert+": Wert aus dem Wörterbuch, Einheiten, von < bis")
			continue
		}
		plaus = append(plaus, x)
	}
	r.Plausibel = plaus
	systeme := map[string]bool{}
	var gute []GBSystem
	for _, s := range r.Systeme {
		f := pruefeSystem(s)
		if f == "" && systeme[s.ID] {
			f = "doppelt"
		}
		if f != "" {
			fehler = append(fehler, "System "+s.ID+": "+f)
			continue
		}
		systeme[s.ID] = true
		gute = append(gute, s)
	}
	r.Systeme = gute
	ids := map[string]bool{}
	var regeln []Regel
	for _, x := range r.Regeln {
		f := pruefeRegel(x, systeme)
		if f == "" && ids[x.ID] {
			f = "doppelt"
		}
		if f != "" {
			fehler = append(fehler, "Regel "+x.ID+": "+f)
			continue
		}
		ids[x.ID] = true
		regeln = append(regeln, x)
	}
	r.Regeln = regeln
	for _, f := range fehler {
		log.Printf("gesamtbild: ausgelassen: %s", f)
	}
	return r, fehler
}

// bekannt is a name the dictionary has under exactly this name.
func bekannt(name string) bool {
	a := finde(name)
	return a != nil && a.Name == name
}

func pruefeSystem(s GBSystem) string {
	switch {
	case s.ID == "" || s.Name == "" || len(s.Gruppen) == 0:
		return "id, Name oder Gruppen fehlen"
	case !strings.HasSuffix(s.Frage, "?") || urteil.MatchString(s.Frage):
		return "die Frage fragt nicht neutral"
	}
	for _, g := range s.Gemeinsam {
		for _, n := range g {
			if !bekannt(n) {
				return "„" + n + "“ steht nicht im Wörterbuch"
			}
		}
	}
	for _, n := range s.Leit {
		if !bekannt(n) {
			return "„" + n + "“ steht nicht im Wörterbuch"
		}
	}
	return ""
}

func (c Bedingung) namen() []string {
	if c.Wert != "" {
		return []string{c.Wert}
	}
	return c.EinerVon
}

// zustand is a condition judged on one examination day; else it compares the first and last day.
func (c Bedingung) zustand() bool {
	return c.Lage != "" || c.Unter != nil || c.Ab != nil || c.SollUnter != nil || c.Minus != ""
}

func (c Bedingung) verlauf() bool {
	return c.Richtung != "" || c.UmAb != nil || c.UmUnter != nil || c.GleichaufMit != "" || c.StaerkerGesunkenAls != ""
}

func (c Bedingung) partner() string {
	if c.GleichaufMit != "" {
		return c.GleichaufMit
	}
	return c.StaerkerGesunkenAls
}

func pruefeBedingung(c Bedingung, imOder bool) string {
	if (c.Wert == "") == (len(c.EinerVon) == 0) {
		return "genau eins von wert und einer_von"
	}
	for _, n := range append(append([]string{}, c.namen()...), c.Minus, c.partner()) {
		if n != "" && !bekannt(n) {
			return "„" + n + "“ steht nicht im Wörterbuch"
		}
	}
	if c.zustand() == c.verlauf() {
		return "eine Bedingung ist ein Zustand an einem Tag oder ein Verlauf, nicht beides oder keins"
	}
	if imOder && !c.zustand() {
		return "unter oder steht nur ein Zustand"
	}
	switch c.Lage {
	case "", "unter", "ueber", "im":
	default:
		return "lage ist unter, ueber oder im"
	}
	if (c.Unter != nil || c.Ab != nil) && len(c.Einheiten) == 0 {
		return "eine feste Grenze braucht ihre Einheiten"
	}
	if c.Minus != "" && c.Unter == nil && c.Ab == nil {
		return "minus braucht ab oder unter"
	}
	if c.Minus != "" && c.Wert == "" {
		return "minus braucht wert"
	}
	switch c.Richtung {
	case "", "gesunken", "gestiegen", "kaum verändert":
	default:
		return "richtung ist gesunken, gestiegen oder kaum verändert"
	}
	if c.verlauf() && c.Wert == "" {
		return "ein Verlauf braucht wert"
	}
	return ""
}

func pruefeRegel(x Regel, systeme map[string]bool) string {
	switch {
	case x.ID == "":
		return "ohne id"
	case !systeme[x.System]:
		return "unbekanntes System " + x.System
	case strings.TrimSpace(x.Bedeutung) == "" || strings.TrimSpace(x.Quelle) == "":
		return "Bedeutung und Quelle gehören zu jeder Regel"
	case !strings.HasSuffix(x.Frage, "?"):
		return "die Frage endet nicht mit einem Fragezeichen"
	case len(x.Wenn)+len(x.Oder) == 0:
		return "ohne Bedingung"
	}
	texte := []string{x.Bedeutung, x.Frage}
	if x.AbAlter != nil {
		if x.AbAlter.Ab <= 0 || strings.TrimSpace(x.AbAlter.Text) == "" {
			return "bedeutung_ab_alter braucht ab und text"
		}
		texte = append(texte, x.AbAlter.Text)
	}
	for _, t := range texte {
		if m := urteil.FindString(t); m != "" {
			return "wertet („" + m + "“)"
		}
	}
	for _, t := range []string{x.Bedeutung, func() string {
		if x.AbAlter != nil {
			return x.AbAlter.Text
		}
		return ""
	}()} {
		if m := persoenlich.FindString(t); m != "" {
			return "spricht über die Person („" + m + "“)"
		}
	}
	if x.Fuer != nil && x.Fuer.Geschlecht != "" && x.Fuer.Geschlecht != "m" && x.Fuer.Geschlecht != "w" {
		return "fuer.geschlecht ist m oder w"
	}
	for _, c := range x.Wenn {
		if f := pruefeBedingung(c, false); f != "" {
			return f
		}
	}
	for _, c := range x.Oder {
		if f := pruefeBedingung(c, true); f != "" {
			return f
		}
	}
	return ""
}

// --- the values a pattern may be built from

// sicherGelesen is a value read without doubt, confirmed or corrected: no
// „prüfen“, no unconfirmed assumption, a day of its own.
func sicherGelesen(m Messwert) bool {
	if m.Pruefen || m.Vermutung != nil || m.Datum == "" {
		return false
	}
	_, err := time.Parse("2006-01-02", m.Datum)
	return err == nil
}

// unmoeglich is a value no body can measure (gesamtbild_regeln.json
// „plausibel“): an unflagged misread, unless the person has spoken on it.
func unmoeglich(m Messwert) bool {
	if m.Korrektur != "" {
		return false
	}
	n := kanon(m)
	for _, x := range regelwerk.Plausibel {
		if x.Wert != n {
			continue
		}
		for _, e := range x.Einheiten {
			if einheitPasst(m.Einheit, e) {
				return m.Wert < x.Von || m.Wert > x.Bis
			}
		}
	}
	return false
}

// bestand is name → day → the values of that day, read without doubt.
type bestand map[string]map[string][]Messwert

func bestandVon(werte []Messwert) bestand {
	b := bestand{}
	for _, m := range werte {
		if !sicherGelesen(m) || unmoeglich(m) {
			continue
		}
		n := kanon(m)
		if b[n] == nil {
			b[n] = map[string][]Messwert{}
		}
		b[n][m.Datum] = append(b[n][m.Datum], m)
	}
	return b
}

func (b bestand) tage(name string) []string {
	var out []string
	for d := range b[name] {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// einheitPasst is a unit against a table's unit: „ml/min*“ a prefix, "" no unit.
func einheitPasst(u, muster string) bool {
	if p, ok := strings.CutSuffix(muster, "*"); ok {
		p = einheitNorm(p)
		return p != "" && strings.HasPrefix(einheitNorm(u), p)
	}
	return einheitNorm(u) == einheitNorm(muster)
}

// inEinheit is a value in one of the table's units: as it stands, or by
// the dictionary's exact factor into the first of them; false when neither.
func inEinheit(m Messwert, einheiten []string) (float64, bool) {
	for _, e := range einheiten {
		if einheitPasst(m.Einheit, e) {
			return m.Wert, true
		}
	}
	a := finde(m.Name)
	if a == nil || len(einheiten) == 0 || einheitNorm(m.Einheit) == "" || einheitNorm(a.Einheit) != einheitNorm(einheiten[0]) {
		return 0, false
	}
	for k, f := range a.Umrechnung {
		if einheitNorm(k) == einheitNorm(m.Einheit) {
			return m.Wert * f, true
		}
	}
	return 0, false
}

// sollProzentVon is the % of the predicted value the report prints, or its quotient.
func sollProzentVon(m Messwert) (float64, bool) {
	switch {
	case m.SollProzent != nil:
		return *m.SollProzent, true
	case m.Soll != nil && *m.Soll > 0:
		return 100 * m.Wert / *m.Soll, true
	}
	return 0, false
}

func lageWort(l Lage) string {
	switch l {
	case LageUnter:
		return "unter"
	case LageUeber:
		return "ueber"
	case LageIn:
		return "im"
	}
	return ""
}

// grenzen is whether v satisfies the condition's fixed limits.
func (c Bedingung) grenzen(v float64) bool {
	if c.Unter != nil && !(v < *c.Unter) {
		return false
	}
	if c.Ab != nil && !(v >= *c.Ab) {
		return false
	}
	return true
}

// tagesmitte is a day's middle value in the condition's units (see tageswert).
func tagesmitte(ms []Messwert, einheiten []string) (Messwert, float64, bool) {
	var in []Messwert
	for _, m := range ms {
		if v, ok := inEinheit(m, einheiten); ok {
			k := m
			k.Wert = v
			in = append(in, k)
		}
	}
	if len(in) == 0 {
		return Messwert{}, 0, false
	}
	m, _, _, _ := tageswert(in)
	return m, m.Wert, true
}

// zustandAm is a state condition on day d: every value of the day agrees
// (several sheets of one day that disagree make no pattern). It answers the
// values it was judged on.
func (b bestand) zustandAm(c Bedingung, d string, p *Profil) (bool, []Messwert) {
	if c.Minus != "" {
		a, va, ok1 := tagesmitte(b[c.Wert][d], c.Einheiten)
		m, vm, ok2 := tagesmitte(b[c.Minus][d], c.Einheiten)
		if !ok1 || !ok2 {
			return c.WennGemessen && len(b[c.Wert][d])+len(b[c.Minus][d]) == 0, nil
		}
		return c.grenzen(va - vm), []Messwert{a, m}
	}
	gemessen := false
	for _, n := range c.namen() {
		ms := b[n][d]
		if len(ms) == 0 {
			continue
		}
		gemessen = true
		alle := true
		for _, m := range ms {
			if !c.gilt(m, p) {
				alle = false
				break
			}
		}
		if alle {
			return true, ms
		}
	}
	return !gemessen && c.WennGemessen, nil
}

// gilt is one value against a state condition.
func (c Bedingung) gilt(m Messwert, p *Profil) bool {
	if c.Lage != "" && lageWort(einordnen(m, p).Lage) != c.Lage {
		return false
	}
	if c.Unter != nil || c.Ab != nil {
		v, ok := inEinheit(m, c.Einheiten)
		if !ok || !c.grenzen(v) {
			return false
		}
	}
	if c.SollUnter != nil {
		sp, ok := sollProzentVon(m)
		if !ok || !(sp < *c.SollUnter) {
			return false
		}
	}
	return true
}

// Spanne is one value between the first and the last of some days, each
// day's middle value in a unit both share.
type Spanne struct {
	Name             string
	A, B             Messwert
	AAnz, BAnz       int
	ALo, AHi         float64
	BLo, BHi         float64
	Rel              float64
	Richtung         string
	Tage             int
	Dazwischen       []Messwert
	NichtVergleichen bool // different units on the two days
}

// spanne is name's course over days (sorted, at least two of them carry it).
func (b bestand) spanne(name string, tage []string) (Spanne, bool) {
	var hat []string
	for _, d := range tage {
		if len(b[name][d]) > 0 {
			hat = append(hat, d)
		}
	}
	if len(hat) < 2 {
		return Spanne{}, false
	}
	s := Spanne{Name: name, Tage: len(hat)}
	erst, letzt := b[name][hat[0]], b[name][hat[len(hat)-1]]
	e, ok := gemeinsameEinheit(name, erst, letzt)
	if !ok {
		s.A, _, _, _ = tageswert(erst)
		s.B, _, _, _ = tageswert(letzt)
		s.NichtVergleichen = true
		return s, true
	}
	s.A, s.AAnz, s.ALo, s.AHi = tageswert(mitEinheitIn(erst, e))
	s.B, s.BAnz, s.BLo, s.BHi = tageswert(mitEinheitIn(letzt, e))
	for _, d := range hat[1 : len(hat)-1] {
		if in := mitEinheitIn(b[name][d], e); len(in) > 0 {
			m, _, _, _ := tageswert(in)
			s.Dazwischen = append(s.Dazwischen, m)
		}
	}
	if s.A.Wert == 0 || math.IsNaN(s.A.Wert) || math.IsNaN(s.B.Wert) {
		s.NichtVergleichen = true // no relative change from nothing
		return s, true
	}
	s.Richtung, s.Rel = richtung(s.A.Wert, s.B.Wert)
	return s, true
}

// gemeinsameTage are the days every name was measured on.
func (b bestand) gemeinsameTage(namen []string) []string {
	if len(namen) == 0 {
		return nil
	}
	var out []string
	for _, d := range b.tage(namen[0]) {
		alle := true
		for _, n := range namen[1:] {
			if len(b[n][d]) == 0 {
				alle = false
				break
			}
		}
		if alle {
			out = append(out, d)
		}
	}
	return out
}

// --- what fired

// Treffer is a rule that fits: its text (with the age's sentence), the
// values it was judged on, in words.
type Treffer struct {
	Regel  Regel
	Text   string
	Belege []string
}

func alterAm(p *Profil, d string) (int, bool) {
	if p == nil || p.Geburtsjahr == 0 {
		return 0, false
	}
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		return 0, false
	}
	return t.Year() - p.Geburtsjahr, true
}

// pruefen evaluates one rule. profilFehlt: the values fit, the profile the rule needs is missing.
func (b bestand) pruefen(x Regel, p *Profil) (t Treffer, ok, profilFehlt bool) {
	// the examination day: the latest of the first state condition's values
	var stich []string
	for _, c := range x.Wenn {
		if c.zustand() {
			stich = c.namen()
			break
		}
	}
	if stich == nil {
		for _, c := range x.Oder {
			stich = append(stich, c.namen()...)
		}
	}
	tag := ""
	for _, n := range stich {
		if ts := b.tage(n); len(ts) > 0 {
			tag = max(tag, ts[len(ts)-1])
		}
	}
	if len(stich) > 0 && tag == "" {
		return t, false, false
	}
	gesehen := map[string]bool{}
	beleg := func(ms ...Messwert) {
		for _, m := range ms {
			n := kanon(m)
			if gesehen[n] {
				continue
			}
			gesehen[n] = true
			t.Belege = append(t.Belege, n+" "+mitEinheit(m.Wert, m.Einheit)+" ("+monatJahr(m.Datum)+")")
		}
	}
	// the course: every trend condition over the days all its values share
	var vnamen []string
	for _, c := range x.Wenn {
		if c.verlauf() {
			vnamen = append(vnamen, c.Wert)
			if c.partner() != "" {
				vnamen = append(vnamen, c.partner())
			}
		}
	}
	spannen := map[string]Spanne{}
	if len(vnamen) > 0 {
		tage := b.gemeinsameTage(einmal(vnamen))
		for _, n := range einmal(vnamen) {
			s, ok := b.spanne(n, tage)
			if !ok || s.NichtVergleichen {
				return t, false, false
			}
			spannen[n] = s
		}
	}
	for _, c := range x.Wenn {
		if c.zustand() {
			gilt, ms := b.zustandAm(c, tag, p)
			if !gilt {
				return Treffer{}, false, false
			}
			switch {
			case c.Minus != "" && len(ms) == 2:
				beleg(ms...) // the two values of the day, each its day's middle one
			case len(ms) > 0:
				mm, _, _, _ := tageswert(ms)
				beleg(mm)
			}
			continue
		}
		s := spannen[c.Wert]
		if c.Richtung != "" && s.Richtung != c.Richtung {
			return Treffer{}, false, false
		}
		if c.UmAb != nil && !(math.Abs(s.Rel) >= *c.UmAb) {
			return Treffer{}, false, false
		}
		if c.UmUnter != nil && !(math.Abs(s.Rel) < *c.UmUnter) {
			return Treffer{}, false, false
		}
		abstand := kaumSchwelle
		if c.Abstand != nil {
			abstand = *c.Abstand
		}
		if c.GleichaufMit != "" && !(math.Abs(s.Rel-spannen[c.GleichaufMit].Rel) < abstand) {
			return Treffer{}, false, false
		}
		if c.StaerkerGesunkenAls != "" && !(s.Rel <= spannen[c.StaerkerGesunkenAls].Rel-abstand) {
			return Treffer{}, false, false
		}
	}
	if len(x.Oder) > 0 {
		eins := false
		for _, c := range x.Oder {
			if gilt, ms := b.zustandAm(c, tag, p); gilt && len(ms) > 0 {
				mm, _, _, _ := tageswert(ms)
				beleg(mm)
				eins = true
			}
		}
		if !eins {
			return Treffer{}, false, false
		}
	}
	for _, n := range einmal(vnamen) {
		s := spannen[n]
		t.Belege = append(t.Belege, spanneKurz(s))
	}
	wann := tag
	if wann == "" {
		for _, s := range spannen {
			wann = max(wann, s.B.Datum)
		}
	}
	alter, hatAlter := alterAm(p, wann)
	if f := x.Fuer; f != nil {
		if (f.Geschlecht != "" && (p == nil || p.Geschlecht == "")) || (f.AlterAb > 0 && !hatAlter) {
			return Treffer{}, false, true
		}
		if (f.Geschlecht != "" && p.Geschlecht != f.Geschlecht) || (f.AlterAb > 0 && alter < f.AlterAb) {
			return Treffer{}, false, false
		}
	}
	t.Regel, t.Text = x, x.Bedeutung
	if x.AbAlter != nil && hatAlter && alter >= x.AbAlter.Ab {
		t.Text += " " + x.AbAlter.Text
	}
	return t, true, false
}

func einmal(namen []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range namen {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

func prozentText(rel float64) string {
	return zahlText(math.Round(math.Abs(rel)*100)) + " %"
}

// spanneKurz is „FEV1 von 3,9 l (Apr. 2021) auf 3,04 l (Dez. 2023), gesunken um 22 %“.
func spanneKurz(s Spanne) string {
	t := fmt.Sprintf("%s von %s (%s) auf %s (%s)", s.Name, mitEinheit(s.A.Wert, s.A.Einheit), monatJahr(s.A.Datum), mitEinheit(s.B.Wert, s.B.Einheit), monatJahr(s.B.Datum))
	if s.Richtung == "kaum verändert" {
		return t + ", kaum verändert"
	}
	return t + ", " + s.Richtung + " um " + prozentText(s.Rel)
}

// --- the picture per system

// SystemBild is one body system's part of the page.
type SystemBild struct {
	System        GBSystem
	Tage          []string // its examination days (values read without doubt)
	Verlauf       []string
	Einmal        []string // values measured on one day only
	Mehr          int      // course lines left out
	Gemeinsam     []string
	NurEinTag     bool // the moving-together sets have values, but one day only
	Treffer       []Treffer
	Fragen        []string
	Offen         int      // values still to confirm: read with doubt, assumed, or not measurable
	Unmoeglich    []string // of them, the ones no body can measure, with value and month
	BrauchtProfil bool     // a rule fits the values but needs sex or birth year
}

// Gesamtbild is the page's content.
type Gesamtbild struct {
	Systeme  []SystemBild
	Tage     int
	Von, Bis string
	Offen    int
}

// gbVerlaufZeilen caps the course lines per system, gbEinmal the values measured once.
const gbVerlaufZeilen, gbEinmal = 6, 6

func imSystem(s GBSystem, gruppe string) bool {
	for _, g := range s.Gruppen {
		if g == gruppe {
			return true
		}
	}
	return false
}

// gesamtbild builds the page from a person's values (their corrections and
// the assumptions applied) and their profile (may be nil or empty).
func gesamtbild(v Verlauf, p *Profil) Gesamtbild {
	b := bestandVon(v.Werte)
	var gb Gesamtbild
	alleTage := map[string]bool{}
	for _, sys := range regelwerk.Systeme {
		sb := SystemBild{System: sys}
		for _, m := range v.Werte {
			if !imSystem(sys, gruppeVon(m)) {
				continue
			}
			switch {
			case m.Pruefen || m.Vermutung != nil:
				sb.Offen++
			case unmoeglich(m):
				sb.Offen++
				sb.Unmoeglich = append(sb.Unmoeglich, kanon(m)+" "+mitEinheit(m.Wert, m.Einheit)+" ("+monatJahr(m.Datum)+")")
			}
		}
		var namen []string
		tage := map[string]bool{}
		for n, proTag := range b {
			a := finde(n)
			g := ""
			if a != nil {
				g = a.Gruppe
			} else {
				for _, ms := range proTag {
					g = ms[0].Gruppe
					break
				}
			}
			if !imSystem(sys, g) {
				continue
			}
			namen = append(namen, n)
			for d := range proTag {
				tage[d] = true
			}
		}
		for d := range tage {
			sb.Tage = append(sb.Tage, d)
			alleTage[d] = true
		}
		sort.Strings(sb.Tage)
		if len(namen) == 0 && sb.Offen == 0 {
			continue
		}
		sb.Verlauf, sb.Einmal, sb.Mehr = verlaufZeilen(b, sys, namen)
		sb.Gemeinsam, sb.NurEinTag = gemeinsamZeilen(b, sys)
		gesehen := map[string]bool{}
		for _, x := range regelwerk.Regeln {
			if x.System != sys.ID {
				continue
			}
			t, ok, fehlt := b.pruefen(x, p)
			if fehlt {
				sb.BrauchtProfil = true
			}
			if !ok {
				continue
			}
			sb.Treffer = append(sb.Treffer, t)
			if !gesehen[x.Frage] && len(sb.Fragen) < 2 {
				gesehen[x.Frage] = true
				sb.Fragen = append(sb.Fragen, ohneUrteil(x.Frage, sys.Frage))
			}
		}
		if len(sb.Fragen) == 0 && len(namen) > 0 {
			sb.Fragen = []string{sys.Frage}
		}
		gb.Offen += sb.Offen
		gb.Systeme = append(gb.Systeme, sb)
	}
	gb.Tage = len(alleTage)
	for d := range alleTage {
		if gb.Von == "" || d < gb.Von {
			gb.Von = d
		}
		gb.Bis = max(gb.Bis, d)
	}
	return gb
}

func leitIn(s GBSystem, name string) int {
	for i, l := range s.Leit {
		if l == name {
			return i
		}
	}
	return 99
}

// verlaufZeilen are a system's values over the years: a line per value
// measured on more than one day, leading values and the biggest moves first;
// the values measured once in one line.
func verlaufZeilen(b bestand, sys GBSystem, namen []string) ([]string, []string, int) {
	type zeile struct {
		name string
		s    Spanne
	}
	var mehrere []zeile
	var einmalig []string
	for _, n := range namen {
		ts := b.tage(n)
		if len(ts) == 1 {
			einmalig = append(einmalig, n)
			continue
		}
		if s, ok := b.spanne(n, ts); ok {
			mehrere = append(mehrere, zeile{n, s})
		}
	}
	sort.SliceStable(mehrere, func(i, j int) bool {
		x, y := mehrere[i], mehrere[j]
		if li, lj := leitIn(sys, x.name), leitIn(sys, y.name); li != lj {
			return li < lj
		}
		if x.s.NichtVergleichen != y.s.NichtVergleichen {
			return !x.s.NichtVergleichen
		}
		if ax, ay := math.Abs(x.s.Rel), math.Abs(y.s.Rel); ax != ay {
			return ax > ay
		}
		return x.name < y.name
	})
	var out []string
	mehr := 0
	for i, z := range mehrere {
		if i >= gbVerlaufZeilen {
			mehr = len(mehrere) - gbVerlaufZeilen
			break
		}
		out = append(out, verlaufText(z.s))
	}
	sort.SliceStable(einmalig, func(i, j int) bool {
		if li, lj := leitIn(sys, einmalig[i]), leitIn(sys, einmalig[j]); li != lj {
			return li < lj
		}
		return einmalig[i] < einmalig[j]
	})
	var ein []string
	for i, n := range einmalig {
		if i >= gbEinmal {
			mehr += len(einmalig) - gbEinmal
			break
		}
		d := b.tage(n)[0]
		m, anz, lo, hi := tageswert(b[n][d])
		ein = append(ein, n+" "+wertText(m, anz, lo, hi)+" ("+monatJahr(d)+")")
	}
	return out, ein, mehr
}

// verlaufText is one value's course: „FEV1: von 3,9 l (Apr. 2021) auf 3,04 l
// (Dez. 2023) gesunken, um 22 % · 3 Untersuchungstage, dazwischen 3,5 l (Apr. 2023)“.
func verlaufText(s Spanne) string {
	if s.NichtVergleichen {
		return fmt.Sprintf("%s: %s (%s) und %s (%s) – in verschiedenen Einheiten oder von null aus, nicht verglichen · %d Untersuchungstage",
			s.Name, mitEinheit(s.A.Wert, s.A.Einheit), monatJahr(s.A.Datum), mitEinheit(s.B.Wert, s.B.Einheit), monatJahr(s.B.Datum), s.Tage)
	}
	a := wertText(s.A, s.AAnz, s.ALo, s.AHi)
	bt := wertText(s.B, s.BAnz, s.BLo, s.BHi)
	var t string
	if s.Richtung == "kaum verändert" {
		t = fmt.Sprintf("%s: %s (%s) und %s (%s) – kaum verändert", s.Name, a, monatJahr(s.A.Datum), bt, monatJahr(s.B.Datum))
	} else {
		t = fmt.Sprintf("%s: von %s (%s) auf %s (%s) %s, um %s", s.Name, a, monatJahr(s.A.Datum), bt, monatJahr(s.B.Datum), s.Richtung, prozentText(s.Rel))
	}
	t += fmt.Sprintf(" · %d Untersuchungstage", s.Tage)
	if len(s.Dazwischen) > 0 {
		var teile []string
		for i, m := range s.Dazwischen {
			if i == 2 {
				teile = append(teile, fmt.Sprintf("%d weitere", len(s.Dazwischen)-2))
				break
			}
			teile = append(teile, mitEinheit(m.Wert, m.Einheit)+" ("+monatJahr(m.Datum)+")")
		}
		t += ", dazwischen " + strings.Join(teile, ", ")
	}
	return t
}

// gemeinsamZeilen are the system's sets that move together, each between
// the first and the last day at least two of its values were measured on:
// „Apr. 2021 bis Dez. 2023: FEV1 und FVC gesunken (um 22 % und 17 %), FEV1/FVC kaum verändert.“
func gemeinsamZeilen(b bestand, sys GBSystem) ([]string, bool) {
	var out []string
	nurEiner := false
	for _, set := range sys.Gemeinsam {
		proTag := map[string]int{}
		vorhanden := 0
		for _, n := range set {
			if len(b[n]) > 0 {
				vorhanden++
			}
			for d := range b[n] {
				proTag[d]++
			}
		}
		var tage []string
		for d, k := range proTag {
			if k >= 2 {
				tage = append(tage, d)
			}
		}
		sort.Strings(tage)
		if len(tage) < 2 {
			if vorhanden >= 2 {
				nurEiner = true
			}
			continue
		}
		span := []string{tage[0], tage[len(tage)-1]}
		gruppen := map[string][]Spanne{}
		var reihenfolge []string
		n := 0
		for _, name := range set {
			if len(b[name][span[0]]) == 0 || len(b[name][span[1]]) == 0 {
				continue
			}
			s, ok := b.spanne(name, span)
			if !ok || s.NichtVergleichen {
				continue
			}
			if _, ok := gruppen[s.Richtung]; !ok {
				reihenfolge = append(reihenfolge, s.Richtung)
			}
			gruppen[s.Richtung] = append(gruppen[s.Richtung], s)
			n++
		}
		if n < 2 {
			continue
		}
		var teile []string
		for _, r := range reihenfolge {
			ss := gruppen[r]
			var ns, um []string
			for _, s := range ss {
				ns = append(ns, s.Name)
				um = append(um, prozentText(s.Rel))
			}
			t := undListe(ns)
			if len(ss) > 1 && r != "kaum verändert" {
				t += " beide"
				if len(ss) > 2 {
					t = undListe(ns) + " alle"
				}
			}
			t += " " + r
			if r != "kaum verändert" {
				t += " (um " + undListe(um) + ")"
			}
			teile = append(teile, t)
		}
		out = append(out, monatJahr(span[0])+" bis "+monatJahr(span[1])+": "+strings.Join(teile, ", ")+".")
	}
	return out, nurEiner && len(out) == 0
}

func undListe(xs []string) string {
	switch len(xs) {
	case 0:
		return ""
	case 1:
		return xs[0]
	}
	return strings.Join(xs[:len(xs)-1], ", ") + " und " + xs[len(xs)-1]
}

// gesamtbildFragen feed „Für den Arzt“: per system whose values a rule
// fits, its first question; at most four, so the sheet stays one sheet.
func gesamtbildFragen(gb Gesamtbild) []string {
	var out []string
	for _, s := range gb.Systeme {
		if len(s.Treffer) == 0 || len(s.Fragen) == 0 {
			continue
		}
		out = append(out, s.System.Name+": "+s.Fragen[0])
		if len(out) == 4 {
			break
		}
	}
	return out
}

// --- the page

// gesamtbildHinweis is the page's honest first line (owner's words, #17).
const gesamtbildHinweis = "Allgemeines Wissen zu Mustern in deinen Werten, keine Diagnose."

func (w *Web) gesamtbildSeite(rw http.ResponseWriter, r *http.Request) {
	p, s, darf := w.wer(rw, r)
	if p == nil {
		return
	}
	v, _ := w.Dienst.Speicher.Verlauf(p.Key)
	pr, _ := w.Dienst.Speicher.Profil(p.Key)
	body := w.neuZuLesen(p.Key) + gesamtbildHTML(gesamtbild(v, &pr), mitPerson(p, s))
	w.rahmen(rw, r, p, s, darf, "/gesamtbild", "Gesamtbild · "+p.Anzeige, body)
}

func anzahl(n int, eins, mehr string) string {
	if n == 1 {
		return "1 " + eins
	}
	return fmt.Sprintf("%d %s", n, mehr)
}

// gesamtbildHTML is the page: the line on what it is, then per system the
// course, what moves together, what such patterns can mean (with their
// sources), the questions.
func gesamtbildHTML(gb Gesamtbild, q string) string {
	var b strings.Builder
	befunde, werte := "/befunde"+q, "/werte"+q
	b.WriteString(`<h2>Gesamtbild</h2><p class=hinweis>` + gesamtbildHinweis + `</p>`)
	if gb.Tage > 0 {
		fmt.Fprintf(&b, `<p class=k>Gebaut aus %s zwischen %s und %s. Es zählen nur Werte, die ohne Zweifel gelesen, bestätigt oder korrigiert sind. Was ein Muster allgemein bedeuten kann, steht in einer festen Regeltabelle mit einer Quelle je Regel; die Seite bewertet nichts.</p>`,
			anzahl(gb.Tage, "Untersuchungstag", "Untersuchungstagen"), html.EscapeString(monatJahr(gb.Von)), html.EscapeString(monatJahr(gb.Bis)))
	} else {
		b.WriteString(`<p>Noch keine Werte, die ohne Zweifel gelesen, bestätigt oder korrigiert sind und ein Datum haben. Daraus baut sich diese Seite.</p>`)
	}
	for _, s := range gb.Systeme {
		fmt.Fprintf(&b, `<section class=system id="%s"><h2>%s</h2>`, html.EscapeString(s.System.ID), html.EscapeString(s.System.Name))
		var kopf []string
		if len(s.Tage) > 0 {
			k := anzahl(len(s.Tage), "Untersuchungstag", "Untersuchungstage")
			if len(s.Tage) > 1 {
				k += ", " + monatJahr(s.Tage[0]) + " bis " + monatJahr(s.Tage[len(s.Tage)-1])
			} else {
				k += ", " + monatJahr(s.Tage[0])
			}
			kopf = append(kopf, html.EscapeString(k))
		}
		if s.Offen > 0 {
			kopf = append(kopf, fmt.Sprintf(`<a href="%s">%s noch zu bestätigen</a>`, html.EscapeString(befunde), anzahl(s.Offen, "Wert", "Werte")))
		}
		if len(kopf) > 0 {
			b.WriteString(`<p class=k>` + strings.Join(kopf, " · ") + `</p>`)
		}
		if len(s.Unmoeglich) > 0 {
			liste := s.Unmoeglich[:min(4, len(s.Unmoeglich))]
			t := strings.Join(liste, ", ")
			if n := len(s.Unmoeglich) - len(liste); n > 0 {
				t += fmt.Sprintf(" und %d weitere", n)
			}
			b.WriteString(`<p class=k>Darunter, weil die Zahl so nicht messbar ist und vermutlich falsch gelesen wurde: ` + html.EscapeString(t) + `. Unter Befunde lässt sie sich korrigieren oder bestätigen.</p>`)
		}
		if len(s.Verlauf)+len(s.Einmal) > 0 {
			b.WriteString(`<h3>Verlauf</h3><ul class=vergleich>`)
			for _, z := range s.Verlauf {
				b.WriteString(`<li>` + html.EscapeString(z) + `</li>`)
			}
			if len(s.Einmal) > 0 {
				b.WriteString(`<li>Einmal gemessen: ` + html.EscapeString(strings.Join(s.Einmal, ", ")) + `</li>`)
			}
			b.WriteString(`</ul>`)
			if s.Mehr > 0 {
				fmt.Fprintf(&b, `<p class=k>und %s unter <a href="%s">Werte</a></p>`, anzahl(s.Mehr, "weiterer Wert", "weitere Werte"), html.EscapeString(werte))
			}
		}
		if len(s.Gemeinsam) > 0 {
			b.WriteString(`<h3>Was sich gemeinsam bewegt</h3><ul class=vergleich>`)
			for _, z := range s.Gemeinsam {
				b.WriteString(`<li>` + html.EscapeString(z) + `</li>`)
			}
			b.WriteString(`</ul>`)
		} else if s.NurEinTag {
			b.WriteString(`<h3>Was sich gemeinsam bewegt</h3><p class=k>Zusammengehörige Werte gibt es bisher nur von einem Tag; ein gemeinsamer Verlauf braucht mindestens zwei Untersuchungen.</p>`)
		}
		if len(s.Verlauf)+len(s.Einmal) > 0 {
			b.WriteString(`<h3>Was solche Muster allgemein bedeuten können</h3>`)
			if len(s.Treffer) == 0 {
				b.WriteString(`<p class=k>Keine Regel der Tabelle passt auf diese Werte.</p>`)
			}
			for _, t := range s.Treffer {
				fmt.Fprintf(&b, `<div class=regel id="r-%s"><p>%s</p><p class=k>Passt auf: %s</p><p class="k quelle">Quelle: %s</p></div>`,
					html.EscapeString(t.Regel.ID), html.EscapeString(t.Text), html.EscapeString(strings.Join(t.Belege, "; ")), html.EscapeString(t.Regel.Quelle))
			}
			if s.BrauchtProfil {
				fmt.Fprintf(&b, `<p class=k>Eine Regel hier gilt je nach Geschlecht und Alter; dafür fehlt die Angabe unter <a href="%s#profil">Werte · Über dich</a>.</p>`, html.EscapeString(werte))
			}
		}
		if len(s.Fragen) > 0 {
			b.WriteString(`<h3>Fragen an den Arzt</h3><ul class=vergleich>`)
			for _, f := range s.Fragen {
				b.WriteString(`<li class=frage-text>` + html.EscapeString(f) + `</li>`)
			}
			b.WriteString(`</ul>`)
		}
		b.WriteString(`</section>`)
	}
	return b.String()
}
