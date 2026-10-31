package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

// --- a PDF as Paperless' archive holds one (#13)

// wortAuf is one word of a test page's text layer: its text, the start of
// its baseline in PDF points, its font size.
type wortAuf struct {
	text       string
	x, y, size float64
}

// zeile lays out a line's words from x on, as an OCR text layer has them.
func zeile(y float64, x float64, woerter ...string) []wortAuf {
	var out []wortAuf
	for _, w := range woerter {
		out = append(out, wortAuf{w, x, y, 10})
		x += float64(len([]rune(w)))*5 + 12
	}
	return out
}

// laborSeite is laborAnna's sheet as words on an A4 page: two values
// carry the same number, so only the name tells them apart.
func laborSeite() []wortAuf {
	var w []wortAuf
	w = append(w, zeile(800, 60, "Praxislabor", "Nord")...)
	w = append(w, zeile(770, 60, "Patient:", "Muster,", "Anna", "geb.", "03.04.1981")...)
	w = append(w, zeile(640, 60, "Parameter", "Ergebnis", "Einheit", "Referenz")...)
	w = append(w, zeile(600, 60, "Hämoglobin", "11,8", "g/dl", "12,0", "-", "16,0")...)
	w = append(w, zeile(580, 60, "Ferritin", "18", "ng/ml", "15", "-", "150")...)
	w = append(w, zeile(560, 60, "MCH", "11,8", "pg", "26", "-", "35")...)
	return w
}

func utf16Hex(s string) string {
	var b strings.Builder
	for _, u := range utf16.Encode([]rune(s)) {
		fmt.Fprintf(&b, "%04X", u)
	}
	return b.String()
}

// ocrPDF builds a PDF as OCRmyPDF writes Paperless' archive: per page the
// scan as an image XObject drawn over the whole page, the invisible text
// layer (3 Tr) in a form XObject with the GlyphLessFont, whose CIDs are
// UTF-16 code units; catalog, pages, page and font dictionaries packed in
// an object stream; the root named by an xref stream.
func ocrPDF(t *testing.T, bild []byte, seiten ...[]wortAuf) []byte {
	t.Helper()
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(bild))
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
	stream := func(num int, dict string, data []byte) {
		fmt.Fprintf(&b, "%d 0 obj\n<< %s /Length %d >>\nstream\n", num, dict, len(data))
		b.Write(data)
		b.WriteString("\nendstream\nendobj\n")
	}
	// in the object stream: 1 catalog, 2 pages, 3 the Type0 font, 4 its CID font, the pages
	packed := map[int]string{
		1: "<< /Type /Catalog /Pages 2 0 R >>",
		3: "<< /Type /Font /Subtype /Type0 /BaseFont /GlyphLessFont /Encoding /Identity-H /DescendantFonts [4 0 R] /ToUnicode 5 0 R >>",
		4: "<< /Type /Font /Subtype /CIDFontType2 /BaseFont /GlyphLessFont /CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >> /FontDescriptor 6 0 R /DW 500 /CIDToGIDMap /Identity >>",
	}
	order := []int{1, 2, 3, 4}
	var kids []string
	for i, woerter := range seiten {
		page, img, content, form := 10+4*i, 11+4*i, 12+4*i, 13+4*i
		kids = append(kids, fmt.Sprintf("%d 0 R", page))
		packed[page] = fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /XObject << /Im0 %d 0 R /OCR-1 %d 0 R >> >> /Contents %d 0 R >>", img, form, content)
		order = append(order, page)
		stream(img, fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /DCTDecode", cfg.Width, cfg.Height), bild)
		stream(content, "/Filter /FlateDecode", flate(t, []byte("q 595.0 0 0 842.0 0 0 cm /Im0 Do Q\nq 1 0 0 1 0 0 cm /OCR-1 Do Q\n")))
		var tl strings.Builder
		tl.WriteString("BT\n3 Tr\n")
		for _, w := range woerter {
			fmt.Fprintf(&tl, "/f-0-0 %g Tf\n1 0 0 1 %g %g Tm\n100 Tz\n[ <%s> ] TJ\n", w.size, w.x, w.y, utf16Hex(w.text))
		}
		tl.WriteString("ET\n")
		stream(form, "/Type /XObject /Subtype /Form /BBox [0 0 595 842] /Resources << /Font << /f-0-0 3 0 R >> >> /Filter /FlateDecode", flate(t, []byte(tl.String())))
	}
	packed[2] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(seiten))
	stream(5, "", []byte("/CIDInit /ProcSet findresource begin\n12 dict begin\nbegincmap\n/CMapName /Adobe-Identity-UCS def\n1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n1 beginbfrange\n<0000> <FFFF> <0000>\nendbfrange\nendcmap\nCMapName currentdict /CMap defineresource pop\nend\nend\n"))
	b.WriteString("6 0 obj\n<< /Type /FontDescriptor /FontName /GlyphLessFont /Flags 5 /FontBBox [0 0 500 1000] >>\nendobj\n")
	var head, body strings.Builder
	for _, n := range order {
		fmt.Fprintf(&head, "%d %d ", n, body.Len())
		body.WriteString(packed[n] + "\n")
	}
	data := head.String() + body.String()
	stream(8, fmt.Sprintf("/Type /ObjStm /N %d /First %d /Filter /FlateDecode", len(order), head.Len()), flate(t, []byte(data)))
	stream(9, "/Type /XRef /Size 30 /Root 1 0 R /W [1 3 1]", []byte{0, 0, 0, 0, 0})
	b.WriteString("startxref\n0\n%%EOF\n")
	return b.Bytes()
}

