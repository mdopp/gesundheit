package main

import (
	"bytes"
	"context"
	"errors"
	"html"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math"
	"net/http"
	"regexp"
	"strings"
)

// The snippet of the page (#13, owner 2026-10-31: „Evtl. sogar mit
// Bildschnipsel daneben“): where a value stands on its document, cut from
// the page image of Paperless' archived PDF around the words of the text
// layer that carry it, with a margin; when no position is found, Paperless'
// thumbnail of the page, and the page shows the words it was read from with
// the number marked. Made on demand for the person whose value it is, sent
// with no-store, never written anywhere.

// ErrKeinePosition is a value whose words are not found on the text layer.
var ErrKeinePosition = errors.New("die Stelle steht nicht in der Textebene")

// zahlenWie are the numbers a value was read as: the number as the report
// wrote it, and the value as read (before a correction).
func zahlenWie(m Messwert) []float64 {
	var out []float64
	if v, ok := zahl(m.Roh); ok {
		out = append(out, v)
	}
	if m.Gelesen != nil {
		out = append(out, m.Gelesen.Wert)
	} else {
		out = append(out, m.Wert)
	}
	return out
}

// tokenZahl is a word as a number: „11,8“, „11,8*“, „(18)“; false when it is none.
func tokenZahl(t string) (float64, bool) {
	t = strings.TrimFunc(t, func(r rune) bool { return !(r >= '0' && r <= '9') && r != ',' && r != '.' && r != '-' })
	t = strings.Trim(t, ",.")
	if t == "" || strings.Trim(t, "-") == "" {
		return 0, false
	}
	return zahl(t)
}

// fundstelle is the page and the box (PDF points) of a value's number and
// its name on the same line. Its words (Beleg), when the reader kept them,
// make a line count more; without its name on the line nothing is found:
// a lab sheet carries the same number more than once.
func fundstelle(seiten []pdfSeite, m Messwert) (int, rechteck, bool) {
	if strings.TrimSpace(m.Name) == "" && strings.TrimSpace(m.Original) == "" {
		return 0, rechteck{}, false
	}
	zahlen := zahlenWie(m)
	namen := []string{}
	for _, n := range []string{m.Original, m.Name} {
		if f := falten(n); f != "" {
			namen = append(namen, f)
		}
	}
	an := finde(m.Name)
	beleg := dicht(m.Beleg)
	bestNr, bestBox, bestWert := 0, rechteck{}, 0
	for nr, s := range seiten {
		for i, w := range s.Woerter {
			v, ok := tokenZahl(w.Text)
			if !ok {
				continue
			}
			passt := false
			for _, z := range zahlen {
				if math.Abs(v-z) < 1e-9 {
					passt = true
				}
			}
			if !passt {
				continue
			}
			zeile := aufZeile(s.Woerter, i)
			box, ok := nameAufZeile(s.Woerter, zeile, i, namen, an)
			if !ok {
				continue
			}
			wert := 2
			if beleg != "" {
				var t strings.Builder
				for _, k := range zeile {
					t.WriteString(s.Woerter[k].Text)
				}
				lt := dicht(t.String())
				if strings.Contains(lt, beleg) || strings.Contains(beleg, lt) {
					wert++
				}
			}
			if wert > bestWert {
				bestNr, bestBox, bestWert = nr, box.mit(w.Box), wert
			}
		}
	}
	return bestNr, bestBox, bestWert > 0
}

// aufZeile are the words on the line of word i (its baseline band), left to right.
func aufZeile(ws []pdfWort, i int) []int {
	b := ws[i].Box
	h := b.Y1 - b.Y0
	mitte := (b.Y0 + b.Y1) / 2
	var out []int
	for k, w := range ws {
		hk := w.Box.Y1 - w.Box.Y0
		if math.Abs((w.Box.Y0+w.Box.Y1)/2-mitte) < 0.5*math.Max(h, hk) {
			out = append(out, k)
		}
	}
	// left to right: a line's words in the order they stand
	for a := 1; a < len(out); a++ {
		for c := a; c > 0 && ws[out[c]].Box.X0 < ws[out[c-1]].Box.X0; c-- {
			out[c], out[c-1] = out[c-1], out[c]
		}
	}
	return out
}

