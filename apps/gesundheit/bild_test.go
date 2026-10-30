package main

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- the fakes for #5: the proxy with its lease, the image preset, the household behind it

// fakeKarte is the lease's proxy: /api/model-lease, the image preset (only
// while gesundheit holds the card, as laib-lease answers), and the
// household model behind it (refused while the image preset holds the card:
// --models-max 1, the household reloads after the card is given back).
type fakeKarte struct {
	mu          sync.Mutex
	haushalt    http.Handler
	holder      string
	fremd       string            // another holder has the card: 409
	vorbereiten int               // POSTs answered 202 preparing before ready
	abschriften map[string]string // the base64 of a page image → its transcript
	bilder      int               // image requests answered
	genommen    int
	zurueck     int
	falsch      []string // requests the real proxy would refuse
	srv         *httptest.Server
}

func newFakeKarte(haushalt http.Handler) *fakeKarte {
	k := &fakeKarte{haushalt: haushalt, abschriften: map[string]string{}}
	k.srv = httptest.NewServer(http.HandlerFunc(k.serve))
	return k
}

func (k *fakeKarte) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	k.mu.Lock()
	if r.URL.Path == "/api/model-lease" {
		defer k.mu.Unlock()
		if r.Method != "GET" && r.Header.Get("Authorization") != "Bearer k" {
			http.Error(w, "unauthorized", 401)
			return
		}
		var req struct{ Model, Holder string }
		json.Unmarshal(body, &req)
		switch r.Method {
		case "POST":
			if k.fremd != "" {
				w.WriteHeader(409)
				json.NewEncoder(w).Encode(map[string]any{"ok": false, "reason": "held", "holder": k.fremd})
				return
			}
			if req.Model != "gemma-4-12b" || req.Holder != "gesundheit" {
				k.falsch = append(k.falsch, "lease "+req.Model+" "+req.Holder)
			}
			k.holder = req.Holder
			k.genommen++
			if k.vorbereiten > 0 {
				w.WriteHeader(202)
				json.NewEncoder(w).Encode(map[string]any{"ok": true, "state": "preparing"})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "state": "ready", "renew_after": 300})
		case "GET":
			state := "ready"
			if k.vorbereiten > 0 {
				k.vorbereiten--
				state = "preparing"
			}
			json.NewEncoder(w).Encode(map[string]any{"state": state, "holder": k.holder})
		case "DELETE":
			if req.Holder != "gesundheit" {
				k.falsch = append(k.falsch, "release by "+req.Holder)
			}
			k.holder = ""
			k.zurueck++
			json.NewEncoder(w).Encode(map[string]any{"ok": true})
		}
		return
	}
	if strings.Contains(string(body), `"image_url"`) {
		defer k.mu.Unlock()
		if k.holder != "gesundheit" || !strings.Contains(string(body), `"model":"gemma-4-12b"`) {
			k.falsch = append(k.falsch, "image without the lease")
			http.Error(w, "no lease", 409)
			return
		}
		if r.Header.Get("X-Laib-Background") == "" {
			k.falsch = append(k.falsch, "image request not marked as background")
		}
		k.bilder++
		ans := "UNLESERLICH"
		for b64, t := range k.abschriften {
			if strings.Contains(string(body), b64) {
				ans = t
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": ans}}}})
		return
	}
	if k.holder != "" {
		k.falsch = append(k.falsch, "household model asked while the image preset holds the card")
		k.mu.Unlock()
		http.Error(w, "held", 409)
		return
	}
	k.mu.Unlock()
	r.Body = io.NopCloser(bytes.NewReader(body))
	k.haushalt.ServeHTTP(w, r)
}

// mitBild is aufbau with the image pass on: the household model and the
// image preset behind one proxy, as on the box.
func mitBild(t *testing.T) (*Dienst, *fakePaperless, *fakeModell, *fakeKarte) {
	t.Helper()
	d, pl, mo := aufbau(t)
	k := newFakeKarte(mo.srv.Config.Handler)
	t.Cleanup(k.srv.Close)
	d.Modell.URL = k.srv.URL
	d.Bild = &Bildleser{Modell: d.Modell, Name: "gemma-4-12b", Lease: &Lease{URL: k.srv.URL + "/api/model-lease", Key: "k", Holder: "gesundheit", Pause: time.Millisecond}}
	return d, pl, mo, k
}

// seitenJPEG is a page image: its gray tone tells the pages apart.
func seitenJPEG(t *testing.T, ton uint8) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, 640, 900))
	for i := range img.Pix {
		img.Pix[i] = ton
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// --- the acceptance examples of #5

// Document 305: a TSH sheet whose OCR is letter salad; the first run read
// „ulm!“ = 0,3 from it. Its image is legible.
const ocrSalat = `,i;l lI!' ulm! 0,3 l1!
:-'i~ rnU/| 4.0 ,., 1;6
.,ll'!i 1;6 \\// _ :i'l
Ii1 l!| ;:,' 03 12 2019 '!l
~-., rn!' 0,27 4,2 ,'i
'l!i |1l 1,16 ;,:`

const abschriftTSH = `# Labor Dr. Beispiel

Patient: Muster, Anna
Abnahme: 03.12.2019

| Parameter | Ergebnis | Einheit | Referenz |
|---|---|---|---|
| TSH basal | 1,16 | mU/l | 0,27 - 4,2 |`

const antwortTSH = `{"patient": {"wert": "Anna Muster", "beleg": "Patient: Muster, Anna"},
"datum": {"wert": "2019-12-03", "beleg": "Abnahme: 03.12.2019"}, "art": "Labor",
"werte": [{"name": "TSH basal", "wert": "1,16", "einheit": "mU/l", "ref": "0,27 - 4,2", "beleg": "| TSH basal | 1,16 | mU/l | 0,27 - 4,2 |"}], "ereignisse": []}`

func TestALetterSaladPageIsReadFromItsImage(t *testing.T) {
	d, pl, mo, k := mitBild(t)
	bild := seitenJPEG(t, 200)
	k.abschriften[b64(bild)] = abschriftTSH
	mo.answers["TSH basal | 1,16"] = antwortTSH
	pl.docs[305] = &fakeDoc{ID: 305, Title: "TSH", Content: ocrSalat, Created: "2019-12-05", Type: 7, File: bild, Mime: "image/jpeg",
		Notes: []string{"Gesundheit: 1 Werte gelesen (ulm!)", "Meine eigene Notiz"}, NoteBy: []int{4, 2}}
	l := d.Lauf(context.Background(), "stündlich")
	if len(k.falsch) > 0 {
		t.Fatalf("the proxy would refuse: %v", k.falsch)
	}
	if l.Gelesen != 1 || l.Bild != 1 || l.Werte != 1 || k.genommen != 1 || k.zurueck != 1 || k.bilder != 1 {
		t.Fatalf("lauf %+v, lease %d/%d, bilder %d", l, k.genommen, k.zurueck, k.bilder)
	}
	v, _ := d.Speicher.Verlauf("anna")
	if len(v.Werte) != 1 || v.Werte[0].Wert != 1.16 || v.Werte[0].Datum != "2019-12-03" || v.Werte[0].Quelle != "bild" || v.Werte[0].Pruefen {
		t.Fatalf("anna: %+v", v.Werte)
	}
	// one note of its own, with the transcript; the person's note stays
	n := pl.docs[305].Notes
	if len(n) != 2 || n[0] != "Meine eigene Notiz" || !strings.HasPrefix(n[1], "Gesundheit: 1 Werte") ||
		!strings.Contains(n[1], "aus dem Bild gelesen") || !strings.Contains(n[1], "| TSH basal | 1,16 | mU/l | 0,27 - 4,2 |") {
		t.Fatalf("notes: %q", n)
	}
	if tags := strings.Join(pl.tagNames(305), ","); strings.Contains(tags, "prüfen") {
		t.Fatalf("image and OCR do not disagree, nothing to check: %s", tags)
	}
	st, _ := os.ReadFile(d.StatusFile)
	if !strings.Contains(string(st), "1 aus dem Bild gelesen") {
		t.Fatalf("status: %s", st)
	}
	// the transcript behind the value, for its person only
	h := (&Web{Dienst: d, Secret: "s3cret", Admins: "admins"}).Handler()
	if rec := rufe(t, h, "GET", "/", als("anna"), ""); !strings.Contains(rec.Body.String(), `/abschrift?dokument=305`) {
		t.Fatalf("start page: %s", rec.Body.String())
	}
	if rec := rufe(t, h, "GET", "/abschrift?dokument=305", als("anna"), ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "| TSH basal | 1,16 |") {
		t.Fatalf("abschrift: %d %s", rec.Code, rec.Body.String())
	}
	if rec := rufe(t, h, "GET", "/abschrift?dokument=305", als("ben"), ""); rec.Code == 200 {
		t.Fatal("ben sees anna's transcript")
	}
}

// Document 307's Hämoglobin, 18.01.2019: the OCR text has the row but
// another number than the paper; the image wins and the value says „prüfen“.
const ocrHb = `l!i ;:' Il1 |!, ;l'
Hämoglobin 13,6 g/dl 13,5 - 17,5
,i;l lI!' 'l!i |1l :-'i~
.,ll'!i \\// _ :i'l Ii1 l!|
~-., rn!' 42 ,'i ;l!' 95 1l|`

const abschriftHb = `# Blutbild

Patient: Muster, Anna
Datum: 18.01.2019

| Parameter | Ergebnis | Einheit | Referenz |
|---|---|---|---|
| Hämoglobin | 14,6 | g/dl | 13,5 - 17,5 |
| Hämatokrit | 42 | % | 40 - 52 |
| MCV | 95 | fl | 80 - 96 |`

const antwortHb = `{"patient": {"wert": "Anna Muster", "beleg": "Patient: Muster, Anna"},
"datum": {"wert": "2019-01-18", "beleg": "Datum: 18.01.2019"}, "art": "Labor",
"werte": [
 {"name": "Hämoglobin", "wert": "14,6", "einheit": "g/dl", "ref": "13,5 - 17,5", "beleg": "| Hämoglobin | 14,6 | g/dl | 13,5 - 17,5 |"},
 {"name": "Hämatokrit", "wert": "42", "einheit": "%", "ref": "40 - 52", "beleg": "| Hämatokrit | 42 | % | 40 - 52 |"},
 {"name": "MCV", "wert": "95", "einheit": "fl", "ref": "80 - 96", "beleg": "| MCV | 95 | fl | 80 - 96 |"}], "ereignisse": []}`

func TestImageAndOCRDisagreeTheValueSaysPruefen(t *testing.T) {
	d, pl, mo, k := mitBild(t)
	bild := seitenJPEG(t, 120)
	k.abschriften[b64(bild)] = abschriftHb
	mo.answers["| Hämoglobin | 14,6"] = antwortHb
	pl.docs[307] = &fakeDoc{ID: 307, Title: "Blutbild", Content: ocrHb, Created: "2019-01-20", Type: 7, File: bild, Mime: "image/jpeg"}
	if !guete(ocrHb).Schwach() {
		t.Fatalf("the Hb page's OCR is weak: %s", guete(ocrHb))
	}
	l := d.Lauf(context.Background(), "stündlich")
	if len(k.falsch) > 0 || l.Werte != 3 || l.Pruefen != 1 {
		t.Fatalf("lauf %+v, %v", l, k.falsch)
	}
	v, _ := d.Speicher.Verlauf("anna")
	byName := map[string]Messwert{}
	for _, m := range v.Werte {
		byName[m.Name] = m
	}
	hb := byName["Hämoglobin"]
	if hb.Wert != 14.6 || hb.Datum != "2019-01-18" || !hb.Pruefen || hb.Quelle != "bild" {
		t.Fatalf("hb: %+v", hb)
	}
	if byName["MCV"].Pruefen {
		t.Fatal("the OCR text has no MCV row: nothing disagrees")
	}
	if n := pl.docs[307].Notes; len(n) != 1 || !strings.Contains(n[0], "Hämoglobin steht im Bild als 14,6, im Text der Seite als 13,6") {
		t.Fatalf("note: %q", n)
	}
	if tags := strings.Join(pl.tagNames(307), ","); !strings.Contains(tags, "prüfen") {
		t.Fatalf("tags: %s", tags)
	}
	h := (&Web{Dienst: d, Secret: "s3cret", Admins: "admins"}).Handler()
	if body := rufe(t, h, "GET", "/", als("anna"), "").Body.String(); !strings.Contains(body, "Der Text der Seite nennt eine andere Zahl als das Bild") {
		t.Fatalf("the page does not say prüfen: %s", body)
	}
}

// Document 303: a Krankenblatt over four examinations; one set per date.
const abschriftSpiro = `# Krankenblatt Spiroergometrie

Patient: Muster, Anna

## 14.05.2012
VO2max 31 ml/min/kg, Leistung 230 W

## 02.06.2014
VO2max 33 ml/min/kg, Leistung 245 W

## 11.03.2016
VO2max 34 ml/min/kg, Leistung 255 W

## 06.01.2020
VO2max 35 ml/min/kg, Leistung 269 W`

func spiroWert(name, wert, einheit, datum, beleg string) string {
	return fmt.Sprintf(`{"name": %q, "wert": %q, "einheit": %q, "datum": %q, "beleg": %q}`, name, wert, einheit, datum, beleg)
}

func TestASheetOverYearsYieldsOneSetPerDate(t *testing.T) {
	d, pl, mo, k := mitBild(t)
	bild := seitenJPEG(t, 80)
	k.abschriften[b64(bild)] = abschriftSpiro
	var werte []string
	for _, x := range [][3]string{{"2012-05-14", "31", "230"}, {"2014-06-02", "33", "245"}, {"2016-03-11", "34", "255"}, {"2020-01-06", "35", "269"}} {
		werte = append(werte, spiroWert("VO2max", x[1], "ml/min/kg", x[0], "VO2max "+x[1]+" ml/min/kg"), spiroWert("Leistung", x[2], "W", x[0], "Leistung "+x[2]+" W"))
	}
	mo.answers["Krankenblatt Spiroergometrie"] = `{"patient": {"wert": "Anna Muster", "beleg": "Patient: Muster, Anna"}, "datum": null, "art": "Lungenfunktion", "werte": [` + strings.Join(werte, ",") + `], "ereignisse": []}`
	pl.docs[303] = &fakeDoc{ID: 303, Title: "Spiroergometrie", Content: "", Created: "2020-01-07", Type: 7, File: bild, Mime: "image/jpeg"}
	l := d.Lauf(context.Background(), "stündlich")
	if len(k.falsch) > 0 || l.Werte != 8 {
		t.Fatalf("lauf %+v, %v", l, k.falsch)
	}
	v, _ := d.Speicher.Verlauf("anna")
	tage := map[string]int{}
	for _, m := range v.Werte {
		tage[m.Datum]++
	}
	if len(tage) != 4 || tage["2012-05-14"] != 2 || tage["2020-01-06"] != 2 {
		t.Fatalf("one set per date: %v", tage)
	}
}

func TestAnUnreadableImageYieldsNoValuesAndSaysSo(t *testing.T) {
	d, pl, mo, k := mitBild(t)
	pl.docs[310] = &fakeDoc{ID: 310, Title: "Fax", Content: ocrSalat, Created: "2019-12-05", Type: 7, File: seitenJPEG(t, 10), Mime: "image/jpeg"}
	l := d.Lauf(context.Background(), "stündlich")
	if len(k.falsch) > 0 || l.Gelesen != 1 || l.Werte != 0 || l.Pruefen != 1 || mo.asked != 0 || k.zurueck != 1 {
		t.Fatalf("lauf %+v, asked %d, %v", l, mo.asked, k.falsch)
	}
	if n := pl.docs[310].Notes; len(n) != 1 || !strings.Contains(n[0], "Keine Werte übernommen") || !strings.Contains(n[0], "konnte auch das Bild nicht lesen") {
		t.Fatalf("note: %q", n)
	}
	if tags := strings.Join(pl.tagNames(310), ","); !strings.Contains(tags, "prüfen") || !strings.Contains(tags, "gesundheit") {
		t.Fatalf("tags: %s", tags)
	}
	// given up on, not asked again every hour
	d.Lauf(context.Background(), "stündlich")
	if k.bilder != 1 {
		t.Fatalf("asked again: %d", k.bilder)
	}
}

func TestAFileWithoutAPictureNeedsNoCard(t *testing.T) {
	d, pl, _, k := mitBild(t)
	pl.docs[311] = &fakeDoc{ID: 311, Title: "Brief", Content: ocrSalat, Created: "2019-12-05", Type: 7, File: []byte("%PDF-1.4\n1 0 obj << /Type /Catalog >> endobj\n%%EOF"), Mime: "application/pdf"}
	l := d.Lauf(context.Background(), "stündlich")
	if k.genommen != 0 || l.Werte != 0 || l.Gelesen != 1 {
		t.Fatalf("lauf %+v, lease taken %d", l, k.genommen)
	}
	if n := pl.docs[311].Notes; len(n) != 1 || !strings.Contains(n[0], "keine lesbare Seite als Bild") {
		t.Fatalf("note: %q", n)
	}
}

func TestABusyCardLeavesTheWeakPagesForTheNextRun(t *testing.T) {
	d, pl, mo, k := mitBild(t)
	bild := seitenJPEG(t, 200)
	k.abschriften[b64(bild)] = abschriftTSH
	mo.answers["TSH basal | 1,16"] = antwortTSH
	k.fremd = "chronicle"
	pl.docs[5] = &fakeDoc{ID: 5, Title: "Labor März", Content: laborAnna, Created: "2026-03-13", Type: 7}
	pl.docs[305] = &fakeDoc{ID: 305, Title: "TSH", Content: ocrSalat, Created: "2019-12-05", Type: 7, File: bild, Mime: "image/jpeg"}
	l := d.Lauf(context.Background(), "stündlich")
	if l.Gelesen != 1 || l.BildWartet != 1 || !strings.Contains(l.BildGrund, "chronicle") {
		t.Fatalf("the strong page is read, the weak one waits: %+v", l)
	}
	if rec, _ := d.Speicher.Record(); rec.Gelesen["305"] != nil {
		t.Fatal("a page that waited for the card is not recorded")
	}
	st, _ := os.ReadFile(d.StatusFile)
	if !strings.Contains(string(st), "1 warten auf das Bild") {
		t.Fatalf("status: %s", st)
	}
	k.fremd = ""
	k.vorbereiten = 2 // the weights load: 202, then GET until ready
	if l := d.Lauf(context.Background(), "stündlich"); l.Gelesen != 1 || l.Bild != 1 || len(k.falsch) > 0 {
		t.Fatalf("next run: %+v %v", l, k.falsch)
	}
}

func TestWithoutAnImageModelWeakPagesWaitAndTheStatusSaysSo(t *testing.T) {
	d, pl, mo := aufbau(t)
	pl.docs[305] = &fakeDoc{ID: 305, Title: "TSH", Content: ocrSalat, Created: "2019-12-05", Type: 7}
	l := d.Lauf(context.Background(), "stündlich")
	if l.Gelesen != 0 || l.BildWartet != 1 || mo.asked != 0 {
		t.Fatalf("no value from letter salad: %+v, asked %d", l, mo.asked)
	}
	st, _ := os.ReadFile(d.StatusFile)
	if !strings.Contains(string(st), "IMAGE_MODEL") {
		t.Fatalf("status: %s", st)
	}
}

// --- the rule for a weak page

func TestGueteTellsAWeakPage(t *testing.T) {
	for name, text := range map[string]string{"laborAnna": laborAnna, "laborAnna2": laborAnna2, "laborFremd": laborFremd, "abschriftHb": abschriftHb, "abschriftSpiro": abschriftSpiro,
		"brief": "Sehr geehrte Kollegin, wir berichten über Frau Anna Muster, die sich am 03.12.2019 in unserer Sprechstunde vorstellte. Die Schilddrüse ist sonographisch unauffällig, TSH 1,16 mU/l."} {
		if g := guete(text); g.Schwach() {
			t.Errorf("%s is readable: %s", name, g)
		}
	}
	for name, text := range map[string]string{"salat": ocrSalat, "hb": ocrHb, "leer": "", "kurz": "Labor 12,0 13,4",
		"zahlen ohne namen": "Labor Nord\n12,4 13,5 17,5\n42 40 52\n95 80 96\n5,4 4,0 10,0\n255 150 400\nfür die Untersuchung danken wir sehr herzlich"} {
		if g := guete(text); !g.Schwach() {
			t.Errorf("%s is weak: %s", name, g)
		}
	}
}

func TestAbweichung(t *testing.T) {
	for _, c := range []struct {
		namen          []string
		roh, ocr, want string
	}{
		{[]string{"Hämoglobin"}, "14,6", "Hämoglobin 13,6 g/dl", "13,6"},
		{[]string{"Hb", "Hämoglobin"}, "14,6", "Hämoglobin 14.6 g/dl", ""},
		{[]string{"MCV"}, "95", "nichts davon", ""},
		{[]string{"TSH"}, "1,16", "TSH 1,16 mU/l TSH 4,0", ""}, // one row agrees: the other is another sheet's
	} {
		if got := abweichung(c.namen, c.roh, c.ocr); got != c.want {
			t.Errorf("%v %s in %q: %q, want %q", c.namen, c.roh, c.ocr, got, c.want)
		}
	}
}

// --- the page images of a scanner's PDF

func flate(t *testing.T, b []byte) []byte {
	var out bytes.Buffer
	z := zlib.NewWriter(&out)
	z.Write(b)
	z.Close()
	return out.Bytes()
}

func pdfMit(objekte ...[2][]byte) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.5\n")
	for i, o := range objekte {
		fmt.Fprintf(&b, "%d 0 obj\n%s\nstream\n", i+1, o[0])
		b.Write(o[1])
		b.WriteString("\nendstream\nendobj\n")
	}
	b.WriteString("%%EOF\n")
	return b.Bytes()
}