// klassischesPDF is a born-digital lab sheet: a plain Helvetica with
// WinAnsi, literal strings shown with Tj and placed with Td, no object
// stream, the page an 8-bit gray Flate image, a classic trailer.
func klassischesPDF(t *testing.T, woerter []wortAuf) []byte {
	t.Helper()
	w, h := 620, 877
	roh := bytes.Repeat([]byte{255}, w*h)
	var tl strings.Builder
	tl.WriteString("q 595 0 0 842 0 0 cm /Bild Do Q\nBT /F1 10 Tf\n")
	x, y := 0.0, 0.0
	for _, wo := range woerter {
		fmt.Fprintf(&tl, "%g %g Td (", wo.x-x, wo.y-y)
		for _, r := range wo.text {
			switch {
			case r == '(' || r == ')' || r == '\\':
				tl.WriteString(`\` + string(r))
			case r < 128:
				tl.WriteRune(r)
			default:
				fmt.Fprintf(&tl, `\%03o`, r) // WinAnsi = Latin-1 for ä, ö, ü, µ
			}
		}
		tl.WriteString(") Tj\n")
		x, y = wo.x, wo.y
	}
	tl.WriteString("ET\n")
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	obj := func(n int, s string) { fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", n, s) }
	obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 595 842] /Resources << /Font << /F1 4 0 R >> /XObject << /Bild 6 0 R >> >> >>")
	obj(3, "<< /Type /Page /Parent 2 0 R /Contents 5 0 R >>")
	obj(4, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	data := flate(t, []byte(tl.String()))
	fmt.Fprintf(&b, "5 0 obj\n<< /Length 7 0 R /Filter /FlateDecode >>\nstream\n")
	b.Write(data)
	b.WriteString("\nendstream\nendobj\n")
	bild := flate(t, roh)
	fmt.Fprintf(&b, "6 0 obj\n<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /FlateDecode /Length %d >>\nstream\n", w, h, len(bild))
	b.Write(bild)
	b.WriteString("\nendstream\nendobj\n")
	obj(7, fmt.Sprint(len(data)))
	b.WriteString("trailer\n<< /Size 8 /Root 1 0 R >>\nstartxref\n0\n%%EOF\n")
	return b.Bytes()
}

// weisseSeite is a white A4 scan at 150 dpi.
func weisseSeite(t *testing.T) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, 1240, 1754))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// The text layer's words and their boxes, from both kinds of PDF.
func TestTheTextLayerGivesWordsWithTheirBoxes(t *testing.T) {
	for name, pdf := range map[string][]byte{"ocrmypdf": ocrPDF(t, weisseSeite(t), laborSeite()), "klassisch": klassischesPDF(t, laborSeite())} {
		seiten, err := pdfSeitenLesen(pdf)
		if err != nil || len(seiten) != 1 {
			t.Fatalf("%s: %d pages, %v", name, len(seiten), err)
		}
		var hb *pdfWort
		for i, w := range seiten[0].Woerter {
			if w.Text == "Hämoglobin" {
				hb = &seiten[0].Woerter[i]
			}
		}
		if hb == nil {
			t.Fatalf("%s: no Hämoglobin among %v", name, seiten[0].Woerter)
		}
		if hb.Box.X0 < 58 || hb.Box.X0 > 62 || hb.Box.Y0 > 600 || hb.Box.Y1 < 605 || hb.Box.Y1 > 612 {
			t.Fatalf("%s: the box %+v is not where the word stands", name, hb.Box)
		}
		if seiten[0].Bild == nil {
			t.Fatalf("%s: the page draws its scan", name)
		}
	}
}

// The value's line is found by its number and its name: 11,8 stands twice.
func TestTheEvidenceIsFoundByNameAndNumber(t *testing.T) {
	seiten, err := pdfSeitenLesen(ocrPDF(t, weisseSeite(t), zeile(800, 60, "Seite", "eins"), laborSeite()))
	if err != nil {
		t.Fatal(err)
	}
	fall := []struct {
		m     Messwert
		y     float64 // the baseline of the line it must be found on; 0: not found
		seite int
	}{
		{Messwert{Name: "Hämoglobin", Wert: 11.8, Einheit: "g/dl"}, 600, 1}, // as the first reader stored it: no words, no raw number
		{Messwert{Name: "MCH", Wert: 11.8, Einheit: "pg"}, 560, 1},          // the same number, another line
		{Messwert{Name: "Hämoglobin", Original: "Hb", Wert: 11.8}, 600, 1},  // the report's own spelling differs: the dictionary's name is on the page
		{Messwert{Name: "Ferritin", Wert: 18, Roh: "18", Beleg: "Ferritin 18 ng/ml 15 - 150"}, 580, 1},
		{Messwert{Name: "Hämoglobin", Wert: 118, Einheit: "g/l"}, 0, 0},                  // converted: its number is not on the page
		{Messwert{Name: "Leukozyten", Wert: 6.4}, 0, 0},                                  // not on the page
		{Messwert{Name: "", Wert: 0}, 0, 0},                                              // sparse: nothing to look for
		{Messwert{Name: "Hämoglobin", Wert: 11.8, Gelesen: &Lesung{Wert: 11.8}}, 600, 1}, // corrected: looked for as read
	}
	for _, f := range fall {
		nr, box, ok := fundstelle(seiten, f.m)
		if f.y == 0 {
			if ok {
				t.Errorf("%+v: found on page %d at %+v", f.m, nr, box)
			}
			continue
		}
		if !ok || nr != f.seite || box.Y0 > f.y || box.Y1 < f.y {
			t.Errorf("%s: page %d box %+v ok %v, want the line at %g", f.m.Name, nr, box, ok, f.y)
		}
	}
}

// The snippet is the line's band of the page image with a margin, the words marked.
func TestTheSnippetIsCutFromThePageImage(t *testing.T) {
	seiten, err := pdfSeitenLesen(ocrPDF(t, weisseSeite(t), laborSeite()))
	if err != nil {
		t.Fatal(err)
	}
	nr, box, ok := fundstelle(seiten, Messwert{Name: "Hämoglobin", Wert: 11.8})
	if !ok {
		t.Fatal("not found")
	}
	px, ok := seiten[nr].pixel(box)
	if !ok {
		t.Fatal("no pixels")
	}
	// 595 pt are 1240 px: the baseline at 600 pt is 1754-600*1754/842 = 504 px from the top
	if px.Min.Y > 500 || px.Max.Y < 498 || px.Min.X > 130 || px.Max.X < 160 {
		t.Fatalf("the word's pixels: %v", px)
	}
	jpg, err := schnipselBild(seiten[nr], box)
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(jpg))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() < 600 || b.Dy() > 300 || b.Dy() < 40 {
		t.Fatalf("a band of the page, not the page: %v", b)
	}
	rot := false
	for y := 0; y < img.Bounds().Dy() && !rot; y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r>>8 > 150 && g>>8 < 110 && b>>8 < 110 {
				rot = true
				break
			}
		}
	}
	if !rot {
		t.Fatal("the words are marked")
	}
	// a page without a picture: no snippet, the caller shows the thumbnail
	ohne := seiten[nr]
	ohne.Bild = nil
	if _, err := schnipselBild(ohne, box); err == nil {
		t.Fatal("no picture, no snippet")
	}
}

// Broken files never panic and never yield a snippet.
func TestABrokenPDFYieldsNothing(t *testing.T) {
	pdf := ocrPDF(t, weisseSeite(t), laborSeite())
	for _, kaputt := range [][]byte{nil, []byte("%PDF-1.7\n"), pdf[:len(pdf)/3], pdf[:len(pdf)/2], bytes.ReplaceAll(pdf, []byte("/Root"), []byte("/Raat")), []byte("1 0 obj << /Type /Catalog /Pages 1 0 R >> endobj trailer << /Root 1 0 R >>")} {
		seiten, _ := pdfSeitenLesen(kaputt)
		if _, _, ok := fundstelle(seiten, Messwert{Name: "Hämoglobin", Wert: 11.8}); ok && len(kaputt) < len(pdf)/3 {
			t.Fatalf("found in %d bytes", len(kaputt))
		}
	}
}

// On the page: the snippet for the person only, never stored, the
// thumbnail when the words are not found.
func TestTheSnippetEndpoint(t *testing.T) {
	d, pl, _ := aufbau(t)
	pl.docs[5] = &fakeDoc{ID: 5, Title: "Labor März", Content: laborAnna, Created: "2026-03-13", Type: 7, Archiv: ocrPDF(t, weisseSeite(t), laborSeite()), Thumb: []byte("RIFF-webp-vorschau")}
	d.Lauf(context.Background(), "test")
	ws, _ := d.Speicher.AlleWerte("anna")
	id := map[string]string{}
	for _, w := range ws {
		id[w.Name] = w.ID
	}
	if id["Hämoglobin"] == "" || id["Kreatinin"] == "" {
		t.Fatalf("ids: %v", id)
	}
	h := (&Web{Dienst: d, Secret: "s3cret", Admins: "admins"}).Handler()
	rec := rufe(t, h, "GET", "/schnipsel?person=anna&id="+id["Hämoglobin"], als("anna"), "")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/jpeg" || rec.Header().Get("X-Schnipsel") != "seite" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") || !strings.Contains(cc, "private") {
		t.Fatalf("never cached: %q", cc)
	}
	if _, err := jpeg.Decode(rec.Body); err != nil {
		t.Fatal(err)
	}
	// Kreatinin is in the text but not on this archive's page: the thumbnail
	rec = rufe(t, h, "GET", "/schnipsel?person=anna&id="+id["Kreatinin"], als("anna"), "")
	if rec.Code != 200 || rec.Header().Get("X-Schnipsel") != "vorschau" || rec.Header().Get("Content-Type") != "image/webp" || rec.Body.String() != "RIFF-webp-vorschau" {
		t.Fatalf("thumbnail: %d %v %q", rec.Code, rec.Header(), rec.Body.String())
	}
	// nobody else's: not even an admin sees an adult's page
	geholt := pl.docs[5].Geholt
	for _, wer := range []map[string]string{als("ben"), als("ben", "admins"), {}} {
		if rec := rufe(t, h, "GET", "/schnipsel?person=anna&id="+id["Hämoglobin"], wer, ""); rec.Code < 400 {
			t.Fatalf("%v: %d", wer, rec.Code)
		}
	}
	if pl.docs[5].Geholt != geholt {
		t.Fatal("a refused request fetched nothing from Paperless")
	}
	// an id that is no value of hers
	if rec := rufe(t, h, "GET", "/schnipsel?person=anna&id=0000", als("anna"), ""); rec.Code != 404 {
		t.Fatalf("unknown id: %d", rec.Code)
	}
	// the page puts the snippet next to the form, and the words it was read from with the number marked
	body := rufe(t, h, "GET", "/werte", als("anna"), "").Body.String()
	for _, want := range []string{`src="/schnipsel?person=anna&amp;id=` + id["Hämoglobin"] + `"`, `loading=lazy`, `<mark>11,8</mark>`, `action="/korrektur"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the values page lacks %s", want)
		}
	}
	// nothing of it is written to the store
	if n := gespeicherteBilder(t, d.Speicher.Dir); n != 0 {
		t.Fatalf("%d images stored", n)
	}
}

// Without an archive the thumbnail; without either, a plain 404.
func TestWithoutAnArchiveTheThumbnail(t *testing.T) {
	d, pl, _ := aufbau(t)
	pl.docs[5] = &fakeDoc{ID: 5, Title: "Labor März", Content: laborAnna, Created: "2026-03-13", Type: 7, Thumb: []byte("RIFF-webp")}
	d.Lauf(context.Background(), "test")
	ws, _ := d.Speicher.AlleWerte("anna")
	h := (&Web{Dienst: d, Secret: "s3cret", Admins: "admins"}).Handler()
	rec := rufe(t, h, "GET", "/schnipsel?person=anna&id="+ws[0].ID, als("anna"), "")
	if rec.Code != 200 || rec.Header().Get("X-Schnipsel") != "vorschau" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	pl.docs[5].Thumb = nil
	if rec := rufe(t, h, "GET", "/schnipsel?person=anna&id="+ws[0].ID, als("anna"), ""); rec.Code != 404 {
		t.Fatalf("nothing to show: %d", rec.Code)
	}
}

func gespeicherteBilder(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		switch strings.ToLower(filepath.Ext(p)) {
		case ".jpg", ".jpeg", ".png", ".webp", ".pdf":
			n++
		}
		return nil
	})
	return n
}

