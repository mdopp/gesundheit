package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- the fakes: Paperless, the model, never a live box

type fakeDoc struct {
	ID      int
	Title   string
	Content string
	Created string
	Type    int
	Tags    []int
	Owner   *int
	Notes   []string
}

type fakePaperless struct {
	mu    sync.Mutex
	docs  map[int]*fakeDoc
	tags  map[int]string
	types map[int]string
	users []paperlessUser
	perms map[int]any
	srv   *httptest.Server
}

func newFakePaperless() *fakePaperless {
	f := &fakePaperless{docs: map[int]*fakeDoc{}, tags: map[int]string{1: "prüfen"}, types: map[int]string{7: "Labor", 8: "Arztbrief", 9: "Rechnung"},
		users: []paperlessUser{{ID: 2, Username: "anna", FirstName: "Anna", LastName: "Muster"}, {ID: 3, Username: "ben", FirstName: "Ben", LastName: "Muster"},
			{ID: 4, Username: "gesundheit", FirstName: "Gesundheit"}, {ID: 1, Username: "admin"}}, perms: map[int]any{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

func listJSON(w http.ResponseWriter, items []any) {
	json.NewEncoder(w).Encode(map[string]any{"count": len(items), "next": nil, "results": items})
}

func (f *fakePaperless) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Token t0ken" {
		http.Error(w, "nope", 401)
		return
	}
	p := r.URL.Path
	switch {
	case p == "/api/users/":
		var items []any
		for _, u := range f.users {
			items = append(items, u)
		}
		listJSON(w, items)
	case p == "/api/tags/" && r.Method == "GET":
		var items []any
		for id, n := range f.tags {
			items = append(items, benannt{id, n})
		}
		listJSON(w, items)
	case p == "/api/tags/" && r.Method == "POST":
		var b benannt
		json.NewDecoder(r.Body).Decode(&b)
		b.ID = 100 + len(f.tags)
		f.tags[b.ID] = b.Name
		json.NewEncoder(w).Encode(b)
	case p == "/api/document_types/":
		var items []any
		for id, n := range f.types {
			items = append(items, benannt{id, n})
		}
		listJSON(w, items)
	case p == "/api/documents/":
		q := r.URL.Query()
		var items []any
		for _, d := range f.docs {
			ok := false
			for _, t := range strings.Split(q.Get("document_type__id__in"), ",") {
				if n, _ := strconv.Atoi(t); n == d.Type && n != 0 {
					ok = true
				}
			}
			for _, t := range strings.Split(q.Get("tags__id__in"), ",") {
				n, _ := strconv.Atoi(t)
				for _, dt := range d.Tags {
					if dt == n && n != 0 {
						ok = true
					}
				}
			}
			if ok {
				items = append(items, map[string]any{"id": d.ID, "title": d.Title, "created_date": d.Created, "owner": d.Owner, "document_type": d.Type, "tags": d.Tags})
			}
		}
		listJSON(w, items)
	case strings.HasSuffix(p, "/notes/") && r.Method == "POST":
		id, _ := strconv.Atoi(strings.Split(strings.TrimPrefix(p, "/api/documents/"), "/")[0])
		var b map[string]string
		json.NewDecoder(r.Body).Decode(&b)
		f.docs[id].Notes = append(f.docs[id].Notes, b["note"])
		w.WriteHeader(201)
	case strings.HasPrefix(p, "/api/documents/"):
		id, _ := strconv.Atoi(strings.Trim(strings.TrimPrefix(p, "/api/documents/"), "/"))
		d := f.docs[id]
		if d == nil {
			http.Error(w, "nicht da", 404)
			return
		}
		if r.Method == "PATCH" {
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			if t, ok := b["tags"].([]any); ok {
				d.Tags = nil
				for _, x := range t {
					d.Tags = append(d.Tags, int(x.(float64)))
				}
			}
			if o, ok := b["owner"].(float64); ok {
				n := int(o)
				d.Owner = &n
				f.perms[id] = b["set_permissions"]
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"id": d.ID, "title": d.Title, "content": d.Content, "created_date": d.Created, "owner": d.Owner, "document_type": d.Type, "tags": d.Tags})
	default:
		http.Error(w, "unbekannt: "+p, 404)
	}
}

func (f *fakePaperless) tagNames(id int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, t := range f.docs[id].Tags {
		out = append(out, f.tags[t])
	}
	return out
}

// fakeModell answers each question with the JSON its answers map holds for a
// text fragment, and counts the questions.
type fakeModell struct {
	mu      sync.Mutex
	answers map[string]string
	asked   int
	weg     bool
	srv     *httptest.Server
}

func newFakeModell(answers map[string]string) *fakeModell {
	m := &fakeModell{answers: answers}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.weg {
			http.Error(w, "window", 503)
			return
		}
		if r.Header.Get("X-Laib-Background") == "" {
			http.Error(w, "not background", 400)
			return
		}
		body, _ := io.ReadAll(r.Body)
		m.asked++
		for frag, ans := range m.answers {
			if strings.Contains(string(body), frag) {
				json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": ans}}}})
				return
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": "{}"}}}})
	}))
	return m
}

