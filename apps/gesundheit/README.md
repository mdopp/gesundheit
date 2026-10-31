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
   each, five since #17; Haus links to these paths, keep them stable). `/` leads to `/werte`.
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
   - **`/arzt` „Für den Arzt“** (#13, owner 2026-10-31: „fass es kürzer …
     als Werte zwischen zwei Tests“): printable, one sheet. Values grouped by
     examination (`untersuchungen` in `arzt_kurz.go`: lung function,
     spiroergometry, blood pressure, blood count, thyroid, serology …; a lab
     value by its dictionary group, any other by what its document examined:
     its title's words, else most of its values — a spiroergometry's resting
     SpO2 stays with it). Per group the two latest examination days compared:
     at most 5 values, across the range first, then changes over 15 %, then
     the group's leading values; a day with several values of one name (a
     lung-function day: several sheets, a value per attempt) gives the middle
     one in the unit both days share, values read with doubt only when there
     is no other; a value in two units is said, not compared; a value only on
     the later day that stands outside its report's range is listed. Then one
     or two neutral questions on the whole („FEV1 und FVC sind beide gesunken
     – was bedeutet das zusammen?“), checked against `urteil` (the
     explanations' guard, extended: no direction that judges, no alarm, no
     therapy). Groups examined once and values of no group: „Außerdem“, at
     most 6 lines. `/arzt?alle=1` „alle Einzelwerte“ is the long list of #10:
     the values outside their range on their latest sheet, changes of more
     than 15 % or across the range, the values read with doubt — each a
     question with value, date and source document, no conclusion.
   - **`/gesamtbild` „Gesamtbild“** (#17, owner 2026-10-31: „Und wie kriege
     ich jetzt eine Einschätzung über meinen Gesamtzustand?“): per body system
     (Lunge und Atmung, Belastbarkeit, Blutbild, Schilddrüse, Herz-Kreislauf
     und Blutdruck, Leber/Niere/Stoffwechsel, Infektionen und Abwehr; only
     systems with values) the course in words (first to last examination day,
     the day's middle value, „kaum verändert“ under 5 %), what moves together
     (curated sets: FEV1+FVC+FEV1/FVC, Hb+Hkt+Ery+MCV/MCH, TSH+fT4+fT3 …),
     what such patterns can mean in general, and questions for the doctor.
     The meanings come only from **`gesamtbild_regeln.json`**, one reviewable
     data file: per rule the system, the pattern (values, directions,
     thresholds), a plain German text phrased as a general possibility (never
     about the person, no diagnosis, no advice; checked by `urteil` and a
     guard against „du/dein“), the doctor's question and its source (ATS/ERS
     2005 and 2022, GOLD, ATS/ACCP 2003, ERS 2019, EACPR/AHA 2012, WHO 2011/2024,
     DGHO, BSH, DEGAM/AWMF, AACE/ATA, ETA, ESH 2023, DDG/NVL, ADA, KDIGO 2024,
     WHO/STIKO). A state is judged on one examination day — the latest of the
     rule's first value, every value of that day agreeing — a trend on the
     first and last day the values share; ranges are always the report's own.
     The profile's sex and birth year pick a rule only where the source is by
     sex and age (WHO's anaemia limits) and add an age's sentence (the fixed
     70 % ratio, isolated systolic hypertension). **Only values read without
     doubt, confirmed or corrected count**: „prüfen“, an unconfirmed
     „vermutlich“, and a number no body can measure (the table's
     `plausibel`, e.g. FEV1/FVC 0 %, a Peak-Flow of 4620 l/min) are left out
     and listed per system as „N Werte noch zu bestätigen“ with a link — a
     misread never becomes a pattern. „Für den Arzt“ gets one question per
     system a rule fits, at most four lines, under „Aus dem Gesamtbild“.
   - **Correcting a value** (#13, owner 2026-10-31: „warum gibst du mir
     nicht die Gelegenheit, den Wert zu korrigieren? Evtl. sogar mit
     Bildschnipsel daneben“): every row of `/werte` (its latest value) and
     every value of a document on `/befunde` has „Korrigieren“ (opened by
     itself where a document still says „prüfen“): value, unit, date; „Stimmt
     so“; „Gehört nicht dazu“; „Wie gelesen“/„zurücknehmen“ takes it back.
     `POST /korrektur`, by the person, an admin for a child (the pages' own
     rule, `wer`). The word is kept in the person's `korrekturen.json`, apart
     from what the reader writes, and laid over the timeline whenever it is
     read (`Speicher.Verlauf`): a reader of a later `leserVersion` replaces
     its values and never the person's. A value is named by its document,
     name, day and place among that name's values on the sheet; a re-read
     that names it otherwise finds it by document, day, number and unit, and
     a re-read that lost it keeps the person's value while the document is
     the person's. Shown as „von dir korrigiert“ („von ben …“ to another
     admin) with what was read; the Bote's `gesundheit_verlauf` says „von
     Hand korrigiert“; a dropped value is on no page but its document's list.
     Next to the form, loaded when it is opened: **the snippet** (`GET
     /schnipsel`, `schnipsel.go`, `pdf.go`) — Paperless' archived PDF
     (OCRmyPDF's: the scan plus its invisible text layer), read in Go
     (no poppler in the distroless image; object streams, form XObjects,
     Type0/Identity-H and simple fonts, ToUnicode, Flate): the number's word
     with the value's name on the same line (a sheet carries a number more
     than once), the reader's words (`beleg`, kept since #13) making a line
     count more; the page image's band around it, the page's full width, two
     and a half lines of margin, the words framed. No position (a born-digital
     page without a picture, JBIG2, a converted unit, a first reader's
     record): Paperless' thumbnail. Beside it always the words the value was
     read from, its number marked. Fetched for the person only, sent
     `private, no-store`, never written to `/data` or anywhere else.
     The form names the value too (#15).
   - **Assuming an obvious misread** (#15, owner 2026-10-31: „Kannst du
     wahrscheinliche Werte annehmen und um Korrektur bitten?“,
     `vermutung.go`): on a value nobody has spoken on, a name the dictionary
     does not know is taken as the one dictionary name that fits — a
     spelling variant (spaces, hyphens, „rn“ for „m“), an OCR confusion
     („0“/„o“, „1“/„l“), or a letter or two misread (only letters: a digit or
     a % is part of the value's name; short names only by the confusions) —
     and a unit that is no unit, or not one the value is given in, as the one
     unit the dictionary names for it. Two candidates or none: no assumption,
     „prüfen“ as before. Never a number, never a unit conversion. Applied
     when the timeline is read, never stored: the value counts under the
     assumed name on `/werte` and „Für den Arzt“, marked „vermutlich
     Hämoglobin – bitte bestätigen“ with „Ja“ (`aktion=annehmen`: it becomes
     the person's correction, number as read) and „Korrigieren“ (the form);
     „Für den Arzt“ says only „N Werte vermutlich richtig gelesen, bitte
     bestätigen“ with a link. A name the dictionary knows in an older
     reader's spelling („SYS“) stands under the dictionary's name.
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
`personen/<key>/profil.json` (#10), `personen/<key>/korrekturen.json` (#13:
the person's word on their values, never written by the reader), `record.json` (what each document
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
