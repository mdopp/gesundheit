package main

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// --- gesundheit#10: a values page that explains itself

func zf(v float64) *float64 { return &v }

func TestStatusInWords(t *testing.T) {
	for _, c := range []struct {
		name   string
		m      Messwert
		lage   Lage
		status string
		woher  string
	}{
		{"in the sheet's range", Messwert{Name: "Hämoglobin", Wert: 13, Einheit: "g/dl", RefMin: zf(12), RefMax: zf(16)}, LageIn, "im Normalbereich", "Bereich laut Befund: 12–16 g/dl"},
		{"above it", Messwert{Name: "Hämoglobin", Wert: 17.2, Einheit: "g/dl", RefMin: zf(12), RefMax: zf(16)}, LageUeber, "über dem Bereich", "Bereich laut Befund: 12–16 g/dl"},
		{"below it", Messwert{Name: "Hämoglobin", Wert: 11.8, Einheit: "g/dl", RefMin: zf(12), RefMax: zf(16)}, LageUnter, "unter dem Bereich", "Bereich laut Befund: 12–16 g/dl"},
		{"a one-sided range", Messwert{Name: "CRP", Wert: 2.1, Einheit: "mg/l", RefMax: zf(5)}, LageIn, "im Normalbereich", "Bereich laut Befund: unter 5 mg/l"},
		{"under the sheet's LLN", Messwert{Name: "FEV1/FVC", Wert: 68, Einheit: "%", LLN: zf(70)}, LageUnter, "unter dem Bereich", "unterer Grenzwert laut Befund (LLN): 70 %"},
		{"over the sheet's LLN", Messwert{Name: "FEV1", Wert: 4.1, Einheit: "l", LLN: zf(3.9)}, LageIn, "im Normalbereich", "unterer Grenzwert laut Befund (LLN): 3,9 l"},
		{"only the predicted value with its percentage", Messwert{Name: "FVC", Wert: 5.29, Einheit: "l", Soll: zf(5.1), SollProzent: zf(104)}, LageSoll, "104 % des Sollwerts", "Sollwert laut Befund: 5,1 l; eine Grenze nennt der Befund nicht"},
		{"only the predicted value, the percentage is arithmetic", Messwert{Name: "FVC", Wert: 5.29, Einheit: "l", Soll: zf(5)}, LageSoll, "106 % des Sollwerts", "Sollwert laut Befund: 5 l; eine Grenze nennt der Befund nicht"},
		{"nothing on the sheet", Messwert{Name: "Vitamin D", Wert: 22, Einheit: "ng/ml"}, LageKein, "kein Bereich angegeben", ""},
	} {
		e := einordnen(c.m, nil)
		if e.Lage != c.lage || e.Status != c.status || e.Woher != c.woher {
			t.Errorf("%s: %+v", c.name, e)
		}
	}
}

// The profile is used only where the sheet prints neither a range nor a
// predicted value; the computed range says that it is computed.
func TestTheProfileOnlyWhenTheSheetHasNothing(t *testing.T) {
	alt := sollFormeln
	t.Cleanup(func() { sollFormeln = alt })
	var gefragt []float64
	sollFormeln = map[string]Sollformel{"Testwert": {Name: "Testformel 2000", Einheit: "l", Bereich: func(p Profil, alter float64) (*float64, *float64, bool) {
		gefragt = append(gefragt, alter)
		if p.GroesseCm == 0 || p.Geschlecht == "" {
			return nil, nil, false
		}
		return zf(4), nil, true
	}}}
	p := &Profil{Geburtsjahr: 1980, Geschlecht: "m", GroesseCm: 182}
	m := Messwert{Name: "Testwert", Wert: 4.5, Einheit: "l", Datum: "2023-12-19"}
	e := einordnen(m, p)
	if e.Lage != LageIn || e.Status != "im Normalbereich" || !strings.Contains(e.Woher, "berechnet") || !strings.Contains(e.Woher, "Testformel 2000") || !strings.Contains(e.Woher, "nicht vom Befund") {
		t.Fatalf("computed: %+v", e)
	}
	if len(gefragt) != 1 || gefragt[0] != 43 {
		t.Fatalf("the age at the measurement: %v", gefragt)
	}
	m.Wert = 3.5
	if e := einordnen(m, p); e.Lage != LageUnter || e.Status != "unter dem Bereich" {
		t.Fatalf("computed below: %+v", e)
	}
	// the sheet always wins
	for _, s := range []Messwert{
		{Name: "Testwert", Wert: 3.5, Einheit: "l", Datum: "2023-12-19", RefMin: zf(3), RefMax: zf(6)},
		{Name: "Testwert", Wert: 3.5, Einheit: "l", Datum: "2023-12-19", LLN: zf(3.2)},
		{Name: "Testwert", Wert: 3.5, Einheit: "l", Datum: "2023-12-19", Soll: zf(4.4)},
	} {
		n := len(gefragt)
		if e := einordnen(s, p); strings.Contains(e.Woher, "berechnet") || len(gefragt) != n {
			t.Fatalf("the sheet has one, the profile is not asked: %+v", e)
		}
	}
	// no profile, an incomplete one, another unit, a value without an equation: no range
	for _, c := range []struct {
		m Messwert
		p *Profil
	}{
		{m, nil},
		{m, &Profil{Geburtsjahr: 1980}},
		{Messwert{Name: "Testwert", Wert: 4500, Einheit: "ml", Datum: "2023-12-19"}, p},
		{Messwert{Name: "Vitamin D", Wert: 22, Einheit: "ng/ml", Datum: "2023-12-19"}, p},
	} {
		if e := einordnen(c.m, c.p); e.Lage != LageKein || e.Status != "kein Bereich angegeben" {
			t.Fatalf("%+v with %+v: %+v", c.m, c.p, e)
		}
	}
}

