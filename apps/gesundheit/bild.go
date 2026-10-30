package main

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png" // a PNG original decodes too
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// --- when a page's text is too weak to read values from (gesundheit#5)

// Guete is how much of a page's OCR text is readable: the first real run
// read „ulm!“ = 0,3 from letter salad, so a page whose text is weak is read
// from its image instead (owner, 2026-10-30).
type Guete struct {
	Woerter     int // tokens that are words: letters only, at least two, a vowel or a short abbreviation (TSH, CRP)
	Muell       int // tokens with letters that are no word and no unit: „l1!“, „rn|“, „,i;l“
	Zeilen      int // lines that carry a number
	OhneName    int // of those, lines without one word: numbers without a label
	Bestandteil int // all tokens
}

// Anteil is the share of words among the tokens that carry letters.
func (g Guete) Anteil() float64 {
	if g.Woerter+g.Muell == 0 {
		return 0
	}
	return float64(g.Woerter) / float64(g.Woerter+g.Muell)
}

// Schwach is a page whose text is no ground for a value: too little of it
// (no text layer, a photo), less than 70 % of its letter tokens words, or
// most lines with a number carrying no word to name it.
func (g Guete) Schwach() bool {
	if g.Bestandteil < 12 || g.Woerter < 6 {
		return true
	}
	if g.Anteil() < 0.7 {
		return true
	}
	return g.Zeilen >= 3 && float64(g.OhneName)/float64(g.Zeilen) > 0.5
}

func (g Guete) String() string {
	return fmt.Sprintf("%d Wörter, %d unlesbare Zeichenfolgen (%.0f %% Wörter), %d von %d Zeilen mit Zahl ohne Namen",
		g.Woerter, g.Muell, 100*g.Anteil(), g.OhneName, g.Zeilen)
}

var zahlImText = regexp.MustCompile(`\d`)

// guete measures a text.
func guete(text string) Guete {
	var g Guete
	for _, zeile := range strings.Split(text, "\n") {
		hatZahl, hatWort := false, false
		for _, tok := range strings.Fields(zeile) {
			g.Bestandteil++
			if zahlImText.MatchString(tok) && !strings.ContainsFunc(tok, unicode.IsLetter) {
				hatZahl = true
				continue
			}
			switch art := tokenArt(tok); art {
			case "wort":
				g.Woerter++
				hatWort = true
			case "muell":
				g.Muell++
			}
		}
		if hatZahl {
			g.Zeilen++
			if !hatWort {
				g.OhneName++
			}
		}
	}
	return g
}

var einheitRe = regexp.MustCompile(`^(?i)[a-zµμ%°]{0,4}/[a-zµμ0-9²³]{1,5}$|^(?i)(mmhg|mg|g|l|ml|kg|cm|mm|mol|mmol|µmol|nmol|pmol|ng|pg|µg|iu|u|ie|w|bpm|coi|fl|pg|%)$`)

// tokenArt is "wort", "einheit", "muell" or "" (punctuation alone).
func tokenArt(tok string) string {
	// sentence punctuation goes; „!“ and „|“ stay: inside a token they are OCR's, not the page's
	t := strings.TrimFunc(tok, func(r rune) bool { return strings.ContainsRune(".,:;()[]\"'„“»«-–*", r) })
	if !strings.ContainsFunc(t, unicode.IsLetter) {
		return "" // a table's „|“, a heading's „#“: layout, not text
	}
	if einheitRe.MatchString(t) {
		return "einheit"
	}
	// a word, maybe hyphenated or with a slash: „Anti-HBs“, „HbA1c“, „Leukozyten“
	buchstaben, ziffern, vokal := 0, 0, false
	for _, r := range t {
		switch {
		case unicode.IsLetter(r):
			buchstaben++
			if strings.ContainsRune("aeiouäöüyéAEIOUÄÖÜY", r) {
				vokal = true
			}
		case unicode.IsDigit(r):
			ziffern++
		case r == '-' || r == '/' || r == '.':
		default:
			return "muell"
		}
	}
	first := []rune(t)[0]
	switch {
	case buchstaben >= 2 && ziffern == 0 && (vokal || (buchstaben <= 6 && strings.ToUpper(t) == t)):
		return "wort"
	case buchstaben >= 2 && ziffern > 0 && vokal && unicode.IsLetter(first) && ziffern <= buchstaben:
		return "wort" // HbA1c, FEV1, VO2max
	}
	return "muell"
}

