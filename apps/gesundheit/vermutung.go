package main

import (
	"fmt"
	"html"
	"strings"
	"unicode"
)

// Assuming the likely name or unit of an obvious OCR misread (#15, owner
// 2026-10-31: „das klingt nach einem offensichtlichen OCR-Fehler. Kannst du
// wahrscheinliche Werte annehmen und um Korrektur bitten? So dass es erst
// einmal richtiger ist.“). The 2019 sheet reads „Haemaglobin“; it belongs to
// Hämoglobin's history. When exactly one name of the dictionary fits a name
// it does not know, the value is shown and compared as that name, marked
// „vermutlich Hämoglobin – bitte bestätigen“, until the person confirms it
// (it becomes their correction, korrektur.go) or corrects it. A unit that is
// none, or not one the value is given in, is assumed the same way when the
// dictionary names exactly one unit for the value. Never a number: a number
// in doubt stays „prüfen“ with the snippet of its page (#13), and a unit is
// never converted. A person's word on a value always wins: an assumption is
// made only for a value nobody has spoken on yet.
//
// What counts as fitting, on the names as the dictionary compares them
// (falten: case, umlauts ae/ä oe/ö ue/ü ß/ss):
//   - a spelling variant: spaces and hyphens left out, „rn“ read for „m“;
//   - an OCR confusion of a letter and a digit: „0“ for „o“, „1“ for „l“
//     (EV0l is EVol, FEVl is FEV1) — the two forms equal after it;
//   - or a small misread of letters: one letter wrong, missing, extra, or two
//     neighbours swapped (two in a name of ten letters or more), and only
//     between letters: a digit or a % is part of what a value is (FEV1,
//     FEV3 and FEV6 are three values; FEV1% is not FEV1). Names under five
//     letters are only taken by the confusions: „GOT“ and „GPT“ are both real.
//
// Two names fitting, or none: no assumption, the value stays „prüfen“ as before.

// Vermutung is what the program assumed of a value as read, applied when
// the timeline is read and never stored: a new dictionary assumes anew.
type Vermutung struct {
	Name           bool   `json:"name,omitempty"`
	GelesenName    string `json:"gelesen_name,omitempty"`
	Einheit        bool   `json:"einheit,omitempty"`
	GelesenEinheit string `json:"gelesen_einheit,omitempty"`
}

// the reasons a reader writes for a name or a unit in doubt (befund.go,
// woerterbuch.go): an assumption takes those back, and only those
const (
	grundKeinWoerterbuch = "steht so in keinem Wörterbuch"
	grundKeineEinheit    = "ist keine Einheit"
	grundEinheitPasst    = "die Einheit „"
	grundTextDerSeite    = "im Text der Seite als" // a number in doubt (ausDemBild): never taken back
)

// nameOderEinheitGrund is whether a value's reason for „prüfen“ is its name
// or its unit and nothing else.
func nameOderEinheitGrund(g string) bool {
	return !strings.Contains(g, grundTextDerSeite) &&
		(strings.Contains(g, grundKeinWoerterbuch) || strings.Contains(g, grundKeineEinheit) || strings.HasPrefix(g, grundEinheitPasst))
}

var (
	ohneTrenner  = strings.NewReplacer(" ", "", "-", "", "rn", "m")
	verwechslung = strings.NewReplacer("0", "o", "1", "l")
)

// schreibform is a name as compared for a misread: folded, without spaces
// and hyphens, „rn“ as „m“.
func schreibform(s string) string { return ohneTrenner.Replace(falten(s)) }

type nameForm struct {
	form, verwechselt string
	analyt            *Analyt
}

var namensFormen = func() []nameForm {
	var out []nameForm
	for i := range Woerterbuch {
		a := &Woerterbuch[i]
		for _, n := range append([]string{a.Name}, a.Aliase...) {
			f := schreibform(n)
			out = append(out, nameForm{f, verwechslung.Replace(f), a})
		}
	}
	return out
}()