const laborAnna = `Praxislabor Nord
Patient: Muster, Anna   geb. 03.04.1981
Abnahme: 12.03.2026
Parameter        Ergebnis   Einheit   Referenz
Hämoglobin       11,8       g/dl      12,0 - 16,0
Ferritin         18         ng/ml     15 - 150
CRP              2,1        mg/l      < 5
Leukozyten       6,4        /nl       4,0 - 10,0
Kreatinin        0,8        mg/dl     0,5 - 0,9
Wir danken für die Überweisung.`

const antwortAnna = `{"patient": {"wert": "Anna Muster", "beleg": "Patient: Muster, Anna"},
"datum": {"wert": "2026-03-12", "beleg": "Abnahme: 12.03.2026"}, "art": "Labor",
"werte": [
 {"name": "Hämoglobin", "wert": "11,8", "einheit": "g/dl", "ref": "12,0 - 16,0", "beleg": "Hämoglobin       11,8       g/dl      12,0 - 16,0"},
 {"name": "Ferritin", "wert": "18", "einheit": "ng/ml", "ref": "15 - 150", "beleg": "Ferritin         18         ng/ml     15 - 150"},
 {"name": "CRP", "wert": "2,1", "einheit": "mg/l", "ref": "< 5", "beleg": "CRP              2,1        mg/l      < 5"},
 {"name": "Leukozyten", "wert": "6,4", "einheit": "/nl", "ref": "4,0 - 10,0", "beleg": "Leukozyten       6,4        /nl       4,0 - 10,0"},
 {"name": "Kreatinin", "wert": "0,8", "einheit": "mg/dl", "ref": "0,5 - 0,9", "beleg": "Kreatinin        0,8        mg/dl     0,5 - 0,9"},
 {"name": "Vitamin D", "wert": "22", "einheit": "ng/ml", "ref": null, "beleg": "Vitamin D 22 ng/ml"}
],
"ereignisse": [{"art": "Empfehlung", "text": "Eisen substituieren", "datum": null, "beleg": "Eisen substituieren"}]}`

const laborAnna2 = `Praxislabor Nord, Musterstraße 1, 12345 Musterstadt
Patient: Muster, Anna
Abnahme: 20.06.2026
Parameter        Ergebnis   Einheit   Referenz
Ferritin         31         ng/ml     15 - 150
Hämoglobin       12,9       g/dl      12,0 - 16,0`

const antwortAnna2 = `{"patient": {"wert": "Anna Muster", "beleg": "Patient: Muster, Anna"},
"datum": {"wert": "2026-06-20", "beleg": "Abnahme: 20.06.2026"}, "art": "Labor",
"werte": [
 {"name": "Ferritin", "wert": "31", "einheit": "ng/ml", "ref": "15 - 150", "beleg": "Ferritin         31         ng/ml     15 - 150"},
 {"name": "Hämoglobin", "wert": "12,9", "einheit": "g/dl", "ref": "12,0 - 16,0", "beleg": "Hämoglobin       12,9       g/dl      12,0 - 16,0"}
], "ereignisse": []}`

const laborFremd = `Labor Süd
Patient: Schmidt, Karl
Abnahme: 01.02.2026
TSH 2,3 mU/l 0,4 - 4,0
und noch ein paar Worte damit der Text lang genug ist für den Lauf der Auswertung hier`

const antwortFremd = `{"patient": {"wert": "Karl Schmidt", "beleg": "Patient: Schmidt, Karl"},
"datum": {"wert": "2026-02-01", "beleg": "Abnahme: 01.02.2026"}, "art": "Labor",
"werte": [{"name": "TSH", "wert": "2,3", "einheit": "mU/l", "ref": "0,4 - 4,0", "beleg": "TSH 2,3 mU/l 0,4 - 4,0"}], "ereignisse": []}`

