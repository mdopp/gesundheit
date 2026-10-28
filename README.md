# gesundheit

Arztunterlagen aus Paperless als Verlauf pro Person, mit kurzer Tagesnotiz —
for the household on a Laib box ([laib#893](https://github.com/mdopp/laib/issues/893)).
The program is `apps/gesundheit/` (its README says what it does and its
contract with the template); `templates/gesundheit/` is the Laib template
that runs it. Made in the Werkstatt (`laib-app new gesundheit`).

- `apps/<name>/` — the program, its tests and its `Containerfile`.
- `templates/<name>/` — the Laib template: `laib.yml`, the Quadlet unit,
  `values.example.yml`.
- CI tests the app, publishes `ghcr.io/mdopp/gesundheit` from main, and
  lints the template.

The box knows this repo as a template registry `apps` (box.yml
`registries`, `laib-app register`); `laib template sync` pulls it, `laib
propose --add gesundheit` installs it as a proposal the owner approves.
Paperless' and the Bote's side of it live in laib's templates
([laib#894](https://github.com/mdopp/laib/issues/894)).
