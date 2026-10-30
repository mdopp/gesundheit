package main

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Profil is what a person says of themselves once (#10, owner 2026-10-30):
// the birth year, the sex, the height. It lives in the person's folder, is
// shown only on their own page (and a child's to the admins) and is used
// only where a report prints no range and no predicted value.
type Profil struct {
	Geburtsjahr int       `json:"geburtsjahr,omitempty"`
	Geschlecht  string    `json:"geschlecht,omitempty"` // "w" | "m"; reference equations are by sex
	GroesseCm   int       `json:"groesse_cm,omitempty"`
	At          time.Time `json:"at,omitempty"`
}

// leer is a profile with nothing entered.
func (p Profil) leer() bool { return p.Geburtsjahr == 0 && p.Geschlecht == "" && p.GroesseCm == 0 }

// Profil reads a person's profile; an empty one when none was entered.
func (s *Speicher) Profil(key string) (Profil, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var p Profil
	err := s.lesen(s.person(key, "profil.json"), &p)
	return p, err
}

// ProfilSetzen writes a person's profile.
func (s *Speicher) ProfilSetzen(key string, p Profil) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.schreiben(s.person(key, "profil.json"), p)
}

// profilAusFormular reads the form; an empty field is no answer.
func profilAusFormular(r *http.Request, jetzt time.Time) (Profil, error) {
	p := Profil{At: jetzt}
	if v := strings.TrimSpace(r.PostFormValue("geburtsjahr")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1900 || n > jetzt.Year() {
			return p, fmt.Errorf("das Geburtsjahr „%s“ ist kein Jahr zwischen 1900 und %d", v, jetzt.Year())
		}
		p.Geburtsjahr = n
	}
	switch g := r.PostFormValue("geschlecht"); g {
	case "", "w", "m":
		p.Geschlecht = g
	default:
		return p, errors.New("beim Geschlecht geht weiblich, männlich oder keine Angabe")
	}
	if v := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(r.PostFormValue("groesse")), "cm")); v != "" {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 40 || n > 250 {
			return p, fmt.Errorf("die Größe „%s“ ist keine Zahl in Zentimetern zwischen 40 und 250", v)
		}
		p.GroesseCm = n
	}
	return p, nil
}

func (w *Web) profil(rw http.ResponseWriter, r *http.Request) {
	p, _, _ := w.wer(rw, r)
	if p == nil {
		return
	}
	zurueck := `<p><a href="/?person=` + html.EscapeString(p.Key) + `#profil">Zurück</a></p>`
	pr, err := profilAusFormular(r, w.Dienst.now())
	if err != nil {
		seite(rw, http.StatusBadRequest, "Nicht gespeichert", `<p>Nicht gespeichert: `+html.EscapeString(err.Error())+`.</p>`+zurueck)
		return
	}
	if err := w.Dienst.Speicher.ProfilSetzen(p.Key, pr); err != nil {
		seite(rw, http.StatusInternalServerError, "Nicht gespeichert", `<p>Das Speichern ging nicht: `+html.EscapeString(err.Error())+`. Bitte später noch einmal.</p>`+zurueck)
		return
	}
	m := "Gespeichert."
	if len(sollFormeln) == 0 {
		m += " Für keinen Wert rechnet Gesundheit bisher selbst einen Bereich aus; wo der Befund keinen nennt, steht weiter „kein Bereich angegeben“."
	}
	http.Redirect(rw, r, "/?person="+p.Key+"&m="+urlQ(m)+"#profil", http.StatusSeeOther)
}

// profilHTML is the profile's section: what it is for, who sees it, the form.
func profilHTML(key string, p Profil) string {
	var b strings.Builder
	offen := ""
	if p.leer() {
		offen = " open"
	}
	fmt.Fprintf(&b, `<details id=profil class=profil%s><summary>Über dich: Geburtsjahr, Geschlecht, Größe</summary>`, offen)
	b.WriteString(`<p>Manche Befunde drucken zu einem Wert keinen Bereich. Für einzelne Werte gibt es anerkannte Formeln, die ihn aus Alter, Geschlecht und Größe ausrechnen; dafür fragt Gesundheit einmal danach. Was der Befund selbst nennt, gilt immer zuerst.</p>`)
	if len(sollFormeln) == 0 {
		b.WriteString(`<p class=k>Bisher rechnet Gesundheit für keinen Wert selbst einen Bereich aus: Die Formel, die Lungenfunktionsblätter benutzen (GLI-2012), braucht große Tabellen, die hier noch nicht sicher nachgebaut sind. Lieber kein Bereich als ein falscher.</p>`)
	}
	fmt.Fprintf(&b, `<form method=post action="/profil"><input type=hidden name=person value="%s">`, html.EscapeString(key))
	jahr := ""
	if p.Geburtsjahr > 0 {
		jahr = strconv.Itoa(p.Geburtsjahr)
	}
	groesse := ""
	if p.GroesseCm > 0 {
		groesse = strconv.Itoa(p.GroesseCm)
	}
	fmt.Fprintf(&b, `<label class=frage><span>Geburtsjahr</span><input name=geburtsjahr inputmode=numeric maxlength=4 value="%s" placeholder="z. B. 1980"></label>`, jahr)
	fmt.Fprintf(&b, `<label class=frage><span>Geschlecht (die Formeln unterscheiden weiblich und männlich)</span><select name=geschlecht><option value=""%s>keine Angabe</option><option value=w%s>weiblich</option><option value=m%s>männlich</option></select></label>`,
		sel(p.Geschlecht == ""), sel(p.Geschlecht == "w"), sel(p.Geschlecht == "m"))
	fmt.Fprintf(&b, `<label class=frage><span>Größe in cm</span><input name=groesse inputmode=numeric maxlength=3 value="%s" placeholder="z. B. 175"></label>`, groesse)
	b.WriteString(`<button>Speichern</button></form><p class=k>Nur du siehst diese Angaben; bei Kindern auch, wer die Box verwaltet. Ein leeres Feld löscht die Angabe.</p></details>`)
	return b.String()
}