// --- the page's image, from the document Paperless keeps

// maxSeite is the long side an image is sent at: a 600-dpi A4 scan is
// 7000 px, which the vision projector shrinks anyway; 2048 px keep a lab
// sheet's small print and a request under a few MB.
const maxSeite = 2048

// maxSeiten caps the pages one document sends: a lab sheet's values are on its first pages.
const maxSeiten = 8

// ErrKeinBild is a document whose file holds no image this program can read
// (a JBIG2 or CCITT scan, a TIFF, a born-digital PDF without pictures).
var ErrKeinBild = errors.New("keine lesbare Seite als Bild in der Datei")

// seitenBilder are the page images of a file, as JPEGs at most maxSeite long.
func seitenBilder(data []byte, mime string) ([][]byte, error) {
	switch {
	case strings.HasPrefix(mime, "image/jpeg"), strings.HasPrefix(mime, "image/png"):
		b, err := verkleinert(data)
		if err != nil {
			return nil, ErrKeinBild
		}
		return [][]byte{b}, nil
	case strings.HasPrefix(mime, "application/pdf"), bytes.HasPrefix(data, []byte("%PDF")):
		var out [][]byte
		for _, img := range pdfBilder(data) {
			if b, err := verkleinert(img); err == nil {
				out = append(out, b)
			} else {
				log.Printf("bild: eine Seite nicht lesbar: %v", err)
			}
			if len(out) >= maxSeiten {
				break
			}
		}
		if len(out) == 0 {
			return nil, ErrKeinBild
		}
		return out, nil
	}
	return nil, ErrKeinBild
}

var (
	objRe       = regexp.MustCompile(`\d+\s+\d+\s+obj\b`)
	streamRe    = regexp.MustCompile(`>>\s*stream\r?\n`)
	bildTypRe   = regexp.MustCompile(`/Subtype\s*/Image\b`)
	maskeRe     = regexp.MustCompile(`/ImageMask\s+true`)
	laengeRe    = regexp.MustCompile(`/Length\s+(\d+)(\s+\d+\s+R)?`)
	breiteRe    = regexp.MustCompile(`/Width\s+(\d+)`)
	hoeheRe     = regexp.MustCompile(`/Height\s+(\d+)`)
	bitsRe      = regexp.MustCompile(`/BitsPerComponent\s+(\d+)`)
	praediktRe  = regexp.MustCompile(`/Predictor\s+(\d+)`)
	umgekehrtRe = regexp.MustCompile(`/Decode\s*\[\s*1(?:\.0)?\s+0(?:\.0)?\s*\]`)
)

