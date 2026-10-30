package main

import (
	"fmt"
	"regexp"
	"sort"
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
	// Einheiten are the units this value is given in, when they are few and
	// certain (#10): a unit outside them, Einheit and Umrechnung says „prüfen“
	// („MVV(calc) 149,2 min“). Empty: any unit that has a unit's form.
	Einheiten []string
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
	{Name: "MCHC", Einheit: "g/dl", Aliase: []string{"mchc"}, Gruppe: "Blutbild"},
	{Name: "RDW", Einheit: "%", Aliase: []string{"rdw", "rdw-cv", "erythrozyten-verteilungsbreite"}, Gruppe: "Blutbild"},
	{Name: "Neutrophile", Aliase: []string{"neutrophile", "neutrophile granulozyten", "neutro", "segmentkernige"}, Gruppe: "Blutbild"},
	{Name: "Lymphozyten", Aliase: []string{"lymphozyten", "lympho", "lymphos"}, Gruppe: "Blutbild"},
	{Name: "Monozyten", Aliase: []string{"monozyten", "mono", "monos"}, Gruppe: "Blutbild"},
	{Name: "Eosinophile", Aliase: []string{"eosinophile", "eosinophile granulozyten", "eosino", "eos"}, Gruppe: "Blutbild"},
	{Name: "Basophile", Aliase: []string{"basophile", "basophile granulozyten", "baso"}, Gruppe: "Blutbild"},
	{Name: "Ferritin", Einheit: "ng/ml", Aliase: []string{"ferritin"}, Gruppe: "Blutbild", Umrechnung: map[string]float64{"µg/l": 1, "ug/l": 1}},
	{Name: "Eisen", Einheit: "µg/dl", Aliase: []string{"eisen", "fe"}, Gruppe: "Blutbild"},
	{Name: "Transferrinsättigung", Einheit: "%", Aliase: []string{"tfs", "transferrinsaettigung"}, Gruppe: "Blutbild"},
	// Entzündung
	{Name: "CRP", Einheit: "mg/l", Aliase: []string{"crp", "c-reaktives protein", "c reaktives protein"}, Gruppe: "Entzündung",
		Umrechnung: map[string]float64{"mg/dl": 10}},
	{Name: "BSG", Einheit: "mm", Aliase: []string{"bsg", "blutsenkung"}, Gruppe: "Entzündung"},
	// Infektionen (#10: serology on the box)
	{Name: "Anti-HBs", Einheit: "IU/l", Aliase: []string{"anti-hbs", "anti hbs", "hbs-antikörper", "hbs-ak", "Hepatitis B (Anti-HBs)"}, Gruppe: "Infektionen",
		Einheiten: []string{"IU/l", "mIU/ml", "U/l", "mU/ml", "IE/l", "mIE/ml"}},
	{Name: "HBsAg", Aliase: []string{"hbsag", "hbs-ag", "Hepatitis B (HBsAg)"}, Gruppe: "Infektionen"},
	{Name: "Anti-HIV", Aliase: []string{"anti-hiv", "hiv-ak", "HIV (Anti-HIV)", "hiv-1/2-antikörper"}, Gruppe: "Infektionen"},
	{Name: "Lues-Suchtest", Aliase: []string{"lues screen", "lues-screen", "lues suchtest", "lues-suchreaktion", "tpha", "tppa", "tpha-test", "tppa-test"}, Gruppe: "Infektionen"},
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
	{Name: "Alkalische Phosphatase", Einheit: "U/l", Aliase: []string{"alkalische phosphatase", "ap", "alp"}, Gruppe: "Leber"},
	{Name: "Albumin", Einheit: "g/dl", Aliase: []string{"albumin"}, Gruppe: "Leber"},
	{Name: "Gesamteiweiß", Einheit: "g/dl", Aliase: []string{"gesamteiweiss", "gesamteiweiß", "eiweiss gesamt", "gesamtprotein", "protein gesamt"}, Gruppe: "Leber"},
	// Gerinnung, Enzyme
	{Name: "Quick", Einheit: "%", Aliase: []string{"quick", "quick-wert", "thromboplastinzeit", "tpz"}, Gruppe: "Gerinnung"},
	{Name: "INR", Aliase: []string{"inr"}, Gruppe: "Gerinnung"},
	{Name: "LDH", Einheit: "U/l", Aliase: []string{"ldh", "laktatdehydrogenase", "lactatdehydrogenase"}, Gruppe: "Weitere"},
	{Name: "CK", Einheit: "U/l", Aliase: []string{"ck", "kreatinkinase", "creatinkinase"}, Gruppe: "Weitere"},
	{Name: "Lipase", Einheit: "U/l", Aliase: []string{"lipase"}, Gruppe: "Weitere"},
	// Schilddrüse, Vitamine, Salze
	{Name: "TSH", Einheit: "mU/l", Aliase: []string{"tsh", "tsh basal"}, Gruppe: "Schilddrüse"},
	{Name: "fT3", Einheit: "pg/ml", Aliase: []string{"ft3", "freies t3"}, Gruppe: "Schilddrüse"},
	{Name: "fT4", Einheit: "ng/dl", Aliase: []string{"ft4", "freies t4"}, Gruppe: "Schilddrüse"},
	{Name: "Schilddrüsenvolumen", Einheit: "ml", Aliase: []string{"sd-volumen", "sd volumen", "schilddrüsenvolumen", "schilddruesenvolumen"}, Gruppe: "Schilddrüse",
		Einheiten: []string{"ml", "ccm", "cm³"}},
	{Name: "Tc-Uptake", Einheit: "%", Aliase: []string{"Tc.-Uptake", "tc uptake", "tctu", "technetium-uptake"}, Gruppe: "Schilddrüse", Einheiten: []string{"%"}},
	{Name: "Vitamin D", Einheit: "ng/ml", Aliase: []string{"vitamin d", "vitamin d3", "25-oh-vitamin d", "25-oh vitamin d", "calcidiol"}, Gruppe: "Vitamine",
		Umrechnung: map[string]float64{"nmol/l": 0.4}},
	{Name: "Vitamin B12", Einheit: "pg/ml", Aliase: []string{"vitamin b12", "b12", "cobalamin"}, Gruppe: "Vitamine"},
	{Name: "Folsäure", Einheit: "ng/ml", Aliase: []string{"folsaeure", "folsäure", "folat"}, Gruppe: "Vitamine"},
	{Name: "Natrium", Einheit: "mmol/l", Aliase: []string{"natrium", "na"}, Gruppe: "Salze"},
	{Name: "Kalium", Einheit: "mmol/l", Aliase: []string{"kalium", "k"}, Gruppe: "Salze"},
	{Name: "Calcium", Einheit: "mmol/l", Aliase: []string{"calcium", "kalzium", "ca"}, Gruppe: "Salze"},
	{Name: "Magnesium", Einheit: "mmol/l", Aliase: []string{"magnesium", "mg"}, Gruppe: "Salze"},
	// Lunge (Spirometrie, Fluss-Volumen-Kurve). MEF x is the flow at x % of the
	// vital capacity still in the lung, FEF y after y % breathed out: MEF75 = FEF25.
	{Name: "FEV1", Einheit: "l", Aliase: []string{"fev1", "fev 1", "einsekundenkapazität", "einsekundenkapazitaet"}, Gruppe: "Lunge", Einheiten: liter},
	{Name: "FVC", Einheit: "l", Aliase: []string{"fvc", "forcierte vitalkapazität", "forcierte vitalkapazitaet", "vitalkapazität"}, Gruppe: "Lunge", Einheiten: liter},
	{Name: "FEV1/FVC", Einheit: "%", Aliase: []string{"fev1/fvc", "fev1/vc", "tiffeneau", "tiffeneau-index"}, Gruppe: "Lunge", Einheiten: prozent},
	{Name: "FEV3", Einheit: "l", Aliase: []string{"fev3", "fev 3"}, Gruppe: "Lunge", Einheiten: liter},
	{Name: "FEV3/FVC", Einheit: "%", Aliase: []string{"fev3/fvc"}, Gruppe: "Lunge", Einheiten: prozent},
	{Name: "FEV6", Einheit: "l", Aliase: []string{"fev6", "fev 6"}, Gruppe: "Lunge", Einheiten: liter},
	{Name: "Peak-Flow", Einheit: "l/min", Aliase: []string{"peak-flow", "peak flow", "pef"}, Gruppe: "Lunge",
		Umrechnung: map[string]float64{"l/s": 60}, Einheiten: fluss},
	{Name: "FEFmax", Einheit: "l/s", Aliase: []string{"fefmax", "fef max"}, Gruppe: "Lunge", Einheiten: fluss},
	{Name: "FEF25", Einheit: "l/s", Aliase: []string{"fef25", "fef 25", "mef75", "mef 75"}, Gruppe: "Lunge", Einheiten: fluss},
	{Name: "FEF50", Einheit: "l/s", Aliase: []string{"fef50", "fef 50", "mef50", "mef 50"}, Gruppe: "Lunge", Einheiten: fluss},
	{Name: "FEF75", Einheit: "l/s", Aliase: []string{"fef75", "fef 75", "mef25", "mef 25"}, Gruppe: "Lunge", Einheiten: fluss},
	{Name: "FEF25-75", Einheit: "l/s", Aliase: []string{"fef25-75", "fef 25-75", "fef25-75%", "mmef", "mmef 75/25", "mef25-75"}, Gruppe: "Lunge", Einheiten: fluss},
	{Name: "FET", Einheit: "s", Aliase: []string{"fet", "exspirationszeit", "forcierte exspirationszeit"}, Gruppe: "Lunge", Einheiten: []string{"s", "sek", "sec"}},
	{Name: "FIVC", Einheit: "l", Aliase: []string{"fivc"}, Gruppe: "Lunge", Einheiten: liter},
	{Name: "FIV1", Einheit: "l", Aliase: []string{"fiv1", "fiv 1"}, Gruppe: "Lunge", Einheiten: liter},
	{Name: "FIV1/FIVC", Einheit: "%", Aliase: []string{"fiv1/fivc"}, Gruppe: "Lunge", Einheiten: prozent},
	{Name: "PIF", Einheit: "l/s", Aliase: []string{"pif", "peak inspiratory flow"}, Gruppe: "Lunge", Einheiten: fluss},
	{Name: "FIF", Einheit: "l/s", Aliase: []string{"fif"}, Gruppe: "Lunge", Einheiten: fluss},
	{Name: "FIF50", Einheit: "l/s", Aliase: []string{"fif50", "fif 50", "mif50", "mif 50"}, Gruppe: "Lunge", Einheiten: fluss},
	{Name: "MVV", Einheit: "l/min", Aliase: []string{"mvv", "mwv", "atemgrenzwert"}, Gruppe: "Lunge", Einheiten: []string{"l/min"}},
	{Name: "Lungenalter", Einheit: "Jahre", Aliase: []string{"ela", "lungenalter", "estimated lung age"}, Gruppe: "Lunge", Einheiten: []string{"Jahre", "J"}},
	{Name: "EVol", Einheit: "l", Aliase: []string{"evol", "bev", "rückextrapoliertes volumen", "extrapoliertes volumen"}, Gruppe: "Lunge", Einheiten: []string{"l", "ml", "%"}},
	{Name: "FeNO", Einheit: "ppb", Aliase: []string{"feno", "no-messung"}, Gruppe: "Lunge"},
	{Name: "SpO2", Einheit: "%", Aliase: []string{"spo2", "sauerstoffsättigung"}, Gruppe: "Lunge", Einheiten: prozent},
	{Name: "SpO2 in Ruhe", Einheit: "%", Aliase: []string{"spo2 in ruhe", "sauerstoffsättigung in ruhe"}, Gruppe: "Lunge", Einheiten: prozent},
	// Belastung (Ergospirometrie)
	{Name: "Maximale Belastung", Einheit: "W", Aliase: []string{"maximale belastung", "max. belastung", "wmax", "maximale leistung"}, Gruppe: "Belastung", Einheiten: watt},
	{Name: "Belastung an der aeroben Schwelle", Einheit: "W", Aliase: []string{"belastung aerobe schwelle", "leistung aerobe schwelle", "leistung an der aeroben schwelle"}, Gruppe: "Belastung", Einheiten: watt},
	{Name: "Belastung an der anaeroben Schwelle", Einheit: "W", Aliase: []string{"belastung anaerobe schwelle", "leistung anaerobe schwelle", "leistung an der anaeroben schwelle"}, Gruppe: "Belastung", Einheiten: watt},
	{Name: "Maximale Herzfrequenz", Einheit: "/min", Aliase: []string{"maximale herzfrequenz", "max. herzfrequenz", "hf max", "hfmax"}, Gruppe: "Belastung"},
	{Name: "Herzfrequenz an der aeroben Schwelle", Einheit: "/min", Aliase: []string{"herzfrequenz aerobe schwelle", "hf aerobe schwelle"}, Gruppe: "Belastung"},
	{Name: "Herzfrequenz an der anaeroben Schwelle", Einheit: "/min", Aliase: []string{"herzfrequenz anaerobe schwelle", "hf anaerobe schwelle"}, Gruppe: "Belastung"},
	{Name: "Maximale Sauerstoffaufnahme", Einheit: "ml/min/kg", Aliase: []string{"maximale sauerstoffaufnahme", "vo2max", "vo2 max", "vo2peak", "vo2 peak", "v'o2max", "v'o2peak", "peak vo2"}, Gruppe: "Belastung"},
	{Name: "Sauerstoffaufnahme an der aeroben Schwelle", Einheit: "ml/min/kg", Aliase: []string{"sauerstoffaufnahme aerobe schwelle"}, Gruppe: "Belastung"},
	{Name: "Sauerstoffaufnahme an der anaeroben Schwelle", Einheit: "ml/min/kg", Aliase: []string{"sauerstoffaufnahme anaerobe schwelle"}, Gruppe: "Belastung"},
	{Name: "Sauerstoffpuls", Einheit: "ml", Aliase: []string{"o2-puls", "o2 puls", "o2-puls maximal", "o2-puls max", "maximaler o2-puls", "maximaler sauerstoffpuls"}, Gruppe: "Belastung", Einheiten: []string{"ml", "ml/Schlag"}},
	{Name: "Maximaler RER", Aliase: []string{"maximaler rer", "rer max", "rer maximal"}, Gruppe: "Belastung"},
	{Name: "VE/VCO2-Slope", Aliase: []string{"V'E/V'CO2-Slope", "ve/vco2-slope", "ve/vco2 slope", "V'E/V'CO2 slope"}, Gruppe: "Belastung"},
	{Name: "PETCO2 in Ruhe", Einheit: "mmHg", Aliase: []string{"endtidalen pco2 in ruhe", "endtidaler pco2 in ruhe", "petco2 in ruhe"}, Gruppe: "Belastung", Einheiten: []string{"mmHg", "kPa"}},
	{Name: "Atemreserve", Einheit: "%", Aliase: []string{"atemreserve", "breathing reserve"}, Gruppe: "Belastung", Einheiten: prozent},
	{Name: "SpO2 bei Belastungsende", Einheit: "%", Aliase: []string{"spo2 bei abbruch", "spo2 bei belastungsende", "spo2 maximale belastung"}, Gruppe: "Belastung", Einheiten: prozent},
	// Kreislauf, Körper
	{Name: "Blutdruck systolisch", Einheit: "mmHg", Aliase: []string{"blutdruck systolisch", "rr systolisch", "systolisch", "sys", "systole"}, Gruppe: "Kreislauf"},
	{Name: "Blutdruck diastolisch", Einheit: "mmHg", Aliase: []string{"blutdruck diastolisch", "rr diastolisch", "diastolisch", "dia", "dias", "diastole"}, Gruppe: "Kreislauf"},
	{Name: "Puls", Einheit: "/min", Aliase: []string{"puls", "herzfrequenz", "hf"}, Gruppe: "Kreislauf"},
	{Name: "Gewicht", Einheit: "kg", Aliase: []string{"gewicht", "körpergewicht", "koerpergewicht"}, Gruppe: "Körper"},
	{Name: "BMI", Einheit: "kg/m²", Aliase: []string{"bmi", "body-mass-index"}, Gruppe: "Körper"},
}