func aufbau(t *testing.T) (*Dienst, *fakePaperless, *fakeModell) {
	t.Helper()
	pl := newFakePaperless()
	mo := newFakeModell(map[string]string{"Muster, Anna   geb.": antwortAnna, "Abnahme: 20.06.2026": antwortAnna2, "Schmidt, Karl": antwortFremd})
	t.Cleanup(func() { pl.srv.Close(); mo.srv.Close() })
	dir := t.TempDir()
	jetzt := time.Date(2026, 10, 28, 18, 0, 0, 0, time.UTC)
	d := &Dienst{
		Paperless:  &Paperless{URL: pl.srv.URL, Token: "t0ken"},
		Modell:     &Modell{URL: mo.srv.URL, Key: "k", Name: "haushalt"},
		Speicher:   &Speicher{Dir: dir},
		Arten:      []string{"Labor", "Arztbrief", "arzt"},
		Kinder:     "Lena Muster",
		Ich:        "gesundheit",
		Public:     "https://paperless.example",
		StatusFile: filepath.Join(dir, "laib-status.json"),
		Jetzt:      func() time.Time { return jetzt },
	}
	return d, pl, mo
}

// --- the reading rules

func TestAuswertenTakesOnlyWhatTheTextBacks(t *testing.T) {
	var a Antwort
	if err := json.Unmarshal([]byte(antwortAnna), &a); err != nil {
		t.Fatal(err)
	}
	g := auswerten(5, "Labor", laborAnna, "2026-03-13", a, time.Now())
	if g.Patient != "Anna Muster" || g.Datum != "2026-03-12" {
		t.Fatalf("patient/datum: %q %q", g.Patient, g.Datum)
	}
	if len(g.Werte) != 5 {
		t.Fatalf("5 Werte erwartet (Vitamin D steht nicht im Text), got %d: %+v", len(g.Werte), g.Werte)
	}
	if len(g.Weg) != 2 || !strings.Contains(g.Weg[0], "Vitamin D") || !strings.Contains(g.Weg[1], "Eisen substituieren") {
		t.Fatalf("weg: %v", g.Weg)
	}
	hb := g.Werte[0]
	if hb.Name != "Hämoglobin" || hb.Wert != 11.8 || hb.RefMin == nil || *hb.RefMin != 12 || *hb.RefMax != 16 || hb.Gruppe != "Blutbild" {
		t.Fatalf("hb: %+v", hb)
	}
	crp := g.Werte[2]
	if crp.RefMin != nil || crp.RefMax == nil || *crp.RefMax != 5 {
		t.Fatalf("crp ref: %+v", crp)
	}
}

func TestAuswertenRefusesANumberNotInItsBeleg(t *testing.T) {
	a := Antwort{Werte: []struct{ Name, Wert, Einheit, Ref, Beleg string }{{"CRP", "21", "mg/l", "", "CRP 2,1 mg/l"}}}
	g := auswerten(1, "x", "CRP 2,1 mg/l und mehr Text", "", a, time.Now())
	if len(g.Werte) != 0 || len(g.Weg) != 1 {
		t.Fatalf("%+v %v", g.Werte, g.Weg)
	}
}

func TestAuswertenConvertsOnlyKnownUnits(t *testing.T) {
	text := "Glukose 5,5 mmol/l (3,9 - 5,6)  Natrium 140 mmol/l"
	a := Antwort{Werte: []struct{ Name, Wert, Einheit, Ref, Beleg string }{
		{"Glukose", "5,5", "mmol/l", "3,9 - 5,6", "Glukose 5,5 mmol/l (3,9 - 5,6)"},
		{"Natrium", "140", "mmol/l", "", "Natrium 140 mmol/l"},
		{"Lipase", "40", "U/l", "", "Lipase 40 U/l"}}}
	g := auswerten(1, "x", text+" Lipase 40 U/l", "2026-01-01", a, time.Now())
	if g.Werte[0].Einheit != "mg/dl" || g.Werte[0].Wert < 99 || g.Werte[0].Wert > 100 || *g.Werte[0].RefMax < 100 {
		t.Fatalf("glukose: %+v", g.Werte[0])
	}
	if g.Werte[1].Einheit != "mmol/l" || g.Werte[1].Wert != 140 {
		t.Fatalf("natrium: %+v", g.Werte[1])
	}
	if !g.Werte[2].Neu || g.Werte[2].Name != "Lipase" || g.Werte[2].Gruppe != "Weitere" {
		t.Fatalf("lipase: %+v", g.Werte[2])
	}
}

