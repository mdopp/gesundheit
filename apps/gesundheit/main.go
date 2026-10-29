// gesundheit: medical records from Paperless as a timeline per person, with
// a short daily note (laib#893). A Paperless client, not a store of
// documents: it reads lab sheets, reports and breath tests with the
// household model, keeps only values whose words are in the text, and draws
// them per person. Behind Authelia; the Bote reaches it over MCP.
package main

import (
	"context"
	"crypto/subtle"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// version is the program's, for the MCP handshake.
const version = "0.1.0"

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// eigeneSeite is where this program's page is reached from a phone: the
// value PUBLIC_URL, or, while a box has none yet, Paperless' public address
// with its first label swapped for ours (both are routes of the same box).
func eigeneSeite(public, paperless string) string {
	if public != "" {
		return strings.TrimRight(public, "/")
	}
	u, err := url.Parse(paperless)
	if err != nil || u.Host == "" {
		return ""
	}
	i := strings.IndexByte(u.Host, '.')
	if i <= 0 {
		return "" // a bare name has no domain to share
	}
	return u.Scheme + "://gesundheit" + u.Host[i:]
}

func main() {
	d := &Dienst{
		Paperless:   &Paperless{URL: env("PAPERLESS_URL", "http://host.containers.internal:8000"), Token: os.Getenv("PAPERLESS_TOKEN")},
		Modell:      &Modell{URL: strings.TrimRight(os.Getenv("MODEL_URL"), "/"), Key: os.Getenv("MODEL_KEY"), Name: os.Getenv("MODEL_NAME")},
		Speicher:    &Speicher{Dir: env("DATA", "/data")},
		Arten:       strings.Split(env("BEFUND_ARTEN", "Befund,Arztbrief,Labor,Laborbefund,Röntgen,Atemtest,Lungenfunktion,Arzt"), ","),
		Kinder:      os.Getenv("KINDER"),
		Ich:         env("PAPERLESS_USER", "gesundheit"),
		Public:      os.Getenv("PAPERLESS_PUBLIC_URL"),
		StatusFile:  env("STATUS_FILE", env("DATA", "/data")+"/laib-status.json"),
		NotifyURL:   os.Getenv("NOTIFY_URL"),
		NotifyToken: os.Getenv("NOTIFY_TOKEN"),
		FrageUm:     env("FRAGE_UHRZEIT", "20:00"),
		Seite:       eigeneSeite(os.Getenv("PUBLIC_URL"), os.Getenv("PAPERLESS_PUBLIC_URL")),
	}
	web := &Web{Dienst: d, Secret: os.Getenv("PROXY_SECRET"), Admins: env("ADMIN_GROUP", "admins")}

	// the inner side: Paperless' hook and the Bote's MCP, on the host for
	// the pods (host.containers.internal), never through Caddy
	innen := http.NewServeMux()
	hookToken := os.Getenv("WEBHOOK_TOKEN")
	innen.HandleFunc("POST /hook", func(w http.ResponseWriter, r *http.Request) {
		if hookToken == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Gesundheit-Token")), []byte(hookToken)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		d.Hook()
		w.WriteHeader(http.StatusAccepted)
	})
	innen.Handle("/mcp", &mcpServer{Token: os.Getenv("BOTE_TOKEN"), Tools: mcpTools(d)})
	innen.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.Status()
	go d.Takt(ctx, dauer("LAUF_RUHE_S", 60*time.Second), dauer("LAUF_SPAETESTENS_S", 10*time.Minute), dauer("LAUF_STUENDLICH_S", time.Hour))

	go func() {
		addr := env("LISTEN_INNEN", ":8081")
		log.Printf("gesundheit: Hook und MCP auf %s", addr)
		log.Fatal(http.ListenAndServe(addr, innen))
	}()
	addr := env("LISTEN", ":8080")
	log.Printf("gesundheit: Seiten auf %s", addr)
	log.Fatal(http.ListenAndServe(addr, web.Handler()))
}