// Gruppen is the order the page shows groups in; a group not here comes last.
var Gruppen = []string{"Blutbild", "Entzündung", "Infektionen", "Stoffwechsel", "Niere", "Leber", "Schilddrüse", "Gerinnung", "Vitamine", "Salze", "Lunge", "Belastung", "Kreislauf", "Körper", "Weitere"}

// the unit sets the lung-function values share
var (
	liter   = []string{"l", "ml"}
	fluss   = []string{"l/s", "l/min"}
	prozent = []string{"%"}
	watt    = []string{"W", "Watt"}
)

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

// --- units (#10: „Hepatitis B 0,03 cal“, „LUES Screen 4 ERS“, „SD-Volumen 30 com“)

// einheitNorm is a unit as compared: lower case, no spaces, µ one way, powers as digits.
func einheitNorm(u string) string {
	u = strings.ToLower(strings.TrimSpace(u))
	return strings.NewReplacer("μ", "µ", " ", "", "²", "2", "³", "3", "x10", "10", "*10", "10", "·", "").Replace(u)
}

// einheitAtome are the parts a unit of the household's reports is made of,
// between its slashes. A calorie is none of them: no lab or lung sheet
// gives one, and „cal“ was an OCR misread.
var einheitAtome = func() map[string]bool {
	m := map[string]bool{}
	for _, a := range strings.Fields(`g mg µg ug ng pg kg
		l dl ml µl ul nl pl fl cl ltr liter
		mol mmol µmol umol nmol pmol mval meq mosm mosmol osmol
		u iu ie e mu miu µu uu µiu uiu mie kiu ku ui
		s sec sek min minute minuten h std d tag tage woche jahr jahre j
		m cm mm µm m2 cm2 cm3 ccm
		mmhg cmh2o kpa hpa pa mbar °c
		w watt kcal kj
		schlag schläge schlaege atemzug atemzüge tsd zellen kopien gpt tpt`) {
		m[a] = true
	}
	return m
}()

