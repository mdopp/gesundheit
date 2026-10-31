package main

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// --- assuming the likely name or unit of an obvious OCR misread (#15)

func TestANameIsAssumedOnlyWhenExactlyOneFits(t *testing.T) {
	for _, c := range []struct{ gelesen, vermutet string }{
		{"Haemaglobin", "Hämoglobin"},      // a letter misread
		{"EV0l", "EVol"},                   // O read as 0
		{"FEVl", "FEV1"},                   // 1 read as l
		{"Thrornbozyten", "Thrombozyten"},  // m read as rn
		{"GammaGT", "Gamma-GT"},            // the hyphen lost
		{"Hamatokrit", "Hämatokrit"},       // the umlaut lost
		{"Leukozyetn", "Leukozyten"},       // two letters swapped
		{"Kreatinin(enzym.)", ""},          // known: no assumption needed
		{"Hämoglobin", ""},                 // known
		{"Belastung naerobe Schwelle", ""}, // as near to the aerobic as to the anaerobic threshold
		{"FEF28", ""},                      // a digit is never assumed: FEV1, FEV3, FEV6 are all real
		{"FEV1%", ""},                      // a % changes what it is
		{"FVC%", ""},
		{"FIVV/FIVC", ""}, // V for 1 is no OCR confusion
		{"GOX", ""},       // short names are too close to each other
		{"FIV", ""},
		{"be ve", ""},
		{"Amylase", ""},
		{"", ""},
	} {
		a := vermuteName(c.gelesen)
		got := ""
		if a != nil {
			got = a.Name
		}
		if got != c.vermutet {
			t.Errorf("%q: assumed %q, want %q", c.gelesen, got, c.vermutet)
		}
	}
}

func TestAUnitIsAssumedOnlyWhenTheValueHasOneUnit(t *testing.T) {
	for _, c := range []struct{ name, einheit, vermutet string }{
		{"MVV", "min", "l/min"},            // not one MVV is measured in, and l/min is its only one
		{"TSH", "plU/ml", "mU/l"},          // no unit; the dictionary names one
		{"Lues-Suchtest", "ERS", ""},       // the dictionary names none
		{"Hämoglobin", "cal", ""},          // g/dl, g/l, mmol/l: three, no assumption
		{"FEV1", "x", ""},                  // l or ml
		{"Hämoglobin", "g/dl", ""},         // a plain unit
		{"MVV", "l/min", ""},               // its unit
		{"TSH", "", ""},                    // no unit read is no misread
		{"Zauberwert", "plU/ml", ""},       // an unknown value has no usual unit
		{"Schilddrüsenvolumen", "com", ""}, // ml, ccm, cm³: three spellings, no assumption
	} {
		got, ok := vermuteEinheit(finde(c.name), c.einheit)
		if (c.vermutet != "") != ok || got != c.vermutet {
			t.Errorf("%s %q: assumed %q (%v), want %q", c.name, c.einheit, got, ok, c.vermutet)
		}
	}
	if _, ok := vermuteEinheit(nil, "cal"); ok {
		t.Error("no analyte, no assumption")
	}
}

// laborAlt is the 2019 sheet as on the box: „Haemaglobin“, and the 2026
// sheet that reads Hämoglobin; an MVV in „min“, a value whose number the
// text of the page gives otherwise.
func laborAlt(t *testing.T, d *Dienst) {
	t.Helper()
	hb := mitRef(mw("Haemaglobin", 13.6, "g/dl"), 14, 18)
	hb.Neu, hb.Gruppe, hb.Pruefen, hb.PruefenGrund = true, "Weitere", true, "„Haemaglobin“ "+grundKeinWoerterbuch+"; gemeint vielleicht Hämoglobin"
	hb.Beleg, hb.Roh = "Haemaglobin 13,6 g/dl 14 - 18", "13,6"
	mvv := mw("MVV", 112.7, "min")
	mvv.Pruefen, mvv.PruefenGrund = true, "die Einheit „min“ passt nicht, MVV steht in l/min"
	ev := mw("EV0l", 120, "")
	ev.Neu, ev.Gruppe, ev.Pruefen, ev.PruefenGrund = true, "Weitere", true, "steht im Bild als 120, "+grundTextDerSeite+" 12" // a number in doubt, too
	befund(t, d, "anna", 306, "2019-01-18 Labor - Blutbild", "Labor", "2019-01-18", hb, mvv, ev)
	befund(t, d, "anna", 401, "2026-03-12 Praxislabor Nord - Blutbild", "Labor", "2026-03-12", mitRef(mw("Hämoglobin", 11.8, "g/dl"), 12, 16))
}