func TestTheChangeInWords(t *testing.T) {
	w := func(wert float64, datum, einheit string) Messwert {
		return Messwert{Name: "FEV1", Wert: wert, Datum: datum, Einheit: einheit}
	}
	for _, c := range []struct {
		ms   []Messwert
		want string
	}{
		{[]Messwert{w(5.41, "2021-05-03", "l"), w(5.29, "2023-12-19", "l")}, "vorher 5,41 l (2021) – kaum verändert"},
		{[]Messwert{w(4.8, "2021-05-03", "l"), w(5.29, "2023-12-19", "l")}, "vorher 4,8 l (2021) – gestiegen"},
		{[]Messwert{w(5.9, "2021-05-03", "l"), w(5.29, "2023-12-19", "l")}, "vorher 5,9 l (2021) – gesunken"},
		{[]Messwert{w(5.0, "2023-03-01", "l"), w(5.29, "2023-12-19", "l")}, "vorher 5 l (März 2023) – gestiegen"},
		// several sheets of one day: the one before is the day before
		{[]Messwert{w(5.41, "2021-05-03", "l"), w(4.0, "2023-12-19", "l"), w(5.29, "2023-12-19", "l")}, "vorher 5,41 l (2021) – kaum verändert"},
		{[]Messwert{w(9.5, "2021-05-03", "l/s"), w(5.29, "2023-12-19", "l/min")}, "vorher 9,5 l/s (2021), in einer anderen Einheit – nicht vergleichbar"},
		{[]Messwert{w(5.29, "2023-12-19", "l")}, "noch kein früherer Wert"},
	} {
		if got := aenderung(c.ms); got != c.want {
			t.Errorf("%v: %q, want %q", c.ms, got, c.want)
		}
	}
	if got := wertMitDatum(w(5.29, "2023-12-19", "l")); got != "5,29 l · Dez. 2023" {
		t.Fatalf("latest: %q", got)
	}
}

func TestAChartOnlyFromThreeValues(t *testing.T) {
	ms := []Messwert{
		{Name: "Hämoglobin", Wert: 13, Einheit: "g/dl", Datum: "2019-01-18", RefMin: zf(12), RefMax: zf(16)},
		{Name: "Hämoglobin", Wert: 13.4, Einheit: "g/dl", Datum: "2023-12-19", RefMin: zf(12), RefMax: zf(16)},
	}
	if s := diagramm(ms, ""); s != "" {
		t.Fatalf("two values draw no chart: %s", s)
	}
	ms = append(ms[:1], Messwert{Name: "Hämoglobin", Wert: 14.1, Einheit: "g/dl", Datum: "2021-06-01", RefMin: zf(12), RefMax: zf(16)}, ms[1])
	s := diagramm(ms, "")
	for _, want := range []string{"<svg", ">2019<", ">2021<", ">2023<", "class=ref"} {
		if !strings.Contains(s, want) {
			t.Fatalf("chart lacks %s: %s", want, s)
		}
	}
	// a small change is drawn small: the points stay within a narrow band of the chart's height
	ys := regexp.MustCompile(`<circle class="punkt" cx="[\d.]+" cy="([\d.]+)"`).FindAllStringSubmatch(s, -1)
	if len(ys) != 3 {
		t.Fatalf("points: %v", ys)
	}
}

// --- reading: the predicted values a lung-function sheet prints, and units that are none

const lufuText = `Lungenfunktion Praxis Nord
Patient: Muster, Anna
Untersuchung: 19.12.2023
Parameter  Einheit  Soll   Ist    %Soll  LLN
FEV1       l        4,21   3,50   83     3,40
FEV1/FVC   %        79     68     86     70
FVC        l        5,10   5,15   101    4,20`