func TestWoerterbuchAliases(t *testing.T) {
	for in, want := range map[string]string{"Hb": "Hämoglobin", "HGB": "Hämoglobin", "LDL-Cholesterin": "LDL", "Kreatinin (enzym.)": "Kreatinin", "eGFR": "GFR", "FEV 1": "FEV1", "Tiffeneau": "FEV1/FVC"} {
		if a := finde(in); a == nil || a.Name != want {
			t.Errorf("%q → %v, want %s", in, a, want)
		}
	}
	if finde("Lipase") != nil {
		t.Error("Lipase is not in the dictionary")
	}
}

func TestWessen(t *testing.T) {
	personen := haushalt([]paperlessUser{{ID: 2, Username: "anna", FirstName: "Anna", LastName: "Muster"}, {ID: 3, Username: "ben", FirstName: "Ben", LastName: "Muster"}, {ID: 4, Username: "gesundheit"}},
		"Lena Muster, Tom", map[string]bool{"gesundheit": true})
	if len(personen) != 4 {
		t.Fatalf("%+v", personen)
	}
	cases := map[string]string{"Anna Muster": "anna", "Muster, Anna": "anna", "Frau Anna Muster": "anna", "Patientin: Lena Muster": "kind-lena-muster", "Tom": "kind-tom", "Ben": "ben", "Muster": "", "Karl Schmidt": "", "": ""}
	for in, want := range cases {
		got := ""
		if p := wessen(in, personen); p != nil {
			got = p.Key
		}
		if got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
	if k := finden("kind-lena-muster", personen); k == nil || !k.Kind() || k.Anzeige != "Lena Muster" {
		t.Fatalf("kind: %+v", k)
	}
}

func TestSichtbar(t *testing.T) {
	personen := haushalt([]paperlessUser{{ID: 2, Username: "anna", FirstName: "Anna"}, {ID: 3, Username: "ben", FirstName: "Ben"}}, "Lena", nil)
	if got := sichtbar("anna", false, personen); len(got) != 1 || got[0].Key != "anna" {
		t.Fatalf("anna sees %+v", got)
	}
	if got := sichtbar("anna", true, personen); len(got) != 2 || got[1].Key != "kind-lena" {
		t.Fatalf("admin anna sees %+v", got)
	}
}

// --- the run

func TestLaufReadsALabSheetForItsPerson(t *testing.T) {
	d, pl, mo := aufbau(t)
	pl.docs[5] = &fakeDoc{ID: 5, Title: "Labor März", Content: laborAnna, Created: "2026-03-13", Type: 7}
	pl.docs[6] = &fakeDoc{ID: 6, Title: "Rechnung", Content: "Rechnung über 100 Euro, bitte zahlen " + strings.Repeat("x ", 30), Created: "2026-03-13", Type: 9}
	pl.docs[7] = &fakeDoc{ID: 7, Title: "Labor fremd", Content: laborFremd, Created: "2026-02-02", Type: 7}
	l := d.Lauf(context.Background(), "test")
	if l.Gelesen != 2 || l.Werte != 6 || l.Offen != 1 || l.Pruefen != 2 || l.Fehler != "" {
		t.Fatalf("lauf: %+v", l)
	}
	if mo.asked != 2 {
		t.Fatalf("the invoice is no medical record, asked %d", mo.asked)
	}
	v, _ := d.Speicher.Verlauf("anna")
	if len(v.Werte) != 5 || v.Werte[0].Dokument != 5 {
		t.Fatalf("anna: %+v", v.Werte)
	}
	if pl.docs[5].Owner == nil || *pl.docs[5].Owner != 2 {
		t.Fatalf("the document is anna's now: %v", pl.docs[5].Owner)
	}
	if pl.perms[5] == nil {
		t.Fatal("the program keeps its own view and change")
	}
	if n := pl.docs[5].Notes; len(n) != 1 || !strings.Contains(n[0], "5 Werte") || !strings.Contains(n[0], "Anna Muster") || !strings.Contains(n[0], "Vitamin D") {
		t.Fatalf("note: %v", n)
	}
	tags := strings.Join(pl.tagNames(5), ",")
	if !strings.Contains(tags, "prüfen") || !strings.Contains(tags, "gesundheit") {
		t.Fatalf("tags of 5: %s", tags)
	}
	// the stranger's sheet waits for a person, nobody's timeline has it
	if o := d.Offene(); len(o) != 1 || o[0].Gelesen.Patient != "Karl Schmidt" {
		t.Fatalf("offen: %+v", o)
	}
	if pl.docs[7].Owner != nil {
		t.Fatal("an unassigned document is given to nobody")
	}
	// a second run asks nothing again
	l2 := d.Lauf(context.Background(), "stündlich")
	if l2.Gelesen != 0 || mo.asked != 2 {
		t.Fatalf("second run: %+v, asked %d", l2, mo.asked)
	}
	st, _ := os.ReadFile(d.StatusFile)
	if !strings.Contains(string(st), "1 warten auf eine Zuordnung") || !strings.Contains(string(st), `"findings"`) {
		t.Fatalf("status: %s", st)
	}
}

func TestSecondSheetExtendsTheLine(t *testing.T) {
	d, pl, _ := aufbau(t)
	pl.docs[5] = &fakeDoc{ID: 5, Title: "Labor März", Content: laborAnna, Created: "2026-03-13", Type: 7}
	d.Lauf(context.Background(), "test")
	pl.docs[8] = &fakeDoc{ID: 8, Title: "Labor Juni", Content: laborAnna2, Created: "2026-06-21", Type: 7}
	d.Lauf(context.Background(), "neue Dokumente")
	v, _ := d.Speicher.Verlauf("anna")
	var ferritin []Messwert
	for _, m := range v.Werte {
		if m.Name == "Ferritin" {
			ferritin = append(ferritin, m)
		}
	}
	if len(ferritin) != 2 || ferritin[0].Wert != 18 || ferritin[1].Wert != 31 || ferritin[1].Datum != "2026-06-20" {
		t.Fatalf("ferritin: %+v", ferritin)
	}
	if s := zuletzt(v.Werte); !strings.Contains(s, "Ferritin 18 → 31") {
		t.Fatalf("zuletzt: %s", s)
	}
}

func TestModellAwayEndsTheRunAndNothingIsRecorded(t *testing.T) {
	d, pl, mo := aufbau(t)
	pl.docs[5] = &fakeDoc{ID: 5, Title: "Labor März", Content: laborAnna, Created: "2026-03-13", Type: 7}
	mo.weg = true
	l := d.Lauf(context.Background(), "test")
	if !l.ModellWeg || l.Gelesen != 0 {
		t.Fatalf("%+v", l)
	}
	rec, _ := d.Speicher.Record()
	if len(rec.Gelesen) != 0 {
		t.Fatal("a document the model did not answer for is asked again next time")
	}
	mo.weg = false
	if l := d.Lauf(context.Background(), "test"); l.Gelesen != 1 {
		t.Fatalf("%+v", l)
	}
}

func TestZuordnenMovesTheValuesToThePerson(t *testing.T) {
	d, pl, _ := aufbau(t)
	pl.docs[7] = &fakeDoc{ID: 7, Title: "Labor fremd", Content: laborFremd, Created: "2026-02-02", Type: 7}
	d.Lauf(context.Background(), "test")
	if err := d.Zuordnen(context.Background(), 7, "kind-lena-muster"); err != nil {
		t.Fatal(err)
	}
	v, _ := d.Speicher.Verlauf("kind-lena-muster")
	if len(v.Werte) != 1 || v.Werte[0].Name != "TSH" {
		t.Fatalf("%+v", v.Werte)
	}
	if len(d.Offene()) != 0 {
		t.Fatal("still open")
	}
	// moved on to anna: gone from the child
	if err := d.Zuordnen(context.Background(), 7, "anna"); err != nil {
		t.Fatal(err)
	}
	v, _ = d.Speicher.Verlauf("kind-lena-muster")
	if len(v.Werte) != 0 {
		t.Fatal("not moved")
	}
}

func TestWithoutATokenTheStatusSaysSo(t *testing.T) {
	d, _, _ := aufbau(t)
	d.Paperless.Token = ""
	l := d.Lauf(context.Background(), "test")
	if !strings.Contains(l.Fehler, "PAPERLESS_TOKEN") {
		t.Fatalf("%+v", l)
	}
	st, _ := os.ReadFile(d.StatusFile)
	if !strings.Contains(string(st), "Kein Zugang zu Paperless") {
		t.Fatalf("%s", st)
	}
}

// --- the pages

func rufe(t *testing.T, h http.Handler, method, path string, headers map[string]string, form string) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if form != "" {
		body = strings.NewReader(form)
	}
	req := httptest.NewRequest(method, path, body)
	if form != "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func als(login string, groups ...string) map[string]string {
	return map[string]string{proxyHeader: "s3cret", "Remote-User": login, "Remote-Groups": strings.Join(groups, ",")}
}

