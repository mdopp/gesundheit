package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Paperless is the store of the documents (laib ADR 0017). The program
// reads with the token of Paperless' user `gesundheit`, no administrator:
// it sees the documents without an owner and those the workflow „Dokument
// hinzugefügt“ shared with it, sets tags and notes, and gives a document
// to the person it is about.
type Paperless struct {
	URL, Token string
	HTTP       *http.Client
}

// Dokument is what the program needs of one.
type Dokument struct {
	ID           int    `json:"id"`
	Title        string `json:"title"`
	Content      string `json:"content"`
	Created      string `json:"created_date"`
	Owner        *int   `json:"owner"`
	DocumentType *int   `json:"document_type"`
	Tags         []int  `json:"tags"`
	Modified     string `json:"modified"`
}

type benannt struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Fehler is Paperless refusing or not answering: the run stops and the
// next one asks again, nothing is dropped.
type Fehler struct {
	Code int
	Err  error
}

func (f *Fehler) Error() string {
	if f.Err != nil {
		return "Paperless: " + f.Err.Error()
	}
	return fmt.Sprintf("Paperless antwortet %d", f.Code)
}

func (p *Paperless) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(p.URL, "/")+path, rd)
	if err != nil {
		return &Fehler{Err: err}
	}
	req.Header.Set("Authorization", "Token "+p.Token)
	req.Header.Set("Accept", "application/json; version=9")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c := p.HTTP
	if c == nil {
		c = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return &Fehler{Err: err}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		return &Fehler{Code: resp.StatusCode}
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return &Fehler{Err: fmt.Errorf("%s: keine gültige Antwort", path)}
		}
	}
	return nil
}

// alle follows a list's pages.
func (p *Paperless) alle(ctx context.Context, path string, each func(json.RawMessage) error) error {
	next := path
	for next != "" {
		var page struct {
			Next    *string           `json:"next"`
			Results []json.RawMessage `json:"results"`
		}
		if err := p.do(ctx, "GET", next, nil, &page); err != nil {
			return err
		}
		for _, r := range page.Results {
			if err := each(r); err != nil {
				return err
			}
		}
		next = ""
		if page.Next != nil {
			if u, err := url.Parse(*page.Next); err == nil {
				next = u.RequestURI()
			}
		}
	}
	return nil
}

// Namen are the tags or document types whose name is one of arten, by id.
func (p *Paperless) Namen(ctx context.Context, path string, arten []string) (map[int]string, error) {
	want := map[string]bool{}
	for _, a := range arten {
		if a = strings.TrimSpace(a); a != "" {
			want[falten(a)] = true
		}
	}
	out := map[int]string{}
	err := p.alle(ctx, path+"?page_size=100", func(r json.RawMessage) error {
		var b benannt
		if json.Unmarshal(r, &b) == nil && want[falten(b.Name)] {
			out[b.ID] = b.Name
		}
		return nil
	})
	return out, err
}

