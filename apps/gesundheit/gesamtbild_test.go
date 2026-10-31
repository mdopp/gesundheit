package main

import (
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// --- „Gesamtbild“ (#17): fixed, sourced rules over values read without doubt

// gw is a value of a day, read without doubt.
func gw(name string, wert float64, einheit, datum string) Messwert {
	m := Messwert{Name: name, Wert: wert, Einheit: einheit, Datum: datum, Dokument: 1}
	if a := finde(name); a != nil {
		m.Gruppe = a.Gruppe
	}
	return m
}

func mitLLN(m Messwert, lln float64) Messwert { m.LLN = zf(lln); return m }

// gefeuert are the rules that fit, sorted.
func gefeuert(werte []Messwert, p *Profil) []string {
	var out []string
	for _, s := range gesamtbild(Verlauf{Werte: werte}, p).Systeme {
		for _, t := range s.Treffer {
			out = append(out, t.Regel.ID)
		}
	}
	sort.Strings(out)
	return out
}

func mann(jahr int) *Profil { return &Profil{Geburtsjahr: jahr, Geschlecht: "m"} }
func frau(jahr int) *Profil { return &Profil{Geburtsjahr: jahr, Geschlecht: "w"} }

const t1, t2 = "2021-04-12", "2023-12-19"

// Each case lists exactly the rules that fit: a rule fires on its pattern
// and on no other (the near misses are cases of their own).
var regelFaelle = []struct {
	name  string
	p     *Profil
	werte []Messwert
	will  []string
}{
	{"obstruktiv unter LLN und unter 70 %", nil, []Messwert{mitLLN(gw("FEV1/FVC", 62, "%", t2), 68)}, []string{"lunge-feste-grenze-70", "lunge-obstruktiv"}},
	{"obstruktiv unter LLN, über 70 %", nil, []Messwert{mitLLN(gw("FEV1/FVC", 72, "%", t2), 74)}, []string{"lunge-obstruktiv"}},
	{"unter 70 % ohne Grenzwert", nil, []Messwert{gw("FEV1/FVC", 68, "%", t2)}, []string{"lunge-feste-grenze-70"}},
	{"FEV1/FVC als Bruch: keine Einheit, keine Regel", nil, []Messwert{gw("FEV1/FVC", 0.68, "", t2)}, nil},
	{"möglich restriktiv", nil, []Messwert{mitLLN(gw("FVC", 3.0, "l", t2), 3.6), mitLLN(gw("FEV1/FVC", 82, "%", t2), 70)}, []string{"lunge-restriktion-moeglich"}},
	{"FVC unter, Verhältnis an einem anderen Tag", nil, []Messwert{mitLLN(gw("FVC", 3.0, "l", t2), 3.6), mitLLN(gw("FEV1/FVC", 82, "%", t1), 70)}, nil},
	{"gemeinsam gesunken", nil, []Messwert{gw("FEV1", 4.0, "l", t1), gw("FVC", 5.0, "l", t1), gw("FEV1", 3.6, "l", t2), gw("FVC", 4.5, "l", t2)},
		[]string{"lunge-fev1-innerhalb-schwankung", "lunge-gemeinsam-gesunken"}},
	{"FEV1 stärker gesunken", nil, []Messwert{gw("FEV1", 4.0, "l", t1), gw("FVC", 5.0, "l", t1), gw("FEV1", 3.2, "l", t2), gw("FVC", 4.8, "l", t2)},
		[]string{"lunge-fev1-staerker-gesunken", "lunge-fev1-ueber-schwankung"}},
	{"FEV1 gestiegen", nil, []Messwert{gw("FEV1", 3.0, "l", t1), gw("FEV1", 3.1, "l", t2)}, []string{"lunge-fev1-innerhalb-schwankung"}},
	{"FEV1 in zwei Einheiten: kein Verlauf", nil, []Messwert{gw("FEV1", 3.0, "l", t1), gw("FEV1", 2900, "ml", t2)}, nil},
	{"Mittelfluss allein", nil, []Messwert{mitLLN(gw("FEF50", 2.0, "l/s", t2), 2.5), mitLLN(gw("FEV1/FVC", 78, "%", t2), 70)}, []string{"lunge-mittelfluesse-allein"}},
	{"Mittelfluss mit Obstruktion", nil, []Messwert{mitLLN(gw("FEF75", 0.5, "l/s", t2), 0.8), mitLLN(gw("FEV1/FVC", 65, "%", t2), 70)}, []string{"lunge-feste-grenze-70", "lunge-obstruktiv"}},
	{"Belastung, eine Richtung", nil, []Messwert{gw("VE/VCO2-Slope", 27, "", t1), gw("Atemreserve", 30, "%", t1), gw("SpO2 in Ruhe", 97, "%", t1), gw("SpO2 bei Belastungsende", 96, "%", t1),
		gw("Maximaler RER", 1.15, "", t1), mitSoll(gw("Maximale Sauerstoffaufnahme", 30, "ml/min/kg", t1), 78)},
		[]string{"belastung-atemreserve-ab-15", "belastung-rer-ausbelastet", "belastung-spo2-gehalten", "belastung-ve-vco2-unter-30", "belastung-vo2-unter-84-prozent"}},
	{"Belastung, die andere", nil, []Messwert{gw("VE/VCO2-Slope", 38, "", t1), gw("Atemreserve", 10, "%", t1), gw("SpO2 in Ruhe", 97, "%", t1), gw("SpO2 bei Belastungsende", 91, "%", t1),
		gw("Maximaler RER", 1.0, "", t1), mitSoll(gw("Maximale Sauerstoffaufnahme", 40, "ml/min/kg", t1), 95)},
		[]string{"belastung-atemreserve-unter-15", "belastung-spo2-abfall", "belastung-ve-vco2-ab-36"}},
	{"Belastung dazwischen", nil, []Messwert{gw("VE/VCO2-Slope", 32, "", t1), gw("SpO2 bei Belastungsende", 91, "%", t1), gw("Atemreserve", 9, "l/min", t1)}, nil},
	{"Anämie und mikrozytär, Mann", mann(1980), []Messwert{mitRef(gw("Hämoglobin", 12.1, "g/dl", t2), 13.5, 17.5), mitRef(gw("MCV", 72, "fl", t2), 80, 96)},
		[]string{"blutbild-mikrozytaer", "blutbild-who-anaemie-maenner"}},
	{"mikrozytär, Frau über 12 g/dl", frau(1980), []Messwert{mitRef(gw("Hämoglobin", 12.1, "g/dl", t2), 12.5, 15.5), mitRef(gw("MCV", 72, "fl", t2), 80, 96)}, []string{"blutbild-mikrozytaer"}},
	{"Hämoglobin in g/l umgerechnet", mann(1980), []Messwert{gw("Hämoglobin", 121, "g/l", t2)}, []string{"blutbild-who-anaemie-maenner"}},
	{"Hämoglobin eines Kindes", mann(2015), []Messwert{gw("Hämoglobin", 12.1, "g/dl", t2)}, nil},
	{"makrozytär, Frau", frau(1970), []Messwert{mitRef(gw("Hämoglobin", 11, "g/dl", t2), 12, 16), mitRef(gw("MCV", 105, "fl", t2), 80, 96)}, []string{"blutbild-makrozytaer", "blutbild-who-anaemie-frauen"}},
	{"latente Unterfunktion", nil, []Messwert{mitRef(gw("TSH", 6, "mU/l", t2), 0.4, 4), mitRef(gw("fT4", 1.2, "ng/dl", t2), 0.9, 1.7)}, []string{"schilddruese-latent-unterfunktion"}},
	{"manifeste Unterfunktion", nil, []Messwert{mitRef(gw("TSH", 9, "mU/l", t2), 0.4, 4), mitRef(gw("fT4", 0.7, "ng/dl", t2), 0.9, 1.7)}, []string{"schilddruese-manifeste-unterfunktion"}},
	{"manifeste Überfunktion", nil, []Messwert{mitRef(gw("TSH", 0.1, "mU/l", t2), 0.4, 4), mitRef(gw("fT4", 2.2, "ng/dl", t2), 0.9, 1.7)}, []string{"schilddruese-manifeste-ueberfunktion"}},
	{"latente Überfunktion", nil, []Messwert{mitRef(gw("TSH", 0.1, "mU/l", t2), 0.4, 4), mitRef(gw("fT4", 1.2, "ng/dl", t2), 0.9, 1.7), mitRef(gw("fT3", 3, "pg/ml", t2), 2, 4.4)},
		[]string{"schilddruese-latent-ueberfunktion"}},
	{"TSH unter, fT3 fehlt", nil, []Messwert{mitRef(gw("TSH", 0.1, "mU/l", t2), 0.4, 4), mitRef(gw("fT4", 1.2, "ng/dl", t2), 0.9, 1.7)}, nil},
	{"TSH im Bereich", nil, []Messwert{mitRef(gw("TSH", 2, "mU/l", t2), 0.4, 4)}, []string{"schilddruese-tsh-im-bereich"}},
	{"TSH im Bereich, fT4 darunter", nil, []Messwert{mitRef(gw("TSH", 2, "mU/l", t2), 0.4, 4), mitRef(gw("fT4", 0.7, "ng/dl", t2), 0.9, 1.7)}, nil},
	{"TSH ohne Bereich", nil, []Messwert{gw("TSH", 2, "mU/l", t2)}, nil},
	{"fT4 nur an einem früheren Tag", nil, []Messwert{mitRef(gw("TSH", 6, "mU/l", t2), 0.4, 4), mitRef(gw("fT4", 1.2, "ng/dl", t1), 0.9, 1.7)}, nil},
	{"ab 140, isoliert systolisch", nil, []Messwert{gw("Blutdruck systolisch", 150, "mmHg", t2), gw("Blutdruck diastolisch", 85, "mmHg", t2)}, []string{"kreislauf-ab-140-90", "kreislauf-isoliert-systolisch"}},
	{"diastolisch ab 90", nil, []Messwert{gw("Blutdruck systolisch", 135, "mmHg", t2), gw("Blutdruck diastolisch", 92, "mmHg", t2)}, []string{"kreislauf-ab-140-90"}},
	{"unter 130/80", nil, []Messwert{gw("Blutdruck systolisch", 120, "mmHg", t2), gw("Blutdruck diastolisch", 75, "mmHg", t2)}, []string{"kreislauf-unter-130-80"}},
	{"dazwischen", nil, []Messwert{gw("Blutdruck systolisch", 135, "mmHg", t2), gw("Blutdruck diastolisch", 85, "mmHg", t2)}, nil},
	{"ein Tag, der nicht übereinstimmt", nil, []Messwert{gw("SYS", 150, "mmHg", t2), gw("SYS", 125, "mmHg", t2), gw("DIAS", 78, "mmHg", t2)}, nil},
	{"ein älterer Wert zählt nicht", nil, []Messwert{gw("Blutdruck systolisch", 150, "mmHg", t1), gw("Blutdruck systolisch", 120, "mmHg", t2), gw("Blutdruck diastolisch", 75, "mmHg", t2)}, []string{"kreislauf-unter-130-80"}},
	{"HbA1c ab 6,5", nil, []Messwert{gw("HbA1c", 6.8, "%", t2)}, []string{"stoffwechsel-hba1c-ab-6-5"}},
	{"HbA1c dazwischen", nil, []Messwert{gw("HbA1c", 6.0, "%", t2)}, []string{"stoffwechsel-hba1c-zwischenbereich"}},
	{"HbA1c darunter, und in mmol/mol", nil, []Messwert{gw("HbA1c", 5.4, "%", t1), gw("HbA1c", 50, "mmol/mol", t2)}, nil},
	{"GFR unter 60", nil, []Messwert{gw("GFR", 52, "ml/min/1,73 m²", t2)}, []string{"niere-gfr-unter-60"}},
	{"GFR darüber", nil, []Messwert{gw("GFR", 75, "ml/min", t2)}, nil},
	{"Anti-HBs ab 10", nil, []Messwert{gw("Anti-HBs", 120, "IU/l", t2)}, []string{"infektion-anti-hbs-ab-10"}},
	{"Anti-HBs unter 10", nil, []Messwert{gw("Anti-HBs", 4, "mIU/ml", t2)}, []string{"infektion-anti-hbs-unter-10"}},
	{"FEV1/FVC 0 % ist nicht messbar", nil, []Messwert{gw("FEV1/FVC", 0, "%", t2)}, nil},
	{"SpO2 von 9 % ist nicht messbar", nil, []Messwert{gw("SpO2 in Ruhe", 97, "%", t1), gw("SpO2 bei Belastungsende", 9, "%", t1)}, nil},
}

func TestEachRuleFiresOnItsPatternAndOnNoOther(t *testing.T) {
	gesehen := map[string]bool{}
	for _, f := range regelFaelle {
		got := gefeuert(f.werte, f.p)
		if strings.Join(got, ",") != strings.Join(f.will, ",") {
			t.Errorf("%s: %v, want %v", f.name, got, f.will)
		}
		for _, id := range f.will {
			gesehen[id] = true
		}
	}
	for _, x := range regelwerk.Regeln {
		if !gesehen[x.ID] {
			t.Errorf("rule %s has no case it fires on", x.ID)
		}
	}
}

func TestTheRuleTableIsSourcedAndJudgesNothing(t *testing.T) {
	if len(regelFehler) > 0 {
		t.Fatalf("rules left out: %q", regelFehler)
	}
	if len(regelwerk.Regeln) < 20 || len(regelwerk.Systeme) != 7 || len(regelwerk.Plausibel) < 20 {
		t.Fatalf("%d rules, %d systems", len(regelwerk.Regeln), len(regelwerk.Systeme))
	}
	quelle := regexp.MustCompile(`\d{4}|AWMF|Onkopedia`) // a year or a register names the document
	for _, x := range regelwerk.Regeln {
		if !quelle.MatchString(x.Quelle) {
			t.Errorf("%s: the source names no document: %q", x.ID, x.Quelle)
		}
		texte := []string{x.Bedeutung, x.Frage}
		if x.AbAlter != nil {
			texte = append(texte, x.AbAlter.Text)
		}
		for _, s := range texte {
			if m := urteilTest.FindString(s); m != "" {
				t.Errorf("%s judges: %q in %q", x.ID, m, s)
			}
		}
		if m := persoenlich.FindString(x.Bedeutung); m != "" {
			t.Errorf("%s speaks of the person: %q", x.ID, m)
		}
		if n := len([]rune(x.Bedeutung)); n > 520 {
			t.Errorf("%s: %d characters, plain German is short", x.ID, n)
		}
	}
	for _, s := range regelwerk.Systeme {
		if urteilTest.MatchString(s.Frage) || !strings.HasSuffix(s.Frage, "?") {
			t.Errorf("%s: %q", s.ID, s.Frage)
		}
	}
	// the loader drops a rule that breaks the table's rules
	kaputt := `{"systeme":[{"id":"x","name":"X","gruppen":["Lunge"],"frage":"Was?"}],"regeln":[
		{"id":"a","system":"x","wenn":[{"wert":"FEV1","lage":"unter"}],"bedeutung":"Das ist erniedrigt.","frage":"Was?","quelle":"Q 2020"},
		{"id":"b","system":"x","wenn":[{"wert":"FEV1","lage":"unter"}],"bedeutung":"Allgemein so.","frage":"Was?","quelle":""},
		{"id":"c","system":"x","wenn":[{"wert":"Zauberwert","lage":"unter"}],"bedeutung":"Allgemein so.","frage":"Was?","quelle":"Q 2020"},
		{"id":"d","system":"x","wenn":[{"wert":"FEV1","lage":"unter"}],"bedeutung":"Bei dir ist das so.","frage":"Was?","quelle":"Q 2020"},
		{"id":"e","system":"x","wenn":[{"wert":"FEV1","unter":3}],"bedeutung":"Allgemein so.","frage":"Was?","quelle":"Q 2020"},
		{"id":"f","system":"y","wenn":[{"wert":"FEV1","lage":"unter"}],"bedeutung":"Allgemein so.","frage":"Was?","quelle":"Q 2020"},
		{"id":"g","system":"x","wenn":[{"wert":"FEV1","lage":"unter","richtung":"gesunken"}],"bedeutung":"Allgemein so.","frage":"Was?","quelle":"Q 2020"},
		{"id":"h","system":"x","wenn":[{"wert":"FEV1","lage":"unter"}],"bedeutung":"Allgemein so.","frage":"Was?","quelle":"Q 2020"}]}`
	r, fehler := ladeRegeln([]byte(kaputt))
	if len(r.Regeln) != 1 || r.Regeln[0].ID != "h" || len(fehler) != 7 {
		t.Fatalf("kept %v, dropped %q", r.Regeln, fehler)
	}
	if r, fehler := ladeRegeln([]byte("{kaputt")); len(r.Regeln) != 0 || len(fehler) != 1 {
		t.Fatal("an unreadable table is no table")
	}
}

// A value read with doubt, or assumed and not confirmed, never makes a
// pattern; it is counted per system to confirm. Confirmed, it counts.
func TestUnconfirmedValuesMakeNoPattern(t *testing.T) {
	zweifel := mitLLN(gw("FEV1/FVC", 62, "%", t2), 68)
	zweifel.Pruefen, zweifel.PruefenGrund = true, "unsicher"
	vermutet := mitRef(gw("Hämoglobin", 11, "g/dl", t2), 12, 16)
	vermutet.Vermutung = &Vermutung{Name: true, GelesenName: "Haemaglobin"}
	mcv := mitRef(gw("MCV", 72, "fl", t2), 80, 96)
	gb := gesamtbild(Verlauf{Werte: []Messwert{zweifel, vermutet, mcv}}, frau(1980))
	offen := map[string]int{}
	for _, s := range gb.Systeme {
		if len(s.Treffer) > 0 {
			t.Errorf("%s: a pattern from a doubtful value: %v", s.System.ID, s.Treffer[0].Regel.ID)
		}
		offen[s.System.ID] = s.Offen
	}
	if offen["lunge"] != 1 || offen["blutbild"] != 1 || gb.Offen != 2 {
		t.Fatalf("to confirm: %v", offen)
	}
	// the same values, confirmed
	zweifel.Pruefen, vermutet.Vermutung = false, nil
	if got := gefeuert([]Messwert{zweifel, vermutet, mcv}, frau(1980)); strings.Join(got, ",") != "blutbild-mikrozytaer,blutbild-who-anaemie-frauen,lunge-feste-grenze-70,lunge-obstruktiv" {
		t.Fatalf("confirmed: %v", got)
	}
}

// A number no body can measure is a misread nobody flagged: out of every
// course and pattern, listed with its value to correct; the person's word counts.
func TestAValueNoBodyCanMeasureIsLeftOut(t *testing.T) {
	werte := []Messwert{
		gw("Peak-Flow", 453.6, "l/min", t1), gw("Peak-Flow", 4620, "l/min", t1), gw("Peak-Flow", 4722, "l/min", t1),
		gw("Peak-Flow", 431.4, "l/min", t2), gw("FEV1/FVC", 0, "%", t2),
	}
	gb := gesamtbild(Verlauf{Werte: werte}, nil)
	lunge := gb.Systeme[0]
	if lunge.Offen != 3 || len(lunge.Unmoeglich) != 3 || !strings.Contains(strings.Join(lunge.Unmoeglich, ","), "FEV1/FVC 0 % (Dez. 2023)") {
		t.Fatalf("to confirm: %d %q", lunge.Offen, lunge.Unmoeglich)
	}
	if v := strings.Join(lunge.Verlauf, "\n"); !strings.Contains(v, "Peak-Flow: 453,6 l/min (Apr. 2021) und 431,4 l/min (Dez. 2023) – kaum verändert") || strings.Contains(v, "4620") {
		t.Fatalf("the course: %q", v)
	}
	if html := gesamtbildHTML(gb, ""); !strings.Contains(html, "nicht messbar") || !strings.Contains(html, "3 Werte noch zu bestätigen") {
		t.Fatal(html)
	}
	werte[4].Korrektur = "bestaetigt"
	if got := gefeuert(werte, nil); strings.Join(got, ",") != "lunge-feste-grenze-70" {
		t.Fatalf("the person's word counts: %v", got)
	}
}

// Through the store: a value „prüfen“ is left out until the person confirms it.
func TestAConfirmationReachesTheGesamtbild(t *testing.T) {
	d, _, _ := aufbau(t)
	befund(t, d, "anna", 90, "Labor", "Labor", t2, unsicher(mitRef(mw("TSH", 6, "mU/l"), 0.4, 4)), mitRef(mw("fT4", 1.2, "ng/dl"), 0.9, 1.7))
	h := (&Web{Dienst: d, Secret: "s3cret", Admins: "admins"}).Handler()
	body := rufe(t, h, "GET", "/gesamtbild", als("anna"), "").Body.String()
	if strings.Contains(body, "r-schilddruese-latent-unterfunktion") || !strings.Contains(body, "1 Wert noch zu bestätigen") {
		t.Fatalf("unconfirmed: %s", body)
	}
	for _, w := range werteNach(t, d, "anna") {
		if w.Name == "TSH" {
			if rec := rufe(t, h, "POST", "/korrektur", als("anna"), korrektur(w.ID, "bestaetigen")); rec.Code != 303 {
				t.Fatalf("confirm: %d %s", rec.Code, rec.Body.String())
			}
		}
	}
	body = rufe(t, h, "GET", "/gesamtbild", als("anna"), "").Body.String()
	if !strings.Contains(body, "r-schilddruese-latent-unterfunktion") || strings.Contains(body, "noch zu bestätigen") {
		t.Fatalf("confirmed: %s", body)
	}
}

func TestTheAgePicksTheRulesText(t *testing.T) {
	werte := []Messwert{gw("FEV1/FVC", 68, "%", t2)}
	text := func(p *Profil) string {
		for _, s := range gesamtbild(Verlauf{Werte: werte}, p).Systeme {
			for _, x := range s.Treffer {
				return x.Text
			}
		}
		return ""
	}
	alt, jung, ohne := text(mann(1950)), text(mann(1990)), text(nil)
	if !strings.Contains(alt, "mit den Jahren von selbst kleiner") || strings.Contains(jung, "mit den Jahren") || jung != ohne || jung == "" {
		t.Fatalf("old %q, young %q, none %q", alt, jung, ohne)
	}
	// a rule that needs the profile says so instead of guessing
	gb := gesamtbild(Verlauf{Werte: []Messwert{gw("Hämoglobin", 11, "g/dl", t2)}}, &Profil{})
	if len(gb.Systeme) != 1 || !gb.Systeme[0].BrauchtProfil || len(gb.Systeme[0].Treffer) != 0 {
		t.Fatalf("%+v", gb.Systeme)
	}
}

func TestTheCourseInWordsAndWhatMovesTogether(t *testing.T) {
	werte := []Messwert{
		gw("FEV1", 4.0, "l", t1), gw("FVC", 5.0, "l", t1), gw("FEV1/FVC", 80, "%", t1),
		gw("FEV1", 3.8, "l", "2022-06-01"),
		gw("FEV1", 3.6, "l", t2), gw("FEV1", 3.7, "l", t2), gw("FVC", 4.5, "l", t2), gw("FEV1/FVC", 80, "%", t2),
		gw("Peak-Flow", 450, "l/min", t2),
		gw("Hämatokrit", 42, "%", "2019-01-18"), gw("MCV", 90, "fl", "2019-01-18"),
	}
	gb := gesamtbild(Verlauf{Werte: werte}, nil)
	if gb.Tage != 4 || gb.Von != "2019-01-18" || gb.Bis != t2 || len(gb.Systeme) != 2 {
		t.Fatalf("%d days %s–%s, %d systems", gb.Tage, gb.Von, gb.Bis, len(gb.Systeme))
	}
	lunge := gb.Systeme[0]
	alles := strings.Join(append(append(lunge.Verlauf, lunge.Einmal...), lunge.Gemeinsam...), "\n")
	for _, want := range []string{
		"FEV1: von 4 l (Apr. 2021) auf 3,6 l (2 Werte am Tag: 3,6–3,7) (Dez. 2023) gesunken, um 10 % · 3 Untersuchungstage, dazwischen 3,8 l (Juni 2022)",
		"FVC: von 5 l (Apr. 2021) auf 4,5 l (Dez. 2023) gesunken, um 10 % · 2 Untersuchungstage",
		"FEV1/FVC: 80 % (Apr. 2021) und 80 % (Dez. 2023) – kaum verändert",
		"Peak-Flow 450 l/min (Dez. 2023)",
		"Apr. 2021 bis Dez. 2023: FEV1 und FVC beide gesunken (um 10 % und 10 %), FEV1/FVC kaum verändert.",
	} {
		if !strings.Contains(alles, want) {
			t.Errorf("lacks %q in\n%s", want, alles)
		}
	}
	if !strings.HasPrefix(lunge.Verlauf[0], "FEV1:") {
		t.Errorf("the leading value first: %q", lunge.Verlauf)
	}
	blut := gb.Systeme[1]
	if blut.System.ID != "blutbild" || !blut.NurEinTag || len(blut.Gemeinsam) != 0 {
		t.Fatalf("one day of the blood count: %+v", blut)
	}
	for _, s := range []string{strings.Join(lunge.Verlauf, " "), strings.Join(lunge.Gemeinsam, " ")} {
		if m := urteilTest.FindString(s); m != "" {
			t.Fatalf("the course judges: %q", m)
		}
	}
}

// The page: fifth in the nav, the honest line, per system the four parts,
// sources, nothing judging outside the sources' titles.
func TestTheGesamtbildPage(t *testing.T) {
	d, _, _ := aufbau(t)
	akte(t, d, "anna")
	d.Speicher.ProfilSetzen("anna", Profil{Geburtsjahr: 1955, Geschlecht: "w"})
	h := (&Web{Dienst: d, Secret: "s3cret", Admins: "admins"}).Handler()
	rec := rufe(t, h, "GET", "/gesamtbild", als("anna"), "")
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, body)
	}
	for _, want := range []string{
		`<a href="/gesamtbild" aria-current=page>Gesamtbild</a></nav>`, `href="/arzt"`,
		gesamtbildHinweis, "Gebaut aus", "Untersuchungstagen zwischen",
		"Lunge und Atmung", "Verlauf", "Was solche Muster allgemein bedeuten können", "Fragen an den Arzt", "Quelle: Pellegrino R",
		`id="r-lunge-fev1-staerker-gesunken"`, "Passt auf: FEV1 von 3,9 l (Apr. 2021) auf 3,04 l (Dez. 2023), gesunken um 22 %",
		"Blutbild", "Hämatokrit", "Schilddrüse", `href="/befunde">1 Wert noch zu bestätigen</a>`,
		"Belastbarkeit (Spiroergometrie)", "Herz-Kreislauf und Blutdruck",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if !strings.Contains(body, "Infektionen und Abwehr") || strings.Contains(body, "Lues-Suchtest 4") {
		t.Error("a system with only a doubtful value: its count, never the value")
	}
	text := textOhneTags(regexp.MustCompile(`(?s)<p class="k quelle">.*?</p>`).ReplaceAllString(body, ""))
	text = strings.ReplaceAll(text, gesamtbildHinweis, "")
	if m := urteilTest.FindString(text); m != "" {
		i := strings.Index(text, m)
		t.Fatalf("the page judges: %q in …%s…", m, text[max(0, i-80):min(len(text), i+40)])
	}
	// every page has the fifth entry
	for _, pfad := range []string{"/werte", "/befunde", "/befinden", "/arzt"} {
		if !strings.Contains(rufe(t, h, "GET", pfad, als("anna"), "").Body.String(), `<a href="/gesamtbild">Gesamtbild</a>`) {
			t.Errorf("%s: no Gesamtbild in the nav", pfad)
		}
	}
	// a child's page for the admin, nobody else's
	akte(t, d, "kind-lena-muster")
	if body := rufe(t, h, "GET", "/gesamtbild?person=kind-lena-muster", als("ben", "admins"), "").Body.String(); !strings.Contains(body, `href="/befunde?person=kind-lena-muster"`) {
		t.Fatal("the child's links")
	}
	if rec := rufe(t, h, "GET", "/gesamtbild?person=anna", als("ben"), ""); rec.Code != 403 {
		t.Fatalf("another adult's: %d", rec.Code)
	}
	// nothing read yet
	if body := rufe(t, h, "GET", "/gesamtbild", als("ben"), "").Body.String(); !strings.Contains(body, "Noch keine Werte") {
		t.Fatalf("empty: %s", body)
	}
}