// nameAufZeile is the box of the value's name among the line's words: up to
// four words in a row that are its name, its report's spelling, or an alias
// of its analyte. The number's own word is no part of it.
func nameAufZeile(ws []pdfWort, zeile []int, zahlWort int, namen []string, an *Analyt) (rechteck, bool) {
	for a := range zeile {
		var teile []string
		box := rechteck{}
		for e := a; e < len(zeile) && e < a+4; e++ {
			k := zeile[e]
			if k == zahlWort {
				break
			}
			teile = append(teile, ws[k].Text)
			box = box.mit(ws[k].Box)
			s := falten(strings.Join(teile, " "))
			if s == "" {
				continue
			}
			for _, n := range namen {
				if s == n {
					return box, true
				}
			}
			if an != nil && aliasIndex[s] == an {
				return box, true
			}
		}
	}
	return rechteck{}, false
}

// pixel is a box of PDF points on the page's picture, in its pixels.
func (s pdfSeite) pixel(box rechteck) (image.Rectangle, bool) {
	if s.Bild == nil {
		return image.Rectangle{}, false
	}
	inv, ok := s.Bild.CTM.invers()
	if !ok {
		return image.Rectangle{}, false
	}
	w, h := s.Bild.datei.zahl(s.Bild.Strom.Dict["Width"]), s.Bild.datei.zahl(s.Bild.Strom.Dict["Height"])
	if w <= 0 || h <= 0 {
		return image.Rectangle{}, false
	}
	u := inv.box(box.X0, box.Y0, box.X1, box.Y1) // the image's unit square
	r := image.Rect(int(u.X0*float64(w)), int((1-u.Y1)*float64(h)), int(math.Ceil(u.X1*float64(w))), int(math.Ceil((1-u.Y0)*float64(h))))
	r = r.Intersect(image.Rect(0, 0, w, h))
	return r, !r.Empty()
}

// bildDekodiert is the page's picture: a JPEG, or an 8- or 1-bit Flate image.
func (b *pdfBild) dekodiert() (image.Image, error) {
	f, d := b.datei, b.Strom.Dict
	var filter []string
	switch x := f.wert(d["Filter"]).(type) {
	case pdfName:
		filter = []string{string(x)}
	case []any:
		for _, e := range x {
			filter = append(filter, f.name(e))
		}
	}
	w, h := f.zahl(d["Width"]), f.zahl(d["Height"])
	switch {
	case len(filter) == 1 && (filter[0] == "DCTDecode" || filter[0] == "DCT"):
		return jpeg.Decode(bytes.NewReader(b.Strom.Raw))
	case len(filter) == 1 && (filter[0] == "FlateDecode" || filter[0] == "Fl"):
		parms := f.dict(d["DecodeParms"])
		umgekehrt := false
		if dec := f.liste(d["Decode"]); len(dec) == 2 && f.zahlF(dec[0], 0) == 1 && f.zahlF(dec[1], 1) == 0 {
			umgekehrt = true
		}
		bits := f.zahl(d["BitsPerComponent"])
		if maske, _ := f.wert(d["ImageMask"]).(bool); maske {
			bits = 1
		}
		return flateBild(b.Strom.Raw, w, h, bits, parms != nil && f.zahl(parms["Predictor"]) >= 10, umgekehrt)
	}
	return nil, ErrKeinBild // JBIG2, CCITT: not read here
}

// schnipselBreite is the snippet's width at most: a line of a sheet, legible on a phone turned sideways.
const schnipselBreite = 1200