func TestPdfBilderFindsThePages(t *testing.T) {
	seite1 := seitenJPEG(t, 50)
	// page 2: an 8-bit gray Flate image with PNG predictors (Up), as OCRmyPDF writes one
	w, h := 700, 640
	var roh []byte
	for y := 0; y < h; y++ {
		roh = append(roh, 2) // Up
		for x := 0; x < w; x++ {
			v := byte(0)
			if y == 0 {
				v = 30
			}
			roh = append(roh, v)
		}
	}
	// page 3: 1-bit black and white, no predictor, the length an indirect object
	w3, h3 := 800, 610
	bw := bytes.Repeat([]byte{0xF0}, (w3+7)/8*h3)
	logo := seitenJPEG(t, 0)[:0] // a small logo: left out by its size, never decoded
	pdf := pdfMit(
		[2][]byte{[]byte(fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width 640 /Height 900 /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>", len(seite1))), seite1},
		[2][]byte{[]byte(fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace [/ICCBased 9 0 R] /BitsPerComponent 8 /Filter /FlateDecode /DecodeParms << /Predictor 15 /Columns %d >> /Length %d >>", w, h, w, len(flate(t, roh)))), flate(t, roh)},
		[2][]byte{[]byte("<< /Type /XObject /Subtype /Image /Width 120 /Height 40 /BitsPerComponent 8 /Filter /DCTDecode /Length 0 >>"), logo},
		[2][]byte{[]byte(fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceGray /BitsPerComponent 1 /Decode [1 0] /Filter /FlateDecode /Length 12 0 R >>", w3, h3)), flate(t, bw)},
	)
	seiten, err := seitenBilder(pdf, "application/pdf")
	if err != nil || len(seiten) != 3 {
		t.Fatalf("3 pages expected: %d %v", len(seiten), err)
	}
	if !bytes.Equal(seiten[0], seite1) {
		t.Fatal("a small JPEG page goes as it is")
	}
	p2, err := jpeg.Decode(bytes.NewReader(seiten[1]))
	if err != nil || p2.Bounds().Dx() != w || p2.Bounds().Dy() != h {
		t.Fatalf("page 2: %v %v", err, p2.Bounds())
	}
	if g := color.GrayModel.Convert(p2.At(10, 300)).(color.Gray); g.Y > 60 {
		t.Fatalf("the Up predictor carries the first row down: %d", g.Y)
	}
	p3, err := jpeg.Decode(bytes.NewReader(seiten[2]))
	if err != nil || p3.Bounds().Dx() != w3 {
		t.Fatalf("page 3: %v", err)
	}
	// /Decode [1 0]: a set bit is black
	if g := color.GrayModel.Convert(p3.At(1, 5)).(color.Gray); g.Y > 60 {
		t.Fatalf("1-bit inverted: %d", g.Y)
	}
}

func TestABigScanIsShrunk(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 4960, 7016))
	var b bytes.Buffer
	jpeg.Encode(&b, img, nil)
	out, err := seitenBilder(b.Bytes(), "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(out[0]))
	if err != nil || cfg.Height != maxSeite || cfg.Width > maxSeite {
		t.Fatalf("%+v %v", cfg, err)
	}
}

// The household model away after the card went back: the transcript is
// kept, the next run files it without asking the image preset again.
func TestATranscriptIsNotReadTwiceWhenTheHouseholdModelIsAway(t *testing.T) {
	d, pl, mo, k := mitBild(t)
	bild := seitenJPEG(t, 200)
	k.abschriften[b64(bild)] = abschriftTSH
	mo.answers["TSH basal | 1,16"] = antwortTSH
	pl.docs[305] = &fakeDoc{ID: 305, Title: "TSH", Content: ocrSalat, Created: "2019-12-05", Type: 7, File: bild, Mime: "image/jpeg"}
	mo.weg = true
	if l := d.Lauf(context.Background(), "stündlich"); !l.ModellWeg || l.Gelesen != 0 || k.bilder != 1 {
		t.Fatalf("%+v, bilder %d", l, k.bilder)
	}
	mo.weg = false
	if l := d.Lauf(context.Background(), "stündlich"); l.Werte != 1 || k.bilder != 1 || k.genommen != 1 {
		t.Fatalf("%+v, bilder %d, lease %d", l, k.bilder, k.genommen)
	}
}

// Without delete_note the old note stays and no second one is written; the status says why.
func TestWithoutDeleteNoteNoSecondNote(t *testing.T) {
	d, pl, mo, k := mitBild(t)
	bild := seitenJPEG(t, 200)
	k.abschriften[b64(bild)] = abschriftTSH
	mo.answers["TSH basal | 1,16"] = antwortTSH
	pl.keinLoeschen = true
	pl.docs[305] = &fakeDoc{ID: 305, Title: "TSH", Content: ocrSalat, Created: "2019-12-05", Type: 7, File: bild, Mime: "image/jpeg", Notes: []string{"Gesundheit: alt"}}
	d.Lauf(context.Background(), "stündlich")
	if n := pl.docs[305].Notes; len(n) != 1 || n[0] != "Gesundheit: alt" {
		t.Fatalf("notes: %q", n)
	}
	st, _ := os.ReadFile(d.StatusFile)
	if !strings.Contains(string(st), "delete_note") {
		t.Fatalf("status: %s", st)
	}
}