var (
	einheitProzent = regexp.MustCompile(`^%[a-z0-9.]{0,12}$`)
	einheitZehner  = regexp.MustCompile(`^(10\^?\d{1,2}|1)$`)
	einheitFlaeche = regexp.MustCompile(`^\d+(?:[.,]\d+)?m2$`) // GFR's „ml/min/1,73 m²“
	einheitSonst   = map[string]bool{"‰": true, "ppb": true, "ppm": true, "index": true, "ratio": true, "titer": true, "s/co": true, "coi": true, "bpm": true}
)

// einheitForm is whether a unit has a unit's form: „mg/dl“, „10^3/µl“,
// „ml/min/kg“, „/min“, „%“. "" (a ratio, a score) counts as one.
func einheitForm(u string) bool {
	n := einheitNorm(u)
	if n == "" || einheitSonst[n] || einheitProzent.MatchString(n) {
		return true
	}
	teile := strings.Split(n, "/")
	if len(teile) > 3 {
		return false
	}
	for i, t := range teile {
		switch {
		case einheitAtome[t]:
		case i == 0 && (t == "" || einheitZehner.MatchString(t)) && len(teile) > 1:
		case i > 0 && einheitFlaeche.MatchString(t):
		default:
			return false
		}
	}
	return true
}

// einheitGrund is why a value's unit says „prüfen“, "" when it is plain:
// for a value given in few certain units, a unit outside them; for any
// other, a unit without a unit's form.
func einheitGrund(a *Analyt, u string) string {
	if strings.TrimSpace(u) == "" {
		return ""
	}
	n := einheitNorm(u)
	if a != nil && len(a.Einheiten) > 0 {
		erlaubt := append([]string{a.Einheit}, a.Einheiten...)
		for k := range a.Umrechnung {
			erlaubt = append(erlaubt, k)
		}
		for _, e := range erlaubt {
			en := einheitNorm(e)
			if en == n || (en == "%" && einheitProzent.MatchString(n)) {
				return ""
			}
		}
		return fmt.Sprintf("die Einheit „%s“ passt nicht, %s steht in %s", u, a.Name, strings.Join(a.Einheiten, " oder "))
	}
	if !einheitForm(u) {
		return fmt.Sprintf("„%s“ ist keine Einheit", u)
	}
	return ""
}

// aehnlich is the dictionary's name a name not in it differs from by one
// character in the same place, as OCR misreads it („FIVV/FIVC“ for
// FIV1/FIVC); "" when there is none. Short names are left out: „GOT“ and
// „GPT“ are both real.
func aehnlich(name string) string {
	f := []rune(falten(name))
	if len(f) < 4 {
		return ""
	}
	namen := map[string]bool{}
	for k, a := range aliasIndex {
		r := []rune(k)
		if len(r) != len(f) {
			continue
		}
		anders := 0
		for i := range r {
			if r[i] != f[i] {
				anders++
			}
		}
		if anders == 1 {
			namen[a.Name] = true
		}
	}
	var out []string
	for n := range namen {
		out = append(out, n)
	}
	sort.Strings(out)
	return strings.Join(out, " oder ")
}