// schnipselBild cuts the band of the page's picture around the box: the
// page's full width (name, value, unit and range of a row stand side by
// side), the line's height and two and a half lines above and below; the
// words framed.
func schnipselBild(s pdfSeite, box rechteck) ([]byte, error) {
	px, ok := s.pixel(box)
	if !ok {
		return nil, ErrKeinBild
	}
	img, err := s.Bild.dekodiert()
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	rand := max(int(2.5*float64(px.Dy())), b.Dy()/60, 12)
	aus := image.Rect(b.Min.X, b.Min.Y+px.Min.Y-rand, b.Max.X, b.Min.Y+px.Max.Y+rand).Intersect(b)
	if aus.Empty() {
		return nil, ErrKeinePosition
	}
	out := image.NewRGBA(image.Rect(0, 0, aus.Dx(), aus.Dy()))
	draw.Draw(out, out.Bounds(), img, aus.Min, draw.Src)
	// the frame around the words
	pad := max(3, px.Dy()/4)
	r := image.Rect(px.Min.X-pad, px.Min.Y-pad, px.Max.X+pad, px.Max.Y+pad).Add(b.Min).Sub(aus.Min).Intersect(out.Bounds())
	staerke := max(2, aus.Dx()/400)
	rot := color.RGBA{180, 67, 47, 255}
	for k := 0; k < staerke; k++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			out.SetRGBA(x, r.Min.Y+k, rot)
			out.SetRGBA(x, r.Max.Y-1-k, rot)
		}
		for y := r.Min.Y; y < r.Max.Y; y++ {
			out.SetRGBA(r.Min.X+k, y, rot)
			out.SetRGBA(r.Max.X-1-k, y, rot)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, kleiner(out, schnipselBreite), &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Schnipsel is the picture next to a value's form: the cut from its page
// ("seite"), else Paperless' thumbnail ("vorschau").
func (d *Dienst) Schnipsel(ctx context.Context, m Messwert) ([]byte, string, string, error) {
	if d.Paperless == nil || d.Paperless.Token == "" || m.Dokument == 0 {
		return nil, "", "", ErrKeinBild
	}
	if pdf, _, err := d.Paperless.Archiv(ctx, m.Dokument); err == nil {
		if seiten, err := pdfSeitenLesen(pdf); err == nil {
			if nr, box, ok := fundstelle(seiten, m); ok {
				if jpg, err := schnipselBild(seiten[nr], box); err == nil {
					return jpg, "image/jpeg", "seite", nil
				}
			}
		}
	}
	data, mime, err := d.Paperless.Vorschau(ctx, m.Dokument)
	if err != nil {
		return nil, "", "", err
	}
	switch strings.TrimSpace(strings.Split(mime, ";")[0]) {
	case "image/webp", "image/png", "image/jpeg":
		return data, strings.TrimSpace(strings.Split(mime, ";")[0]), "vorschau", nil
	}
	return nil, "", "", ErrKeinBild // never serve Paperless' anything else as ours
}

func (w *Web) schnipsel(rw http.ResponseWriter, r *http.Request) {
	p, _, _ := w.wer(rw, r)
	if p == nil {
		return
	}
	ws, err := w.Dienst.Speicher.AlleWerte(p.Key)
	var m *WertStand
	for i := range ws {
		if err == nil && ws[i].ID == r.FormValue("id") && ws[i].ID != "" {
			m = &ws[i]
		}
	}
	rw.Header().Set("Cache-Control", "private, no-store")
	if m == nil {
		http.Error(rw, "Diesen Wert gibt es in deinem Verlauf nicht.", http.StatusNotFound)
		return
	}
	data, mime, art, err := w.Dienst.Schnipsel(r.Context(), m.Messwert)
	if err != nil {
		http.Error(rw, "Kein Bild der Seite.", http.StatusNotFound)
		return
	}
	rw.Header().Set("Content-Type", mime)
	rw.Header().Set("X-Schnipsel", art)
	rw.Header().Set("X-Content-Type-Options", "nosniff")
	rw.Write(data)
}

// belegHTML are the words a value was read from, its number marked; without
// the words (a record of the first reader) its name and number as read.
func belegHTML(m Messwert) string {
	roh := m.Roh
	wert := m.Wert
	einheit := m.Einheit
	if m.Gelesen != nil {
		wert, einheit = m.Gelesen.Wert, m.Gelesen.Einheit
	}
	if roh == "" {
		roh = zahlText(wert)
	}
	text := m.Beleg
	if strings.TrimSpace(text) == "" {
		name := m.Original
		if name == "" {
			name = m.Name
		}
		text = strings.TrimSpace(name + " " + roh + " " + einheit)
	}
	text = strings.Join(strings.Fields(text), " ")
	// the number as a whole: „5“ is not marked inside „15“
	loc := regexp.MustCompile(`(?:^|[^\d.,])(` + regexp.QuoteMeta(roh) + `)(?:$|[^\d.,]|[.,]\D|[.,]$)`).FindStringSubmatchIndex(text)
	if roh == "" || loc == nil {
		return html.EscapeString(text)
	}
	i, j := loc[2], loc[3]
	return html.EscapeString(text[:i]) + "<mark>" + html.EscapeString(text[i:j]) + "</mark>" + html.EscapeString(text[j:])
}