func TestPagesShowOnlyTheOwnTimeline(t *testing.T) {
	d, pl, _ := aufbau(t)
	pl.docs[5] = &fakeDoc{ID: 5, Title: "Labor März", Content: laborAnna, Created: "2026-03-13", Type: 7}
	d.Lauf(context.Background(), "test")
	h := (&Web{Dienst: d, Secret: "s3cret", Admins: "admins"}).Handler()

	if rec := rufe(t, h, "GET", "/healthz", nil, ""); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	if rec := rufe(t, h, "GET", "/", map[string]string{"Remote-User": "anna"}, ""); rec.Code != 401 {
		t.Fatalf("a forged Remote-User without the secret: %d", rec.Code)
	}
	rec := rufe(t, h, "GET", "/", als("anna"), "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Hämoglobin") || !strings.Contains(rec.Body.String(), saysWerSieht) || !strings.Contains(rec.Body.String(), "Wie hast du geschlafen?") {
		t.Fatalf("anna: %d %s", rec.Code, rec.Body.String()[:400])
	}
	if strings.Contains(rec.Body.String(), "Lena") {
		t.Fatal("anna is no admin and does not see the child")
	}
	rec = rufe(t, h, "GET", "/?person=anna", als("ben"), "")
	if rec.Code != 403 || strings.Contains(rec.Body.String(), "Hämoglobin") {
		t.Fatalf("ben opening anna's: %d", rec.Code)
	}
	rec = rufe(t, h, "GET", "/?person=kind-lena-muster", als("ben", "family", "admins"), "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Lena Muster") {
		t.Fatalf("admin ben opening the child's: %d", rec.Code)
	}
	if rec = rufe(t, h, "GET", "/?person=anna", als("ben", "admins"), ""); rec.Code != 403 {
		t.Fatalf("an admin still does not see another adult's: %d", rec.Code)
	}
	if rec = rufe(t, h, "GET", "/export.csv", als("anna"), ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "2026-03-12;wert;Hämoglobin;11,8") {
		t.Fatalf("csv: %d %s", rec.Code, rec.Body.String())
	}
}