// pdfBilder are the page-sized pictures a scanner's PDF carries, in file
// order, as JPEG or PNG bytes: a JPEG stream (DCTDecode) as it is, an 8- or
// 1-bit Flate stream of gray or colour drawn anew. Logos and stamps (under
// 600 px on a side) are left out. Paperless keeps the scanner's file as the
// original: one picture per page, no rendering needed.
func pdfBilder(pdf []byte) [][]byte {
	var out [][]byte
	starts := objRe.FindAllIndex(pdf, -1)
	for i, s := range starts {
		end := len(pdf)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		obj := pdf[s[1]:end]
		m := streamRe.FindIndex(obj)
		if m == nil {
			continue
		}
		dict := string(obj[:m[0]])
		if !bildTypRe.MatchString(dict) || maskeRe.MatchString(dict) {
			continue
		}
		w, h := zahlIn(breiteRe, dict), zahlIn(hoeheRe, dict)
		if w < 600 && h < 600 {
			continue
		}
		data := obj[m[1]:]
		if l := laengeRe.FindStringSubmatch(dict); l != nil && l[2] == "" {
			if n := zahlIn(laengeRe, dict); n > 0 && n <= len(data) {
				data = data[:n]
			}
		} else if e := bytes.Index(data, []byte("endstream")); e >= 0 {
			data = bytes.TrimRight(data[:e], "\r\n")
		}
		switch {
		case strings.Contains(dict, "/DCTDecode"):
			out = append(out, data)
		case strings.Contains(dict, "/FlateDecode") && !strings.Contains(dict, "/DCTDecode"):
			img, err := flateBild(data, w, h, zahlIn(bitsRe, dict), zahlIn(praediktRe, dict) >= 10, umgekehrtRe.MatchString(dict))
			if err != nil {
				log.Printf("bild: %v", err)
				continue
			}
			var b bytes.Buffer
			if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 90}); err == nil {
				out = append(out, b.Bytes())
			}
		}
	}
	return out
}

func zahlIn(re *regexp.Regexp, s string) int {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	n := 0
	fmt.Sscan(m[1], &n)
	return n
}

// flateBild draws a Flate image: the number of colours is what the size of
// the data says (an ICCBased colour space names it only by reference).
func flateBild(data []byte, w, h, bits int, praediktor, umgekehrt bool) (image.Image, error) {
	if w <= 0 || h <= 0 || (bits != 8 && bits != 1) {
		return nil, fmt.Errorf("Flate-Bild %dx%d mit %d Bit: nicht lesbar", w, h, bits)
	}
	roh, err := inflate(data, 64<<20)
	if err != nil {
		return nil, err
	}
	for _, n := range []int{1, 3, 4} {
		zeile := (w*n*bits + 7) / 8
		pz := zeile
		if praediktor {
			pz++
		}
		if len(roh) < pz*h {
			continue
		}
		if len(roh) > pz*h+pz { // more than a row's slack: another colour count
			continue
		}
		px := roh
		if praediktor {
			px, err = pngUnfilter(roh, zeile, h, (n*bits+7)/8)
			if err != nil {
				return nil, err
			}
		}
		return zeichnen(px, w, h, n, bits, zeile, umgekehrt), nil
	}
	return nil, fmt.Errorf("Flate-Bild %dx%d: %d Bytes passen zu keiner Farbzahl", w, h, len(roh))
}