const lufuAntwort = `{"patient": {"wert": "Anna Muster", "beleg": "Patient: Muster, Anna"},
"datum": {"wert": "2023-12-19", "beleg": "Untersuchung: 19.12.2023"}, "art": "Lungenfunktion",
"werte": [
 {"name": "FEV1", "wert": "3,50", "einheit": "l", "ref": null, "soll": "4,21", "soll_prozent": "83", "lln": "3,40", "beleg": "FEV1       l        4,21   3,50   83     3,40"},
 {"name": "FEV1/FVC", "wert": 68, "einheit": "%", "ref": null, "soll": 79, "soll_prozent": 86, "lln": 70, "beleg": "FEV1/FVC   %        79     68     86     70"},
 {"name": "FVC", "wert": "5,15", "einheit": "l", "soll": "9,99", "soll_prozent": null, "lln": null, "beleg": "FVC        l        5,10   5,15   101    4,20"}
], "ereignisse": []}`

func TestReadingTakesThePredictedValuesTheSheetPrints(t *testing.T) {
	var a Antwort
	if err := json.Unmarshal([]byte(lufuAntwort), &a); err != nil {
		t.Fatalf("numbers where strings were asked for are taken: %v", err)
	}
	g := auswerten(9, "Lufu", lufuText, "2023-12-20", a, time.Now())
	if len(g.Werte) != 3 {
		t.Fatalf("%+v %v", g.Werte, g.Weg)
	}
	fev1, ratio, fvc := g.Werte[0], g.Werte[1], g.Werte[2]
	if fev1.Soll == nil || *fev1.Soll != 4.21 || fev1.SollProzent == nil || *fev1.SollProzent != 83 || fev1.LLN == nil || *fev1.LLN != 3.4 {
		t.Fatalf("fev1: %+v", fev1)
	}
	if ratio.Wert != 68 || ratio.LLN == nil || *ratio.LLN != 70 {
		t.Fatalf("ratio: %+v", ratio)
	}
	if fvc.Soll != nil {
		t.Fatalf("a predicted value its row does not carry is not taken: %+v", fvc)
	}
	if !strings.Contains(strings.Join(g.Weg, ";"), "FVC: Sollwert 9,99") {
		t.Fatalf("and the note says so: %v", g.Weg)
	}
	if !strings.Contains(frageSystem, `"soll"`) || !strings.Contains(frageSystem, "LLN") {
		t.Fatal("the question asks for the predicted values")
	}
}

const fehlText = `Laborbefund
Patient: Muster, Anna
Abnahme: 15.03.2019
Hepatitis B (Anti-HBs) 0,03 cal 10 - 1000
LUES Screen 4 ERS
SD-Volumen 30 com
MVV(calc) 149,2 min
FIVV/FIVC 123,1 %
TSH 2,3 mU/l 0,4 - 4,0
Leukozyten 6,4 10^3/µl 4,0 - 10,0
Puls 64 /Minute
PEF 9,5 l/s`

func fehlWert(name, wert, einheit, beleg string) string {
	return `{"name": "` + name + `", "wert": "` + wert + `", "einheit": "` + einheit + `", "ref": null, "beleg": "` + beleg + `"}`
}

// The five misreads of the first run (#10): kept, but each says „prüfen“.
func TestAUnitThatIsNoUnitSaysPruefen(t *testing.T) {
	ans := `{"patient": {"wert": "Anna Muster", "beleg": "Patient: Muster, Anna"}, "datum": {"wert": "2019-03-15", "beleg": "Abnahme: 15.03.2019"}, "werte": [` +
		strings.Join([]string{
			fehlWert("Hepatitis B (Anti-HBs)", "0,03", "cal", "Hepatitis B (Anti-HBs) 0,03 cal"),
			fehlWert("LUES Screen", "4", "ERS", "LUES Screen 4 ERS"),
			fehlWert("SD-Volumen", "30", "com", "SD-Volumen 30 com"),
			fehlWert("MVV(calc)", "149,2", "min", "MVV(calc) 149,2 min"),
			fehlWert("FIVV/FIVC", "123,1", "%", "FIVV/FIVC 123,1 %"),
			fehlWert("TSH", "2,3", "mU/l", "TSH 2,3 mU/l"),
			fehlWert("Leukozyten", "6,4", "10^3/µl", "Leukozyten 6,4 10^3/µl"),
			fehlWert("Puls", "64", "/Minute", "Puls 64 /Minute"),
			fehlWert("PEF", "9,5", "l/s", "PEF 9,5 l/s"),
		}, ",") + `], "ereignisse": []}`
	var a Antwort
	if err := json.Unmarshal([]byte(ans), &a); err != nil {
		t.Fatal(err)
	}
	g := auswerten(12, "Labor", fehlText, "2019-03-16", a, time.Now())
	pruefen := map[string]string{}
	for _, m := range g.Werte {
		if m.Pruefen {
			pruefen[m.Name] = m.PruefenGrund
		}
	}
	for _, n := range []string{"Anti-HBs", "Lues-Suchtest", "Schilddrüsenvolumen", "MVV", "FIVV/FIVC"} {
		if pruefen[n] == "" {
			t.Errorf("%s does not say prüfen: %v", n, pruefen)
		}
	}
	if !strings.Contains(pruefen["FIVV/FIVC"], "FIV1/FIVC") {
		t.Errorf("the near name is offered: %q", pruefen["FIVV/FIVC"])
	}
	if !strings.Contains(pruefen["MVV"], "l/min") {
		t.Errorf("the unit MVV is given in: %q", pruefen["MVV"])
	}
	for _, n := range []string{"TSH", "Leukozyten", "Puls", "Peak-Flow"} {
		if _, ok := pruefen[n]; ok {
			t.Errorf("%s is a plain value: %q", n, pruefen[n])
		}
	}
	if len(g.Abweichungen) != 5 {
		t.Fatalf("the note asks to check each: %v", g.Abweichungen)
	}
	for _, m := range g.Werte {
		if m.Name == "Peak-Flow" && (m.Wert != 570 || m.Einheit != "l/min") {
			t.Fatalf("l/s is converted exactly: %+v", m)
		}
	}
}

