package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"
)

// Modell is the household model through the lease's proxy (laib ADR 0011),
// asked as background work: X-Laib-Background lets the proxy hold the
// request while a person's session has the card, instead of evicting it.
// While it does not answer, the run stops and the next one asks again.
type Modell struct {
	URL, Key, Name string
	HTTP           *http.Client
}

// ErrModellWeg is the model not answering: a passing reason, never a drop.
var ErrModellWeg = errors.New("das Modell antwortet gerade nicht")

var objektRe = regexp.MustCompile(`(?s)\{.*\}`)

// Frage asks for one document's data.
func (m *Modell) Frage(ctx context.Context, text string) (Antwort, error) {
	r := []rune(text)
	if len(r) > zeichen {
		text = string(r[:zeichen])
	}
	var a Antwort
	content, err := m.fragen(ctx, frageSystem, "Text:\n"+text, 4096)
	if err != nil {
		return a, err
	}
	if obj := objektRe.FindString(content); obj != "" {
		if err := json.Unmarshal([]byte(obj), &a); err != nil {
			return Antwort{}, fmt.Errorf("Antwort ist kein JSON: %v", err)
		}
	}
	return a, nil
}

// fragen sends one system line and one text, and answers the model's JSON as it came.
func (m *Modell) fragen(ctx context.Context, system, user string, maxTokens int) (string, error) {
	if m.URL == "" {
		return "", ErrModellWeg
	}
	body := map[string]any{
		"model":                m.Name,
		"messages":             []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": user}},
		"temperature":          0.1,
		"max_tokens":           maxTokens,
		"response_format":      map[string]string{"type": "json_object"},
		"chat_template_kwargs": map[string]bool{"enable_thinking": false},
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", m.URL+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.Key)
	req.Header.Set("X-Laib-Background", "gesundheit")
	c := m.HTTP
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Minute}
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrModellWeg, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("%w (%d)", ErrModellWeg, resp.StatusCode)
	}
	var out struct {
		Choices []struct {
			Message struct{ Content string } `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(data, &out) != nil || len(out.Choices) == 0 {
		return "", fmt.Errorf("%w: keine Antwort", ErrModellWeg)
	}
	return out.Choices[0].Message.Content, nil
}