// vermuteName is the one analyte a name the dictionary does not know is a
// misread of; nil when the dictionary knows it, when none fits, or when more
// than one does.
func vermuteName(name string) *Analyt {
	if strings.TrimSpace(name) == "" || finde(name) != nil {
		return nil
	}
	formen := []string{schreibform(name)}
	if f := falten(name); strings.IndexAny(f, "([") > 0 {
		formen = append(formen, schreibform(f[:strings.IndexAny(f, "([")]))
	}
	kandidaten := map[*Analyt]bool{}
	for _, f := range formen {
		if f == "" {
			continue
		}
		r := []rune(f)
		v := verwechslung.Replace(f)
		for _, nf := range namensFormen {
			if v == nf.verwechselt {
				kandidaten[nf.analyt] = true
				continue
			}
			n := []rune(nf.form)
			if len(r) < 5 || len(n) < 5 {
				continue
			}
			grenze := 1
			if min(len(r), len(n)) >= 10 {
				grenze = 2
			}
			if buchstabenAbstand(r, n, grenze) <= grenze {
				kandidaten[nf.analyt] = true
			}
		}
	}
	if len(kandidaten) != 1 {
		return nil
	}
	for a := range kandidaten {
		return a
	}
	return nil
}

// buchstabenAbstand is the edit distance of a and b by letters only: a
// letter replaced, left out, added, or two neighbouring letters swapped
// count one; any edit of a digit or a sign is not a misread and counts
// more than grenze.
func buchstabenAbstand(a, b []rune, grenze int) int {
	if d := len(a) - len(b); d > grenze || -d > grenze {
		return grenze + 1
	}
	zu := grenze + 1
	kosten := func(r rune) int {
		if unicode.IsLetter(r) {
			return 1
		}
		return zu
	}
	d := make([][]int, len(a)+1)
	for i := range d {
		d[i] = make([]int, len(b)+1)
	}
	for i := 1; i <= len(a); i++ {
		d[i][0] = min(zu, d[i-1][0]+kosten(a[i-1]))
	}
	for j := 1; j <= len(b); j++ {
		d[0][j] = min(zu, d[0][j-1]+kosten(b[j-1]))
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			ersetzen := 0
			if a[i-1] != b[j-1] {
				ersetzen = zu
				if unicode.IsLetter(a[i-1]) && unicode.IsLetter(b[j-1]) {
					ersetzen = 1
				}
			}
			x := min(d[i-1][j-1]+ersetzen, d[i-1][j]+kosten(a[i-1]), d[i][j-1]+kosten(b[j-1]))
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] && a[i-1] != a[i-2] && unicode.IsLetter(a[i-1]) && unicode.IsLetter(a[i-2]) {
				x = min(x, d[i-2][j-2]+1)
			}
			d[i][j] = min(x, zu)
		}
	}
	return d[len(a)][len(b)]
}

// einzigeEinheit is the one unit the dictionary names for a value — its
// usual unit, the units it is given in, the ones it converts — when it
// names exactly one.
func einzigeEinheit(a *Analyt) (string, bool) {
	if a == nil {
		return "", false
	}
	alle := map[string]string{}
	for _, e := range append(append([]string{a.Einheit}, a.Einheiten...), schluesselVon(a.Umrechnung)...) {
		if strings.TrimSpace(e) != "" {
			alle[einheitNorm(e)] = e
		}
	}
	if len(alle) != 1 {
		return "", false
	}
	for _, e := range alle {
		return e, true
	}
	return "", false
}

func schluesselVon(m map[string]float64) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// vermuteEinheit is the unit a unit in doubt is assumed to be: one that is
// no unit or not one the value is given in, when the dictionary names
// exactly one unit for the value. The number is never converted.
func vermuteEinheit(a *Analyt, u string) (string, bool) {
	if a == nil || strings.TrimSpace(u) == "" || einheitGrund(a, u) == "" {
		return "", false
	}
	return einzigeEinheit(a)
}

// benennen gives a value a name: the dictionary's, with its group, when it
// knows the name; else the name as given, as a new one. The name the value
// was read as stays as its original (the snippet finds the line by it).
func benennen(m *Messwert, name string) {
	gelesen := m.Name
	if a := finde(name); a != nil {
		m.Name, m.Gruppe, m.Neu = a.Name, a.Gruppe, false
	} else {
		m.Name, m.Gruppe, m.Neu = name, "Weitere", true
	}
	if m.Original == "" && !strings.EqualFold(gelesen, m.Name) {
		m.Original = gelesen
	}
}

