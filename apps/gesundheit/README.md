# gesundheit

Deine Blutwerte, Befunde und wie es dir ging, als Verlauf — Zahlen aus den
Unterlagen, keine ärztliche Einschätzung.

Concept and decisions: [mdopp/laib#893](https://github.com/mdopp/laib/issues/893).
The program is a **Paperless client** (laib ADR 0017): Paperless keeps the
documents, this keeps what it read out of them, per person.

## What it does

1. **Reads.** Paperless' workflow „Dokument hinzugefügt“ posts to `/hook`
   (debounced: a run once the hooks are quiet for `LAUF_RUHE_S`, at the latest
   after `LAUF_SPAETESTENS_S`), and every `LAUF_STUENDLICH_S` a run starts
   anyway. A run lists the documents whose type or tag is one of
   `BEFUND_ARTEN`, skips those in its record, and reads at most 10, oldest
   first. Each is one question to the household model (through the lease,
   `X-Laib-Background`), answered as JSON with the words every value stands
   on. **A value is kept only when its words are in the document's text**
   (`belegt`), its number is in those words, and it has a number's shape;
   else it is listed in the note on the document and the document gets the
   tag `prüfen`. Units are converted only where `woerterbuch.go` names the
   exact factor; a name the dictionary does not know is kept as the report
   writes it and marked `neu`. Images are never interpreted: of an X-ray or
   MRT report only the written finding becomes an event.
2. **Assigns.** The patient's name on the sheet against the household's
   people: Paperless' users (first and last name) and `KINDER`. One match →
   the values go to that person and the document is given to them (owner;
   the program keeps its own view and change). No match → the document waits
   under „Für wen?“ on the page, with `prüfen`; a person assigns it to
   themselves or, as an admin, to a child.
3. **Shows.** `/` is the viewer's own timeline (Remote-User through Caddy's
   proxy secret, laib ADR 0012 §3): what changed last, one row per value
   with a sparkline and the laboratory's reference band *of that report*,
   the events, the daily notes on the same axis. An admin (`Remote-Groups`
   has `ADMIN_GROUP`) also sees the children's; nobody sees another adult's.
   `/export.csv`, `/loeschen` (the person's folder; the record stays, so the
   documents are not read again).
4. **Asks.** The daily note: four base questions (sleep, energy, mood 1–5;
   pain 0–10) and up to three of the person's own (`/fragen`), a free line.
   From the page (`/tag`) or from the Bote over MCP.
5. **MCP** on the inner port (`/mcp`, bearer `BOTE_TOKEN`): `fragen`,
   `tagesnotiz_eintragen`, `gesundheit_verlauf`; a person is named by login,
   a child has no channel and so no tool.

## Contract with laib's template

| Env | |
|---|---|
| `LISTEN` (`:8080`), `LISTEN_INNEN` (`:8081`) | the pages (loopback, Caddy); the hook and MCP (the host, for the pods) |
| `PROXY_SECRET`, `ADMIN_GROUP` (`admins`) | Caddy's `X-Gesundheit-Proxy`; the group that sees the children |
| `PAPERLESS_URL`, `PAPERLESS_TOKEN`, `PAPERLESS_USER` (`gesundheit`), `PAPERLESS_PUBLIC_URL` | the API; the user's token (empty: no reading, the status says so); its own name; the address for links |
| `MODEL_URL`, `MODEL_KEY`, `MODEL_NAME` | the lease's proxy, OpenAI-shaped `/chat/completions` |
| `BEFUND_ARTEN`, `KINDER` | what is a medical record; the people without a login |
| `WEBHOOK_TOKEN`, `BOTE_TOKEN` | the hook's `X-Gesundheit-Token`; the MCP's bearer |
| `DATA` (`/data`), `STATUS_FILE` | the store; `laib-status.json` for `laib status` |
| `LAUF_RUHE_S` (60), `LAUF_SPAETESTENS_S` (600), `LAUF_STUENDLICH_S` (3600) | the run's timing |

**Store** (`/data`): `personen/<key>/{verlauf,tage,fragen}.json`,
`record.json` (what each document yielded, and the last run),
`laib-status.json`. Plain JSON, written whole and renamed into place; a
person's folder is theirs to export or delete.

**Paperless' side** (laib#894, paperless' post-deploy): user `gesundheit`
with view/change on documents, notes and tags; the workflow's second
webhook action; the types and tags of `BEFUND_ARTEN`.

Tests: `go test ./...` — fakes for Paperless and the model, never a live box.
