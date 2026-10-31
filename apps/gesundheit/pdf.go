package main

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
)

// A small reader of what the snippet needs of a PDF (#13): the words of
// each page's text layer with their boxes, and the picture the page draws.
// Paperless' archive is OCRmyPDF's output: the scan drawn over the page,
// the OCR text invisible on top (3 Tr) in a form XObject, the dictionaries
// often packed in object streams. The container has no poppler (distroless
// static), so this reads it in Go, standard library only: objects found by
// scanning (no xref table needed), Flate the one filter, fonts read for
// their widths and ToUnicode. Anything it cannot read yields no position,
// and the page shows the thumbnail instead.

// --- objects

type pdfName string

type pdfRef struct{ Num, Gen int }

type pdfOp string // an operator in a content stream, or a keyword

type pdfDict map[string]any

type pdfStream struct {
	Dict pdfDict
	Raw  []byte // as stored, before its filters
}

// pdfLexer reads PDF objects from bytes.
type pdfLexer struct {
	b []byte
	i int
}

func istLeer(c byte) bool {
	return c == ' ' || c == '\n' || c == '\r' || c == '\t' || c == '\f' || c == 0
}

func istTrenner(c byte) bool {
	return istLeer(c) || strings.IndexByte("()<>[]{}/%", c) >= 0
}

func (l *pdfLexer) leer() {
	for l.i < len(l.b) {
		switch c := l.b[l.i]; {
		case istLeer(c):
			l.i++
		case c == '%':
			for l.i < len(l.b) && l.b[l.i] != '\n' && l.b[l.i] != '\r' {
				l.i++
			}
		default:
			return
		}
	}
}

var errPDF = errors.New("pdf: nicht lesbar")