// zeichnen makes an image of rows of pixels: gray, RGB or CMYK; 1 bit is black and white.
func zeichnen(px []byte, w, h, n, bits, zeile int, umgekehrt bool) image.Image {
	if n == 1 {
		g := image.NewGray(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			row := px[y*zeile:]
			for x := 0; x < w; x++ {
				var v byte
				if bits == 1 {
					if row[x/8]&(0x80>>(x%8)) != 0 {
						v = 255
					}
				} else {
					v = row[x]
				}
				if umgekehrt {
					v = 255 - v
				}
				g.Pix[y*g.Stride+x] = v
			}
		}
		return g
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		row := px[y*zeile:]
		for x := 0; x < w; x++ {
			var c color.RGBA
			if n == 3 {
				c = color.RGBA{row[3*x], row[3*x+1], row[3*x+2], 255}
			} else {
				r, g, b := color.CMYKToRGB(row[4*x], row[4*x+1], row[4*x+2], row[4*x+3])
				c = color.RGBA{r, g, b, 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// pngUnfilter undoes the PNG predictors a PDF's Flate image may carry (Predictor ≥ 10).
func pngUnfilter(roh []byte, zeile, h, bpp int) ([]byte, error) {
	out := make([]byte, zeile*h)
	prev := make([]byte, zeile)
	for y := 0; y < h; y++ {
		in := roh[y*(zeile+1):]
		ft, cur := in[0], out[y*zeile:(y+1)*zeile]
		copy(cur, in[1:zeile+1])
		for i := range cur {
			var a, b, c byte
			if i >= bpp {
				a, c = cur[i-bpp], prev[i-bpp]
			}
			b = prev[i]
			switch ft {
			case 0:
			case 1:
				cur[i] += a
			case 2:
				cur[i] += b
			case 3:
				cur[i] += byte((int(a) + int(b)) / 2)
			case 4:
				cur[i] += paeth(a, b, c)
			default:
				return nil, fmt.Errorf("PNG-Filter %d unbekannt", ft)
			}
		}
		prev = cur
	}
	return out, nil
}

func paeth(a, b, c byte) byte {
	p := int(a) + int(b) - int(c)
	pa, pb, pc := abs(p-int(a)), abs(p-int(b)), abs(p-int(c))
	switch {
	case pa <= pb && pa <= pc:
		return a
	case pb <= pc:
		return b
	}
	return c
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// verkleinert is an image as a JPEG at most maxSeite on its long side; a
// JPEG small enough, in gray or colour, goes as it is.
func verkleinert(data []byte) ([]byte, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if format == "jpeg" && max(cfg.Width, cfg.Height) <= maxSeite && cfg.ColorModel != color.CMYKModel {
		return data, nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, kleiner(img, maxSeite), &jpeg.Options{Quality: 90}); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// kleiner shrinks an image to at most n on its long side, each new pixel
// the mean of the ones it covers (a scan's thin strokes stay gray, not gone).
func kleiner(img image.Image, n int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if max(w, h) <= n {
		return img
	}
	f := float64(max(w, h)) / float64(n)
	nw, nh := max(1, int(float64(w)/f)), max(1, int(float64(h)/f))
	out := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		y0, y1 := b.Min.Y+y*h/nh, b.Min.Y+(y+1)*h/nh
		for x := 0; x < nw; x++ {
			x0, x1 := b.Min.X+x*w/nw, b.Min.X+(x+1)*w/nw
			var r, g, bl, cnt uint64
			for sy := y0; sy < max(y1, y0+1); sy++ {
				for sx := x0; sx < max(x1, x0+1); sx++ {
					cr, cg, cb, _ := img.At(sx, sy).RGBA()
					r, g, bl, cnt = r+uint64(cr), g+uint64(cg), bl+uint64(cb), cnt+1
				}
			}
			out.SetRGBA(x, y, color.RGBA{uint8(r / cnt >> 8), uint8(g / cnt >> 8), uint8(bl / cnt >> 8), 255})
		}
	}
	return out
}

// --- reading the image: the lease, then the image preset

// abschriftSystem is the image preset's task: transcribe, never interpret (owner, 2026-10-30).
const abschriftSystem = `Du schreibst ab, was auf dieser Seite eines deutschen medizinischen Dokuments gedruckt oder geschrieben steht, als Markdown. Überschriften als #-Zeilen, Tabellen (etwa Laborwerte: Parameter | Ergebnis | Einheit | Referenz) als Markdown-Tabellen, jedes Datum und jede Zahl genau so, wie sie dasteht. Hat die Seite mehrere Untersuchungen mit eigenem Datum, bekommt jede ihren Abschnitt mit dem Datum in der Überschrift. Schreib nur ab: keine Deutung, keine Diagnose, keine Zusammenfassung, nichts ergänzen, nichts ausrechnen, keine Bilder beschreiben. Unleserliches schreibst du als [unleserlich]. Kannst du die Seite gar nicht lesen, antworte nur mit dem Wort UNLESERLICH.`

// unleserlich is the image preset's word for a page it cannot read.
const unleserlich = "UNLESERLICH"

// Bildleser reads page images with an image preset (IMAGE_MODEL, by the
// owner's decision `gemma-4-12b`), which is not the household model: it
// takes the lease first, renews it while it reads, and gives it back.
type Bildleser struct {
	Modell *Modell // the proxy and its key; its Name is not used
	Name   string  // the image preset
	Lease  *Lease
}

// Lease is the GPU's lease (laib ADR 0011, llama README §Images): a holder
// asks for a preset, renews within renew_after, and gives the card back.
type Lease struct {
	URL, Key, Holder string
	HTTP             *http.Client
	Warten           time.Duration // how long a preparing card is waited for (default 10 min)
	Pause            time.Duration // between two looks while it prepares (default 5 s)

	model      string
	renewAfter time.Duration
	zuletzt    time.Time
}

// ErrKarteVergeben is the card held by another holder: the weak documents
// wait for the next run (the hourly run may wait for the GPU).
var ErrKarteVergeben = errors.New("die Grafikkarte ist vergeben")

const leaseTTL = 15 * time.Minute

func (l *Lease) client() *http.Client {
	if l.HTTP != nil {
		return l.HTTP
	}
	return &http.Client{Timeout: time.Minute}
}

func (l *Lease) senden(ctx context.Context, method string, body any) (int, map[string]any, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, l.URL, rd)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+l.Key)
	resp, err := l.client().Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: die Lease: %v", ErrModellWeg, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&out)
	return resp.StatusCode, out, nil
}

// Nehmen takes the card for model; a card being prepared is waited for.
func (l *Lease) Nehmen(ctx context.Context, model string) error {
	if l.URL == "" {
		return fmt.Errorf("%w: keine Lease (MODEL_LEASE)", ErrModellWeg)
	}
	l.model = model
	code, out, err := l.senden(ctx, "POST", map[string]any{"model": model, "holder": l.Holder, "ttl_s": int(leaseTTL / time.Second)})
	if err != nil {
		return err
	}
	switch code {
	case http.StatusOK:
		l.gemerkt(out)
		return nil
	case http.StatusAccepted:
		return l.bereit(ctx)
	case http.StatusConflict:
		return fmt.Errorf("%w: %v hält sie", ErrKarteVergeben, out["holder"])
	}
	return fmt.Errorf("%w: die Lease antwortet %d (%v)", ErrModellWeg, code, out["reason"])
}

func (l *Lease) gemerkt(out map[string]any) {
	l.zuletzt = time.Now()
	l.renewAfter = 5 * time.Minute
	if f, ok := out["renew_after"].(float64); ok && f > 0 {
		l.renewAfter = time.Duration(f) * time.Second
	}
}

// bereit waits while the weights load (202 preparing): GET, never a second POST.
func (l *Lease) bereit(ctx context.Context) error {
	warten, pause := l.Warten, l.Pause
	if warten == 0 {
		warten = 10 * time.Minute
	}
	if pause == 0 {
		pause = 5 * time.Second
	}
	bis := time.Now().Add(warten)
	for time.Now().Before(bis) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pause):
		}
		_, out, err := l.senden(ctx, "GET", nil)
		if err != nil {
			return err
		}
		if out["holder"] != l.Holder {
			return fmt.Errorf("%w: %v hält sie", ErrKarteVergeben, out["holder"])
		}
		if out["state"] == "ready" {
			l.gemerkt(out)
			return nil
		}
	}
	l.Zurueck(context.WithoutCancel(ctx))
	return fmt.Errorf("%w: %s wurde in %s nicht bereit", ErrModellWeg, l.model, warten)
}

// Erneuern posts again once renew_after has passed: a holder that misses
// two renewals loses the card.
func (l *Lease) Erneuern(ctx context.Context) error {
	if time.Since(l.zuletzt) < l.renewAfter {
		return nil
	}
	code, out, err := l.senden(ctx, "POST", map[string]any{"model": l.model, "holder": l.Holder, "ttl_s": int(leaseTTL / time.Second)})
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("%w: die Lease erneuert nicht (%d)", ErrModellWeg, code)
	}
	l.gemerkt(out)
	return nil
}