// kanonisch is a value under the dictionary's name when the dictionary
// knows the name as read (an older reader's „SYS“ is Blutdruck
// systolisch): the dictionary's word, no assumption.
func kanonisch(m Messwert) Messwert {
	if a := finde(m.Name); a != nil && (m.Name != a.Name || m.Neu) {
		benennen(&m, a.Name)
	}
	return m
}

// vermuten is a value nobody has spoken on, with its likely name and unit
// assumed. Its „prüfen“ goes only when the name or the unit was its reason
// and nothing is left in doubt; a number in doubt keeps it.
func vermuten(m Messwert) Messwert {
	m = kanonisch(m)
	var v Vermutung
	if a := vermuteName(m.Name); a != nil {
		v.Name, v.GelesenName = true, m.Name
		benennen(&m, a.Name)
	}
	an := finde(m.Name)
	if e, ok := vermuteEinheit(an, m.Einheit); ok {
		v.Einheit, v.GelesenEinheit = true, m.Einheit
		m.Einheit = e
	}
	if !v.Name && !v.Einheit {
		return m
	}
	m.Vermutung = &v
	if m.Pruefen && nameOderEinheitGrund(m.PruefenGrund) {
		rest := ""
		if !v.Einheit {
			rest = einheitGrund(an, m.Einheit)
		}
		m.Pruefen, m.PruefenGrund = rest != "", rest
	}
	return m
}

// --- what the pages say

// vermutungText is the mark: „vermutlich Hämoglobin – bitte bestätigen“.
func vermutungText(m Messwert) string {
	v := m.Vermutung
	if v == nil {
		return ""
	}
	t := "vermutlich"
	if v.Name {
		t += " " + m.Name
	}
	if v.Einheit {
		t += " in " + m.Einheit
	}
	return t + " – bitte bestätigen"
}

// gelesenWie is what was read: „gelesen als „Haemaglobin““.
func gelesenWie(m Messwert) string {
	v := m.Vermutung
	if v == nil {
		return ""
	}
	var teile []string
	if v.Name {
		teile = append(teile, "„"+v.GelesenName+"“")
	}
	if v.Einheit {
		teile = append(teile, "„"+v.GelesenEinheit+"“")
	}
	return "gelesen als " + strings.Join(teile, " in ")
}

// vermutetKurz is the doctor's page's short word on an assumed value.
func vermutetKurz(m Messwert) string {
	v := m.Vermutung
	switch {
	case v == nil:
		return ""
	case v.Name && v.Einheit:
		return "Name und Einheit vermutet"
	case v.Name:
		return "Name vermutet"
	}
	return "Einheit vermutet"
}

// vermutungHTML is the mark with its one-tap „Ja“; „Korrigieren“ is the
// correction form (korrekturHTML) beside it.
func vermutungHTML(m Messwert, person, zurueck string) string {
	if m.Vermutung == nil || m.ID == "" {
		return ""
	}
	wann := ""
	if m.Datum != "" {
		wann = " am " + datumText(m.Datum)
	}
	return fmt.Sprintf(`<div class=vermutung><span class="marke vermutet">%s</span> %s · %s <form method=post action="/korrektur" class=inline><input type=hidden name=person value="%s"><input type=hidden name=id value="%s"><input type=hidden name=zurueck value="%s"><button name=aktion value=annehmen>Ja</button></form></div>`,
		html.EscapeString(vermutungText(m)), html.EscapeString(mitEinheit(m.Wert, m.Einheit)+wann), html.EscapeString(gelesenWie(m)),
		html.EscapeString(person), html.EscapeString(m.ID), html.EscapeString(zurueck))
}

// vermutetHinweis is „Für den Arzt“'s one line on the open assumptions, a
// link to where they are confirmed.
func vermutetHinweis(n int, befunde string) string {
	if n == 0 {
		return ""
	}
	werte := "Werte"
	if n == 1 {
		werte = "Wert"
	}
	return fmt.Sprintf(`<p class="hinweis nicht-drucken"><a href="%s">%d %s vermutlich richtig gelesen, bitte bestätigen</a></p>`, html.EscapeString(befunde), n, werte)
}
