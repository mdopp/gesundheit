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
   anyway. A run first **sorts** (#3): Paperless only suggests a type, it
   never sets one, so a scanned record would never be read. The newest
   documents without a type that the record has not asked about yet, at
   most 20 a run, each go to the household model with their first 3000
   characters: a medical record, and which of the `BEFUND_ARTEN` types
   Paperless has? A yes sets that type on the document in Paperless
   (visible and correctable there); a no is kept and never asked again.
   A type the owner changes in Paperless wins: a record Gesundheit typed
   that is no longer one leaves the timeline, and typing it back reads it
   again. Then the run lists the documents whose type or tag is one of
   `BEFUND_ARTEN`, skips those in its record, and reads at most 10, oldest
   first. Each is one question to the household model (through the lease,
   `X-Laib-Background`), answered as JSON with the words every value stands
   on. **A value is kept only when its words are in the document's text**
   (`belegt`), its number is in those words, and it has a number's shape;
   its name is one the dictionary knows or a word of letters standing in
   those words (no „ulm!“ from letter salad, #5); a value with a date of its
   own (a sheet over several examinations gives each value the day of its
   examination) is kept only when that date stands in the text;
   else it is listed in the note on the document and the document gets the
   tag `prüfen`. **A page whose text is weak is read from its image** (#5,
   owner 2026-10-30): `guete` in `bild.go` weighs the OCR text (fewer than 12
   tokens or 6 words, under 70 % words among the tokens with letters, or most
   lines with a number carrying no word to name it). Such a document's text
   never yields a value. Its original (the scanner's JPEG/PNG, or the page
   pictures a scanner's PDF carries: JPEG, 8- or 1-bit Flate; at most 8 pages,
   2048 px) goes to `IMAGE_MODEL` (`gemma-4-12b`) through the lease: one lease
   for the run's weak documents (holder `gesundheit`, renewed while it reads,
   given back before the household model reads the transcripts; held by
   someone else, they wait for the next run). The image preset only
   transcribes, as Markdown (headings, tables, each examination under its
   date), never interprets; that transcript is the text the evidence rule
   checks, its values are marked „aus dem Bild gelesen“, and a value whose
   name the OCR text carries with another number gets `prüfen`. A page the
   preset cannot read, or a file without a picture, yields no value and the
   note says so. The note is one per document (`Gesundheit: …`, replaced on
   every reading, a person's own notes stay) and carries the transcript;
   the page links it behind the value (`/abschrift`). Units are converted only where `woerterbuch.go` names the
   exact factor; a name the dictionary does not know is kept as the report
   writes it and marked `neu`. A lung-function sheet's predicted value,
   % predicted and LLN are kept with the value when their numbers stand in
   its row (#10). A value says „prüfen“ when its unit is no unit („0,03 cal“)
   or not one its analyte is given in („MVV 149,2 min“), or when its name is
   one character off a known one („FIVV/FIVC“). Each record carries the
   reader version that read it (`leserVersion`); raising it reads every
   record again, after the new documents, at most 10 a run, keeping the
   person it belonged to and replacing its values; the pages say „noch N
   Befunde neu zu lesen“. Images are never interpreted: of an X-ray or
   MRT report only the written finding becomes an event.
2. **Assigns.** The patient's name on the sheet against the household's
   people: Paperless' users (first and last name) and `KINDER`. One match →
   the values go to that person and the document is given to them (owner;
   the program keeps its own view and change). No match → the document waits
   under „Für wen?“ on the page, with `prüfen`; a person assigns it to
   themselves or, as an admin, to a child.
3. **Shows** (#10, owner 2026-10-30: a small group of pages, the same nav on
   each; Haus links to these paths, keep them stable). `/` leads to `/werte`.
   Remote-User through Caddy's proxy secret (laib ADR 0012 §3); an admin
   (`Remote-Groups` has `ADMIN_GROUP`) also sees the children's pages, nobody
   another adult's.
   - **`/werte` „Werte“**: one row per value that reads at first sight: the
     latest value with its month („5,29 l · Dez. 2023“); its status in words
     against what *its* report prints — the range („im Normalbereich“ / „über
     dem Bereich“ / „unter dem Bereich“), else the lower limit of normal
     (LLN), else the predicted value as „84 % des Sollwerts“ (no boundary is
     invented), else a range computed from the person's profile, said as
     such, else „kein Bereich angegeben“; the change against the value of the
     day before in words („vorher 5,41 l (2021) – kaum verändert“; under 5 %
     is „kaum verändert“); a chart only from three days on, with its own
     time axis, the years and the report's band. „Was ist das?“ behind each
     row (`erklaerung.go`): a curated list of plain sentences, what the value
     measures, no assessment, one per dictionary analyte; a name not on it
     gets one sentence from the household model, once, marked, and an admin
     can write the household's own (`POST /erklaerung`, for everyone). The
     profile (`POST /profil`: Geburtsjahr, Geschlecht, Größe) is entered once,
     seen by the person (a child's by the admins) and used only where the
     report prints nothing; `sollFormeln` is empty on purpose — GLI-2012
     needs its lookup tables, an approximation would print wrong numbers.
   - **`/befunde` „Befunde“**: the person's documents, newest first: date,
     title, values read, how many places say „prüfen“, the link to
     Paperless and to the transcript (`/abschrift`), the events; „Für wen?“
     for the documents no one was matched to.
   - **`/befinden` „Wie es mir ging“**: today's note and the days.
   - **`/arzt` „Für den Arzt“**: printable; the values outside their range on
     their latest sheet, changes of more than 15 % or across the range, the
     values read with doubt — each a question with value, date and source
     document, no conclusion.
   `/export.csv`, `/loeschen` (the person's folder; the record stays, so the
   documents are not read again).
4. **Asks.** The daily note: four base questions (sleep, energy, mood 1–5;
   pain 0–10) and up to three of the person's own (`/fragen`), a free line.
   From the page (`/tag`) or from the Bote over MCP. From `FRAGE_UHRZEIT` on,
   everyone with a login who has not noted the day gets the question once on
   their own channel (bote-notify `/mitteilung`, `an` = the login); asked is
   asked, a day without an answer stays a gap. The Bote writes the answer
   through `tagesnotiz_eintragen` (its SOUL rule, laib#894).
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
| `IMAGE_MODEL` (template: `gemma-4-12b`), `MODEL_LEASE` | the preset that reads a weak page's image, and the lease it is taken through (`POST/DELETE /api/model-lease` with `MODEL_KEY`; empty: derived from `MODEL_URL`); `IMAGE_MODEL` empty: weak pages wait, the status says so |
| `BEFUND_ARTEN`, `KINDER` | what is a medical record; the people without a login |
| `WEBHOOK_TOKEN`, `BOTE_TOKEN` | the hook's `X-Gesundheit-Token`; the MCP's bearer |
| `NOTIFY_URL`, `NOTIFY_TOKEN`, `FRAGE_UHRZEIT` (`20:00`) | bote-notify's `/mitteilung` and its bearer: the daily question, once a day from that time for every login who has not noted the day (laib ADR 0022 §3); empty: nobody is asked |
| `DATA` (`/data`), `STATUS_FILE` | the store; `laib-status.json` for `laib status` |
| `LAUF_RUHE_S` (60), `LAUF_SPAETESTENS_S` (600), `LAUF_STUENDLICH_S` (3600) | the run's timing |

**Store** (`/data`): `personen/<key>/{verlauf,tage,fragen}.json`,
`personen/<key>/profil.json` (#10), `record.json` (what each document
yielded with its reader version, and the last run), `erklaerungen.json` (the
model's and the household's „Was ist das?“),
`laib-status.json`. Plain JSON, written whole and renamed into place; a
person's folder is theirs to export or delete.

**Paperless' side** (laib#894, paperless' post-deploy): user `gesundheit`
with view/change on documents, notes and tags, and `delete_note` (its one
note is replaced, Paperless cannot edit a note; without it the old note
stays, no second one is written, and the status says so — #5); the workflow's second
webhook action; the types and tags of `BEFUND_ARTEN`.

Tests: `go test ./...` — fakes for Paperless, the model and the lease, never a live box.