func TestTheDailyNoteFromThePage(t *testing.T) {
	d, _, _ := aufbau(t)
	h := (&Web{Dienst: d, Secret: "s3cret", Admins: "admins"}).Handler()
	rec := rufe(t, h, "POST", "/tag", als("anna"), "person=anna&datum=2026-10-28&schlaf=4&energie=3&stimmung=4&schmerz=0&text=lange+gelaufen")
	if rec.Code != 303 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	tage, _ := d.Speicher.Tage("anna")
	if len(tage) != 1 || tage[0].Antworten["schlaf"] != 4 || tage[0].Text != "lange gelaufen" || tage[0].Nachgetragen {
		t.Fatalf("%+v", tage)
	}
	// out of range: refused on the page, in German
	rec = rufe(t, h, "POST", "/tag", als("anna"), "person=anna&datum=2026-10-28&schmerz=11")
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "liegt nicht zwischen") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	// yesterday: nachgetragen; another person's day: refused
	rec = rufe(t, h, "POST", "/tag", als("anna"), "person=anna&datum=2026-10-27&schlaf=2")
	tage, _ = d.Speicher.Tage("anna")
	if rec.Code != 303 || len(tage) != 2 || !tage[0].Nachgetragen {
		t.Fatalf("%d %+v", rec.Code, tage)
	}
	if rec = rufe(t, h, "POST", "/tag", als("ben"), "person=anna&datum=2026-10-28&schlaf=1"); rec.Code != 403 {
		t.Fatalf("ben writing anna's day: %d", rec.Code)
	}
	// own questions: at most three
	rec = rufe(t, h, "POST", "/fragen", als("anna"), "person=anna&text1=Atemnot&skala1=10&text2=Kopfschmerz&skala2=5")
	if rec.Code != 303 {
		t.Fatal(rec.Code)
	}
	fragen, _ := d.Speicher.Fragen("anna")
	if len(fragen) != 6 || !fragen[4].Eigen || fragen[4].Max != 10 || fragen[4].Key != "eigen-atemnot" {
		t.Fatalf("%+v", fragen)
	}
	rec = rufe(t, h, "GET", "/", als("anna"), "")
	if !strings.Contains(rec.Body.String(), "Atemnot") || !strings.Contains(rec.Body.String(), "Für heute notiert") {
		t.Fatal("the page shows the own question and today's note")
	}
}

// --- the inner side

