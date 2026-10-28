package main

import (
	"regexp"
	"strings"
)

// Analyt is one measured quantity the household's reports name, under the
// one name the timeline draws it by. A report's own spelling ("Hämoglobin",
// "HGB", "Hb") is an alias; its unit is kept as the report gives it, and
// never converted unless Umrechnung names the factor.
type Analyt struct {
	Name       string             // the canonical German name
	Einheit    string             // the usual unit, for the label only
	Aliase     []string           // spellings reports use, compared folded
	Gruppe     string             // Blutbild, Stoffwechsel, Lunge …: the order on the page
	Umrechnung map[string]float64 // unit → factor into Einheit, only where it is exact
}

// Woerterbuch is the small list of analytes the program knows. A value
// whose name is not here is kept under the report's own name and marked
// neu, so the owner sees what to add.
var Woerterbuch = []Analyt{
	// Blutbild
	{Name: "Hämoglobin", Einheit: "g/dl", Aliase: []string{"hb", "hgb", "haemoglobin"}, Gruppe: "Blutbild",
		Umrechnung: map[string]float64{"g/l": 0.1, "mmol/l": 1.6114}},
	{Name: "Hämatokrit", Einheit: "%", Aliase: []string{"hk", "hkt", "hct", "haematokrit"}, Gruppe: "Blutbild"},
	{Name: "Erythrozyten", Einheit: "/pl", Aliase: []string{"ery", "rbc", "erythrozyten"}, Gruppe: "Blutbild"},
	{Name: "Leukozyten", Einheit: "/nl", Aliase: []string{"leuko", "leukos", "wbc", "leukozyten"}, Gruppe: "Blutbild"},
	{Name: "Thrombozyten", Einheit: "/nl", Aliase: []string{"thrombo", "plt", "thrombozyten"}, Gruppe: "Blutbild"},
	{Name: "MCV", Einheit: "fl", Aliase: []string{"mcv"}, Gruppe: "Blutbild"},
	{Name: "MCH", Einheit: "pg", Aliase: []string{"mch"}, Gruppe: "Blutbild"},
	{Name: "Ferritin", Einheit: "ng/ml", Aliase: []string{"ferritin"}, Gruppe: "Blutbild", Umrechnung: map[string]float64{"µg/l": 1, "ug/l": 1}},
	{Name: "Eisen", Einheit: "µg/dl", Aliase: []string{"eisen", "fe"}, Gruppe: "Blutbild"},
	{Name: "Transferrinsättigung", Einheit: "%", Aliase: []string{"tfs", "transferrinsaettigung"}, Gruppe: "Blutbild"},
	// Entzündung
	{Name: "CRP", Einheit: "mg/l", Aliase: []string{"crp", "c-reaktives protein", "c reaktives protein"}, Gruppe: "Entzündung",
		Umrechnung: map[string]float64{"mg/dl": 10}},
	{Name: "BSG", Einheit: "mm", Aliase: []string{"bsg", "blutsenkung"}, Gruppe: "Entzündung"},
	// Stoffwechsel
	{Name: "Glukose", Einheit: "mg/dl", Aliase: []string{"glucose", "glukose", "blutzucker", "nüchternglukose", "nuechternglukose"}, Gruppe: "Stoffwechsel",
		Umrechnung: map[string]float64{"mmol/l": 18.016}},
	{Name: "HbA1c", Einheit: "%", Aliase: []string{"hba1c", "hb a1c", "langzeitzucker"}, Gruppe: "Stoffwechsel"},
	{Name: "Cholesterin", Einheit: "mg/dl", Aliase: []string{"cholesterin", "gesamtcholesterin", "cholesterol"}, Gruppe: "Stoffwechsel",
		Umrechnung: map[string]float64{"mmol/l": 38.67}},
	{Name: "LDL", Einheit: "mg/dl", Aliase: []string{"ldl", "ldl-cholesterin", "ldl cholesterin"}, Gruppe: "Stoffwechsel",
		Umrechnung: map[string]float64{"mmol/l": 38.67}},
	{Name: "HDL", Einheit: "mg/dl", Aliase: []string{"hdl", "hdl-cholesterin", "hdl cholesterin"}, Gruppe: "Stoffwechsel",
		Umrechnung: map[string]float64{"mmol/l": 38.67}},
	{Name: "Triglyceride", Einheit: "mg/dl", Aliase: []string{"triglyceride", "triglyzeride", "tg"}, Gruppe: "Stoffwechsel",
		Umrechnung: map[string]float64{"mmol/l": 88.57}},
	{Name: "Harnsäure", Einheit: "mg/dl", Aliase: []string{"harnsaeure", "harnsäure"}, Gruppe: "Stoffwechsel"},
	// Niere
	{Name: "Kreatinin", Einheit: "mg/dl", Aliase: []string{"kreatinin", "krea", "creatinin"}, Gruppe: "Niere",
		Umrechnung: map[string]float64{"µmol/l": 0.0113, "umol/l": 0.0113}},
	{Name: "GFR", Einheit: "ml/min", Aliase: []string{"gfr", "egfr", "gfr (ckd-epi)", "glomeruläre filtrationsrate"}, Gruppe: "Niere"},
	{Name: "Harnstoff", Einheit: "mg/dl", Aliase: []string{"harnstoff", "urea"}, Gruppe: "Niere"},
	// Leber
	{Name: "GOT", Einheit: "U/l", Aliase: []string{"got", "ast", "asat"}, Gruppe: "Leber"},
	{Name: "GPT", Einheit: "U/l", Aliase: []string{"gpt", "alt", "alat"}, Gruppe: "Leber"},
	{Name: "Gamma-GT", Einheit: "U/l", Aliase: []string{"ggt", "gamma-gt", "gamma gt", "γ-gt"}, Gruppe: "Leber"},
	{Name: "Bilirubin", Einheit: "mg/dl", Aliase: []string{"bilirubin", "gesamtbilirubin"}, Gruppe: "Leber"},
	// Schilddrüse, Vitamine, Salze
	{Name: "TSH", Einheit: "mU/l", Aliase: []string{"tsh", "tsh basal"}, Gruppe: "Schilddrüse"},
	{Name: "fT3", Einheit: "pg/ml", Aliase: []string{"ft3", "freies t3"}, Gruppe: "Schilddrüse"},
	{Name: "fT4", Einheit: "ng/dl", Aliase: []string{"ft4", "freies t4"}, Gruppe: "Schilddrüse"},
	{Name: "Vitamin D", Einheit: "ng/ml", Aliase: []string{"vitamin d", "vitamin d3", "25-oh-vitamin d", "25-oh vitamin d", "calcidiol"}, Gruppe: "Vitamine",
		Umrechnung: map[string]float64{"nmol/l": 0.4}},
	{Name: "Vitamin B12", Einheit: "pg/ml", Aliase: []string{"vitamin b12", "b12", "cobalamin"}, Gruppe: "Vitamine"},
	{Name: "Folsäure", Einheit: "ng/ml", Aliase: []string{"folsaeure", "folsäure", "folat"}, Gruppe: "Vitamine"},
	{Name: "Natrium", Einheit: "mmol/l", Aliase: []string{"natrium", "na"}, Gruppe: "Salze"},
	{Name: "Kalium", Einheit: "mmol/l", Aliase: []string{"kalium", "k"}, Gruppe: "Salze"},
	{Name: "Calcium", Einheit: "mmol/l", Aliase: []string{"calcium", "kalzium", "ca"}, Gruppe: "Salze"},
	{Name: "Magnesium", Einheit: "mmol/l", Aliase: []string{"magnesium", "mg"}, Gruppe: "Salze"},
	// Lunge (Spirometrie)
	{Name: "FEV1", Einheit: "l", Aliase: []string{"fev1", "fev 1", "einsekundenkapazität", "einsekundenkapazitaet"}, Gruppe: "Lunge"},
	{Name: "FVC", Einheit: "l", Aliase: []string{"fvc", "forcierte vitalkapazität", "forcierte vitalkapazitaet", "vitalkapazität"}, Gruppe: "Lunge"},
	{Name: "FEV1/FVC", Einheit: "%", Aliase: []string{"fev1/fvc", "fev1/vc", "tiffeneau", "tiffeneau-index"}, Gruppe: "Lunge"},
	{Name: "Peak-Flow", Einheit: "l/min", Aliase: []string{"peak-flow", "peak flow", "pef"}, Gruppe: "Lunge"},
	{Name: "FeNO", Einheit: "ppb", Aliase: []string{"feno", "no-messung"}, Gruppe: "Lunge"},
	// Kreislauf, Körper
	{Name: "Blutdruck systolisch", Einheit: "mmHg", Aliase: []string{"blutdruck systolisch", "rr systolisch", "systolisch"}, Gruppe: "Kreislauf"},
	{Name: "Blutdruck diastolisch", Einheit: "mmHg", Aliase: []string{"blutdruck diastolisch", "rr diastolisch", "diastolisch"}, Gruppe: "Kreislauf"},
	{Name: "Puls", Einheit: "/min", Aliase: []string{"puls", "herzfrequenz", "hf"}, Gruppe: "Kreislauf"},
	{Name: "Gewicht", Einheit: "kg", Aliase: []string{"gewicht", "körpergewicht", "koerpergewicht"}, Gruppe: "Körper"},
	{Name: "BMI", Einheit: "kg/m²", Aliase: []string{"bmi", "body-mass-index"}, Gruppe: "Körper"},
}

