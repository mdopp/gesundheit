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
	Bild       *Bildleser // the image preset through the lease, for pages whose text is weak (#5); nil: such pages wait
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
	// transcripts read but not yet filed (the household model did not answer
	// after the card went back): the next run uses them instead of the GPU again
	abschriften map[int]Abschrift
	hooks       struct{ erster, letzter time.Time }
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
	if err := d.sortieren(ctx, typen, rec, &l); errors.Is(err, ErrModellWeg) {
		l.ModellWeg = true
		log.Printf("lauf: %v, der nächste Lauf fragt wieder", err)
		return l
	} else if err != nil {
		l.Fehler = err.Error()
		return l
	}
	docs, err := d.Paperless.Befunde(ctx, typen, tags)
	if err != nil {
		l.Fehler = err.Error()
		return l
	}
	d.umgestellt(ctx, rec, docs, typen, tags)
	sort.Slice(docs, func(i, j int) bool { return docs[i].ID < docs[j].ID })
	ich := d.meineID(ctx)
	// fetch the unread ones and weigh their text: a weak page is read from its image (#5)
	var stapel []*kandidat
	geholt := 0
	for _, meta := range docs {
		if _, done := rec.Gelesen[strconv.Itoa(meta.ID)]; done {
			continue
		}
		if len(stapel) >= proLauf || geholt >= 3*proLauf {
			break
		}
		geholt++
		doc, err := d.Paperless.Lesen(ctx, meta.ID)
		if err != nil {
			l.Fehler = err.Error()
			break
		}
		k := &kandidat{doc: doc, guete: guete(doc.Content)}
		if k.guete.Schwach() && d.Bild == nil {
			l.BildWartet++ // waits for IMAGE_MODEL; its weak text never yields a value
			continue
		}
		stapel = append(stapel, k)
	}
	if err := d.bilderLesen(ctx, stapel, &l); err != nil {
		var pf *Fehler
		if errors.As(err, &pf) {
			l.Fehler = err.Error()
			return l
		}
		log.Printf("lauf: Bilder: %v, der nächste Lauf versucht es wieder", err)
		l.BildGrund = err.Error()
	}
	for _, k := range stapel {
		if k.guete.Schwach() && k.abschrift == nil {
			l.BildWartet++ // the card was busy or away: next run
			continue
		}
		err := d.lesen(ctx, k, personen, ich, &l)
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
			log.Printf("dokument %d: %v", k.doc.ID, err)
		}
	}
	for _, e := range rec.Gelesen {
		if e.Person == "" && e.Gelesen != nil {
			l.Offen++
		}
	}
	return l
}

// kandidat is one unread document of a run: its text, how readable that
// is, and, for a weak one, what its image said.
type kandidat struct {
	doc       Dokument
	guete     Guete
	abschrift *Abschrift
}

// bilderLesen transcribes the images of the run's weak documents under one
// lease: the image preset replaces the household model on the card, so the
// images are read together and the card goes back before the household
// model reads the transcripts. A busy card leaves them for the next run.
func (d *Dienst) bilderLesen(ctx context.Context, stapel []*kandidat, l *Lauf) error {
	var schwach []*kandidat
	for _, k := range stapel {
		if k.guete.Schwach() {
			schwach = append(schwach, k)
		}
	}
	if len(schwach) == 0 || d.Bild == nil {
		return nil
	}
	// the files first: a document without a readable image needs no card
	seiten := map[*kandidat][][]byte{}
	for _, k := range schwach {
		d.mu.Lock()
		a, schon := d.abschriften[k.doc.ID]
		d.mu.Unlock()
		if schon {
			k.abschrift = &a
			continue
		}
		data, mime, err := d.Paperless.Original(ctx, k.doc.ID)
		if err != nil {
			return err
		}
		s, err := seitenBilder(data, mime)
		if err != nil {
			k.abschrift = &Abschrift{Grund: err.Error()}
			continue
		}
		seiten[k] = s
	}
	if len(seiten) == 0 {
		return nil
	}
	start := time.Now()
	if err := d.Bild.Lease.Nehmen(ctx, d.Bild.Name); err != nil {
		return err
	}
	defer d.Bild.Lease.Zurueck(context.WithoutCancel(ctx))
	for _, k := range schwach {
		s, ok := seiten[k]
		if !ok {
			continue
		}
		t0 := time.Now()
		a, err := d.Bild.abschreiben(ctx, s)
		if err != nil {
			return err
		}
		k.abschrift = &a
		d.mu.Lock()
		if d.abschriften == nil {
			d.abschriften = map[int]Abschrift{}
		}
		d.abschriften[k.doc.ID] = a
		d.mu.Unlock()
		l.Bild++
		log.Printf("dokument %d: %d Seiten aus dem Bild gelesen in %s (%s)", k.doc.ID, len(s), time.Since(t0).Round(time.Second), k.guete)
	}
	l.BildSekunden += int(time.Since(start).Seconds())
	return nil
}

