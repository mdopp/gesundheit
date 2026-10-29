package main

import (
	"regexp"
	"sort"
	"strings"
)

// Person is someone whose values the program keeps: a family member with a
// login (Paperless knows them as a user, Authelia sends them as
// Remote-User), or a child without one, named in KINDER. Key is the folder
// under /data: the login, or kind-<name> for a child.
type Person struct {
	Key       string
	Login     string // "" for a child
	Anzeige   string // the name on the page
	Vorname   []string
	Nachname  []string
	Paperless int // Paperless' user id, 0 when unknown: then a document cannot be given to them
}

// Kind is whether the person has no login: their timeline is shown to the admins.
func (p Person) Kind() bool { return p.Login == "" }

// anredeWorte are words before a name that are no part of it.
var anredeWorte = map[string]bool{"herr": true, "herrn": true, "frau": true, "hr": true, "fr": true, "dr": true, "prof": true, "med": true, "dipl": true, "ing": true, "patient": true, "patientin": true, "pat": true}

var wortRe = regexp.MustCompile(`[a-z0-9]+`)

// worte is a name as words to compare, as dokumente's worte(): case,
// umlauts, accents and punctuation folded; "Muster, Anna" is anna muster.
func worte(name string) []string {
	s := name
	if i := strings.IndexByte(s, ','); i >= 0 {
		s = s[i+1:] + " " + s[:i]
	}
	return wortRe.FindAllString(falten(s), -1)
}

// paperlessUser is what /api/users/ says of one person.
type paperlessUser struct {
	ID        int    `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

// haushalt is the household's people: Paperless' users (first and last
// name as allauth took them from Authelia, else the login), minus the
// service accounts, plus the children from KINDER ("Lena Muster, Tom Muster").
func haushalt(users []paperlessUser, kinder string, dienste map[string]bool) []Person {
	var out []Person
	for _, u := range users {
		if u.Username == "" || dienste[u.Username] || u.Username == "AnonymousUser" {
			continue
		}
		p := Person{Key: u.Username, Login: u.Username, Paperless: u.ID,
			Vorname: worte(u.FirstName), Nachname: worte(u.LastName)}
		p.Anzeige = strings.TrimSpace(u.FirstName + " " + u.LastName)
		if p.Anzeige == "" {
			p.Anzeige = u.Username
		}
		if len(p.Vorname) == 0 && len(p.Nachname) == 0 && regexp.MustCompile(`^[a-zA-Z]+$`).MatchString(u.Username) {
			p.Vorname = worte(u.Username) // "anna": a login that is a name
		}
		out = append(out, p)
	}
	for _, n := range strings.Split(kinder, ",") {
		n = strings.TrimSpace(n)
		w := worte(n)
		if len(w) == 0 {
			continue
		}
		p := Person{Key: "kind-" + strings.Join(w, "-"), Anzeige: n, Vorname: w}
		if len(w) > 1 {
			p.Vorname, p.Nachname = w[:len(w)-1], w[len(w)-1:]
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Anzeige < out[j].Anzeige })
	return out
}

// wessen is the household person a report's patient name is, as
// dokumente's eigener(): the full name in either order, or a given name
// alone when only one person has it. Salutations and titles do not count.
// nil when no one or more than one matches.
func wessen(name string, personen []Person) *Person {
	w := worte(name)
	for len(w) > 0 && anredeWorte[w[0]] {
		w = w[1:]
	}
	if len(w) == 0 {
		return nil
	}
	var voll, nurVorname []*Person
	for i := range personen {
		p := &personen[i]
		ganz := append(append([]string{}, p.Vorname...), p.Nachname...)
		umgekehrt := append(append([]string{}, p.Nachname...), p.Vorname...)
		switch {
		case gleich(w, ganz) || (len(p.Nachname) > 0 && gleich(w, umgekehrt)):
			voll = append(voll, p)
		case len(p.Vorname) > 0 && gleich(w, p.Vorname):
			nurVorname = append(nurVorname, p)
		}
	}
	if len(voll) == 1 {
		return voll[0]
	}
	if len(voll) == 0 && len(nurVorname) == 1 {
		return nurVorname[0]
	}
	return nil
}

func gleich(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// finden is the person with this key.
func finden(key string, personen []Person) *Person {
	for i := range personen {
		if personen[i].Key == key {
			return &personen[i]
		}
	}
	return nil
}

// sichtbar is whose timelines a viewer may open: their own, and the
// children's when they are an admin. Nobody else's, ever.
func sichtbar(login string, admin bool, personen []Person) []Person {
	var out []Person
	for _, p := range personen {
		if p.Login == login || (admin && p.Kind()) {
			out = append(out, p)
		}
	}
	return out
}