// objekt reads one object; in a content stream an operator comes back as pdfOp.
func (l *pdfLexer) objekt(tiefe int) (any, error) {
	if tiefe > 50 {
		return nil, errPDF
	}
	l.leer()
	if l.i >= len(l.b) {
		return nil, errPDF
	}
	c := l.b[l.i]
	switch {
	case c == '/':
		l.i++
		s := l.i
		for l.i < len(l.b) && !istTrenner(l.b[l.i]) {
			l.i++
		}
		return pdfName(nameDecode(l.b[s:l.i])), nil
	case c == '(':
		return l.literal()
	case c == '<' && l.i+1 < len(l.b) && l.b[l.i+1] == '<':
		l.i += 2
		d := pdfDict{}
		for {
			l.leer()
			if l.i+1 < len(l.b) && l.b[l.i] == '>' && l.b[l.i+1] == '>' {
				l.i += 2
				return d, nil
			}
			k, err := l.objekt(tiefe + 1)
			if err != nil {
				return nil, err
			}
			n, ok := k.(pdfName)
			if !ok {
				return nil, errPDF
			}
			v, err := l.objekt(tiefe + 1)
			if err != nil {
				return nil, err
			}
			d[string(n)] = v
		}
	case c == '<':
		l.i++
		var hex []byte
		for l.i < len(l.b) && l.b[l.i] != '>' {
			if !istLeer(l.b[l.i]) {
				hex = append(hex, l.b[l.i])
			}
			l.i++
		}
		l.i++
		if len(hex)%2 == 1 {
			hex = append(hex, '0')
		}
		out := make([]byte, len(hex)/2)
		for j := range out {
			v, err := strconv.ParseUint(string(hex[2*j:2*j+2]), 16, 8)
			if err != nil {
				return nil, errPDF
			}
			out[j] = byte(v)
		}
		return out, nil
	case c == '[':
		l.i++
		var arr []any
		for {
			l.leer()
			if l.i < len(l.b) && l.b[l.i] == ']' {
				l.i++
				return arr, nil
			}
			v, err := l.objekt(tiefe + 1)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
	case c == ']' || c == '>' || c == ')' || c == '{' || c == '}':
		l.i++
		return pdfOp(string(c)), nil
	case c == '+' || c == '-' || c == '.' || (c >= '0' && c <= '9'):
		s := l.i
		l.i++
		for l.i < len(l.b) && !istTrenner(l.b[l.i]) {
			l.i++
		}
		f, err := strconv.ParseFloat(string(l.b[s:l.i]), 64)
		if err != nil {
			return nil, errPDF
		}
		// „12 0 R“: a reference
		if f == math.Trunc(f) && f >= 0 {
			merk := l.i
			l.leer()
			gs := l.i
			for l.i < len(l.b) && l.b[l.i] >= '0' && l.b[l.i] <= '9' {
				l.i++
			}
			if l.i > gs {
				gen, _ := strconv.Atoi(string(l.b[gs:l.i]))
				l.leer()
				if l.i < len(l.b) && l.b[l.i] == 'R' && (l.i+1 == len(l.b) || istTrenner(l.b[l.i+1])) {
					l.i++
					return pdfRef{int(f), gen}, nil
				}
			}
			l.i = merk
		}
		return f, nil
	}
	s := l.i
	for l.i < len(l.b) && !istTrenner(l.b[l.i]) {
		l.i++
	}
	if l.i == s { // a lone delimiter
		l.i++
	}
	switch w := string(l.b[s:l.i]); w {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "null":
		return nil, nil
	default:
		return pdfOp(w), nil
	}
}

func (l *pdfLexer) literal() ([]byte, error) {
	l.i++ // (
	var out []byte
	tiefe := 1
	for l.i < len(l.b) {
		c := l.b[l.i]
		l.i++
		switch c {
		case '(':
			tiefe++
		case ')':
			tiefe--
			if tiefe == 0 {
				return out, nil
			}
		case '\\':
			if l.i >= len(l.b) {
				return out, nil
			}
			e := l.b[l.i]
			l.i++
			switch e {
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case '\r':
				if l.i < len(l.b) && l.b[l.i] == '\n' {
					l.i++
				}
			case '\n':
			default:
				if e >= '0' && e <= '7' {
					v := int(e - '0')
					for k := 0; k < 2 && l.i < len(l.b) && l.b[l.i] >= '0' && l.b[l.i] <= '7'; k++ {
						v = v*8 + int(l.b[l.i]-'0')
						l.i++
					}
					out = append(out, byte(v))
				} else {
					out = append(out, e)
				}
			}
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

func nameDecode(b []byte) string {
	if bytes.IndexByte(b, '#') < 0 {
		return string(b)
	}
	var out []byte
	for i := 0; i < len(b); i++ {
		if b[i] == '#' && i+2 < len(b) {
			if v, err := strconv.ParseUint(string(b[i+1:i+3]), 16, 8); err == nil {
				out = append(out, byte(v))
				i += 2
				continue
			}
		}
		out = append(out, b[i])
	}
	return string(out)
}

// --- the file: objects by number, also from object streams

type pdfDatei struct {
	b         []byte
	offen     map[int]int         // object number → offset after „n g obj“
	inStrom   map[int][2]int      // object number → (object stream, index)
	gelesen   map[int]any         // parsed
	stroeme   map[int]map[int]any // an object stream's objects, parsed
	aufloesen int                 // guards against reference loops
}

var objKopfRe = regexp.MustCompile(`(?:^|[\s>])(\d+)\s+(\d+)\s+obj\b`)
var rootRe = regexp.MustCompile(`/Root\s+(\d+)\s+(\d+)\s+R`)

func pdfOeffnen(b []byte) *pdfDatei {
	f := &pdfDatei{b: b, offen: map[int]int{}, inStrom: map[int][2]int{}, gelesen: map[int]any{}, stroeme: map[int]map[int]any{}}
	for _, m := range objKopfRe.FindAllSubmatchIndex(b, -1) {
		n, _ := strconv.Atoi(string(b[m[2]:m[3]]))
		f.offen[n] = m[1] // a later definition (an incremental update) wins
	}
	// the object streams: which numbers they hold
	for n, off := range f.offen {
		ende := min(len(b), off+400)
		if !bytes.Contains(b[off:ende], []byte("/ObjStm")) {
			continue
		}
		s, ok := f.objekt(n).(*pdfStream)
		if !ok {
			continue
		}
		daten, err := f.dekodiert(s)
		if err != nil {
			continue
		}
		anzahl, erste := f.zahl(s.Dict["N"]), f.zahl(s.Dict["First"])
		if anzahl <= 0 || erste <= 0 || erste > len(daten) {
			continue
		}
		l := &pdfLexer{b: daten[:erste]}
		for k := 0; k < anzahl; k++ {
			a, err1 := l.objekt(0)
			_, err2 := l.objekt(0)
			num, ok := a.(float64)
			if err1 != nil || err2 != nil || !ok {
				break
			}
			if _, schon := f.inStrom[int(num)]; !schon {
				f.inStrom[int(num)] = [2]int{n, k}
			}
		}
	}
	return f
}

// objekt is object n, nil when there is none.
func (f *pdfDatei) objekt(n int) any {
	if v, ok := f.gelesen[n]; ok {
		return v
	}
	f.gelesen[n] = nil // a loop reads nil
	var v any
	if off, ok := f.offen[n]; ok {
		v = f.direkt(off)
	} else if pos, ok := f.inStrom[n]; ok {
		v = f.ausStrom(pos[0], pos[1])
	}
	f.gelesen[n] = v
	return v
}

func (f *pdfDatei) direkt(off int) any {
	l := &pdfLexer{b: f.b, i: off}
	v, err := l.objekt(0)
	if err != nil {
		return nil
	}
	d, ok := v.(pdfDict)
	if !ok {
		return v
	}
	merk := l.i
	if w, _ := l.objekt(0); w != pdfOp("stream") {
		l.i = merk
		return d
	}
	start := l.i
	if start < len(f.b) && f.b[start] == '\r' {
		start++
	}
	if start < len(f.b) && f.b[start] == '\n' {
		start++
	}
	ende := -1
	if n := f.zahl(d["Length"]); n > 0 && start+n <= len(f.b) {
		rest := bytes.TrimLeft(f.b[start+n:min(len(f.b), start+n+20)], "\r\n \t")
		if bytes.HasPrefix(rest, []byte("endstream")) {
			ende = start + n
		}
	}
	if ende < 0 {
		e := bytes.Index(f.b[start:], []byte("endstream"))
		if e < 0 {
			return nil
		}
		ende = start + e
		for ende > start && (f.b[ende-1] == '\n' || f.b[ende-1] == '\r') {
			ende--
		}
	}
	return &pdfStream{Dict: d, Raw: f.b[start:ende]}
}

func (f *pdfDatei) ausStrom(strom, index int) any {
	objs, ok := f.stroeme[strom]
	if !ok {
		objs = map[int]any{}
		f.stroeme[strom] = objs
		s, ok := f.objekt(strom).(*pdfStream)
		if !ok {
			return nil
		}
		daten, err := f.dekodiert(s)
		if err != nil {
			return nil
		}
		anzahl, erste := f.zahl(s.Dict["N"]), f.zahl(s.Dict["First"])
		if erste <= 0 || erste > len(daten) {
			return nil
		}
		kopf := &pdfLexer{b: daten[:erste]}
		var offs []int
		for k := 0; k < anzahl; k++ {
			_, err1 := kopf.objekt(0)
			o, err2 := kopf.objekt(0)
			of, ok := o.(float64)
			if err1 != nil || err2 != nil || !ok {
				break
			}
			offs = append(offs, int(of))
		}
		for k, o := range offs {
			if erste+o >= len(daten) || o < 0 {
				continue
			}
			l := &pdfLexer{b: daten, i: erste + o}
			if v, err := l.objekt(0); err == nil {
				objs[k] = v
			}
		}
	}
	return objs[index]
}

// wert resolves a reference.
func (f *pdfDatei) wert(v any) any {
	for i := 0; i < 10; i++ {
		r, ok := v.(pdfRef)
		if !ok {
			return v
		}
		v = f.objekt(r.Num)
	}
	return nil
}

func (f *pdfDatei) dict(v any) pdfDict {
	switch x := f.wert(v).(type) {
	case pdfDict:
		return x
	case *pdfStream:
		return x.Dict
	}
	return nil
}

func (f *pdfDatei) zahl(v any) int {
	if x, ok := f.wert(v).(float64); ok {
		return int(x)
	}
	return 0
}

func (f *pdfDatei) zahlF(v any, def float64) float64 {
	if x, ok := f.wert(v).(float64); ok {
		return x
	}
	return def
}

func (f *pdfDatei) liste(v any) []any {
	x, _ := f.wert(v).([]any)
	return x
}

func (f *pdfDatei) name(v any) string {
	x, _ := f.wert(v).(pdfName)
	return string(x)
}

// dekodiert is a stream's data with its filters undone: Flate (with PNG
// predictors) or none; any other filter is not read here.
func (f *pdfDatei) dekodiert(s *pdfStream) ([]byte, error) {
	var filter []string
	switch x := f.wert(s.Dict["Filter"]).(type) {
	case pdfName:
		filter = []string{string(x)}
	case []any:
		for _, e := range x {
			filter = append(filter, f.name(e))
		}
	}
	data := s.Raw
	for i, fl := range filter {
		if fl != "FlateDecode" && fl != "Fl" {
			return nil, fmt.Errorf("pdf: Filter %s", fl)
		}
		out, err := inflate(data, 64<<20)
		if err != nil {
			return nil, err
		}
		var parms pdfDict
		switch p := f.wert(s.Dict["DecodeParms"]).(type) {
		case pdfDict:
			parms = p
		case []any:
			if i < len(p) {
				parms = f.dict(p[i])
			}
		}
		if parms != nil && f.zahl(parms["Predictor"]) >= 10 {
			spalten := max(1, f.zahl(parms["Columns"]))
			farben := max(1, f.zahl(parms["Colors"]))
			bits := f.zahl(parms["BitsPerComponent"])
			if bits == 0 {
				bits = 8
			}
			zeile := (spalten*farben*bits + 7) / 8
			if zeile+1 > 0 && len(out)%(zeile+1) == 0 {
				out, err = pngUnfilter(out, zeile, len(out)/(zeile+1), max(1, (farben*bits+7)/8))
				if err != nil {
					return nil, err
				}
			}
		}
		data = out
	}
	return data, nil
}

// --- pages, their words and their picture

type rechteck struct{ X0, Y0, X1, Y1 float64 }

func (r rechteck) leer() bool { return r.X1 <= r.X0 || r.Y1 <= r.Y0 }

func (r rechteck) mit(o rechteck) rechteck {
	if r.leer() {
		return o
	}
	if o.leer() {
		return r
	}
	return rechteck{math.Min(r.X0, o.X0), math.Min(r.Y0, o.Y0), math.Max(r.X1, o.X1), math.Max(r.Y1, o.Y1)}
}

// matrix is a PDF matrix [a b c d e f]: x' = a x + c y + e, y' = b x + d y + f.
type matrix [6]float64

var einheitsMatrix = matrix{1, 0, 0, 1, 0, 0}

// mal is m then n (m × n in PDF's row-vector order).
func (m matrix) mal(n matrix) matrix {
	return matrix{
		m[0]*n[0] + m[1]*n[2], m[0]*n[1] + m[1]*n[3],
		m[2]*n[0] + m[3]*n[2], m[2]*n[1] + m[3]*n[3],
		m[4]*n[0] + m[5]*n[2] + n[4], m[4]*n[1] + m[5]*n[3] + n[5],
	}
}

func (m matrix) punkt(x, y float64) (float64, float64) {
	return m[0]*x + m[2]*y + m[4], m[1]*x + m[3]*y + m[5]
}

func (m matrix) invers() (matrix, bool) {
	det := m[0]*m[3] - m[1]*m[2]
	if math.Abs(det) < 1e-12 {
		return matrix{}, false
	}
	return matrix{
		m[3] / det, -m[1] / det, -m[2] / det, m[0] / det,
		(m[2]*m[5] - m[3]*m[4]) / det, (m[1]*m[4] - m[0]*m[5]) / det,
	}, true
}

// box is the axis-aligned box of a rectangle mapped by m.
func (m matrix) box(x0, y0, x1, y1 float64) rechteck {
	r := rechteck{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	for _, p := range [][2]float64{{x0, y0}, {x1, y0}, {x0, y1}, {x1, y1}} {
		x, y := m.punkt(p[0], p[1])
		r = rechteck{math.Min(r.X0, x), math.Min(r.Y0, y), math.Max(r.X1, x), math.Max(r.Y1, y)}
	}
	return r
}

// pdfWort is one word of a page's text layer, its box in PDF points.
type pdfWort struct {
	Text string
	Box  rechteck
}

// pdfBild is the picture a page draws, with the matrix it is drawn by.
type pdfBild struct {
	Strom *pdfStream
	CTM   matrix
	datei *pdfDatei
}

// pdfSeite is one page: its words in content order and its largest picture.
type pdfSeite struct {
	Woerter []pdfWort
	Bild    *pdfBild
}

// maxPDFSeiten caps the pages read: a record's values are on its first pages.
const maxPDFSeiten = 40

// pdfSeitenLesen reads the pages of a PDF; a file it cannot read gives none.
func pdfSeitenLesen(data []byte) (seiten []pdfSeite, err error) {
	defer func() {
		if r := recover(); r != nil { // a malformed file is no reason to fall over
			seiten, err = nil, fmt.Errorf("pdf: %v", r)
		}
	}()
	if !bytes.HasPrefix(bytes.TrimLeft(data, " \r\n\t"), []byte("%PDF")) {
		return nil, errPDF
	}
	f := pdfOeffnen(data)
	ms := rootRe.FindAllSubmatch(data, -1)
	if len(ms) == 0 {
		return nil, errPDF
	}
	root, _ := strconv.Atoi(string(ms[len(ms)-1][1]))
	katalog := f.dict(pdfRef{Num: root})
	if katalog == nil {
		return nil, errPDF
	}
	var blaetter []pdfDict
	var res []pdfDict
	var gehe func(knoten pdfDict, erbe pdfDict, tiefe int)
	gehe = func(knoten pdfDict, erbe pdfDict, tiefe int) {
		if knoten == nil || tiefe > 20 || len(blaetter) >= maxPDFSeiten {
			return
		}
		if r := f.dict(knoten["Resources"]); r != nil {
			erbe = r
		}
		if f.name(knoten["Type"]) == "Page" || knoten["Kids"] == nil {
			blaetter = append(blaetter, knoten)
			res = append(res, erbe)
			return
		}
		for _, k := range f.liste(knoten["Kids"]) {
			gehe(f.dict(k), erbe, tiefe+1)
		}
	}
	gehe(f.dict(katalog["Pages"]), nil, 0)
	if len(blaetter) == 0 {
		return nil, errPDF
	}
	for i, b := range blaetter {
		s := &seitenLeser{f: f}
		for _, c := range inhalte(f, b["Contents"]) {
			s.lauf(c, res[i], einheitsMatrix, 0)
		}
		s.wortEnde()
		seiten = append(seiten, pdfSeite{Woerter: s.woerter, Bild: s.bild})
	}
	return seiten, nil
}

// inhalte are a page's content streams, decoded.
func inhalte(f *pdfDatei, v any) [][]byte {
	var out [][]byte
	add := func(x any) {
		if s, ok := f.wert(x).(*pdfStream); ok {
			if d, err := f.dekodiert(s); err == nil {
				out = append(out, d)
			}
		}
	}
	if l, ok := f.wert(v).([]any); ok {
		// the parts of one content stream: joined, an operator may span two
		var alle []byte
		for _, x := range l {
			if s, ok := f.wert(x).(*pdfStream); ok {
				if d, err := f.dekodiert(s); err == nil {
					alle = append(append(alle, d...), '\n')
				}
			}
		}
		return [][]byte{alle}
	}
	add(v)
	return out
}

// --- fonts: how a string's bytes become characters and how wide they are

type pdfFont struct {
	zweiByte  bool
	breite    map[int]float64 // per code, in 1/1000 of the size
	standard  float64
	toUnicode []cmapEintrag
	enc       map[int]rune // a simple font's encoding (WinAnsi with Differences)
}

type cmapEintrag struct {
	lo, hi int
	ziel   []rune // for a range: the first target, counted up
	liste  [][]rune
}

func (f *pdfDatei) font(v any) *pdfFont {
	d := f.dict(v)
	pf := &pdfFont{breite: map[int]float64{}, standard: 500}
	if d == nil {
		return pf
	}
	if f.name(d["Subtype"]) == "Type0" {
		pf.zweiByte = true
		if ds := f.liste(d["DescendantFonts"]); len(ds) > 0 {
			cid := f.dict(ds[0])
			pf.standard = f.zahlF(cid["DW"], 1000)
			w := f.liste(cid["W"])
			for i := 0; i+1 < len(w); {
				c := f.zahl(w[i])
				if arr, ok := f.wert(w[i+1]).([]any); ok {
					for k, x := range arr {
						pf.breite[c+k] = f.zahlF(x, pf.standard)
					}
					i += 2
					continue
				}
				if i+2 >= len(w) {
					break
				}
				bis, b := f.zahl(w[i+1]), f.zahlF(w[i+2], pf.standard)
				for k := c; k <= bis && k-c < 65536; k++ {
					pf.breite[k] = b
				}
				i += 3
			}
		}
	} else {
		erste := f.zahl(d["FirstChar"])
		for k, x := range f.liste(d["Widths"]) {
			pf.breite[erste+k] = f.zahlF(x, 500)
		}
		pf.enc = map[int]rune{}
		if enc := f.dict(d["Encoding"]); enc != nil {
			code := 0
			for _, x := range f.liste(enc["Differences"]) {
				switch y := f.wert(x).(type) {
				case float64:
					code = int(y)
				case pdfName:
					if r, ok := glyphRune(string(y)); ok {
						pf.enc[code] = r
					}
					code++
				}
			}
		}
	}
	if s, ok := f.wert(d["ToUnicode"]).(*pdfStream); ok {
		if data, err := f.dekodiert(s); err == nil {
			pf.toUnicode = cmapLesen(data)
		}
	}
	return pf
}

// cmapLesen reads a ToUnicode CMap's bfchar and bfrange.
func cmapLesen(data []byte) []cmapEintrag {
	var out []cmapEintrag
	l := &pdfLexer{b: data}
	var stapel []any
	modus := ""
	for n := 0; n < 200000; n++ {
		v, err := l.objekt(0)
		if err != nil {
			break
		}
		if op, ok := v.(pdfOp); ok {
			switch op {
			case "beginbfchar", "beginbfrange":
				modus = string(op)
			case "endbfchar", "endbfrange":
				modus = ""
			}
			stapel = stapel[:0]
			continue
		}
		if modus == "" {
			continue
		}
		stapel = append(stapel, v)
		switch {
		case modus == "beginbfchar" && len(stapel) == 2:
			src, ok1 := stapel[0].([]byte)
			dst, ok2 := stapel[1].([]byte)
			if ok1 && ok2 {
				c := codeVon(src)
				out = append(out, cmapEintrag{lo: c, hi: c, ziel: utf16Runen(dst)})
			}
			stapel = stapel[:0]
		case modus == "beginbfrange" && len(stapel) == 3:
			lo, ok1 := stapel[0].([]byte)
			hi, ok2 := stapel[1].([]byte)
			if ok1 && ok2 {
				e := cmapEintrag{lo: codeVon(lo), hi: codeVon(hi)}
				switch z := stapel[2].(type) {
				case []byte:
					e.ziel = utf16Runen(z)
				case []any:
					for _, x := range z {
						if b, ok := x.([]byte); ok {
							e.liste = append(e.liste, utf16Runen(b))
						}
					}
				}
				out = append(out, e)
			}
			stapel = stapel[:0]
		}
	}
	return out
}

func codeVon(b []byte) int {
	c := 0
	for _, x := range b {
		c = c<<8 | int(x)
	}
	return c
}

func utf16Runen(b []byte) []rune {
	var u []uint16
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
	}
	if len(b)%2 == 1 {
		u = append(u, uint16(b[len(b)-1]))
	}
	return utf16.Decode(u)
}

// text is the characters a code stands for.
func (pf *pdfFont) text(code int) []rune {
	for _, e := range pf.toUnicode {
		if code < e.lo || code > e.hi {
			continue
		}
		if e.liste != nil {
			if k := code - e.lo; k < len(e.liste) {
				return e.liste[k]
			}
			return nil
		}
		if len(e.ziel) == 0 {
			return nil
		}
		r := append([]rune{}, e.ziel...)
		r[len(r)-1] += rune(code - e.lo)
		return r
	}
	if pf.zweiByte {
		return []rune{rune(code)} // a GlyphLessFont without its map: the CID is the UTF-16 unit
	}
	if r, ok := pf.enc[code]; ok {
		return []rune{r}
	}
	if code >= 0x80 && code < 0xA0 {
		if r := winAnsi[code-0x80]; r != 0 {
			return []rune{r}
		}
	}
	return []rune{rune(code)} // WinAnsi is Latin-1 for the rest
}

func (pf *pdfFont) weite(code int) float64 {
	if w, ok := pf.breite[code]; ok {
		return w
	}
	return pf.standard
}

// winAnsi is WinAnsiEncoding from 0x80 to 0x9F.
var winAnsi = [32]rune{'€', 0, '‚', 'ƒ', '„', '…', '†', '‡', 'ˆ', '‰', 'Š', '‹', 'Œ', 0, 'Ž', 0, 0, '‘', '’', '“', '”', '•', '–', '—', '˜', '™', 'š', '›', 'œ', 0, 'ž', 'Ÿ'}

// glyphRune is the character of a glyph name a font's Differences use.
func glyphRune(n string) (rune, bool) {
	if len(n) == 1 {
		return rune(n[0]), true
	}
	if strings.HasPrefix(n, "uni") && len(n) == 7 {
		if v, err := strconv.ParseUint(n[3:], 16, 32); err == nil {
			return rune(v), true
		}
	}
	r, ok := glyphNamen[n]
	return r, ok
}

var glyphNamen = map[string]rune{
	"space": ' ', "comma": ',', "period": '.', "hyphen": '-', "minus": '-', "slash": '/', "percent": '%', "colon": ':', "semicolon": ';',
	"parenleft": '(', "parenright": ')', "bracketleft": '[', "bracketright": ']', "less": '<', "greater": '>', "equal": '=', "plus": '+',
	"zero": '0', "one": '1', "two": '2', "three": '3', "four": '4', "five": '5', "six": '6', "seven": '7', "eight": '8', "nine": '9',
	"adieresis": 'ä', "odieresis": 'ö', "udieresis": 'ü', "Adieresis": 'Ä', "Odieresis": 'Ö', "Udieresis": 'Ü', "germandbls": 'ß',
	"mu": 'µ', "micro": 'µ', "degree": '°', "endash": '–', "emdash": '—', "asterisk": '*', "quotesingle": '\'', "quotedbl": '"',
	"eacute": 'é', "egrave": 'è', "twosuperior": '²', "threesuperior": '³', "underscore": '_', "numbersign": '#', "ampersand": '&',
}

// --- the content stream: where each glyph lands

type grafik struct {
	ctm            matrix
	font           *pdfFont
	groesse        float64
	tc, tw, th, tl float64
	rise           float64
}

type seitenLeser struct {
	f       *pdfDatei
	woerter []pdfWort
	bild    *pdfBild
	flaeche float64
	fonts   map[any]*pdfFont

	wort   []rune
	wortBx rechteck
}

func (s *seitenLeser) wortEnde() {
	if t := strings.TrimSpace(string(s.wort)); t != "" {
		s.woerter = append(s.woerter, pdfWort{Text: t, Box: s.wortBx})
	}
	s.wort, s.wortBx = nil, rechteck{}
}

func (s *seitenLeser) fontVon(res pdfDict, name string) *pdfFont {
	fonts := s.f.dict(res["Font"])
	ref := fonts[name]
	key := any(name)
	if r, ok := ref.(pdfRef); ok {
		key = r
	}
	if s.fonts == nil {
		s.fonts = map[any]*pdfFont{}
	}
	if pf, ok := s.fonts[key]; ok {
		return pf
	}
	pf := s.f.font(ref)
	s.fonts[key] = pf
	return pf
}

// lauf follows one content stream: the graphics state, the text state, the
// glyphs shown, and the XObjects drawn (a form is followed, an image noted).
func (s *seitenLeser) lauf(data []byte, res pdfDict, ctm matrix, tiefe int) {
	if tiefe > 6 {
		return
	}
	g := grafik{ctm: ctm, th: 1}
	var stapel []grafik
	var tm, tlm matrix
	var ops []any
	l := &pdfLexer{b: data}
	zahlen := func(n int) ([]float64, bool) {
		if len(ops) < n {
			return nil, false
		}
		out := make([]float64, n)
		for i, o := range ops[len(ops)-n:] {
			f, ok := o.(float64)
			if !ok {
				return nil, false
			}
			out[i] = f
		}
		return out, true
	}
	td := func(x, y float64) {
		tlm = matrix{1, 0, 0, 1, x, y}.mal(tlm)
		tm = tlm
		s.wortEnde()
	}
	zeigen := func(str []byte) {
		if g.font == nil {
			g.font = &pdfFont{breite: map[int]float64{}, standard: 500}
		}
		n := 1
		if g.font.zweiByte {
			n = 2
		}
		for i := 0; i+n <= len(str); i += n {
			code := int(str[i])
			if n == 2 {
				code = int(str[i])<<8 | int(str[i+1])
			}
			w0 := g.font.weite(code) / 1000
			trm := matrix{g.groesse * g.th, 0, 0, g.groesse, 0, g.rise}.mal(tm).mal(g.ctm)
			rs := g.font.text(code)
			leer := len(rs) == 0
			for _, r := range rs {
				if r != ' ' && r != ' ' && r != 0 {
					leer = false
				} else {
					leer = true
				}
			}
			if leer {
				s.wortEnde()
			} else {
				s.wort = append(s.wort, rs...)
				s.wortBx = s.wortBx.mit(trm.box(0, -0.2, w0, 0.8))
			}
			tx := w0*g.groesse + g.tc
			if n == 1 && code == 32 {
				tx += g.tw
			}
			tm = matrix{1, 0, 0, 1, tx * g.th, 0}.mal(tm)
		}
	}
	for n := 0; n < 2_000_000; n++ {
		v, err := l.objekt(0)
		if err != nil {
			break
		}
		op, ok := v.(pdfOp)
		if !ok {
			ops = append(ops, v)
			if len(ops) > 64 {
				ops = ops[len(ops)-64:]
			}
			continue
		}
		switch op {
		case "q":
			stapel = append(stapel, g)
		case "Q":
			if len(stapel) > 0 {
				g, stapel = stapel[len(stapel)-1], stapel[:len(stapel)-1]
			}
		case "cm":
			if z, ok := zahlen(6); ok {
				g.ctm = matrix{z[0], z[1], z[2], z[3], z[4], z[5]}.mal(g.ctm)
			}
		case "BT":
			tm, tlm = einheitsMatrix, einheitsMatrix
			s.wortEnde()
		case "ET":
			s.wortEnde()
		case "Tf":
			if len(ops) >= 2 {
				if nm, ok := ops[len(ops)-2].(pdfName); ok {
					g.font = s.fontVon(res, string(nm))
				}
				if f, ok := ops[len(ops)-1].(float64); ok {
					g.groesse = f
				}
			}
		case "Tc":
			if z, ok := zahlen(1); ok {
				g.tc = z[0]
			}
		case "Tw":
			if z, ok := zahlen(1); ok {
				g.tw = z[0]
			}
		case "Tz":
			if z, ok := zahlen(1); ok {
				g.th = z[0] / 100
			}
		case "TL":
			if z, ok := zahlen(1); ok {
				g.tl = z[0]
			}
		case "Ts":
			if z, ok := zahlen(1); ok {
				g.rise = z[0]
			}
		case "Td":
			if z, ok := zahlen(2); ok {
				td(z[0], z[1])
			}
		case "TD":
			if z, ok := zahlen(2); ok {
				g.tl = -z[1]
				td(z[0], z[1])
			}
		case "Tm":
			if z, ok := zahlen(6); ok {
				tlm = matrix{z[0], z[1], z[2], z[3], z[4], z[5]}
				tm = tlm
				s.wortEnde()
			}
		case "T*":
			td(0, -g.tl)
		case "Tj":
			if len(ops) > 0 {
				if b, ok := ops[len(ops)-1].([]byte); ok {
					zeigen(b)
				}
			}
		case "'":
			td(0, -g.tl)
			if len(ops) > 0 {
				if b, ok := ops[len(ops)-1].([]byte); ok {
					zeigen(b)
				}
			}
		case "\"":
			if len(ops) >= 3 {
				if a, ok := ops[len(ops)-3].(float64); ok {
					g.tw = a
				}
				if c, ok := ops[len(ops)-2].(float64); ok {
					g.tc = c
				}
				td(0, -g.tl)
				if b, ok := ops[len(ops)-1].([]byte); ok {
					zeigen(b)
				}
			}
		case "TJ":
			if len(ops) > 0 {
				if arr, ok := ops[len(ops)-1].([]any); ok {
					for _, e := range arr {
						switch x := e.(type) {
						case []byte:
							zeigen(x)
						case float64:
							if x < -250 || x > 250 { // a gap of a quarter em parts two words
								s.wortEnde()
							}
							tm = matrix{1, 0, 0, 1, -x / 1000 * g.groesse * g.th, 0}.mal(tm)
						}
					}
				}
			}
		case "Do":
			if len(ops) > 0 {
				if nm, ok := ops[len(ops)-1].(pdfName); ok {
					s.zeichnen(res, string(nm), g.ctm, tiefe)
				}
			}
		case "BI":
			// an inline image: its data runs to „EI“
			if e := bytes.Index(data[l.i:], []byte("EI")); e >= 0 {
				l.i += e + 2
			} else {
				l.i = len(data)
			}
		}
		ops = ops[:0]
	}
	s.wortEnde()
}

// zeichnen follows an XObject: a form's content, an image's place.
func (s *seitenLeser) zeichnen(res pdfDict, name string, ctm matrix, tiefe int) {
	xo := s.f.dict(res["XObject"])
	strom, ok := s.f.wert(xo[name]).(*pdfStream)
	if !ok {
		return
	}
	switch s.f.name(strom.Dict["Subtype"]) {
	case "Image":
		x0, y0 := ctm.punkt(0, 0)
		x1, y1 := ctm.punkt(1, 1)
		fl := math.Abs((x1 - x0) * (y1 - y0))
		if fl > s.flaeche {
			s.flaeche = fl
			s.bild = &pdfBild{Strom: strom, CTM: ctm, datei: s.f}
		}
	case "Form":
		m := einheitsMatrix
		if z := s.f.liste(strom.Dict["Matrix"]); len(z) == 6 {
			for i := range m {
				m[i] = s.f.zahlF(z[i], m[i])
			}
		}
		innen := s.f.dict(strom.Dict["Resources"])
		if innen == nil {
			innen = res
		}
		data, err := s.f.dekodiert(strom)
		if err != nil {
			return
		}
		s.wortEnde()
		s.lauf(data, innen, m.mal(ctm), tiefe+1)
	}
}