func TestAnAssumedNameCountsUnderItAndIsMarked(t *testing.T) {
	d, _, _ := aufbau(t)
	laborAlt(t, d)
	v, err := d.Speicher.Verlauf("anna")
	if err != nil {
		t.Fatal(err)
	}
	var hb []Messwert
	for _, m := range v.Werte {
		switch m.Name {
		case "Hämoglobin":
			hb = append(hb, m)
		case "MVV":
			if m.Einheit != "l/min" || m.Wert != 112.7 || m.Pruefen || m.Vermutung == nil || !m.Vermutung.Einheit || m.Vermutung.GelesenEinheit != "min" {
				t.Errorf("MVV in l/min, assumed, the number as read: %+v %+v", m, m.Vermutung)
			}
		case "EVol":
			if m.Wert != 120 || !m.Pruefen || !strings.Contains(m.PruefenGrund, grundTextDerSeite) || m.Vermutung == nil || !m.Vermutung.Name {
				t.Errorf("the name assumed, the number still in doubt: %+v", m)
			}
		default:
			t.Errorf("no value under the name as read: %q", m.Name)
		}
	}
	if len(hb) != 2 || hb[0].Wert != 13.6 || hb[0].Vermutung == nil || !hb[0].Vermutung.Name || hb[0].Vermutung.GelesenName != "Haemaglobin" || hb[0].Pruefen || hb[0].Gruppe != "Blutbild" || hb[1].Vermutung != nil {
		t.Fatalf("Haemaglobin is Hämoglobin's first value, marked: %+v", hb)
	}

	h := (&Web{Dienst: d, Secret: "s3cret", Admins: "admins"}).Handler()
	body := rufe(t, h, "GET", "/werte", als("anna"), "").Body.String()
	for _, want := range []string{"vermutlich Hämoglobin – bitte bestätigen", "„Haemaglobin“", "vorher 13,6 g/dl (2019)", `value=annehmen`, "vermutlich in l/min – bitte bestätigen"} {
		if !strings.Contains(body, want) {
			t.Errorf("the values page lacks %q", want)
		}
	}
	if strings.Contains(body, "<h3>Haemaglobin") {
		t.Error("no row under the name as read")
	}
	body = rufe(t, h, "GET", "/befunde", als("anna"), "").Body.String()
	for _, want := range []string{"vermutlich Hämoglobin – bitte bestätigen", "3 vermutet, bitte bestätigen", `value=annehmen`, `name=name`} {
		if !strings.Contains(body, want) {
			t.Errorf("the documents page lacks %q", want)
		}
	}

	// „Für den Arzt“: one line with a link, never a question
	body = rufe(t, h, "GET", "/arzt", als("anna"), "").Body.String()
	if !regexp.MustCompile(`<a href="/befunde">3 Werte vermutlich richtig gelesen, bitte bestätigen</a>`).MatchString(body) {
		t.Errorf("the short page has the line: %s", body)
	}
	if !strings.Contains(body, "Hämoglobin: 13,6 g/dl → 11,8 g/dl") {
		t.Errorf("Hämoglobin is compared with its assumed first value: %s", body)
	}
	alle := rufe(t, h, "GET", "/arzt?alle=1", als("anna"), "").Body.String()
	if !strings.Contains(alle, "3 Werte vermutlich richtig gelesen, bitte bestätigen") {
		t.Error("the long page has the line too")
	}
	for _, f := range arztFragen(v, nil) {
		if f.Art == "pruefen" && (f.Name == "Hämoglobin" || f.Name == "MVV") {
			t.Errorf("an assumption is no question: %+v", f)
		}
	}
}