// Zurueck gives the card back; the household model loads again.
func (l *Lease) Zurueck(ctx context.Context) {
	if code, _, err := l.senden(ctx, "DELETE", map[string]string{"holder": l.Holder}); err != nil || code >= 300 {
		log.Printf("lease: nicht zurückgegeben (%d): %v", code, err)
	}
}

// Seite transcribes one page image; "" is a page the model cannot read.
func (b *Bildleser) Seite(ctx context.Context, jpg []byte, nr, von int) (string, error) {
	if err := b.Lease.Erneuern(ctx); err != nil {
		return "", err
	}
	body := map[string]any{
		"model": b.Name,
		"messages": []any{
			map[string]string{"role": "system", "content": abschriftSystem},
			map[string]any{"role": "user", "content": []any{
				map[string]string{"type": "text", "text": fmt.Sprintf("Seite %d von %d. Schreib ab, was dasteht.", nr, von)},
				map[string]any{"type": "image_url", "image_url": map[string]string{"url": "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpg)}},
			}},
		},
		"temperature":          0,
		"max_tokens":           4096,
		"chat_template_kwargs": map[string]bool{"enable_thinking": false},
	}
	text, err := b.Modell.senden(ctx, body)
	if err != nil {
		return "", err
	}
	text = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(text), "```markdown"), "```"))
	if text == "" || strings.EqualFold(strings.Trim(text, ".* "), unleserlich) {
		return "", nil
	}
	return text, nil
}

