package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// The Bote's way in (laib ADR 0016, 0022): the same small MCP server Haus's
// Haushalt speaks, JSON-RPC over POST with initialize, tools/list and
// tools/call, one bearer. Three tools: the day's note, a person's course of
// one value, and the questions to ask. The Bote names the person by login;
// a tool never shows one person another's data.

type mcpTool struct {
	Name, Description string
	Schema            map[string]any
	Call              func(ctx context.Context, args map[string]any) (string, error)
}

type mcpServer struct {
	Token string
	Tools []mcpTool
}

type mcpRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func mcpReply(w http.ResponseWriter, id json.RawMessage, result any, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	m := map[string]any{"jsonrpc": "2.0", "id": id}
	if msg != "" {
		m["error"] = map[string]any{"code": code, "message": msg}
	} else {
		m["result"] = result
	}
	json.NewEncoder(w).Encode(m)
}

const mcpInstructions = "Gesundheit hält pro Person Werte aus Arztunterlagen und eine kurze Tagesnotiz. Die Zahlen stammen aus Befunden; sie sind keine ärztliche Einschätzung, und du gibst keine. Frag die Person einmal am Tag nach ihrer Tagesnotiz (fragen), trag die Antworten ein (tagesnotiz_eintragen) und beantworte Fragen zum Verlauf mit gesundheit_verlauf. Jede Person sieht nur ihren eigenen Verlauf: frag nie für jemand anderen."

func (s *mcpServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || s.Token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(s.Token)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var req mcpRequest
	if err := json.Unmarshal(body, &req); err != nil {
		mcpReply(w, nil, nil, -32700, "parse error")
		return
	}
	if strings.HasPrefix(req.Method, "notifications/") {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	switch req.Method {
	case "initialize":
		mcpReply(w, req.ID, map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{"name": "gesundheit", "version": version}, "instructions": mcpInstructions}, 0, "")
	case "ping":
		mcpReply(w, req.ID, map[string]any{}, 0, "")
	case "tools/list":
		tools := []map[string]any{}
		for _, t := range s.Tools {
			tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.Schema})
		}
		mcpReply(w, req.ID, map[string]any{"tools": tools}, 0, "")
	case "tools/call":
		var p struct {
			Name string         `json:"name"`
			Args map[string]any `json:"arguments"`
		}
		json.Unmarshal(req.Params, &p)
		for _, t := range s.Tools {
			if t.Name != p.Name {
				continue
			}
			if p.Args == nil {
				p.Args = map[string]any{}
			}
			out, err := t.Call(r.Context(), p.Args)
			isErr := err != nil
			if isErr {
				out = err.Error()
			}
			mcpReply(w, req.ID, map[string]any{"content": []map[string]string{{"type": "text", "text": out}}, "isError": isErr}, 0, "")
			return
		}
		mcpReply(w, req.ID, nil, -32602, "unknown tool: "+p.Name)
	default:
		mcpReply(w, req.ID, nil, -32601, "method not found: "+req.Method)
	}
}

func str(args map[string]any, k string) string {
	s, _ := args[k].(string)
	return strings.TrimSpace(s)
}

func schema(props map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": props, "required": required}
}

