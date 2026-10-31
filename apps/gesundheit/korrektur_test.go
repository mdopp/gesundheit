package main

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// --- correcting a value (#13)

func werteNach(t *testing.T, d *Dienst, key string) map[string]WertStand {
	t.Helper()
	ws, err := d.Speicher.AlleWerte(key)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]WertStand{}
	for _, w := range ws {
		out[w.Name] = w
	}
	return out
}

func korrektur(id, aktion string, felder ...string) string {
	f := url.Values{"id": {id}, "aktion": {aktion}}
	for i := 0; i+1 < len(felder); i += 2 {
		f.Set(felder[i], felder[i+1])
	}
	return f.Encode()
}

func TestACorrectionIsThePersonsAndOutlivesTheReread(t *testing.T) {
	d, pl, mo := aufbau(t)
	pl.docs[5] = &fakeDoc{ID: 5, Title: "Labor März", Content: laborAnna, Created: "2026-03-13", Type: 7}
	d.Lauf(context.Background(), "test")
	h := (&Web{Dienst: d, Secret: "s3cret", Admins: "admins"}).Handler()
	w := werteNach(t, d, "anna")
	hb := w["Hämoglobin"]
	if hb.ID == "" || hb.Beleg == "" || hb.Roh != "11,8" {
		t.Fatalf("a value keeps the words it stands on and its number as written: %+v", hb)
	}

	// correct: value, unit, date
	rec := rufe(t, h, "POST", "/korrektur", als("anna"), korrektur(hb.ID, "korrigieren", "wert", "12,8", "einheit", "g/dl", "datum", "2026-03-11", "zurueck", "/werte"))
	if rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), "/werte?m=") {
		t.Fatalf("%d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	v, _ := d.Speicher.Verlauf("anna")
	var neu *Messwert
	for i := range v.Werte {
		if v.Werte[i].Name == "Hämoglobin" {
			neu = &v.Werte[i]
		}
	}
	if neu == nil || neu.Wert != 12.8 || neu.Datum != "2026-03-11" || neu.Korrektur != "korrigiert" || neu.KorrigiertVon != "anna" || neu.Gelesen == nil || neu.Gelesen.Wert != 11.8 {
		t.Fatalf("corrected: %+v", neu)
	}
	body := rufe(t, h, "GET", "/werte", als("anna"), "").Body.String()
	if !strings.Contains(body, "von dir korrigiert") || !strings.Contains(body, "12,8 g/dl · März 2026") || !strings.Contains(body, "gelesen als 11,8 g/dl") {
		t.Fatalf("the page says so: %s", body)
	}

	// „stimmt so“ and „gehört nicht dazu“
	rufe(t, h, "POST", "/korrektur", als("anna"), korrektur(w["Ferritin"].ID, "bestaetigen"))
	rufe(t, h, "POST", "/korrektur", als("anna"), korrektur(w["CRP"].ID, "weg", "zurueck", "/befunde"))
	w = werteNach(t, d, "anna")
	if w["Ferritin"].Korrektur != "bestaetigt" || w["Ferritin"].Pruefen || !w["CRP"].Weg {
		t.Fatalf("confirmed and dropped: %+v %+v", w["Ferritin"], w["CRP"])
	}
	v, _ = d.Speicher.Verlauf("anna")
	for _, m := range v.Werte {
		if m.Name == "CRP" {
			t.Fatal("a dropped value is in no timeline")
		}
	}
	body = rufe(t, h, "GET", "/befunde", als("anna"), "").Body.String()
	for _, want := range []string{"von dir bestätigt", "gehört nicht dazu", `value=zuruecknehmen`, "von dir korrigiert"} {
		if !strings.Contains(body, want) {
			t.Errorf("the documents page lacks %q", want)
		}
	}
	if strings.Contains(rufe(t, h, "GET", "/werte", als("anna"), "").Body.String(), ">CRP") {
		t.Error("the values page has no row for a dropped value")
	}

	// read again with a new reader: the record is replaced, the person's word is not
	rec2, _ := d.Speicher.Record()
	e := rec2.Gelesen["5"]
	e.Leser = 0
	d.Speicher.Merken("5", e)
	mo.mu.Lock()
	mo.answers["Muster, Anna   geb."] = strings.NewReplacer(`"wert": "11,8"`, `"wert": "11,9"`, `"beleg": "Hämoglobin       11,8`, `"beleg": "Hämoglobin       11,9`).Replace(antwortAnna)
	mo.mu.Unlock()
	pl.docs[5].Content = strings.Replace(laborAnna, "11,8", "11,9", 1)
	vorher, _ := os.ReadFile(filepath.Join(d.Speicher.Dir, "personen", "anna", "korrekturen.json"))
	if l := d.Lauf(context.Background(), "stündlich"); l.Gelesen != 1 {
		t.Fatalf("re-read: %+v", l)
	}
	roh, _ := os.ReadFile(filepath.Join(d.Speicher.Dir, "personen", "anna", "verlauf.json"))
	if !strings.Contains(string(roh), "11.9") {
		t.Fatal("the reader did read it again")
	}
	nachher, _ := os.ReadFile(filepath.Join(d.Speicher.Dir, "personen", "anna", "korrekturen.json"))
	if string(vorher) != string(nachher) || len(nachher) == 0 {
		t.Fatal("the reader never touches the corrections")
	}
	w = werteNach(t, d, "anna")
	if w["Hämoglobin"].Wert != 12.8 || w["Hämoglobin"].Gelesen.Wert != 11.9 || w["Ferritin"].Korrektur != "bestaetigt" || !w["CRP"].Weg {
		t.Fatalf("the person's word wins over the re-read: %+v", w)
	}

	// taken back: as read again
	rufe(t, h, "POST", "/korrektur", als("anna"), korrektur(w["CRP"].ID, "zuruecknehmen"))
	if w = werteNach(t, d, "anna"); w["CRP"].Weg || w["CRP"].Korrektur != "" {
		t.Fatalf("taken back: %+v", w["CRP"])
	}

	// no number, no date, a foreign return address: refused or ignored, nothing stored
	for _, f := range []string{
		korrektur(w["Kreatinin"].ID, "korrigieren", "wert", "viel", "einheit", "mg/dl"),
		korrektur(w["Kreatinin"].ID, "korrigieren", "wert", "0,9", "datum", "gestern"),
		korrektur(w["Kreatinin"].ID, "korrigieren", "wert", "0,9", "einheit", strings.Repeat("x", 40)),
		korrektur(w["Kreatinin"].ID, "zaubern"),
	} {
		if rec := rufe(t, h, "POST", "/korrektur", als("anna"), f); rec.Code != 400 {
			t.Errorf("%s: %d", f, rec.Code)
		}
	}
	if w := werteNach(t, d, "anna"); w["Kreatinin"].Korrektur != "" {
		t.Fatal("nothing stored")
	}
	rec = rufe(t, h, "POST", "/korrektur", als("anna"), korrektur(w["Kreatinin"].ID, "bestaetigen", "zurueck", "//evil.example/x"))
	if loc := rec.Header().Get("Location"); rec.Code != 303 || !strings.HasPrefix(loc, "/werte") {
		t.Fatalf("only a path of this program: %q", loc)
	}
	// a correction that equals the reading is a confirmation
	rufe(t, h, "POST", "/korrektur", als("anna"), korrektur(w["Leukozyten"].ID, "korrigieren", "wert", "6,4", "einheit", "/nl", "datum", "2026-03-12"))
	if w := werteNach(t, d, "anna"); w["Leukozyten"].Korrektur != "bestaetigt" {
		t.Fatalf("unchanged is confirmed: %+v", w["Leukozyten"])
	}
	// the Bote reads the person's values too
	srv := &mcpServer{Token: "b0te", Tools: mcpTools(d)}
	out, _ := text(mcpCall(t, srv, "b0te", "tools/call", map[string]any{"name": "gesundheit_verlauf", "arguments": map[string]any{"person": "anna", "wert": "Hämoglobin"}}))
	if !strings.Contains(out, "2026-03-11: 12,8 g/dl") || !strings.Contains(out, "von Hand korrigiert") {
		t.Fatalf("bote: %s", out)
	}
}

func TestOnlyThePersonOrAnAdminForAChildCorrects(t *testing.T) {
	d, pl, _ := aufbau(t)
	pl.docs[5] = &fakeDoc{ID: 5, Title: "Labor März", Content: laborAnna, Created: "2026-03-13", Type: 7}
	d.Lauf(context.Background(), "test")
	lena := Gelesen{Dokument: 50, Titel: "Kinderarzt", Werte: []Messwert{{Name: "Hämoglobin", Wert: 12, Einheit: "g/dl", Datum: "2025-05-05", Dokument: 50, Pruefen: true}}}
	d.Speicher.Eintragen("kind-lena-muster", lena)
	d.Speicher.Merken("50", &Eintrag{At: time.Now(), Person: "kind-lena-muster", Gelesen: &lena, Leser: leserVersion})
	h := (&Web{Dienst: d, Secret: "s3cret", Admins: "admins"}).Handler()
	anna := werteNach(t, d, "anna")["Hämoglobin"].ID
	kind := werteNach(t, d, "kind-lena-muster")["Hämoglobin"].ID
	for _, f := range []struct {
		wer    map[string]string
		person string
		id     string
		code   int
	}{
		{map[string]string{}, "anna", anna, 401},
		{als("ben"), "anna", anna, 403},
		{als("ben", "admins"), "anna", anna, 403}, // an admin sees no other adult's values
		{als("ben"), "kind-lena-muster", kind, 403},
		{als("anna"), "anna", kind, 404}, // the child's value is not in anna's timeline
		{als("ben", "admins"), "kind-lena-muster", kind, 303},
		{als("anna"), "anna", anna, 303},
	} {
		rec := rufe(t, h, "POST", "/korrektur", f.wer, korrektur(f.id, "bestaetigen", "person", f.person))
		if rec.Code != f.code {
			t.Errorf("%v on %s: %d, want %d", f.wer, f.person, rec.Code, f.code)
		}
	}
	if w := werteNach(t, d, "kind-lena-muster")["Hämoglobin"]; w.Korrektur != "bestaetigt" || w.KorrigiertVon != "ben" {
		t.Fatalf("the admin's confirmation: %+v", w)
	}
	// another admin sees who confirmed it
	body := rufe(t, h, "GET", "/werte?person=kind-lena-muster", als("chris", "admins"), "").Body.String()
	if !strings.Contains(body, "von ben bestätigt") {
		t.Fatalf("whose word: %s", body)
	}
}

// A correction whose value a re-read names otherwise is kept: by its
// number when the re-read has it, as the person's own value when not, and
// it goes with the document when the document goes to someone else.
func TestACorrectionFindsItsValueAfterARereadRenamesIt(t *testing.T) {
	d, _, _ := aufbau(t)
	s := d.Speicher
	alt := Gelesen{Dokument: 9, Titel: "Spirometrie", Werte: []Messwert{
		{Name: "FIV", Neu: true, Wert: 3.61, Einheit: "l", Datum: "2023-12-19", Dokument: 9},
		{Name: "be ve", Neu: true, Wert: 7.09, Einheit: "l/s", Datum: "2023-12-19", Dokument: 9},
		{Name: "FEV1", Wert: 3.04, Einheit: "l", Datum: "2023-12-19", Dokument: 9},
		{Name: "FEV1", Wert: 3.22, Einheit: "l", Datum: "2023-12-19", Dokument: 9}, // the same name twice on one sheet
	}}
	s.Eintragen("anna", alt)
	s.Merken("9", &Eintrag{At: time.Now(), Person: "anna", Gelesen: &alt, Leser: 1})
	w, _ := s.AlleWerte("anna")
	ids := map[string]string{}
	for _, x := range w {
		ids[x.Name+"="+zahlText(x.Wert)] = x.ID
	}
	if ids["FEV1=3,04"] == ids["FEV1=3,22"] || ids["FEV1=3,04"] == "" {
		t.Fatalf("two values of one name have two ids: %v", ids)
	}
	setze := func(id string, k Korrektur) {
		t.Helper()
		ws, _ := s.AlleWerte("anna")
		for _, x := range ws {
			if x.ID == id {
				k.Von, k.At = "anna", time.Now()
				if err := s.KorrekturSetzen("anna", x, k); err != nil {
					t.Fatal(err)
				}
				return
			}
		}
		t.Fatalf("no value %s", id)
	}
	setze(ids["FIV=3,61"], Korrektur{Aktion: "korrigiert", Wert: zf(3.66), Einheit: "l", Datum: "2023-12-19"})
	setze(ids["be ve=7,09"], Korrektur{Aktion: "bestaetigt"})
	setze(ids["FEV1=3,22"], Korrektur{Aktion: "weg"})
	// the new reader names FIV as FIV1 with the same number, and reads no „be ve“ at all
	neu := Gelesen{Dokument: 9, Titel: "Spirometrie", Werte: []Messwert{
		{Name: "FIV1", Wert: 3.61, Einheit: "l", Datum: "2023-12-19", Dokument: 9},
		{Name: "FEV1", Wert: 3.04, Einheit: "l", Datum: "2023-12-19", Dokument: 9},
		{Name: "FEV1", Wert: 3.22, Einheit: "l", Datum: "2023-12-19", Dokument: 9},
	}}
	s.Eintragen("anna", neu)
	v, _ := s.Verlauf("anna")
	var got []string
	for _, m := range v.Werte {
		got = append(got, m.Name+"="+zahlText(m.Wert)+":"+m.Korrektur)
	}
	want := "FIV1=3,66:korrigiert FEV1=3,04: be ve=7,09:bestaetigt"
	if strings.Join(got, " ") != want {
		t.Fatalf("got  %s\nwant %s", strings.Join(got, " "), want)
	}
	// the document goes to ben: anna's word on it goes out of her timeline with it
	e, _ := s.Record()
	e.Gelesen["9"].Person = "ben"
	s.Merken("9", e.Gelesen["9"])
	s.Austragen("anna", 9)
	if v, _ := s.Verlauf("anna"); len(v.Werte) != 0 {
		t.Fatalf("nothing of document 9 is anna's: %+v", v.Werte)
	}
}

// Sparse values: no unit, no date, no range, a % Soll without its Soll,
// a name of nothing but a number — every page renders.
func TestSparseValuesRenderOnEveryPage(t *testing.T) {
	d, pl, _ := aufbau(t)
	pl.docs[60] = &fakeDoc{ID: 60, Title: "Zettel", Archiv: []byte("%PDF-1.4 kaputt")}
	karg := Gelesen{Dokument: 60, Werte: []Messwert{
		{Name: "Zauberwert", Neu: true, Dokument: 60},
		{Name: "FEV1", Wert: 3.2, SollProzent: zf(75), Datum: "2023-12-19", Dokument: 60, Pruefen: true},
		{Name: "FEV1", Wert: 0, Einheit: "l", Datum: "2021-01-01", Dokument: 60, Soll: zf(0)},
		{Name: "1.69 Ws", Neu: true, Wert: 1.69, Einheit: "Ws", Datum: "2023-12-19", Dokument: 60, Gruppe: "Weitere", Pruefen: true},
		{Name: "TSH", Wert: 4, Datum: "2018-02-27", Dokument: 60, LLN: zf(0.3)},
		{Name: "TSH", Wert: 1.16, Einheit: "plU/ml", Datum: "2019-12-03", Dokument: 60, RefMin: zf(0.3)},
		{Name: "Hämoglobin", Wert: 12, Einheit: "g/dl", Datum: "", Dokument: 60, RefMax: zf(16)},
	}}
	d.Speicher.Eintragen("anna", karg)
	d.Speicher.Merken("60", &Eintrag{At: time.Now(), Person: "anna", Gelesen: &Gelesen{Dokument: 60}, Leser: leserVersion}) // a record without title, art or date
	d.Speicher.Merken("61", &Eintrag{At: time.Now(), Person: "anna", Fehler: "nicht lesbar"})
	h := (&Web{Dienst: d, Secret: "s3cret", Admins: "admins"}).Handler()
	for _, pfad := range []string{"/werte", "/befunde", "/arzt", "/arzt?alle=1", "/befinden", "/export.csv", "/gesamtbild"} {
		if rec := rufe(t, h, "GET", pfad, als("anna"), ""); rec.Code != 200 {
			t.Errorf("%s: %d", pfad, rec.Code)
		}
	}
	ws, _ := d.Speicher.AlleWerte("anna")
	for _, w := range ws {
		if rec := rufe(t, h, "GET", "/schnipsel?id="+w.ID, als("anna"), ""); rec.Code != 404 && rec.Code != 200 {
			t.Errorf("snippet of %s: %d", w.Name, rec.Code)
		}
		if rec := rufe(t, h, "POST", "/korrektur", als("anna"), korrektur(w.ID, "korrigieren", "wert", "1", "einheit", "", "datum", "")); rec.Code != 303 {
			t.Errorf("correct %s: %d %s", w.Name, rec.Code, rec.Body.String())
		}
	}
	for _, pfad := range []string{"/werte", "/befunde", "/arzt", "/arzt?alle=1", "/gesamtbild"} {
		if rec := rufe(t, h, "GET", pfad, als("anna"), ""); rec.Code != 200 {
			t.Errorf("after the corrections %s: %d", pfad, rec.Code)
		}
	}
	// a broken corrections file hides nothing
	os.WriteFile(filepath.Join(d.Speicher.Dir, "personen", "anna", "korrekturen.json"), []byte("{kaputt"), 0o600)
	if v, err := d.Speicher.Verlauf("anna"); err != nil || len(v.Werte) != len(karg.Werte) {
		t.Fatalf("as read: %d values, %v", len(v.Werte), err)
	}
	_ = strconv.Itoa
}