func TestHookNeedsTheToken(t *testing.T) {
	d, _, _ := aufbau(t)
	called := false
	innen := http.NewServeMux()
	innen.HandleFunc("POST /hook", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Gesundheit-Token") != "h00k" {
			http.Error(w, "unauthorized", 401)
			return
		}
		d.Hook()
		called = true
		w.WriteHeader(202)
	})
	if rec := rufe(t, innen, "POST", "/hook", nil, ""); rec.Code != 401 || called {
		t.Fatal(rec.Code)
	}
	if rec := rufe(t, innen, "POST", "/hook", map[string]string{"X-Gesundheit-Token": "h00k"}, ""); rec.Code != 202 || !called {
		t.Fatal(rec.Code)
	}
	if d.hooks.erster.IsZero() {
		t.Fatal("the hook was not noted")
	}
}

func mcpCall(t *testing.T, h http.Handler, token, method string, params any) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(string(body)))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		return map[string]any{"status": float64(rec.Code)}
	}
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

func text(res map[string]any) (string, bool) {
	r, _ := res["result"].(map[string]any)
	c, _ := r["content"].([]any)
	if len(c) == 0 {
		return fmt.Sprint(res), false
	}
	m, _ := c[0].(map[string]any)
	return m["text"].(string), r["isError"] == true
}

func TestMCPForTheBote(t *testing.T) {
	d, pl, _ := aufbau(t)
	pl.docs[5] = &fakeDoc{ID: 5, Title: "Labor März", Content: laborAnna, Created: "2026-03-13", Type: 7}
	d.Lauf(context.Background(), "test")
	h := &mcpServer{Token: "b0te", Tools: mcpTools(d)}
	if res := mcpCall(t, h, "", "tools/list", nil); res["status"] != float64(401) {
		t.Fatalf("no bearer: %v", res)
	}
	if res := mcpCall(t, h, "b0te", "tools/list", nil); !strings.Contains(fmt.Sprint(res), "tagesnotiz_eintragen") {
		t.Fatalf("%v", res)
	}
	out, isErr := text(mcpCall(t, h, "b0te", "tools/call", map[string]any{"name": "fragen", "arguments": map[string]any{"person": "anna"}}))
	if isErr || !strings.Contains(out, "schlaf: Wie hast du geschlafen? (1–5)") {
		t.Fatalf("%v %s", isErr, out)
	}
	out, isErr = text(mcpCall(t, h, "b0te", "tools/call", map[string]any{"name": "tagesnotiz_eintragen", "arguments": map[string]any{"person": "anna", "antworten": map[string]any{"schlaf": 4, "schmerz": 0}, "text": "gut"}}))
	if isErr || !strings.Contains(out, "2026-10-28") {
		t.Fatalf("%v %s", isErr, out)
	}
	tage, _ := d.Speicher.Tage("anna")
	if len(tage) != 1 || tage[0].Von != "bote" {
		t.Fatalf("%+v", tage)
	}
	out, isErr = text(mcpCall(t, h, "b0te", "tools/call", map[string]any{"name": "tagesnotiz_eintragen", "arguments": map[string]any{"person": "anna", "antworten": map[string]any{"laune": 4}}}))
	if !isErr || !strings.Contains(out, "keine Frage") {
		t.Fatalf("%v %s", isErr, out)
	}
	out, isErr = text(mcpCall(t, h, "b0te", "tools/call", map[string]any{"name": "gesundheit_verlauf", "arguments": map[string]any{"person": "anna", "wert": "Hb"}}))
	if isErr || !strings.Contains(out, "2026-03-12: 11,8 g/dl (Referenz 12–16)") || !strings.Contains(out, "keine ärztliche Einschätzung") {
		t.Fatalf("%v %s", isErr, out)
	}
	out, isErr = text(mcpCall(t, h, "b0te", "tools/call", map[string]any{"name": "gesundheit_verlauf", "arguments": map[string]any{"person": "kind-lena-muster"}}))
	if !isErr || !strings.Contains(out, "keinen Verlauf") {
		t.Fatalf("a child has no channel: %v %s", isErr, out)
	}
}