func TestConfirmingAnAssumptionMakesItTheCorrectionAndARereadKeepsIt(t *testing.T) {
	d, _, _ := aufbau(t)
	laborAlt(t, d)
	h := (&Web{Dienst: d, Secret: "s3cret", Admins: "admins"}).Handler()
	hb := assumed(t, d, "Hämoglobin")
	rec := rufe(t, h, "POST", "/korrektur", als("anna"), korrektur(hb.ID, "annehmen", "zurueck", "/werte"))
	if rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), "/werte?m=") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	w := wertMitID(t, d, hb.ID)
	if w.Name != "Hämoglobin" || w.Wert != 13.6 || w.Einheit != "g/dl" || w.Datum != "2019-01-18" || w.Korrektur != "korrigiert" || w.Vermutung != nil || w.Gelesen == nil || w.Gelesen.Name != "Haemaglobin" {
		t.Fatalf("confirmed: %+v %+v", w, w.Gelesen)
	}
	if body := rufe(t, h, "GET", "/werte", als("anna"), "").Body.String(); strings.Contains(body, "vermutlich Hämoglobin") {
		t.Errorf("a confirmed value is no assumption: %s", body)
	}
	if body := rufe(t, h, "GET", "/befunde", als("anna"), "").Body.String(); !strings.Contains(body, "Von dir korrigiert – gelesen als Haemaglobin 13,6 g/dl am 18.01.2019") {
		t.Errorf("a confirmed value is a corrected one: %s", body)
	}
	// „Ja“ on a value without an assumption stores nothing
	if rec := rufe(t, h, "POST", "/korrektur", als("anna"), korrektur(hb.ID, "annehmen")); rec.Code != 400 {
		t.Errorf("nothing to confirm: %d", rec.Code)
	}

	// read again: the reader now reads „Hämoglobin“ on the 2019 sheet; the word stays
	befund(t, d, "anna", 306, "2019-01-18 Labor - Blutbild", "Labor", "2019-01-18", mitRef(mw("Hämoglobin", 13.6, "g/dl"), 14, 18))
	ws, _ := d.Speicher.AlleWerte("anna")
	n := 0
	for _, x := range ws {
		if x.Dokument == 306 && x.Name == "Hämoglobin" {
			n++
			if x.Korrektur != "korrigiert" || x.KorrigiertVon != "anna" {
				t.Errorf("the confirmation outlives the re-read: %+v", x)
			}
		}
	}
	if n != 1 {
		t.Fatalf("one value, not the read and the person's: %d", n)
	}
}

func TestCorrectingAnAssumptionTakesTheNameTheUnitButNeverTheNumberOnItsOwn(t *testing.T) {
	d, _, _ := aufbau(t)
	laborAlt(t, d)
	h := (&Web{Dienst: d, Secret: "s3cret", Admins: "admins"}).Handler()

	// „Korrigieren“: another name, the number as read
	hb := assumed(t, d, "Hämoglobin")
	rufe(t, h, "POST", "/korrektur", als("anna"), korrektur(hb.ID, "korrigieren", "name", "hämatokrit", "wert", "13,6", "einheit", "g/dl"))
	w := wertMitID(t, d, hb.ID)
	if w.Name != "Hämatokrit" || w.Gruppe != "Blutbild" || w.Wert != 13.6 || w.Korrektur != "korrigiert" || w.Vermutung != nil {
		t.Fatalf("named by the person: %+v", w)
	}

	// a name the dictionary does not know either: the person's, as a new one
	rufe(t, h, "POST", "/korrektur", als("anna"), korrektur(hb.ID, "korrigieren", "name", "Blutfarbstoff alt", "wert", "13,6", "einheit", "g/dl"))
	if w := wertMitID(t, d, hb.ID); w.Name != "Blutfarbstoff alt" || !w.Neu || w.Gruppe != "Weitere" || w.Korrektur != "korrigiert" {
		t.Fatalf("an unknown name of the person's: %+v", w)
	}

	// the name as read is right: the person's word wins over the assumption
	rufe(t, h, "POST", "/korrektur", als("anna"), korrektur(hb.ID, "korrigieren", "name", "Haemaglobin", "wert", "13,6", "einheit", "g/dl"))
	w = wertMitID(t, d, hb.ID)
	if w.Name != "Haemaglobin" || w.Korrektur != "bestaetigt" || w.Vermutung != nil || w.Pruefen {
		t.Fatalf("as read, by the person's word: %+v", w)
	}

	// „Stimmt so“ on an assumed unit confirms what is shown
	mvv := assumed(t, d, "MVV")
	rufe(t, h, "POST", "/korrektur", als("anna"), korrektur(mvv.ID, "bestaetigen"))
	if w := wertMitID(t, d, mvv.ID); w.Einheit != "l/min" || w.Korrektur != "korrigiert" || w.Vermutung != nil || w.Wert != 112.7 {
		t.Fatalf("the shown unit confirmed: %+v", w)
	}

	// a name too long is refused
	ev := assumed(t, d, "EVol")
	if rec := rufe(t, h, "POST", "/korrektur", als("anna"), korrektur(ev.ID, "korrigieren", "name", strings.Repeat("x", 90), "wert", "120")); rec.Code != 400 {
		t.Errorf("a name of 90 letters: %d", rec.Code)
	}
	// confirming a value whose number is in doubt keeps the number
	rufe(t, h, "POST", "/korrektur", als("anna"), korrektur(ev.ID, "annehmen"))
	if w := wertMitID(t, d, ev.ID); w.Name != "EVol" || w.Wert != 120 || w.Korrektur != "korrigiert" {
		t.Fatalf("the name confirmed, the number as read: %+v", w)
	}
}