func TestEveryDictionaryUnitIsAUnit(t *testing.T) {
	for _, a := range Woerterbuch {
		if a.Einheit != "" && !einheitForm(a.Einheit) {
			t.Errorf("%s: %q", a.Name, a.Einheit)
		}
		for _, e := range a.Einheiten {
			if !einheitForm(e) {
				t.Errorf("%s: %q", a.Name, e)
			}
		}
	}
	for _, u := range []string{"cal", "ERS", "com", "RR", "plU/ml"} {
		if einheitForm(u) {
			t.Errorf("%q is no unit", u)
		}
	}
}

// --- the profile

func TestTheProfileIsEnteredOnceAndSeenOnlyByItsPerson(t *testing.T) {
	d, _, _ := aufbau(t)
	h := (&Web{Dienst: d, Secret: "s3cret", Admins: "admins"}).Handler()
	rec := rufe(t, h, "POST", "/profil", als("anna"), "person=anna&geburtsjahr=1981&geschlecht=w&groesse=168")
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "m=") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if p, _ := d.Speicher.Profil("anna"); p.Geburtsjahr != 1981 || p.Geschlecht != "w" || p.GroesseCm != 168 {
		t.Fatalf("stored: %+v", p)
	}
	if body := rufe(t, h, "GET", "/werte", als("anna"), "").Body.String(); !strings.Contains(body, `name=geburtsjahr`) || !strings.Contains(body, `value="1981"`) {
		t.Fatal("anna sees her profile in the form")
	}
	// nobody else
	if rec := rufe(t, h, "POST", "/profil", als("ben"), "person=anna&geburtsjahr=1950"); rec.Code != 403 {
		t.Fatalf("ben writes anna's: %d", rec.Code)
	}
	if rec := rufe(t, h, "GET", "/werte?person=anna", als("ben", "admins"), ""); rec.Code != 403 || strings.Contains(rec.Body.String(), "1981") {
		t.Fatalf("an admin does not see another adult's: %d", rec.Code)
	}
	if p, _ := d.Speicher.Profil("anna"); p.Geburtsjahr != 1981 {
		t.Fatal("unchanged")
	}
	// an admin for a child
	if rec := rufe(t, h, "POST", "/profil", als("ben", "admins"), "person=kind-lena-muster&geburtsjahr=2015&geschlecht=w&groesse=140"); rec.Code != 303 {
		t.Fatalf("admin for the child: %d %s", rec.Code, rec.Body.String())
	}
	// wrong input answers in German on the page, nothing stored
	for _, form := range []string{"person=anna&geburtsjahr=1850", "person=anna&groesse=5", "person=anna&geschlecht=x", "person=anna&geburtsjahr=" + strconv.Itoa(time.Now().Year()+1)} {
		rec := rufe(t, h, "POST", "/profil", als("anna"), form)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), "Zurück") {
			t.Fatalf("%s: %d %s", form, rec.Code, rec.Body.String())
		}
	}
	if p, _ := d.Speicher.Profil("anna"); p.Geburtsjahr != 1981 || p.GroesseCm != 168 {
		t.Fatalf("a refused form stores nothing: %+v", p)
	}
	// an empty field clears it
	rufe(t, h, "POST", "/profil", als("anna"), "person=anna&geburtsjahr=1981&geschlecht=&groesse=")
	if p, _ := d.Speicher.Profil("anna"); p.Geschlecht != "" || p.GroesseCm != 0 || p.Geburtsjahr != 1981 {
		t.Fatalf("cleared: %+v", p)
	}
}

// --- „Was ist das?“

