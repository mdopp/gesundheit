package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// proLauf caps the documents read in one run: the household model is shared.
const proLauf = 10

const (
	tagPruefen = "prüfen"
	tagGelesen = "gesundheit"
)

// Dienst ties the parts together: Paperless, the model, the store, the
// household's people, and the runs the hook and the hour start.
type Dienst struct {
	Paperless  *Paperless
	Modell     *Modell
	Speicher   *Speicher
	Arten      []string        // BEFUND_ARTEN: document types or tags that are medical records
	Kinder     string          // KINDER: the children's names
	Dienste    map[string]bool // Paperless' service accounts, left out of the household
	Ich        string          // PAPERLESS_USER, the program's own Paperless user
	Public     string          // PAPERLESS_PUBLIC_URL, for links
	StatusFile string
	Jetzt      func() time.Time
	// the daily question (laib ADR 0022 §3): bote-notify's /mitteilung with its bearer, from
	// FrageUm (HH:MM, local time) on; without URL or token nobody is asked
	NotifyURL, NotifyToken, FrageUm string
	// Seite is the page's public address, the daily question's link; "" sends the scales alone
	Seite string
	HTTP  *http.Client

	mu       sync.Mutex
	personen []Person
	laeuft   bool
	hooks    struct{ erster, letzter time.Time }
}

// Personen are the household's people as last fetched from Paperless, plus
// the children; the cache is refreshed by each run.
func (d *Dienst) Personen(ctx context.Context) []Person {
	d.mu.Lock()
	have := d.personen
	d.mu.Unlock()
	if have != nil {
		return have
	}
	return d.ladePersonen(ctx)
}

func (d *Dienst) ladePersonen(ctx context.Context) []Person {
	var users []paperlessUser
	if d.Paperless != nil && d.Paperless.Token != "" {
		if u, err := d.Paperless.Users(ctx); err == nil {
			users = u
		} else {
			log.Printf("personen: %v", err)
		}
	}
	// solaris: the retired stack's Paperless account (laib ADR 0015), still on boxes that migrated
	dienste := map[string]bool{"admin": true, "dokumente": true, "haus": true, "gesundheit": true, "solaris": true}
	for k := range d.Dienste {
		dienste[k] = true
	}
	if d.Ich != "" {
		dienste[d.Ich] = true
	}
	p := haushalt(users, d.Kinder, dienste)
	d.mu.Lock()
	d.personen = p
	d.mu.Unlock()
	return p
}

// meineID is the program's own Paperless user id, 0 when unknown.
func (d *Dienst) meineID(ctx context.Context) int {
	if d.Paperless == nil || d.Ich == "" {
		return 0
	}
	users, err := d.Paperless.Users(ctx)
	if err != nil {
		return 0
	}
	for _, u := range users {
		if u.Username == d.Ich {
			return u.ID
		}
	}
	return 0
}

// Hook is Paperless telling of a new document: a run starts once the hooks
// have been quiet for ruhe, at the latest spaetestens after the first.
func (d *Dienst) Hook() {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	if d.hooks.erster.IsZero() {
		d.hooks.erster = now
	}
	d.hooks.letzter = now
}

func (d *Dienst) now() time.Time {
	if d.Jetzt != nil {
		return d.Jetzt()
	}
	return time.Now()
}

// Takt decides every tick whether a run is due: the hooks quiet, or the hour.
func (d *Dienst) Takt(ctx context.Context, ruhe, spaetestens, stuendlich time.Duration) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	letzter := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now := d.now()
		d.mu.Lock()
		anlass := ""
		if !d.hooks.erster.IsZero() && (now.Sub(d.hooks.letzter) >= ruhe || now.Sub(d.hooks.erster) >= spaetestens) {
			anlass, d.hooks.erster, d.hooks.letzter = "neue Dokumente", time.Time{}, time.Time{}
		} else if now.Sub(letzter) >= stuendlich {
			anlass = "stündlich"
		}
		d.mu.Unlock()
		if anlass != "" {
			letzter = now
			d.Lauf(ctx, anlass)
		}
		d.Fragen(ctx)
	}
}

