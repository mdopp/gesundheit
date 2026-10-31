package main

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// --- „Für den Arzt“, short, per examination (#13)

// befund files one document for a person, as a run would: its values in
// the timeline, its title and kind in the record.
func befund(t *testing.T, d *Dienst, person string, doc int, titel, art, datum string, werte ...Messwert) {
	t.Helper()
	for i := range werte {
		werte[i].Dokument = doc
		if werte[i].Datum == "" {
			werte[i].Datum = datum
		}
		if a := finde(werte[i].Name); a != nil && werte[i].Gruppe == "" {
			werte[i].Gruppe = a.Gruppe
		} else if werte[i].Gruppe == "" {
			werte[i].Gruppe, werte[i].Neu = "Weitere", true
		}
	}
	g := Gelesen{Dokument: doc, Titel: titel, Art: art, Datum: datum, Werte: werte}
	if err := d.Speicher.Eintragen(person, g); err != nil {
		t.Fatal(err)
	}
	if err := d.Speicher.Merken(itoa(doc), &Eintrag{At: time.Now(), Person: person, Gelesen: &g, Leser: leserVersion}); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string { return strings.TrimSpace(zahlText(float64(n))) }

func mw(name string, wert float64, einheit string) Messwert {
	return Messwert{Name: name, Wert: wert, Einheit: einheit}
}

func mitRef(m Messwert, lo, hi float64) Messwert { m.RefMin, m.RefMax = zf(lo), zf(hi); return m }
func mitSoll(m Messwert, p float64) Messwert     { m.SollProzent = zf(p); return m }
func unsicher(m Messwert) Messwert               { m.Pruefen, m.PruefenGrund = true, "unsicher"; return m }

// akte is a person's documents shaped as on the box (2026-10-31): several
// documents of one lung-function day, one name twice on a sheet, a unit
// missing on one sheet and not on the next, a misread, a spiroergometry
// whose SpO2 belongs to it, a 24-hour blood pressure read as „Labor“,
// TSH in two units, a lone serology value, values without unit and date.
func akte(t *testing.T, d *Dienst, p string) {
	befund(t, d, p, 296, "2021-04-12 Praxis Lunge - Spirometrie-Befund", "Lungenfunktion", "2021-04-12",
		mitSoll(mw("FEV1", 3.9, "l"), 91), mitSoll(mw("FVC", 4.8, "l"), 91), mw("FEV1/FVC", 81, "%"), mw("Peak-Flow", 470, "l/min"), mw("FEF50", 3.6, "l/s"), mw("MVV", 130, "l/min"),
		mw("FEF75", 1.9, "l/s"), mw("FIVC", 4.7, "l"), mw("FET", 3.1, "s"))
	befund(t, d, p, 317, "2023-12-19 Zimmer MedizinSysteme - Lungenfunktionsprüfung", "Lungenfunktion", "2023-12-19",
		mitSoll(mw("FEV1", 3.22, ""), 75), mitSoll(mw("FVC", 3.99, ""), 75), mw("Lungenalter", 73, "Jahre"))
	befund(t, d, p, 337, "2023-12-19 Zimmer MedizinSysteme - Lungenfunktionsprüfung (2)", "Lungenfunktion", "2023-12-19",
		mitSoll(mw("FEV1", 3.22, "l"), 75), mw("FVC", 3.99, "l"), mw("Peak-Flow", 431.4, "l/min"), mw("Peak-Flow", 393, "l/min"))
	befund(t, d, p, 272, "2023-12-19 Meyer Mortzeck - FVC-Messung", "Lungenfunktion", "2023-12-19",
		mitSoll(mw("FEV1", 3.04, "l"), 71), mw("FVC", 4.14, "l"), mw("FEF50", 2.65, "l/s"), mw("PIF", 7.59, "l/s"))
	befund(t, d, p, 292, "2023-12-19 Meyer Mortzeck - Spirometrie-Befund", "Lungenfunktion", "2023-12-19",
		mw("FEV6", 13.99, "l"), unsicher(mw("1.69 Ws", 1.69, "Ws")), mw("be ve", 7.09, "l/s"), mw("FEF50", 2.53, "l/s"),
		mw("FEF75", 1.18, "l/s"), mw("FIVC", 4.01, "l"), mw("FET", 3.14, "s"))
	befund(t, d, p, 303, "2020-01-06 Dres. Beispiel - Spiroergometrie Verlaufskontrolle", "Lungenfunktion", "2020-01-06",
		mw("Maximale Sauerstoffaufnahme", 35, "ml/min/kg"), mw("Maximale Belastung", 269, "Watt"), mw("Maximale Herzfrequenz", 179, "/Minute"), mw("SpO2 in Ruhe", 96, "%"), mw("VE/VCO2-Slope", 21.5, ""))
	befund(t, d, p, 306, "2019-01-18 Labor - Laborbefund Blutbild", "Labor", "2019-01-18",
		mitRef(mw("Hämatokrit", 42, "%"), 33, 52), mitRef(mw("Leukozyten", 5.4, "/nl"), 4, 10), mitRef(mw("Thrombozyten", 255, "/nl"), 150, 400), unsicher(mitRef(mw("Haemaglobin", 13.6, "g/dl"), 14, 18)))
	befund(t, d, p, 401, "2026-03-12 Praxislabor Nord - Blutbild", "Labor", "2026-03-12",
		mitRef(mw("Hämatokrit", 31, "%"), 33, 52), mitRef(mw("Leukozyten", 6.4, "/nl"), 4, 10), mitRef(mw("Thrombozyten", 240, "/nl"), 150, 400), mitRef(mw("Hämoglobin", 11.8, "g/dl"), 12, 16))
	befund(t, d, p, 300, "2018-02-27 Röntgen-Institut - Schilddrüse", "Arztbrief", "2018-02-27", mw("TSH", 4, ""))
	befund(t, d, p, 308, "2019-12-03 Labor - TSH basal", "Labor", "2019-12-03", unsicher(mitRef(mw("TSH", 1.16, "plU/ml"), 0.3, 3.69)))
	befund(t, d, p, 307, "Laborarztpraxis - Hepatitis", "Labor", "2013-01-13", unsicher(mw("Lues-Suchtest", 4, "ERS")))
	befund(t, d, p, 312, "2019-03-15 Ärztehaus - Langzeit-Blutdruckmessung", "Labor", "2019-03-15",
		mw("SYS", 104, "mmHg"), mw("DIAS", 70, "mmHg"), mw("Puls", 98, "1/min"), mw("SYS", 114, "mmHg"), mw("DIAS", 79, "mmHg"), mw("Puls", 90, "1/min"))
	befund(t, d, p, 500, "", "", "", Messwert{Name: "Zauberwert"})
}

func TestTheDoctorsPageIsShortPerExamination(t *testing.T) {
	d, _, _ := aufbau(t)
	akte(t, d, "anna")
	v, _ := d.Speicher.Verlauf("anna")
	rec, _ := d.Speicher.Record()
	k := arztKurz(v, rec, nil)
	var titel []string
	for _, b := range k.Bloecke {
		titel = append(titel, b.Titel())
	}
	want := []string{"Blutbild: März 2026 gegenüber Jan. 2019", "Lungenfunktion: Dez. 2023 gegenüber Apr. 2021", "Schilddrüse: Dez. 2019 gegenüber Feb. 2018"}
	if strings.Join(titel, " | ") != strings.Join(want, " | ") {
		t.Fatalf("blocks:\n %s\nwant\n %s", strings.Join(titel, "\n "), strings.Join(want, "\n "))
	}
	blut, lunge, sd := k.Bloecke[0], k.Bloecke[1], k.Bloecke[2]
	// across the range first
	if blut.Zeilen[0].Name != "Hämatokrit" || !blut.Zeilen[0].Gekreuzt {
		t.Fatalf("the value that left its range leads: %+v", blut.Zeilen[0])
	}
	// outside its range on the later day, with nothing to compare it to: still said
	if len(blut.NurNeu) != 1 || !strings.Contains(blut.NurNeu[0], "Hämoglobin: 11,8 g/dl – nur in dieser Untersuchung, laut Befund unter dem Bereich") {
		t.Fatalf("only in the later examination: %q", blut.NurNeu)
	}
	// one value per day: the middle of the day's values in the unit both days share
	zeilen := map[string]Vergleich{}
	for _, z := range lunge.Zeilen {
		zeilen[z.Name] = z
	}
	if z := zeilen["FEV1"]; z.B.Wert != 3.04 || z.A.Wert != 3.9 || z.BAnzahl != 2 || z.B.Einheit != "l" {
		t.Fatalf("FEV1: %+v", z)
	}
	if len(lunge.Zeilen) != arztProGruppe || lunge.Weitere != 2 {
		t.Fatalf("a few values per block, the rest counted: %d shown, %d more", len(lunge.Zeilen), lunge.Weitere)
	}
	if lunge.Zeilen[0].Name != "FEV1" || lunge.Zeilen[1].Name != "FVC" {
		t.Fatalf("among the big moves the leading values first: %s, %s", lunge.Zeilen[0].Name, lunge.Zeilen[1].Name)
	}
	for _, z := range lunge.Zeilen {
		if z.Name == "SpO2 in Ruhe" || z.Name == "FEV6" || z.Name == "be ve" {
			t.Fatalf("%s is no value of both examinations", z.Name)
		}
	}
	if len(lunge.Fragen) < 1 || len(lunge.Fragen) > 2 || !strings.Contains(lunge.Fragen[0], "FEV1 und FVC") {
		t.Fatalf("one or two questions on the whole: %q", lunge.Fragen)
	}
	// TSH in two units: not compared, said so
	if len(sd.Zeilen) != 0 || len(sd.NichtVergleichbar) != 1 || len(sd.Fragen) == 0 {
		t.Fatalf("thyroid: %+v", sd)
	}
	// the spiroergometry keeps its SpO2, the 24-hour sheet read as „Labor“ is blood pressure
	dok := dokumentArten(v.Werte, rec)
	for _, m := range v.Werte {
		u := untersuchungVon(m, dok)
		switch {
		case m.Dokument == 303 && (u < 0 || untersuchungen[u].Name != "Belastungstest (Spiroergometrie)"):
			t.Errorf("%s of the spiroergometry: %d", m.Name, u)
		case m.Dokument == 312 && (u < 0 || untersuchungen[u].Name != "Blutdruck und Puls"):
			t.Errorf("%s of the 24-hour sheet: %d", m.Name, u)
		case m.Name == "Haemaglobin" && (u < 0 || untersuchungen[u].Name != "Blutbild"):
			t.Errorf("a misread name on a blood count sheet: %d", u)
		}
	}
	// the rest: one examination each
	aus := strings.Join(k.Ausserdem, "\n")
	for _, want := range []string{"Belastungstest", "Jan. 2020", "Blutdruck und Puls", "März 2019", "Lues-Suchtest"} {
		if !strings.Contains(aus, want) {
			t.Errorf("Außerdem lacks %q:\n%s", want, aus)
		}
	}
	if len(k.Ausserdem) > arztAusserdem+1 {
		t.Fatalf("Außerdem stays short: %d", len(k.Ausserdem))
	}
	if k.Unsicher == 0 {
		t.Fatal("the unsure values are counted for the person")
	}
}

// extended guard (#13): besides the explanations' words, no direction
// that judges, no alarm, no therapy
var urteilTest = regexp.MustCompile(`(?i)\bsollte|\bsolltest|empfehl|\brat(e|en)\b|gefährlich|bedenklich|\bkrank|erhöht|erniedrigt|zu hoch|zu niedrig|normal|\bgesund\b|diagnos|behandl|auffällig|besorg|verschlechter|verbesser|schlechter|\bbesser|alarm|kritisch|harmlos|therapie|ursache`)

func TestTheDoctorsPageRendersShortAndJudgesNothing(t *testing.T) {
	d, _, _ := aufbau(t)
	akte(t, d, "anna")
	h := (&Web{Dienst: d, Secret: "s3cret", Admins: "admins"}).Handler()
	rec := rufe(t, h, "GET", "/arzt", als("anna"), "")
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, body)
	}
	for _, want := range []string{
		"Lungenfunktion: Dez. 2023 gegenüber Apr. 2021", "FEV1: 3,9 l → 3,04 l", "gesunken um 22 %", "91 → 71 % des Sollwerts", "2 Werte am Tag: 3,04–3,22",
		"Hämatokrit: 42 % → 31 %", "vorher im Bereich, jetzt unter dem Bereich",
		"Außerdem", `href="/arzt?alle=1"`, "alle Einzelwerte", "@media print",
		"unsicher gelesen", `href="/befunde"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the short page lacks %q", want)
		}
	}
	text := textOhneTags(body)
	if m := urteilTest.FindString(text); m != "" {
		t.Fatalf("the page judges: %q", m)
	}
	if m := urteil.FindString(text); m != "" {
		t.Fatalf("the page judges by its own guard: %q", m)
	}
	if n := len([]rune(strings.Join(strings.Fields(text), " "))); n > 3200 {
		t.Fatalf("short: %d characters", n)
	}
	// the long list stays one link away, for a child with the child's name
	alle := rufe(t, h, "GET", "/arzt?alle=1", als("anna"), "").Body.String()
	if !strings.Contains(alle, "Werte außerhalb ihres Bereichs") || !strings.Contains(alle, `href="/arzt"`) {
		t.Fatalf("all values: %s", alle)
	}
	akte(t, d, "kind-lena-muster")
	if body := rufe(t, h, "GET", "/arzt?person=kind-lena-muster", als("ben", "admins"), "").Body.String(); !strings.Contains(body, `href="/arzt?alle=1&amp;person=kind-lena-muster"`) {
		t.Fatal("the child's long list")
	}
	// nothing read yet
	if body := rufe(t, h, "GET", "/arzt", als("ben"), "").Body.String(); !strings.Contains(body, "Noch keine Werte") {
		t.Fatalf("empty: %s", body)
	}
}

// The questions are generated: whatever moved, they ask and never judge.
func TestTheQuestionsAskAndNeverJudge(t *testing.T) {
	zeilen := func(rels ...float64) []Vergleich {
		var out []Vergleich
		for i, r := range rels {
			out = append(out, Vergleich{Name: []string{"FEV1", "FVC", "Peak-Flow"}[i], Rel: r, Richtung: richtungVon(r), Deutlich: r > deutlichSchwelle || r < -deutlichSchwelle})
		}
		return out
	}
	for _, f := range [][]Vergleich{zeilen(), zeilen(0.01), zeilen(-0.3), zeilen(-0.3, -0.2), zeilen(0.3, -0.2), zeilen(0.02, 0.01, -0.03), append(zeilen(0.3), Vergleich{Name: "Hämatokrit", Gekreuzt: true, Nachher: "unter dem Bereich"})} {
		b := Block{Art: "Lungenfunktion", Zeilen: f}
		fr := blockFragen(b)
		if len(fr) < 1 || len(fr) > 2 {
			t.Fatalf("%+v: %q", f, fr)
		}
		for _, q := range fr {
			if !strings.HasSuffix(q, "?") || urteilTest.MatchString(q) {
				t.Fatalf("not a neutral question: %q", q)
			}
		}
	}
	if q := ohneUrteil("Ist das zu hoch?", "Was sagt der Vergleich?"); q != "Was sagt der Vergleich?" {
		t.Fatalf("the guard replaces: %q", q)
	}
}

// A correction reaches the doctor's page: TSH in one unit is compared, a
// dropped misread is gone.
func TestACorrectionReachesTheDoctorsPage(t *testing.T) {
	d, _, _ := aufbau(t)
	akte(t, d, "anna")
	ws, _ := d.Speicher.AlleWerte("anna")
	for _, w := range ws {
		switch {
		case w.Name == "TSH":
			d.Speicher.KorrekturSetzen("anna", w, Korrektur{Aktion: "korrigiert", Wert: zf(w.Wert), Einheit: "mU/l", Datum: w.Datum, Von: "anna"})
		case w.Name == "FEF50" && w.Wert == 2.53:
			d.Speicher.KorrekturSetzen("anna", w, Korrektur{Aktion: "weg", Von: "anna"})
		}
	}
	v, _ := d.Speicher.Verlauf("anna")
	rec, _ := d.Speicher.Record()
	k := arztKurz(v, rec, nil)
	for _, b := range k.Bloecke {
		if b.Art == "Schilddrüse" && (len(b.Zeilen) != 1 || b.Zeilen[0].Rel > -0.7) {
			t.Fatalf("TSH compared now: %+v", b)
		}
		for _, z := range b.Zeilen {
			if z.Name == "FEF50" && z.BAnzahl != 1 {
				t.Fatalf("the dropped one is not counted: %+v", z)
			}
		}
	}
}
