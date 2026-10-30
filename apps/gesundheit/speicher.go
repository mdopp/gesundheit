package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Speicher is the program's own store under /data (backed up): one folder
// per person with their values, events, days and questions, and the record
// of the documents read, so none is asked twice. Plain JSON files, written
// whole and renamed into place; the data is small (a few hundred points a
// person) and a person's folder is theirs to export or delete.
type Speicher struct {
	Dir string
	mu  sync.Mutex
}

// Verlauf is one person's data.
type Verlauf struct {
	Werte      []Messwert `json:"werte"`
	Ereignisse []Ereignis `json:"ereignisse"`
}

// Tagesnotiz is one day's note: the answers to the questions, by key, and
// a free line. Nachgetragen marks an entry made for an earlier day.
type Tagesnotiz struct {
	Datum        string         `json:"datum"`
	Antworten    map[string]int `json:"antworten"`
	Text         string         `json:"text,omitempty"`
	Nachgetragen bool           `json:"nachgetragen,omitempty"`
	At           time.Time      `json:"at"`
	Von          string         `json:"von,omitempty"` // seite | bote
}

// Frage is one question of the daily note: a scale from Min to Max.
type Frage struct {
	Key   string `json:"key"`
	Text  string `json:"text"`
	Min   int    `json:"min"`
	Max   int    `json:"max"`
	Eigen bool   `json:"eigen,omitempty"` // the person's own, not the base
}

// Grundfragen are the four everyone is asked; under a minute together.
var Grundfragen = []Frage{
	{Key: "schlaf", Text: "Wie hast du geschlafen?", Min: 1, Max: 5},
	{Key: "energie", Text: "Wie viel Energie hattest du?", Min: 1, Max: 5},
	{Key: "stimmung", Text: "Wie war deine Stimmung?", Min: 1, Max: 5},
	{Key: "schmerz", Text: "Hattest du Schmerzen? (0 = keine)", Min: 0, Max: 10},
}

// MaxEigeneFragen caps a person's own questions: a catalogue answered every day stays short.
const MaxEigeneFragen = 3

// Record is what the program did with each document, and whom it asked.
type Record struct {
	Gelesen map[string]*Eintrag `json:"gelesen"` // by document id
	Lauf    *Lauf               `json:"lauf,omitempty"`
	Gefragt map[string][]string `json:"gefragt,omitempty"` // day → the persons asked for their note that day
	// Sortiert are the untyped documents the model was asked about (#3), by id:
	// the kind of record it set as their type, "" for none
	Sortiert map[string]string `json:"sortiert,omitempty"`
}

// Eintrag is one document's state: read and assigned, or waiting for a person.
type Eintrag struct {
	At      time.Time `json:"at"`
	Person  string    `json:"person,omitempty"` // the key; "" is unassigned
	Gelesen *Gelesen  `json:"gelesen,omitempty"`
	Fehler  string    `json:"fehler,omitempty"` // a reason the document was given up on (no text)
}

// Lauf is what the last run did (the status line).
type Lauf struct {
	Am        time.Time `json:"am"`
	Anlass    string    `json:"anlass"`
	Gelesen   int       `json:"gelesen"`
	Werte     int       `json:"werte"`
	Pruefen   int       `json:"pruefen"`
	Offen     int       `json:"offen"`              // documents waiting for a person
	Sortiert  int       `json:"sortiert,omitempty"` // untyped documents recognised as records and typed (#3)
	ModellWeg bool      `json:"modell_weg,omitempty"`
	Fehler    string    `json:"fehler,omitempty"`
}

func (s *Speicher) lesen(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return json.Unmarshal(b, v)
}

func (s *Speicher) schreiben(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	tmp := path + ".neu"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Speicher) person(key, name string) string {
	return filepath.Join(s.Dir, "personen", filepath.Base(key), name)
}

// Record reads the record.
func (s *Speicher) Record() (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.record()
}

func (s *Speicher) record() (*Record, error) {
	r := &Record{}
	if err := s.lesen(filepath.Join(s.Dir, "record.json"), r); err != nil {
		return nil, err
	}
	if r.Gelesen == nil {
		r.Gelesen = map[string]*Eintrag{}
	}
	if r.Sortiert == nil {
		r.Sortiert = map[string]string{}
	}
	return r, nil
}

// Merken writes one document's entry into the record.
func (s *Speicher) Merken(id string, e *Eintrag) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.record()
	if err != nil {
		return err
	}
	r.Gelesen[id] = e
	return s.schreiben(filepath.Join(s.Dir, "record.json"), r)
}

// SortiertMerken keeps the model's verdict on an untyped document (#3).
func (s *Speicher) SortiertMerken(id, art string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.record()
	if err != nil {
		return err
	}
	r.Sortiert[id] = art
	return s.schreiben(filepath.Join(s.Dir, "record.json"), r)
}

// Vergessen drops a document's entry, so a later run reads it again when it is a record again.
func (s *Speicher) Vergessen(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.record()
	if err != nil {
		return err
	}
	delete(r.Gelesen, id)
	return s.schreiben(filepath.Join(s.Dir, "record.json"), r)
}