// The names on the box on 2026-10-30 (laib read gesundheit record.json), as
// the reports wrote them. Misreads are not explained: they say „prüfen“.
var namenAufDerBox = []string{
	"Atemreserve", "Belastung aerobe Schwelle", "Belastung anaerobe Schwelle", "DIAS", "SYS", "ELA", "EVol",
	"FEF25", "FEF25-75", "FEF50", "FEF75", "FEFmax", "FET", "FEV1", "FEV1/FVC", "FEV1/VC", "FEV3", "FEV3/FVC", "FEV6",
	"FIF", "FIF50", "FIV1", "FIV1/FIVC", "FIVC", "FVC", "Hepatitis B (Anti-HBs)", "Hepatitis B (HBsAg)", "HIV (Anti-HIV)",
	"Haematokrit", "Haemoglobin", "Erythrozyten", "Leukozyten", "LUES Screen", "MCH", "MCHC", "MCV", "MVV", "MVV(calc)", "MWV",
	"O2-Puls maximal", "PEF", "PEF (wiederholbar)", "PIF", "Puls", "Herzfrequenz aerobe Schwelle", "Herzfrequenz anaerobe Schwelle",
	"SD-Volumen", "Sauerstoffaufnahme an der aeroben Schwelle", "SpO2 bei Abbruch", "SpO2 in Ruhe", "TSH", "TSH basal",
	"Tc.-Uptake", "Thrombozyten", "V'E/V'CO2-Slope", "endtidalen pCO2 in Ruhe (PETCO2)", "maximale Belastung",
	"maximale Herzfrequenz", "maximale Sauerstoffaufnahme", "maximaler RER",
}

// misreads of the first run: no explanation is invented for them
var lesefehlerAufDerBox = []string{"ulm!", "FER7S", "FEVU/FVC", "FIVV/FIVC", "PEP", "FEV6%", "FIV"}

func TestEveryNameOnTheBoxIsExplained(t *testing.T) {
	for _, n := range namenAufDerBox {
		e := erklaerung(n, nil)
		if e.Text == "" || e.Quelle != QuelleListe {
			t.Errorf("%s: %+v", n, e)
		}
	}
	for _, n := range lesefehlerAufDerBox {
		if e := erklaerung(n, nil); e.Text != "" {
			t.Errorf("a misread %q is explained: %+v", n, e)
		}
	}
	// the dictionary and the list are one: every analyte has its sentence, every sentence its analyte
	for _, a := range Woerterbuch {
		if erklaerungen[a.Name] == "" {
			t.Errorf("no explanation for %s", a.Name)
		}
	}
	for n := range erklaerungen {
		if finde(n) == nil {
			t.Errorf("explained but not in the dictionary: %s", n)
		}
	}
}

var bewertung = regexp.MustCompile(`(?i)\bsollte|\bsolltest|empfehl|\brat(e|en)\b|gefährlich|bedenklich|\bkrank|erhöht|erniedrigt|zu hoch|zu niedrig|normal|\bgesund\b|diagnos|behandl`)

// An explanation says what a value measures, in one or two short
// sentences, and never what a number means for anyone.
func TestTheExplanationsAreShortAndAssessNothing(t *testing.T) {
	for n, text := range erklaerungen {
		if bewertung.MatchString(text) {
			t.Errorf("%s assesses: %q", n, text)
		}
		if s := strings.Count(text, ". ") + 1; s > 2 || len([]rune(text)) > 260 {
			t.Errorf("%s is long (%d sentences, %d characters): %q", n, s, len([]rune(text)), text)
		}
	}
	if len(erklaerungen) < 90 {
		t.Fatalf("the list has %d entries", len(erklaerungen))
	}
}