// Gruppen is the order the page shows groups in; a group not here comes last.
var Gruppen = []string{"Blutbild", "Entzündung", "Stoffwechsel", "Niere", "Leber", "Schilddrüse", "Vitamine", "Salze", "Lunge", "Kreislauf", "Körper", "Weitere"}

var nichtWort = regexp.MustCompile(`[^a-z0-9/%µ-]+`)

// akzente folds umlauts and the accents names carry (standard library
// only: no unicode normalisation package).
var akzente = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss",
	"á", "a", "à", "a", "â", "a", "ã", "a", "å", "a", "é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i", "ó", "o", "ò", "o", "ô", "o", "õ", "o", "ø", "o",
	"ú", "u", "ù", "u", "û", "u", "ý", "y", "ñ", "n", "ç", "c", "š", "s", "ž", "z", "č", "c", "ł", "l")

// falten is a name as the dictionary compares it: lower case, umlauts and
// accents folded, one space between words, no punctuation but / % - µ.
func falten(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = akzente.Replace(s)
	return strings.TrimSpace(nichtWort.ReplaceAllString(s, " "))
}

var aliasIndex = func() map[string]*Analyt {
	m := map[string]*Analyt{}
	for i := range Woerterbuch {
		a := &Woerterbuch[i]
		m[falten(a.Name)] = a
		for _, al := range a.Aliase {
			m[falten(al)] = a
		}
	}
	return m
}()

// finde is the analyte a report's name stands for, or nil when the
// dictionary does not know it. A trailing unit or a bracket is ignored
// ("Kreatinin (enzym.)", "LDL-Cholesterin mg/dl").
func finde(name string) *Analyt {
	f := falten(name)
	if a := aliasIndex[f]; a != nil {
		return a
	}
	if i := strings.IndexAny(f, "(["); i > 0 {
		if a := aliasIndex[strings.TrimSpace(f[:i])]; a != nil {
			return a
		}
	}
	w := strings.Fields(f)
	for n := len(w) - 1; n > 0; n-- {
		if a := aliasIndex[strings.Join(w[:n], " ")]; a != nil {
			return a
		}
	}
	return nil
}

// gruppenRang is a group's place on the page.
func gruppenRang(g string) int {
	for i, x := range Gruppen {
		if x == g {
			return i
		}
	}
	return len(Gruppen)
}
