package main

import (
	"strings"
	"testing"
)

// The daily question leads with the page's form (owner 2026-10-29) and
// keeps the one-line scales for an answer in the chat; without a public
// address only the scales go.
func TestFrageTextLinksTheForm(t *testing.T) {
	fragen := []Frage{{Text: "Wie hast du geschlafen?", Min: 1, Max: 5}, {Text: "Hattest du Schmerzen (0 = keine)?", Min: 0, Max: 10}}
	got := frageText(fragen, "https://gesundheit.example.org")
	for _, want := range []string{"Wie war dein Tag? https://gesundheit.example.org/ – vier kurze Fragen", "Wie hast du geschlafen (1–5) · Hattest du Schmerzen (0–10), in dieser Reihenfolge."} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
	ohne := frageText(fragen, "")
	if !strings.HasPrefix(ohne, "Kurz zu deinem Tag: Wie hast du geschlafen (1–5) · Hattest du Schmerzen (0–10). Antworte mir") || strings.Contains(ohne, "http") {
		t.Errorf("without an address: %s", ohne)
	}
}

func TestEigeneSeite(t *testing.T) {
	for _, c := range [][3]string{
		{"https://gesundheit.dopp.cloud/", "https://paperless.dopp.cloud", "https://gesundheit.dopp.cloud"},
		{"", "https://paperless.dopp.cloud", "https://gesundheit.dopp.cloud"},
		{"", "http://paperless", ""},
		{"", "", ""},
	} {
		if got := eigeneSeite(c[0], c[1]); got != c[2] {
			t.Errorf("eigeneSeite(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}