// The words a value was read from, its number marked as a whole.
func TestBelegMarksTheNumberAsAWhole(t *testing.T) {
	for _, f := range []struct {
		m    Messwert
		want string
	}{
		{Messwert{Name: "CRP", Roh: "5", Beleg: "CRP 15 mg/l < 5"}, "CRP 15 mg/l &lt; <mark>5</mark>"},
		{Messwert{Name: "Hb", Roh: "11,8", Beleg: "Hb 11,8 g/dl 12,0 - 16,0"}, "Hb <mark>11,8</mark> g/dl 12,0 - 16,0"},
		{Messwert{Name: "FEV1", Wert: 3.04, Einheit: "l"}, "FEV1 <mark>3,04</mark> l"},                                  // a first reader's record: no words
		{Messwert{Name: "TSH", Wert: 1.2, Gelesen: &Lesung{Wert: 1.16, Einheit: "mU/l"}}, "TSH <mark>1,16</mark> mU/l"}, // corrected: as read
		{Messwert{Name: "", Beleg: "", Roh: ""}, "<mark>0</mark>"},
		{Messwert{Name: "X", Roh: "7", Beleg: "X sieben"}, "X sieben"},
	} {
		if got := belegHTML(f.m); got != f.want {
			t.Errorf("%+v: %q, want %q", f.m, got, f.want)
		}
	}
}