// A dictionary name read in a short spelling of an older reader („SYS“)
// stands under the dictionary's name: no assumption, no mark.
func TestAKnownSpellingStandsUnderTheDictionarysName(t *testing.T) {
	d, _, _ := aufbau(t)
	sys := mw("SYS", 114, "mmHg")
	sys.Neu, sys.Gruppe = true, "Weitere"
	befund(t, d, "anna", 312, "Blutdruck", "Labor", "2019-03-15", sys)
	v, _ := d.Speicher.Verlauf("anna")
	if len(v.Werte) != 1 || v.Werte[0].Name != "Blutdruck systolisch" || v.Werte[0].Neu || v.Werte[0].Gruppe != "Kreislauf" || v.Werte[0].Vermutung != nil || v.Werte[0].Original != "SYS" {
		t.Fatalf("%+v", v.Werte)
	}
}

func TestSparseAssumptionsRenderOnEveryPage(t *testing.T) {
	d, _, _ := aufbau(t)
	karg := Gelesen{Dokument: 70, Werte: []Messwert{
		{Name: "Haemaglobin", Neu: true, Dokument: 70},                                  // no unit, no date, no range
		{Name: "MVV", Einheit: "min", Dokument: 70, Pruefen: true},                      // no date, no reason
		{Name: "TSH", Wert: 1.16, Einheit: "plU/ml", Datum: "2019-12-03", Dokument: 70}, // not marked prüfen at all
		{Name: "EV0l", Neu: true, Wert: 0, Datum: "2023-02-06", Dokument: 70, SollProzent: zf(0)},
	}}
	d.Speicher.Eintragen("anna", karg)
	d.Speicher.Merken("70", &Eintrag{At: time.Now(), Person: "anna", Gelesen: &Gelesen{Dokument: 70}, Leser: leserVersion})
	h := (&Web{Dienst: d, Secret: "s3cret", Admins: "admins"}).Handler()
	seiten := []string{"/werte", "/befunde", "/arzt", "/arzt?alle=1", "/export.csv"}
	for _, pfad := range seiten {
		if rec := rufe(t, h, "GET", pfad, als("anna"), ""); rec.Code != 200 {
			t.Errorf("%s: %d", pfad, rec.Code)
		}
	}
	ws, _ := d.Speicher.AlleWerte("anna")
	if len(ws) != 4 {
		t.Fatalf("%d values", len(ws))
	}
	for _, w := range ws {
		if w.Vermutung == nil {
			t.Errorf("%s: assumed", w.Name)
			continue
		}
		if rec := rufe(t, h, "POST", "/korrektur", als("anna"), korrektur(w.ID, "annehmen")); rec.Code != 303 {
			t.Errorf("confirm %s: %d %s", w.Name, rec.Code, rec.Body.String())
		}
	}
	for _, pfad := range seiten {
		if rec := rufe(t, h, "GET", pfad, als("anna"), ""); rec.Code != 200 {
			t.Errorf("after confirming %s: %d", pfad, rec.Code)
		}
	}
}

func assumed(t *testing.T, d *Dienst, name string) WertStand {
	t.Helper()
	ws, _ := d.Speicher.AlleWerte("anna")
	for _, w := range ws {
		if w.Name == name && w.Vermutung != nil {
			return w
		}
	}
	t.Fatalf("no assumed %s in %+v", name, ws)
	return WertStand{}
}

func wertMitID(t *testing.T, d *Dienst, id string) WertStand {
	t.Helper()
	ws, _ := d.Speicher.AlleWerte("anna")
	for _, w := range ws {
		if w.ID == id {
			return w
		}
	}
	t.Fatalf("no value %s", id)
	return WertStand{}
}