func TestLoeschenRemovesThePersonsFolderOnly(t *testing.T) {
	d, pl, _ := aufbau(t)
	pl.docs[5] = &fakeDoc{ID: 5, Title: "Labor März", Content: laborAnna, Created: "2026-03-13", Type: 7}
	d.Lauf(context.Background(), "test")
	h := (&Web{Dienst: d, Secret: "s3cret", Admins: "admins"}).Handler()
	if rec := rufe(t, h, "POST", "/loeschen", als("anna"), "person=anna"); rec.Code != 303 {
		t.Fatal(rec.Code)
	}
	v, _ := d.Speicher.Verlauf("anna")
	if len(v.Werte) != 0 {
		t.Fatal("not deleted")
	}
	rec, _ := d.Speicher.Record()
	if len(rec.Gelesen) != 1 {
		t.Fatal("the record stays: the document is not read again")
	}
}

// --- the daily question

// A person the Bote does not know (404) is tried once a day, not every
// tick (2026-10-29: 200 log lines in 15 minutes for a retired account);
// the others still get their question.
func TestAnUnknownRecipientIsTriedOnceADay(t *testing.T) {
	d, _, _ := aufbau(t)
	var mu sync.Mutex
	tries := map[string]int{}
	notify := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]string
		json.NewDecoder(r.Body).Decode(&m)
		mu.Lock()
		tries[m["an"]]++
		mu.Unlock()
		if m["an"] == "ben" {
			http.Error(w, "unbekannt", 404)
			return
		}
		w.WriteHeader(200)
	}))
	defer notify.Close()
	d.NotifyURL, d.NotifyToken, d.FrageUm = notify.URL, "n0tify", "20:00"
	jetzt := time.Date(2026, 10, 29, 20, 0, 5, 0, time.UTC)
	d.Jetzt = func() time.Time { return jetzt }
	n := d.Fragen(context.Background())
	for i := 0; i < 5; i++ {
		jetzt = jetzt.Add(5 * time.Second)
		n += d.Fragen(context.Background())
	}
	if n != 1 || tries["anna"] != 1 || tries["ben"] != 1 {
		t.Fatalf("sent %d, tries %v: ben once a day, anna once", n, tries)
	}
	jetzt = time.Date(2026, 10, 30, 20, 0, 5, 0, time.UTC)
	d.Fragen(context.Background())
	if tries["ben"] != 2 {
		t.Fatalf("next day ben tried again: %v", tries)
	}
}

func TestTheDailyQuestionOncePerPersonAndDay(t *testing.T) {
	d, _, _ := aufbau(t)
	var mu sync.Mutex
	var sent []map[string]string
	notify := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer n0tify" {
			http.Error(w, "nope", 401)
			return
		}
		var m map[string]string
		json.NewDecoder(r.Body).Decode(&m)
		mu.Lock()
		sent = append(sent, m)
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer notify.Close()
	d.NotifyURL, d.NotifyToken, d.FrageUm = notify.URL, "n0tify", "20:00"
	jetzt := time.Date(2026, 10, 28, 19, 59, 0, 0, time.UTC)
	d.Jetzt = func() time.Time { return jetzt }
	// anna noted the day already; ben has not; lena is a child without a channel
	if _, err := tagesnotiz(d, "anna", "", map[string]int{"schlaf": 3}, "", "seite"); err != nil {
		t.Fatal(err)
	}
	if n := d.Fragen(context.Background()); n != 0 || len(sent) != 0 {
		t.Fatalf("before the hour: %d %v", n, sent)
	}
	jetzt = time.Date(2026, 10, 28, 20, 0, 5, 0, time.UTC)
	if n := d.Fragen(context.Background()); n != 1 || len(sent) != 1 || sent[0]["an"] != "ben" {
		t.Fatalf("at the hour: %d %v", n, sent)
	}
	if !strings.Contains(sent[0]["text"], "Wie hast du geschlafen (1–5)") || !strings.Contains(sent[0]["text"], "Hattest du Schmerzen (0–10)") {
		t.Fatalf("text: %s", sent[0]["text"])
	}
	// the next ticks of the day: nobody again, whether ben answers or not
	jetzt = jetzt.Add(2 * time.Hour)
	if n := d.Fragen(context.Background()); n != 0 || len(sent) != 1 {
		t.Fatalf("again: %d %v", n, sent)
	}
	// the next day both are asked, anna's note was yesterday's
	jetzt = time.Date(2026, 10, 29, 20, 30, 0, 0, time.UTC)
	if n := d.Fragen(context.Background()); n != 2 || len(sent) != 3 {
		t.Fatalf("next day: %d %v", n, sent)
	}
	// without a token nothing goes out
	d.NotifyToken = ""
	jetzt = time.Date(2026, 10, 30, 20, 30, 0, 0, time.UTC)
	if n := d.Fragen(context.Background()); n != 0 {
		t.Fatal("asked without a token")
	}
}