// lesen reads one document and files what it yields: its text, or, when
// that is weak, the transcript of its image.
func (d *Dienst) lesen(ctx context.Context, k *kandidat, personen []Person, ich int, l *Lauf) error {
	doc := k.doc
	id := strconv.Itoa(doc.ID)
	if k.guete.Schwach() && !k.abschrift.lesbar() {
		// #5: no invented numbers. The text is no ground and the image said nothing.
		l.Gelesen++
		l.Pruefen++
		grund := k.abschrift.Grund
		if err := d.Speicher.Merken(id, &Eintrag{At: d.now(), Fehler: "nicht lesbar: " + grund}); err != nil {
			return err
		}
		note := fmt.Sprintf("%s Keine Werte übernommen. Der Text dieser Seiten ist zu schwach zum Lesen (%s), und aus dem Bild: %s. Bitte selbst ansehen; Zahlen, die hier nicht sicher stehen, trage ich nicht ein.", notizAnfang, k.guete, grund)
		d.notiz(ctx, doc.ID, note, ich, l)
		d.taggen(ctx, &doc, tagPruefen, tagGelesen)
		return nil
	}
	text := doc.Content
	if k.abschrift.lesbar() {
		text = k.abschrift.Text
	}
	a, err := d.Modell.Frage(ctx, text)
	if err != nil {
		return err
	}
	l.Gelesen++
	g := auswerten(doc.ID, doc.Title, text, doc.Created, a, d.now())
	if k.abschrift.lesbar() {
		g.ausDemBild(k.abschrift.Text, doc.Content)
	}
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
	d.mu.Lock()
	delete(d.abschriften, doc.ID)
	d.mu.Unlock()
	d.notiz(ctx, doc.ID, notizText(g, e.Person, personen, k.guete), ich, l)
	tags := []string{tagGelesen}
	if e.Person == "" || len(g.Weg) > 0 || len(g.Abweichungen) > 0 {
		l.Pruefen++
		tags = append([]string{tagPruefen}, tags...)
	}
	d.taggen(ctx, &doc, tags...)
	return nil
}

// notizText is the document's one note: what was read, for whom, what was
// not taken, and for a page read from its image the transcript in Markdown.
func notizText(g Gelesen, person string, personen []Person, q Guete) string {
	var b strings.Builder
	switch {
	case person != "":
		fmt.Fprintf(&b, "%s %d Werte und %d Ereignisse für %s in den Verlauf übernommen.", notizAnfang, len(g.Werte), len(g.Ereignisse), finden(person, personen).Anzeige)
	case g.Patient != "":
		fmt.Fprintf(&b, "%s %d Werte gelesen, aber „%s“ ist niemand aus dem Haushalt. Bitte unter Gesundheit zuordnen.", notizAnfang, len(g.Werte), g.Patient)
	default:
		fmt.Fprintf(&b, "%s %d Werte gelesen, aber kein Patient im Text. Bitte unter Gesundheit zuordnen.", notizAnfang, len(g.Werte))
	}
	if g.Quelle == "bild" {
		fmt.Fprintf(&b, " Der Text der Seiten war zu schwach (%s); die Werte sind aus dem Bild gelesen.", q)
	}
	if len(g.Abweichungen) > 0 {
		b.WriteString("\n\nBitte prüfen: " + strings.Join(g.Abweichungen, "; ") + ".")
	}
	if len(g.Weg) > 0 {
		b.WriteString("\n\nNicht übernommen: " + strings.Join(g.Weg, "; ") + ".")
	}
	if g.Abschrift != "" {
		b.WriteString("\n\n---\n\n**Abschrift aus dem Bild** (nur abgeschrieben, nicht gedeutet):\n\n" + g.Abschrift)
	}
	return b.String()
}

func (d *Dienst) notiz(ctx context.Context, doc int, text string, ich int, l *Lauf) {
	if err := d.Paperless.Notiz(ctx, doc, text, ich); err != nil {
		log.Printf("dokument %d: notiz: %v", doc, err)
		if errors.Is(err, ErrNotizRecht) {
			l.NotizRecht = true
		}
	}
}

func (d *Dienst) taggen(ctx context.Context, doc *Dokument, namen ...string) {
	for _, n := range namen {
		if err := d.Paperless.Tag(ctx, doc, n); err != nil {
			log.Printf("dokument %d: tag: %v", doc.ID, err)
		}
	}
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
		if l.Sortiert > 0 {
			detail += fmt.Sprintf(", %d neu als Befund erkannt", l.Sortiert)
		}
		if l.Offen > 0 {
			detail += fmt.Sprintf(", %d warten auf eine Zuordnung", l.Offen)
			findings = append(findings, map[string]string{"severity": "gelb", "detail": fmt.Sprintf("%d Befunde sind niemandem zugeordnet", l.Offen), "fix": "Unter Gesundheit → „Für wen?“ zuordnen"})
		}
		if l.Bild > 0 {
			detail += fmt.Sprintf(", %d aus dem Bild gelesen (%d s Grafikkarte)", l.Bild, l.BildSekunden)
		}
		if l.BildWartet > 0 {
			warum := "die Grafikkarte war vergeben, der nächste Lauf liest sie"
			if d.Bild == nil {
				warum = "IMAGE_MODEL ist nicht gesetzt"
				findings = append(findings, map[string]string{"severity": "gelb", "detail": fmt.Sprintf("%d Befunde haben zu schwachen Text und warten auf das Lesen ihres Bildes", l.BildWartet), "fix": "IMAGE_MODEL im Template gesundheit setzen (gemma-4-12b)"})
			} else if l.BildGrund != "" {
				warum = l.BildGrund
			}
			detail += fmt.Sprintf(", %d warten auf das Bild (%s)", l.BildWartet, warum)
		}
		if l.NotizRecht {
			findings = append(findings, map[string]string{"severity": "gelb", "detail": ErrNotizRecht.Error() + "; die neue Notiz fehlt deshalb an diesen Dokumenten", "fix": "laib template update paperless"})
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