// mcpTools are the Bote's three tools over the service.
func mcpTools(d *Dienst) []mcpTool {
	person := func(ctx context.Context, args map[string]any) (*Person, error) {
		login := str(args, "person")
		if login == "" {
			return nil, fmt.Errorf("person fehlt: der Login der Person, für die du fragst")
		}
		for _, p := range d.Personen(ctx) {
			if p.Login == login {
				return &p, nil
			}
		}
		return nil, fmt.Errorf("„%s“ hat keinen Verlauf: nur Personen mit Login, über ihren eigenen Kanal", login)
	}
	return []mcpTool{
		{Name: "fragen", Description: "Die Fragen der Tagesnotiz einer Person: Schlüssel, Text und Skala. Stell sie der Person einmal am Tag, kurz, auf ihrem eigenen Kanal.",
			Schema: schema(map[string]any{"person": map[string]any{"type": "string", "description": "der Login der Person"}}, "person"),
			Call: func(ctx context.Context, args map[string]any) (string, error) {
				p, err := person(ctx, args)
				if err != nil {
					return "", err
				}
				fragen, err := d.Speicher.Fragen(p.Key)
				if err != nil {
					return "", err
				}
				var b strings.Builder
				for _, f := range fragen {
					fmt.Fprintf(&b, "%s: %s (%d–%d)\n", f.Key, f.Text, f.Min, f.Max)
				}
				b.WriteString("text: eine freie Zeile, wie der Tag war (optional)\n")
				return b.String(), nil
			}},
		{Name: "tagesnotiz_eintragen", Description: "Die Tagesnotiz einer Person eintragen: die Antworten als Zahlen je Schlüssel (siehe fragen) und eine freie Zeile. Ohne datum ist es heute; ein früherer Tag wird als nachgetragen vermerkt. Antwortet mit dem, was gespeichert wurde.",
			Schema: schema(map[string]any{
				"person":    map[string]any{"type": "string", "description": "der Login der Person"},
				"antworten": map[string]any{"type": "object", "description": "Schlüssel → Zahl, z. B. {\"schlaf\": 4, \"schmerz\": 0}", "additionalProperties": map[string]any{"type": "integer"}},
				"text":      map[string]any{"type": "string", "description": "die freie Zeile"},
				"datum":     map[string]any{"type": "string", "description": "JJJJ-MM-TT, leer für heute"},
			}, "person", "antworten"),
			Call: func(ctx context.Context, args map[string]any) (string, error) {
				p, err := person(ctx, args)
				if err != nil {
					return "", err
				}
				raw, _ := args["antworten"].(map[string]any)
				antworten := map[string]int{}
				for k, v := range raw {
					if f, ok := v.(float64); ok {
						antworten[k] = int(f)
					}
				}
				n, err := tagesnotiz(d, p.Key, str(args, "datum"), antworten, str(args, "text"), "bote")
				if err != nil {
					return "", err
				}
				return "Gespeichert für " + n.Datum + ": " + notizZeile(n), nil
			}},
		{Name: "gesundheit_verlauf", Description: "Der Verlauf eines Werts einer Person (z. B. „Ferritin“, „LDL“, „FEV1“) oder, ohne wert, die Namen der Werte, die es für sie gibt. Zahlen aus Befunden mit Datum, Einheit und dem Referenzbereich des Labors — keine Einschätzung.",
			Schema: schema(map[string]any{
				"person": map[string]any{"type": "string", "description": "der Login der Person"},
				"wert":   map[string]any{"type": "string", "description": "der Name des Werts; leer: alle Namen"},
				"seit":   map[string]any{"type": "string", "description": "JJJJ-MM-TT, nur Punkte ab diesem Tag"},
			}, "person"),
			Call: func(ctx context.Context, args map[string]any) (string, error) {
				p, err := person(ctx, args)
				if err != nil {
					return "", err
				}
				v, err := d.Speicher.Verlauf(p.Key)
				if err != nil {
					return "", err
				}
				wert, seit := str(args, "wert"), tag(str(args, "seit"))
				if wert == "" {
					namen := map[string]int{}
					for _, m := range v.Werte {
						namen[m.Name]++
					}
					var keys []string
					for k := range namen {
						keys = append(keys, k)
					}
					sort.Strings(keys)
					var b strings.Builder
					for _, k := range keys {
						fmt.Fprintf(&b, "%s (%d Punkte)\n", k, namen[k])
					}
					if b.Len() == 0 {
						return "Noch keine Werte.", nil
					}
					return b.String(), nil
				}
				var b strings.Builder
				for _, m := range v.Werte {
					if !strings.EqualFold(m.Name, wert) && (finde(wert) == nil || finde(wert).Name != m.Name) {
						continue
					}
					if seit != "" && m.Datum < seit {
						continue
					}
					fmt.Fprintf(&b, "%s: %s %s", m.Datum, zahlText(m.Wert), m.Einheit)
					if m.RefMin != nil || m.RefMax != nil {
						fmt.Fprintf(&b, " (Referenz %s)", refText(m.RefMin, m.RefMax))
					}
					if m.LLN != nil {
						fmt.Fprintf(&b, " (LLN %s)", zahlText(*m.LLN))
					}
					if s := sollText(m); s != "" {
						b.WriteString(" (" + s + ")")
					}
					if m.Pruefen {
						b.WriteString(" (unsicher gelesen, prüfen)")
					}
					if m.Korrektur == "korrigiert" {
						b.WriteString(" (von Hand korrigiert)")
					}
					if m.Vermutung != nil {
						b.WriteString(" (" + gelesenWie(m) + ", " + vermutungText(m) + ")")
					}
					b.WriteString("\n")
				}
				if b.Len() == 0 {
					return "Keine Punkte für „" + wert + "“.", nil
				}
				return b.String() + "Zahlen aus Befunden, keine ärztliche Einschätzung.", nil
			}},
	}
}

// tagesnotiz writes one day's note after checking the answers against the
// person's questions.
func tagesnotiz(d *Dienst, key, datum string, antworten map[string]int, text, von string) (Tagesnotiz, error) {
	heute := d.now().Format("2006-01-02")
	if datum == "" {
		datum = heute
	} else if datum = tag(datum); datum == "" {
		return Tagesnotiz{}, fmt.Errorf("datum: bitte als JJJJ-MM-TT")
	}
	if datum > heute {
		return Tagesnotiz{}, fmt.Errorf("ein Tag in der Zukunft bekommt keine Notiz")
	}
	fragen, err := d.Speicher.Fragen(key)
	if err != nil {
		return Tagesnotiz{}, err
	}
	ok := map[string]Frage{}
	for _, f := range fragen {
		ok[f.Key] = f
	}
	clean := map[string]int{}
	for k, v := range antworten {
		f, known := ok[k]
		if !known {
			return Tagesnotiz{}, fmt.Errorf("„%s“ ist keine Frage dieser Person", k)
		}
		if v < f.Min || v > f.Max {
			return Tagesnotiz{}, fmt.Errorf("%s: %d liegt nicht zwischen %d und %d", f.Text, v, f.Min, f.Max)
		}
		clean[k] = v
	}
	if len(clean) == 0 && strings.TrimSpace(text) == "" {
		return Tagesnotiz{}, fmt.Errorf("keine Antwort und kein Text: nichts zu speichern")
	}
	n := Tagesnotiz{Datum: datum, Antworten: clean, Text: kurz(strings.TrimSpace(text), 500), Nachgetragen: datum != heute, At: d.now(), Von: von}
	return n, d.Speicher.TagEintragen(key, n)
}

func notizZeile(n Tagesnotiz) string {
	var keys []string
	for k := range n.Antworten {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, n.Antworten[k]))
	}
	if n.Text != "" {
		parts = append(parts, "„"+n.Text+"“")
	}
	return strings.Join(parts, ", ")
}