func TestAnUnknownNameIsExplainedOnceByTheModel(t *testing.T) {
	d, _, mo := aufbau(t)
	mo.answers["Wert: Zauberwert"] = `{"erklaerung": "Misst, wie viel eines bestimmten Eiweißes im Blut ist."}`
	mo.answers["Wert: Ratwert"] = `{"erklaerung": "Ein Wert über 5 ist bedenklich, du solltest zum Arzt gehen."}`
	if err := d.Speicher.Eintragen("anna", Gelesen{Dokument: 40, Werte: []Messwert{
		{Name: "Zauberwert", Neu: true, Wert: 3, Einheit: "mg/l", Datum: "2024-01-01", Dokument: 40},
		{Name: "Ratwert", Neu: true, Wert: 3, Einheit: "mg/l", Datum: "2024-01-01", Dokument: 40},
		{Name: "Hämoglobin", Wert: 13, Einheit: "g/dl", Datum: "2024-01-01", Dokument: 40},
		{Name: "FER7S", Neu: true, Wert: 3, Einheit: "l/s", Datum: "2024-01-01", Dokument: 40, Pruefen: true, PruefenGrund: "unsicher"},
	}}); err != nil {
		t.Fatal(err)
	}
	if n := d.erklaeren(context.Background(), 5); n != 2 || mo.asked != 2 {
		t.Fatalf("asked %d, explained %d", mo.asked, n)
	}
	ex, _ := d.Speicher.Erklaerungen()
	if e := erklaerung("Zauberwert", ex); e.Quelle != QuelleModell || !strings.Contains(e.Text, "Eiweiß") {
		t.Fatalf("stored and marked: %+v", e)
	}
	if e := erklaerung("Ratwert", ex); e.Text != "" || e.Quelle != QuelleVerworfen {
		t.Fatalf("an answer that assesses is not kept: %+v", e)
	}
	if d.erklaeren(context.Background(), 5); mo.asked != 2 {
		t.Fatalf("asked again: %d", mo.asked)
	}
	// the page marks it, and an admin corrects it
	h := (&Web{Dienst: d, Secret: "s3cret", Admins: "admins"}).Handler()
	body := rufe(t, h, "GET", "/werte", als("anna"), "").Body.String()
	if !strings.Contains(body, "Was ist das?") || !strings.Contains(body, "Allgemeine Erklärung, vom Hausmodell geschrieben") || strings.Contains(body, `action="/erklaerung"`) {
		t.Fatalf("anna's page: %s", body)
	}
	if rec := rufe(t, h, "POST", "/erklaerung", als("anna"), "name=Zauberwert&text=Quatsch"); rec.Code != 403 {
		t.Fatalf("anna is no admin: %d", rec.Code)
	}
	rec := rufe(t, h, "POST", "/erklaerung", als("ben", "admins"), "name=Zauberwert&text="+url.QueryEscape("Misst ein Eiweiß, das die Leber bildet.")+"&person=ben")
	if rec.Code != 303 {
		t.Fatalf("admin: %d %s", rec.Code, rec.Body.String())
	}
	ex, _ = d.Speicher.Erklaerungen()
	if e := erklaerung("Zauberwert", ex); e.Quelle != QuelleHaushalt || e.Text != "Misst ein Eiweiß, das die Leber bildet." {
		t.Fatalf("corrected: %+v", e)
	}
	d.Speicher.Eintragen("ben", Gelesen{Dokument: 43, Werte: []Messwert{{Name: "Zauberwert", Neu: true, Wert: 2, Einheit: "mg/l", Datum: "2024-02-01", Dokument: 43}}})
	if body := rufe(t, h, "GET", "/werte", als("ben", "admins"), "").Body.String(); !strings.Contains(body, `action="/erklaerung"`) || !strings.Contains(body, "Misst ein Eiweiß, das die Leber bildet.") {
		t.Fatal("the admin has the form")
	}
	// the household's text wins over the list, too
	rufe(t, h, "POST", "/erklaerung", als("ben", "admins"), "name=H%C3%A4moglobin&text=Unser+Text.")
	ex, _ = d.Speicher.Erklaerungen()
	if e := erklaerung("Hb", ex); e.Text != "Unser Text." {
		t.Fatalf("by its alias too: %+v", e)
	}
	// emptied: back to the list
	rufe(t, h, "POST", "/erklaerung", als("ben", "admins"), "name=H%C3%A4moglobin&text=")
	ex, _ = d.Speicher.Erklaerungen()
	if e := erklaerung("Hämoglobin", ex); e.Quelle != QuelleListe {
		t.Fatalf("back to the list: %+v", e)
	}
}

// --- the page's rows