// Abschrift is what the image of one document said.
type Abschrift struct {
	Text  string // the Markdown transcript, pages under „## Seite n“; "" when nothing was legible
	Grund string // why there is no transcript, for the note
}

// lesbar is whether the transcript carries anything to read values from.
func (a *Abschrift) lesbar() bool { return a != nil && a.Text != "" }

// abschreiben transcribes the pages of one document while the lease is held.
func (b *Bildleser) abschreiben(ctx context.Context, seiten [][]byte) (Abschrift, error) {
	var teile []string
	for i, s := range seiten {
		t, err := b.Seite(ctx, s, i+1, len(seiten))
		if err != nil {
			return Abschrift{}, err
		}
		if t == "" {
			continue
		}
		if len(seiten) > 1 {
			t = fmt.Sprintf("## Seite %d\n\n%s", i+1, t)
		}
		teile = append(teile, t)
	}
	if len(teile) == 0 {
		return Abschrift{Grund: "das Bildmodell konnte auch das Bild nicht lesen"}, nil
	}
	return Abschrift{Text: strings.Join(teile, "\n\n")}, nil
}

// --- OCR against the image (gesundheit#5: Hämoglobin 13,6 where the paper says 14,6)

var zahlRoh = regexp.MustCompile(`-?\d+(?:[.,]\d+)?`)

// abweichung is the number the OCR text carries next to a value's name when
// it differs from the image's, "" when the text agrees or has no number for it.
func abweichung(namen []string, roh string, ocr string) string {
	bild, ok := zahl(roh)
	if !ok {
		return ""
	}
	text := eng(ocr)
	anders := ""
	for _, n := range namen {
		n = eng(n)
		if n == "" {
			continue
		}
		for i := 0; ; {
			j := strings.Index(text[i:], n)
			if j < 0 {
				break
			}
			rest := []rune(text[i+j+len(n):])
			if len(rest) > 40 {
				rest = rest[:40]
			}
			if m := zahlRoh.FindString(string(rest)); m != "" {
				if v, ok := zahl(m); ok && v == bild {
					return ""
				} else if anders == "" {
					anders = m
				}
			}
			i += j + len(n)
		}
	}
	return anders
}

// inflate is a Flate stream, at most limit bytes.
func inflate(data []byte, limit int64) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("Flate-Bild: %v", err)
	}
	defer r.Close()
	out, err := io.ReadAll(io.LimitReader(r, limit))
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("Flate-Bild: %v", err)
	}
	return out, nil
}