// Fragen sends the day's question once to every person with a login who
// has not noted the day yet, from FrageUm on; a person asked today is not
// asked again, whether or not they answer (a gap stays a gap).
func (d *Dienst) Fragen(ctx context.Context) int {
	if d.NotifyURL == "" || d.NotifyToken == "" || d.FrageUm == "" {
		return 0
	}
	now := d.now()
	um, err := time.ParseInLocation("15:04", d.FrageUm, now.Location())
	if err != nil {
		return 0
	}
	heute := now.Format("2006-01-02")
	if now.Before(time.Date(now.Year(), now.Month(), now.Day(), um.Hour(), um.Minute(), 0, 0, now.Location())) {
		return 0
	}
	rec, err := d.Speicher.Record()
	if err != nil {
		return 0
	}
	schon := map[string]bool{}
	for _, k := range rec.Gefragt[heute] {
		schon[k] = true
	}
	n := 0
	for _, p := range d.Personen(ctx) {
		if p.Kind() || schon[p.Key] {
			continue
		}
		tage, _ := d.Speicher.Tage(p.Key)
		if len(tage) > 0 && tage[len(tage)-1].Datum == heute {
			continue
		}
		fragen, _ := d.Speicher.Fragen(p.Key)
		if err := d.mitteilung(ctx, p.Login, frageText(fragen, d.Seite)); err != nil {
			// one try a day: 2026-10-29 a person the Bote did not know (404) was asked every tick,
			// 200 log lines in 15 minutes and the Bote hammered; tomorrow is soon enough
			log.Printf("tagesfrage %s: %v (heute kein weiterer Versuch)", p.Login, err)
			if err := d.Speicher.GefragtMerken(heute, p.Key); err != nil {
				log.Printf("tagesfrage: %v", err)
			}
			continue
		}
		if err := d.Speicher.GefragtMerken(heute, p.Key); err != nil {
			log.Printf("tagesfrage: %v", err)
		}
		n++
	}
	return n
}

// frageText is the question as the Bote delivers it: first the link to
// the page's form (the owner, 2026-10-29: „Sollten das nicht eher ein
// Verweis auf ein Formular sein?“), then, for whoever would rather answer
// in the chat, the scales in one line so the answer can be one line too.
// Without a public address only the line of scales goes.
func frageText(fragen []Frage, seite string) string {
	var teile []string
	for _, f := range fragen {
		text := strings.TrimSuffix(f.Text, "?")
		text = strings.TrimSuffix(strings.TrimSuffix(text, " (0 = keine)"), "?")
		teile = append(teile, fmt.Sprintf("%s (%d–%d)", text, f.Min, f.Max))
	}
	zahlen := strings.Join(teile, " · ")
	if seite == "" {
		return "Kurz zu deinem Tag: " + zahlen + ". Antworte mir mit den Zahlen in dieser Reihenfolge und, wenn du magst, einem Satz – ich trage es in deine Gesundheit ein."
	}
	return "Wie war dein Tag? " + strings.TrimRight(seite, "/") + "/ – vier kurze Fragen, unter einer Minute. Oder antworte mir hier mit den Zahlen: " + zahlen + ", in dieser Reihenfolge."
}

// mitteilung posts one message for one person to bote-notify.
func (d *Dienst) mitteilung(ctx context.Context, an, text string) error {
	b, _ := json.Marshal(map[string]string{"an": an, "text": text})
	req, err := http.NewRequestWithContext(ctx, "POST", d.NotifyURL, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+d.NotifyToken)
	c := d.HTTP
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("bote-notify antwortet %d", resp.StatusCode)
	}
	return nil
}