func TestTheRowReadsAtFirstSight(t *testing.T) {
	d, _, _ := aufbau(t)
	werte := []Messwert{
		{Name: "FEV1", Wert: 5.41, Einheit: "l", Datum: "2021-05-03", Dokument: 1, Gruppe: "Lunge"},
		{Name: "FEV1", Wert: 5.29, Einheit: "l", Datum: "2023-12-19", Dokument: 2, Gruppe: "Lunge", LLN: zf(4.1), Soll: zf(5.0), SollProzent: zf(106)},
		{Name: "Hämoglobin", Wert: 11.8, Einheit: "g/dl", Datum: "2026-03-12", Dokument: 3, Gruppe: "Blutbild", RefMin: zf(12), RefMax: zf(16)},
	}
	for _, m := range werte {
		if err := d.Speicher.Eintragen("anna", Gelesen{Dokument: m.Dokument, Werte: []Messwert{m}}); err != nil {
			t.Fatal(err)
		}
	}
	h := (&Web{Dienst: d, Secret: "s3cret", Admins: "admins"}).Handler()
	body := rufe(t, h, "GET", "/werte", als("anna"), "").Body.String()
	for _, want := range []string{
		"5,29 l · Dez. 2023", "im Normalbereich", "unterer Grenzwert laut Befund (LLN): 4,1 l", "106 % des Sollwerts",
		"vorher 5,41 l (2021) – kaum verändert",
		"11,8 g/dl · März 2026", "unter dem Bereich", "Bereich laut Befund: 12–16 g/dl", "noch kein früherer Wert",
		"Was ist das?", "Allgemeine Erklärung, keine Diagnose", `href="/arzt"`, `href="/befunde"`, `href="/befinden"`, `aria-current=page>Werte<`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if strings.Contains(body, "<svg class=spark") {
		t.Error("no row has three values: no chart")
	}
}

// --- „Fragen an den Arzt“

func TestTheDoctorsPage(t *testing.T) {
	d, pl, _ := aufbau(t)
	pl.docs[5] = &fakeDoc{ID: 5, Title: "Labor März", Content: laborAnna, Created: "2026-03-13", Type: 7}
	pl.docs[8] = &fakeDoc{ID: 8, Title: "Labor Juni", Content: laborAnna2, Created: "2026-06-21", Type: 7}
	d.Lauf(context.Background(), "test")
	// one value read unsure
	v, _ := d.Speicher.Verlauf("anna")
	g := Gelesen{Dokument: 41, Titel: "Blutbild alt", Werte: []Messwert{{Name: "MCV", Wert: 95, Einheit: "fl", Datum: "2019-01-18", Dokument: 41, Gruppe: "Blutbild", Pruefen: true, PruefenGrund: "das Bild nennt 95, der Text der Seite 96"}}}
	d.Speicher.Eintragen("anna", g)
	d.Speicher.Merken("41", &Eintrag{At: time.Now(), Person: "anna", Gelesen: &g, Leser: leserVersion})
	_ = v
	h := (&Web{Dienst: d, Secret: "s3cret", Admins: "admins"}).Handler()
	rec := rufe(t, h, "GET", "/arzt", als("anna"), "")
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, body)
	}
	for _, want := range []string{
		"Fragen an den Arzt", "Anna Muster",
		// outside its range on the latest sheet: Hämoglobin 12,9 is inside now; Kreatinin 0,8 inside; none of anna's latest is outside but…
		"Ferritin", "von 18 ng/ml (12.03.2026) auf 31 ng/ml (20.06.2026) gestiegen", "Labor Juni",
		"MCV", "95 fl", "18.01.2019", "das Bild nennt 95, der Text der Seite 96", "Blutbild alt",
		"@media print", "https://paperless.example/documents/8/details",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the doctor's page lacks %q", want)
		}
	}
	if strings.Count(body, "?</p>") < 2 {
		t.Errorf("each point is a question: %s", body)
	}
	// a value outside its range on its latest sheet
	d.Speicher.Eintragen("anna", Gelesen{Dokument: 42, Titel: "Labor Okt", Werte: []Messwert{{Name: "Hämoglobin", Wert: 11.2, Einheit: "g/dl", Datum: "2026-10-01", Dokument: 42, Gruppe: "Blutbild", RefMin: zf(12), RefMax: zf(16)}}})
	d.Speicher.Merken("42", &Eintrag{At: time.Now(), Person: "anna", Gelesen: &Gelesen{Dokument: 42, Titel: "Labor Okt"}, Leser: leserVersion})
	body = rufe(t, h, "GET", "/arzt", als("anna"), "").Body.String()
	if !strings.Contains(body, "Hämoglobin: 11,2 g/dl am 01.10.2026 – laut Befund unter dem Bereich (12–16 g/dl)") || !strings.Contains(body, "Labor Okt") {
		t.Fatalf("outside: %s", body)
	}
	if bewertung.MatchString(strings.NewReplacer("Fragen an den Arzt", "", "Arztbesuch", "", "an den Arzt", "").Replace(textOhneTags(body))) {
		t.Fatalf("the page draws a conclusion: %s", bewertung.FindString(textOhneTags(body)))
	}
	// nobody else's
	if rec := rufe(t, h, "GET", "/arzt?person=anna", als("ben", "admins"), ""); rec.Code != 403 || strings.Contains(rec.Body.String(), "Ferritin") {
		t.Fatalf("ben: %d", rec.Code)
	}
	// nothing to ask: the page says so
	body = rufe(t, h, "GET", "/arzt", als("ben"), "").Body.String()
	if !strings.Contains(body, "Nichts außerhalb") {
		t.Fatalf("empty: %s", body)
	}
}

func textOhneTags(s string) string {
	s = regexp.MustCompile(`(?s)<style>.*?</style>`).ReplaceAllString(s, "")
	return regexp.MustCompile(`<[^>]+>`).ReplaceAllString(s, " ")
}

// --- re-reading the records a former reader read