// „Für den Arzt“ gets a question per system a rule fits, and stays short.
func TestTheDoctorsPageTakesTheGesamtbildsQuestions(t *testing.T) {
	d, _, _ := aufbau(t)
	akte(t, d, "anna")
	h := (&Web{Dienst: d, Secret: "s3cret", Admins: "admins"}).Handler()
	body := rufe(t, h, "GET", "/arzt", als("anna"), "").Body.String()
	for _, want := range []string{"Aus dem Gesamtbild", "Lunge und Atmung: FEV1 ist stärker gesunken als FVC", `href="/gesamtbild"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the doctor's page lacks %q", want)
		}
	}
	if n := strings.Count(body, `<li class=gesamtbild-frage>`); n < 1 || n > 4 {
		t.Fatalf("%d questions from the Gesamtbild", n)
	}
	// a person without a fitting rule: no section
	befund(t, d, "ben", 91, "Labor", "Labor", t2, mw("Zauberwert", 1, ""))
	if body := rufe(t, h, "GET", "/arzt", als("ben"), "").Body.String(); strings.Contains(body, "Aus dem Gesamtbild") {
		t.Fatal("no rule fits, no section")
	}
}

// Sparse data (the box crashed on a nil pointer on 2026-10-31): no unit,
// no date, a date that is none, a zero to change from, no range, a % Soll
// without its Soll, no profile — the page and the doctor's page render.
func TestSparseValuesRenderTheGesamtbild(t *testing.T) {
	karg := []Messwert{
		{Name: "FEV1", Wert: 0, Einheit: "l", Datum: t1},
		{Name: "FEV1", Wert: 3, Einheit: "l", Datum: t2, SollProzent: zf(70)},
		{Name: "FVC", Wert: 4, Datum: t2, Soll: zf(0)},
		{Name: "FVC", Wert: 4, Datum: "2023-13-45"},
		{Name: "FEV1/FVC", Wert: 0, Datum: t2},
		{Name: "TSH", Wert: 2, Datum: ""},
		{Name: "TSH", Wert: 2, Einheit: "mU/l", Datum: t2, LLN: zf(0.4)},
		{Name: "Hämoglobin", Datum: t2, RefMax: zf(16)},
		{Name: "MCV", Datum: t2, RefMin: zf(80)},
		{Name: "SpO2 in Ruhe", Wert: 97, Einheit: "%", Datum: t2},
		{Name: "SpO2 bei Belastungsende", Wert: 90, Einheit: "", Datum: t2},
		{Name: "Blutdruck systolisch", Wert: 150, Datum: t2},
		{Name: "Zauberwert", Neu: true},
		{Name: "Zauberwert", Neu: true, Datum: t2, Gruppe: ""},
		{Name: "1.69 Ws", Neu: true, Wert: 1.69, Einheit: "Ws", Datum: t2, Gruppe: "Weitere", Pruefen: true},
	}
	for _, p := range []*Profil{nil, {}, {Geburtsjahr: 2030}, frau(1960)} {
		gb := gesamtbild(Verlauf{Werte: karg}, p)
		gesamtbildHTML(gb, "")
		gesamtbildFragen(gb)
	}
	gesamtbildHTML(gesamtbild(Verlauf{}, nil), "")
	d, _, _ := aufbau(t)
	d.Speicher.Eintragen("anna", Gelesen{Dokument: 80, Werte: karg})
	d.Speicher.Merken("80", &Eintrag{At: time.Now(), Person: "anna", Gelesen: &Gelesen{Dokument: 80}, Leser: leserVersion})
	h := (&Web{Dienst: d, Secret: "s3cret", Admins: "admins"}).Handler()
	for _, pfad := range []string{"/gesamtbild", "/arzt", "/arzt?alle=1", "/werte", "/befunde"} {
		if rec := rufe(t, h, "GET", pfad, als("anna"), ""); rec.Code != 200 {
			t.Errorf("%s: %d", pfad, rec.Code)
		}
	}
}