// Lauf reads the medical documents not read yet, at most proLauf, oldest
// first; a model or Paperless away ends the run and the next asks again.
func (d *Dienst) Lauf(ctx context.Context, anlass string) Lauf {
	d.mu.Lock()
	if d.laeuft {
		d.mu.Unlock()
		return Lauf{}
	}
	d.laeuft = true
	d.mu.Unlock()
	defer func() { d.mu.Lock(); d.laeuft = false; d.mu.Unlock() }()

	l := Lauf{Am: d.now(), Anlass: anlass}
	defer func() {
		if err := d.Speicher.LaufMerken(l); err != nil {
			log.Printf("lauf: %v", err)
		}
		d.Status()
	}()
	if d.Paperless == nil || d.Paperless.Token == "" {
		l.Fehler = "kein Zugang zu Paperless (PAPERLESS_TOKEN fehlt noch)"
		return l
	}
	personen := d.ladePersonen(ctx)
	rec, err := d.Speicher.Record()
	if err != nil {
		l.Fehler = err.Error()
		return l
	}
	typen, err := d.Paperless.Namen(ctx, "/api/document_types/", d.Arten)
	if err != nil {
		l.Fehler = err.Error()
		return l
	}
	tags, err := d.Paperless.Namen(ctx, "/api/tags/", d.Arten)
	if err != nil {
		l.Fehler = err.Error()
		return l
	}
	docs, err := d.Paperless.Befunde(ctx, typen, tags)
	if err != nil {
		l.Fehler = err.Error()
		return l
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].ID < docs[j].ID })
	ich := d.meineID(ctx)
	for _, doc := range docs {
		if _, done := rec.Gelesen[strconv.Itoa(doc.ID)]; done {
			continue
		}
		if l.Gelesen >= proLauf {
			break
		}
		err := d.lesen(ctx, doc, personen, ich, &l)
		if errors.Is(err, ErrModellWeg) {
			l.ModellWeg = true
			log.Printf("lauf: %v, der nächste Lauf fragt wieder", err)
			break
		}
		var pf *Fehler
		if errors.As(err, &pf) {
			l.Fehler = err.Error()
			break
		}
		if err != nil {
			log.Printf("dokument %d: %v", doc.ID, err)
		}
	}
	for _, e := range rec.Gelesen {
		if e.Person == "" && e.Gelesen != nil {
			l.Offen++
		}
	}
	return l
}

// lesen reads one document and files what it yields.
func (d *Dienst) lesen(ctx context.Context, meta Dokument, personen []Person, ich int, l *Lauf) error {
	doc, err := d.Paperless.Lesen(ctx, meta.ID)
	if err != nil {
		return err
	}
	id := strconv.Itoa(doc.ID)
	if len(strings.Fields(doc.Content)) < 12 {
		// no text yet (OCR still running) or none at all: not this run, asked again by the hour
		return nil
	}
	a, err := d.Modell.Frage(ctx, doc.Content)
	if err != nil {
		return err
	}
	l.Gelesen++
	g := auswerten(doc.ID, doc.Title, doc.Content, doc.Created, a, d.now())
	l.Werte += len(g.Werte)
	e := &Eintrag{At: d.now(), Gelesen: &g}
	if p := wessen(g.Patient, personen); p != nil {
		e.Person = p.Key
		if err := d.Speicher.Eintragen(p.Key, g); err != nil {
			return err
		}
		if err := d.Paperless.Geben(ctx, doc, p.Paperless, ich); err != nil {
			log.Printf("dokument %d: nicht übergeben: %v", doc.ID, err)
			g.Weg = append(g.Weg, "Dokument konnte nicht der Person übergeben werden")
		}
	} else {
		l.Offen++
	}
	if err := d.Speicher.Merken(id, e); err != nil {
		return err
	}
	// the note and the tags: what was read, for whom, what was not taken
	var note []string
	if e.Person != "" {
		note = append(note, fmt.Sprintf("Gesundheit: %d Werte und %d Ereignisse für %s in den Verlauf übernommen.", len(g.Werte), len(g.Ereignisse), finden(e.Person, personen).Anzeige))
	} else if g.Patient != "" {
		note = append(note, fmt.Sprintf("Gesundheit: %d Werte gelesen, aber „%s“ ist niemand aus dem Haushalt. Bitte unter Gesundheit zuordnen.", len(g.Werte), g.Patient))
	} else {
		note = append(note, fmt.Sprintf("Gesundheit: %d Werte gelesen, aber kein Patient im Text. Bitte unter Gesundheit zuordnen.", len(g.Werte)))
	}
	if len(g.Weg) > 0 {
		note = append(note, "Nicht übernommen: "+strings.Join(g.Weg, "; ")+".")
	}
	if err := d.Paperless.Notiz(ctx, doc.ID, strings.Join(note, " ")); err != nil {
		log.Printf("dokument %d: notiz: %v", doc.ID, err)
	}
	if e.Person == "" || len(g.Weg) > 0 {
		l.Pruefen++
		if err := d.Paperless.Tag(ctx, &doc, tagPruefen); err != nil {
			log.Printf("dokument %d: tag: %v", doc.ID, err)
		}
	}
	if err := d.Paperless.Tag(ctx, &doc, tagGelesen); err != nil {
		log.Printf("dokument %d: tag: %v", doc.ID, err)
	}
	return nil
}