// GefragtMerken notes that a person was asked for the day's note; days
// older than a week are dropped.
func (s *Speicher) GefragtMerken(tag, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.record()
	if err != nil {
		return err
	}
	if r.Gefragt == nil {
		r.Gefragt = map[string][]string{}
	}
	r.Gefragt[tag] = append(r.Gefragt[tag], key)
	for d := range r.Gefragt {
		if daysBetween(d, tag) > 7 {
			delete(r.Gefragt, d)
		}
	}
	return s.schreiben(filepath.Join(s.Dir, "record.json"), r)
}

func daysBetween(a, b string) int {
	ta, err1 := time.Parse("2006-01-02", a)
	tb, err2 := time.Parse("2006-01-02", b)
	if err1 != nil || err2 != nil {
		return 0
	}
	return int(tb.Sub(ta).Hours() / 24)
}

// LaufMerken writes what the last run did.
func (s *Speicher) LaufMerken(l Lauf) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.record()
	if err != nil {
		return err
	}
	r.Lauf = &l
	return s.schreiben(filepath.Join(s.Dir, "record.json"), r)
}

// Verlauf reads a person's values and events.
func (s *Speicher) Verlauf(key string) (Verlauf, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var v Verlauf
	err := s.lesen(s.person(key, "verlauf.json"), &v)
	return v, err
}

// Eintragen adds a document's values and events to a person, replacing
// what the same document gave before (a document read again, or moved
// from another person).
func (s *Speicher) Eintragen(key string, g Gelesen) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var v Verlauf
	if err := s.lesen(s.person(key, "verlauf.json"), &v); err != nil {
		return err
	}
	v.Werte = ohneDokument(v.Werte, g.Dokument)
	v.Werte = append(v.Werte, g.Werte...)
	var ev []Ereignis
	for _, e := range v.Ereignisse {
		if e.Dokument != g.Dokument {
			ev = append(ev, e)
		}
	}
	v.Ereignisse = append(ev, g.Ereignisse...)
	sort.SliceStable(v.Werte, func(i, j int) bool { return v.Werte[i].Datum < v.Werte[j].Datum })
	sort.SliceStable(v.Ereignisse, func(i, j int) bool { return v.Ereignisse[i].Datum < v.Ereignisse[j].Datum })
	return s.schreiben(s.person(key, "verlauf.json"), v)
}

// Austragen removes a document's values and events from a person.
func (s *Speicher) Austragen(key string, doc int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var v Verlauf
	if err := s.lesen(s.person(key, "verlauf.json"), &v); err != nil {
		return err
	}
	v.Werte = ohneDokument(v.Werte, doc)
	var ev []Ereignis
	for _, e := range v.Ereignisse {
		if e.Dokument != doc {
			ev = append(ev, e)
		}
	}
	v.Ereignisse = ev
	return s.schreiben(s.person(key, "verlauf.json"), v)
}

func ohneDokument(w []Messwert, doc int) []Messwert {
	var out []Messwert
	for _, m := range w {
		if m.Dokument != doc {
			out = append(out, m)
		}
	}
	return out
}

// Tage reads a person's daily notes, oldest first.
func (s *Speicher) Tage(key string) ([]Tagesnotiz, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var t []Tagesnotiz
	err := s.lesen(s.person(key, "tage.json"), &t)
	sort.SliceStable(t, func(i, j int) bool { return t[i].Datum < t[j].Datum })
	return t, err
}

// TagEintragen writes one day's note, replacing the day's earlier one.
func (s *Speicher) TagEintragen(key string, n Tagesnotiz) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var t []Tagesnotiz
	if err := s.lesen(s.person(key, "tage.json"), &t); err != nil {
		return err
	}
	var out []Tagesnotiz
	for _, x := range t {
		if x.Datum != n.Datum {
			out = append(out, x)
		}
	}
	out = append(out, n)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Datum < out[j].Datum })
	return s.schreiben(s.person(key, "tage.json"), out)
}

// Fragen are a person's questions: the base ones, then their own.
func (s *Speicher) Fragen(key string) ([]Frage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var eigene []Frage
	if err := s.lesen(s.person(key, "fragen.json"), &eigene); err != nil {
		return nil, err
	}
	out := append([]Frage{}, Grundfragen...)
	for _, f := range eigene {
		f.Eigen = true
		out = append(out, f)
	}
	return out, nil
}

// FragenSetzen writes a person's own questions (at most MaxEigeneFragen).
func (s *Speicher) FragenSetzen(key string, eigene []Frage) error {
	if len(eigene) > MaxEigeneFragen {
		return errors.New("höchstens drei eigene Fragen")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range eigene {
		eigene[i].Eigen = true
		if eigene[i].Key == "" {
			eigene[i].Key = "eigen-" + strings.Join(worte(eigene[i].Text), "-")
		}
	}
	return s.schreiben(s.person(key, "fragen.json"), eigene)
}

// Loeschen removes a person's folder: their values, days and questions.
func (s *Speicher) Loeschen(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return os.RemoveAll(filepath.Join(s.Dir, "personen", filepath.Base(key)))
}