// Befunde are the documents of the given types or tags, newest first,
// without their text (Lesen fetches it one by one).
func (p *Paperless) Befunde(ctx context.Context, typen, tags map[int]string) ([]Dokument, error) {
	seen := map[int]bool{}
	var out []Dokument
	add := func(r json.RawMessage) error {
		var d Dokument
		if json.Unmarshal(r, &d) == nil && !seen[d.ID] {
			seen[d.ID] = true
			out = append(out, d)
		}
		return nil
	}
	fields := "&fields=id,title,created_date,owner,document_type,tags,modified&ordering=-created&page_size=100"
	if len(typen) > 0 {
		if err := p.alle(ctx, "/api/documents/?document_type__id__in="+ids(typen)+fields, add); err != nil {
			return nil, err
		}
	}
	if len(tags) > 0 {
		if err := p.alle(ctx, "/api/documents/?tags__id__in="+ids(tags)+fields, add); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func ids(m map[int]string) string {
	var s []string
	for id := range m {
		s = append(s, strconv.Itoa(id))
	}
	return strings.Join(s, ",")
}

// Lesen is one document with its text.
func (p *Paperless) Lesen(ctx context.Context, id int) (Dokument, error) {
	var d Dokument
	err := p.do(ctx, "GET", fmt.Sprintf("/api/documents/%d/?fields=id,title,content,created_date,owner,document_type,tags,modified", id), nil, &d)
	return d, err
}

// notizAnfang starts every note this program writes: how it finds its own again.
const notizAnfang = "Gesundheit:"

// Notiz puts the program's note on the document: one note, replaced on
// every reading, never a second one beside it (#5). Paperless cannot edit a
// note, so the old one is deleted first — only one that starts with
// notizAnfang and is the program's own (ich; 0: any user).
func (p *Paperless) Notiz(ctx context.Context, id int, text string, ich int) error {
	var alte []struct {
		ID   int             `json:"id"`
		Note string          `json:"note"`
		User json.RawMessage `json:"user"`
	}
	if err := p.do(ctx, "GET", fmt.Sprintf("/api/documents/%d/notes/", id), nil, &alte); err != nil {
		return err
	}
	for _, n := range alte {
		if !strings.HasPrefix(strings.TrimSpace(n.Note), notizAnfang) || (ich > 0 && notizVon(n.User) != 0 && notizVon(n.User) != ich) {
			continue
		}
		if err := p.do(ctx, "DELETE", fmt.Sprintf("/api/documents/%d/notes/?id=%d", id, n.ID), nil, nil); err != nil {
			var f *Fehler
			if errors.As(err, &f) && f.Code == http.StatusForbidden {
				return ErrNotizRecht // never a second note beside the old one
			}
			return err
		}
	}
	return p.do(ctx, "POST", fmt.Sprintf("/api/documents/%d/notes/", id), map[string]string{"note": text}, nil)
}

// ErrNotizRecht is Paperless refusing to delete the program's old note: its
// user lacks delete_note (paperless' post-deploy grants it, laib#894).
var ErrNotizRecht = errors.New("Paperless lässt Gesundheit seine alte Notiz nicht ersetzen (Recht delete_note fehlt)")

// notizVon is a note's user: an id, or an object with one (newer API versions).
func notizVon(raw json.RawMessage) int {
	var id int
	if json.Unmarshal(raw, &id) == nil {
		return id
	}
	var u struct {
		ID int `json:"id"`
	}
	json.Unmarshal(raw, &u)
	return u.ID
}

// Original is the file as it came in (the scanner's PDF or photo), with its
// type: the page images the image preset reads (#5). At most 128 MB.
func (p *Paperless) Original(ctx context.Context, id int) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(p.URL, "/")+fmt.Sprintf("/api/documents/%d/download/?original=true", id), nil)
	if err != nil {
		return nil, "", &Fehler{Err: err}
	}
	req.Header.Set("Authorization", "Token "+p.Token)
	c := p.HTTP
	if c == nil {
		c = &http.Client{Timeout: 5 * time.Minute}
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, "", &Fehler{Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, "", &Fehler{Code: resp.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 128<<20))
	if err != nil {
		return nil, "", &Fehler{Err: err}
	}
	return data, resp.Header.Get("Content-Type"), nil
}

// Tag adds the tag named name to the document, making the tag when it is
// missing (the user may add tags, not change their definition); d's tags
// are updated, so a second Tag keeps the first.
func (p *Paperless) Tag(ctx context.Context, d *Dokument, name string) error {
	have, err := p.Namen(ctx, "/api/tags/", []string{name})
	if err != nil {
		return err
	}
	var id int
	for k := range have {
		id = k
	}
	if id == 0 {
		var b benannt
		if err := p.do(ctx, "POST", "/api/tags/", map[string]any{"name": name, "matching_algorithm": 0}, &b); err != nil {
			return err
		}
		id = b.ID
	}
	for _, t := range d.Tags {
		if t == id {
			return nil
		}
	}
	tags := append(append([]int{}, d.Tags...), id)
	if err := p.do(ctx, "PATCH", fmt.Sprintf("/api/documents/%d/", d.ID), map[string]any{"tags": tags}, nil); err != nil {
		return err
	}
	d.Tags = tags
	return nil
}

// Geben makes the person the document's owner, keeping the program's own
// view and change (as dokumente's geben does for a private stack). A
// document that has an owner already is left as it is.
func (p *Paperless) Geben(ctx context.Context, d Dokument, person, ich int) error {
	if d.Owner != nil || person == 0 {
		return nil
	}
	body := map[string]any{"owner": person}
	if ich > 0 {
		// set_permissions replaces: who could see and change it (Dokumente, by the
		// workflow) still can, and the program too
		var alt struct {
			Permissions map[string]struct {
				Users  []int `json:"users"`
				Groups []int `json:"groups"`
			} `json:"permissions"`
		}
		if err := p.do(ctx, "GET", fmt.Sprintf("/api/documents/%d/?full_perms=true&fields=id,permissions", d.ID), nil, &alt); err != nil {
			return err
		}
		rechte := map[string]any{}
		for _, k := range []string{"view", "change"} {
			users := []int{ich}
			for _, u := range alt.Permissions[k].Users {
				if u != ich && u != person {
					users = append(users, u)
				}
			}
			groups := alt.Permissions[k].Groups
			if groups == nil {
				groups = []int{}
			}
			rechte[k] = map[string]any{"users": users, "groups": groups}
		}
		body["set_permissions"] = rechte
	}
	return p.do(ctx, "PATCH", fmt.Sprintf("/api/documents/%d/", d.ID), body, nil)
}

// Users are Paperless' users (the household's people, and the service
// accounts); the program's own id is the one whose username is me.
func (p *Paperless) Users(ctx context.Context) ([]paperlessUser, error) {
	var out []paperlessUser
	err := p.alle(ctx, "/api/users/?page_size=100", func(r json.RawMessage) error {
		var u paperlessUser
		if json.Unmarshal(r, &u) == nil {
			out = append(out, u)
		}
		return nil
	})
	return out, err
}