// Zuordnen gives a document read without a match to a person (the page's
// „Für wen?“), or moves it from one person to another.
func (d *Dienst) Zuordnen(ctx context.Context, doc int, key string) error {
	rec, err := d.Speicher.Record()
	if err != nil {
		return err
	}
	e := rec.Gelesen[strconv.Itoa(doc)]
	if e == nil || e.Gelesen == nil {
		return errors.New("dieses Dokument ist nicht gelesen")
	}
	personen := d.Personen(ctx)
	p := finden(key, personen)
	if p == nil {
		return errors.New("diese Person gibt es nicht")
	}
	if e.Person != "" && e.Person != key {
		if err := d.Speicher.Austragen(e.Person, doc); err != nil {
			return err
		}
	}
	if err := d.Speicher.Eintragen(key, *e.Gelesen); err != nil {
		return err
	}
	e.Person = key
	if d.Paperless != nil && d.Paperless.Token != "" && p.Paperless > 0 {
		if dok, err := d.Paperless.Lesen(ctx, doc); err == nil {
			if err := d.Paperless.Geben(ctx, dok, p.Paperless, d.meineID(ctx)); err != nil {
				log.Printf("dokument %d: nicht übergeben: %v", doc, err)
			}
		}
	}
	return d.Speicher.Merken(strconv.Itoa(doc), e)
}

// Offene are the documents read but not assigned to anyone.
func (d *Dienst) Offene() []Eintrag {
	rec, err := d.Speicher.Record()
	if err != nil {
		return nil
	}
	var out []Eintrag
	for _, e := range rec.Gelesen {
		if e.Person == "" && e.Gelesen != nil {
			out = append(out, *e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Gelesen.Dokument < out[j].Gelesen.Dokument })
	return out
}

// Status writes the status file laib reads (STATE.md: at, detail,
// findings; further fields reach laib_status as eigenes).
func (d *Dienst) Status() {
	if d.StatusFile == "" {
		return
	}
	rec, _ := d.Speicher.Record()
	st := map[string]any{"at": d.now().Format(time.RFC3339)}
	var findings []map[string]string
	detail := "Noch kein Lauf."
	if rec != nil && rec.Lauf != nil {
		l := rec.Lauf
		detail = fmt.Sprintf("Zuletzt %s: %d Dokumente gelesen, %d Werte", l.Am.Format("02.01. 15:04"), l.Gelesen, l.Werte)
		if l.Offen > 0 {
			detail += fmt.Sprintf(", %d warten auf eine Zuordnung", l.Offen)
			findings = append(findings, map[string]string{"severity": "gelb", "detail": fmt.Sprintf("%d Befunde sind niemandem zugeordnet", l.Offen), "fix": "Unter Gesundheit → „Für wen?“ zuordnen"})
		}
		if l.ModellWeg {
			detail += "; das Modell antwortete nicht, der nächste Lauf fragt wieder"
		}
		if l.Fehler != "" {
			detail += "; " + l.Fehler
			findings = append(findings, map[string]string{"severity": "gelb", "detail": l.Fehler, "fix": "laib logs gesundheit"})
		}
		st["lauf"] = l
	}
	if d.Paperless == nil || d.Paperless.Token == "" {
		findings = append(findings, map[string]string{"severity": "gelb", "detail": "Kein Zugang zu Paperless: PAPERLESS_TOKEN fehlt (paperless' post-deploy stellt ihn aus)", "fix": "laib template update paperless"})
	}
	st["detail"] = detail
	if findings != nil {
		st["findings"] = findings
	}
	s := &Speicher{Dir: filepath.Dir(d.StatusFile)}
	if err := s.schreiben(d.StatusFile, st); err != nil {
		log.Printf("status: %v", err)
	}
}

// dauer reads a duration in seconds from the environment, with a default.
func dauer(name string, def time.Duration) time.Duration {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return def
}