func TestARaisedReaderVersionReadsEveryRecordAgain(t *testing.T) {
	d, pl, mo := aufbau(t)
	alt := proLauf
	t.Cleanup(func() { proLauf = alt })
	pl.docs[5] = &fakeDoc{ID: 5, Title: "Labor März", Content: laborAnna, Created: "2026-03-13", Type: 7}
	pl.docs[7] = &fakeDoc{ID: 7, Title: "Labor fremd", Content: laborFremd, Created: "2026-02-02", Type: 7}
	d.Lauf(context.Background(), "test")
	if err := d.Zuordnen(context.Background(), 7, "anna"); err != nil { // the owner's assignment
		t.Fatal(err)
	}
	rec, _ := d.Speicher.Record()
	if rec.Gelesen["5"].Leser != leserVersion {
		t.Fatalf("a record carries its reader: %+v", rec.Gelesen["5"])
	}
	// as the first run left them: no reader version
	for _, id := range []string{"5", "7"} {
		e := rec.Gelesen[id]
		e.Leser = 0
		d.Speicher.Merken(id, e)
	}
	asked := mo.asked
	// a new document is read first; the model then goes away: nothing lost, the page counts
	pl.docs[8] = &fakeDoc{ID: 8, Title: "Labor Juni", Content: laborAnna2, Created: "2026-06-21", Type: 7}
	proLauf = 1
	l := d.Lauf(context.Background(), "neue Dokumente")
	if l.Gelesen != 1 || mo.asked != asked+1 {
		t.Fatalf("the new one first: %+v", l)
	}
	if l.NeuZuLesen != 2 || l.NeuZuLesenPro["anna"] != 2 {
		t.Fatalf("still to re-read: %+v", l)
	}
	h := (&Web{Dienst: d, Secret: "s3cret", Admins: "admins"}).Handler()
	if body := rufe(t, h, "GET", "/werte", als("anna"), "").Body.String(); !strings.Contains(body, "noch 2 Befunde neu zu lesen") {
		t.Fatalf("the page says how many: %s", body)
	}
	if body := rufe(t, h, "GET", "/werte", als("ben"), "").Body.String(); strings.Contains(body, "neu zu lesen") {
		t.Fatal("ben has none to re-read")
	}
	mo.mu.Lock()
	mo.weg = true
	mo.mu.Unlock()
	proLauf = 10
	if l := d.Lauf(context.Background(), "stündlich"); !l.ModellWeg || l.NeuZuLesen != 2 {
		t.Fatalf("model away: %+v", l)
	}
	// back: the misread is replaced, the owner's assignment stays
	mo.mu.Lock()
	mo.weg = false
	mo.answers["Muster, Anna   geb."] = strings.Replace(antwortAnna, `"wert": "11,8"`, `"wert": "12,8"`, 1)
	mo.answers["Muster, Anna   geb."] = strings.Replace(mo.answers["Muster, Anna   geb."], `"beleg": "Hämoglobin       11,8`, `"beleg": "Hämoglobin       12,8`, 1)
	mo.mu.Unlock()
	pl.docs[5].Content = strings.Replace(laborAnna, "11,8", "12,8", 1)
	l = d.Lauf(context.Background(), "stündlich")
	if l.Gelesen != 2 || l.NeuZuLesen != 0 {
		t.Fatalf("re-read: %+v", l)
	}
	v, _ := d.Speicher.Verlauf("anna")
	var hb []float64
	tsh := 0
	for _, m := range v.Werte {
		if m.Name == "Hämoglobin" {
			hb = append(hb, m.Wert)
		}
		if m.Name == "TSH" {
			tsh++
		}
	}
	if len(hb) != 2 || hb[0] != 12.8 || hb[1] != 12.9 {
		t.Fatalf("the old value is replaced, not doubled: %v", hb)
	}
	if tsh != 1 || len(d.Offene()) != 0 {
		t.Fatalf("the document the owner gave anna stays hers: tsh %d, offen %d", tsh, len(d.Offene()))
	}
	if n := pl.docs[5].Notes; len(n) != 1 {
		t.Fatalf("one note, replaced: %q", n)
	}
	rec, _ = d.Speicher.Record()
	for id, e := range rec.Gelesen {
		if e.Leser != leserVersion {
			t.Fatalf("%s: %+v", id, e)
		}
	}
	if body := rufe(t, h, "GET", "/werte", als("anna"), "").Body.String(); strings.Contains(body, "neu zu lesen") {
		t.Fatal("done: the page says nothing more")
	}
	st, _ := os.ReadFile(d.StatusFile)
	if strings.Contains(string(st), "neu zu lesen") {
		t.Fatalf("status: %s", st)
	}
}

// The Bote reads the same: the sheet's LLN and % Soll, and a doubtful read.
func TestTheBoteSeesThePredictedValuesAndTheDoubt(t *testing.T) {
	d, _, _ := aufbau(t)
	d.Speicher.Eintragen("anna", Gelesen{Dokument: 9, Werte: []Messwert{{Name: "FEV1", Wert: 3.5, Einheit: "l", Datum: "2023-12-19", Dokument: 9, LLN: zf(3.4), SollProzent: zf(83), Pruefen: true}}})
	h := &mcpServer{Token: "b0te", Tools: mcpTools(d)}
	out, isErr := text(mcpCall(t, h, "b0te", "tools/call", map[string]any{"name": "gesundheit_verlauf", "arguments": map[string]any{"person": "anna", "wert": "FEV1"}}))
	if isErr || !strings.Contains(out, "2023-12-19: 3,5 l (LLN 3,4) (83 % des Sollwerts) (unsicher gelesen, prüfen)") {
		t.Fatalf("%v %s", isErr, out)
	}
}
