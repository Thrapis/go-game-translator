# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A Go CLI that machine-translates a game's localization files. One file at a time
runs through: `extract -> analyze markup -> translate text -> render -> compose`,
so engine control symbols are preserved and only human-readable text is sent to a
translator. Everything about a run lives in a YAML config; nothing is compiled in.

## Commands

```bash
go build ./...
go vet ./...
go test ./...
go test ./internal/pipeline/ -run TestPipelineMaskerPath -v   # single test
go test ./internal/pipeline/ -race                             # concurrency-sensitive packages: pipeline, translate
gofmt -l .                                                     # must print nothing; CI-style formatting check

# run a translation (config path is required; there are no other flags)
go run ./cmd/gametranslator -config config/tcoaal.ru-be.yaml

# inspection helper: split a combined TCOAAL dialogue.csv into per-section files
go run ./tools/csvsplit -in "path/to/dialogue.csv"

# Cyberpunk 2077: WolvenKit JSON tree <-> Crowdin CSV tree (translator runs on the CSV)
go run ./tools/cp77loc export -in <raw> -out <flat-ru>
go run ./tools/cp77loc xliff -in <flat-be> -out <xliff-be>   # XLIFF 1.2 for human review
go run ./tools/cp77loc import -template <raw> -in <flat-be|xliff-be> -out <be>
go run ./tools/cp77loc ref -in <raw> -ref-lang-dir en-us -out <xliff-ru-en>   # official loc as reference XLIFF
go run ./tools/cp77loc verify -in <raw>      # every string re-encoded must be byte-identical
```

Real `config/*.yaml` files are gitignored; only `config/*.example.yaml` templates
are checked in. Copy an example, edit paths, drop `.example`. `.vscode/launch.json`
has a ready debug target pointing at `config/tcoaal.ru-be.yaml`.

Test suites need no network or server: the Lingvanex client tests use an
`httptest` stub, translator tests use echo doubles.

## Architecture

### Config-driven, plugin registries

`main.go` only parses `-config`, wires a signal context, starts/stops the
Lingvanex server, and calls `app.New(cfg).Run(ctx)`. Behaviour must come from
config, not code changes.

Games, extract formats, and translation backends are each a **string-keyed
registry populated from `func init()`**. `internal/plugins` blank-imports every
implementation so the inits run; `main.go` blank-imports `internal/plugins`.
To add an implementation you register it in its own `init` and add one line to
`internal/plugins/plugins.go` — nothing else enumerates the full list.

`internal/config` is data + structural validation only. Semantic checks that need
the registries (is `game` known? does the format exist? does the game support a
requested capability?) live in `internal/app`.

### The three registries and their interfaces

| Package | Interface | Registry key = config field |
|---|---|---|
| `internal/game` | `game.Game` (`Name`, `Analyzer`, `DefaultFormat`, `DefaultDelimiter`) | `game` |
| `internal/extract` | `extract.Format` = `Extractor` + `Composer` | `source.format` (empty => game's `DefaultFormat`) |
| `internal/translate` | `translate.Translator` (`Translate`); optional `BatchTranslator` | `translators` (ordered fallback `Chain`) |

**Optional capability interfaces** — a game implements them only if applicable, and
`app.New` type-asserts (`g.(game.ReplicaGrouper)`) rather than requiring them:

- `game.ReplicaGrouper.SameReplica(a, b)` — consecutive rows that form one logical
  line, translated as a unit (`multiRowReplicas` mode). tcoaal requires
  `a.Tag != "" && a.Tag == b.Tag`.
- `game.Parasitizer.Replica(file, tag)` — pull an existing human translation from
  an external file by row tag instead of machine-translating.
- `game.Verbatimer.Verbatim(tag)` — rows that must **never** be machine-translated
  (localization identity: language name, font, credits). Filled only from a
  `carryOver` file; unmatched => keep source value.
- `markup.Masker.Mask(ps) -> (masked, markers)` — see "whole-string translation".

### markup: Analyzer contract

`markup.Analyzer` (one per game engine) tokenizes a raw string into a
`PartialString` of ordered parts. **Invariant: `Render(Analyze(s)) == s`**, and
mutating only `.Value` on the parts from `Translatable()` substitutes translated
text while keeping every control symbol.

Two translation strategies, chosen by whether the Analyzer also implements
`markup.Masker`:

- **Fragment (no Masker)** — each translatable part is sent to the translator
  alone. taleworld / titanquest use this (no real corpus to validate a change).
- **Whole-string (Masker)** — markup parts become `§0§ §1§ …` sentinels, the
  whole line is translated in one request, then `markup.Unmask` splices markup
  back. tcoaal and cyberpunk2077 use this. Sentinels are `§N§` specifically because they survive
  the ru→be CTranslate2 model (~98.5%); `` / `⟦⟧` do not. When sentinels
  come back corrupted (`markup.SentinelsIntact` is false) the pipeline falls back
  to per-fragment translation for that string.

`markup.Segmenter` (optional, Masker path only) splits a string at hard line
breaks. Each segment is translated as its own batch item and then joined back
with the original separators. cyberpunk2077 uses it because a single
onscreens string can be 71k chars, far past the model's decoding limit. A
segment boundary must never fall inside markup.

### extract: section-aware formats and `Settings.Extra`

A format that is more than flat `key<delim>value` (the TCOAAL combined
`dialogue.csv` / `dialogue.txt`) keeps the **whole document skeleton** in
`extract.Settings.Extra` (a format-private `*Doc`) and returns only translatable
cells as `[]DataLine`. `Compose` type-asserts `Extra` back to its `*Doc`, splices
the translated values into the skeleton, and re-serialises — so every
non-translated byte round-trips exactly. `DataLine.Tag` carries the grouping key
(dialogue `#id`; `LABELS/<key>`, `MENUS/<key>`, `FONT/<key>`, `CREDITS/<n>`;
bare `LANGUAGE`).

Formats: `delimited` (generic), `crowdin-csv`, `tcoaal-csv`, `tcoaal-txt`. For
TCOAAL, TXT is preferred for automation. `[VERSION]` always passes through from
the source file.

### Cyberpunk 2077: a conversion step outside the pipeline

WolvenKit JSON (`*.json.json`) is never an extract format. `tools/cp77loc`
flattens it to `crowdin-csv` (`id,source,translation,context`; id =
`primaryKey`/`stringId`, the male variant is `id@male`). This CSV is what the
pipeline and Crowdin both read and write. `[en_us]…` VO placeholders and
glitch text are never exported. Glitch text is deliberately corrupted Zalgo
stored as Latin-1 mojibake, detected by U+0080–U+009F characters. The analyzer
also marks both kinds verbatim (`cyberpunk2077.Untranslatable`), as a safety
net. `import` goes back through
`internal/wolvenkit.Splice`, which rewrites **only** the variant string literals
in the original bytes. It asserts that each literal matches the parsed entry,
and `Quote` encodes new text exactly like System.Text.Json: ASCII only, with
uppercase `\uXXXX` for non-ASCII and for `" & ' + < > \``. Don't
re-serialise the JSON, because WolvenKit exports are CRLF, have no trailing
newline, and contain empty keys that `omitempty` would drop.

The cyberpunk2077 analyzer's `symbolRe` whitelist (the characters sent to the
model) is empirical, taken from the real ru→be run. Don't widen it without
re-measuring survival on real output. A first-letter case fix must run on
*masked* text (`caseFirstLetter` before `Unmask`), or a leading `<Rich>` or
`{VALUE}` gets re-cased.

There are two different markup splits, on purpose. `Mask` (machine
translation) also hides characters the model mangles. `cyberpunk2077.Pieces`
(human review, XLIFF) locks only real engine markup, because a person can
handle `—`. `internal/xliff` numbers `<ph>` ids from the source. `Read`
rebuilds every target placeholder from the source markup with the same id,
and rejects a unit whose placeholders don't map one-to-one.

`onscreens_final.json.json` duplicates `onscreens.json.json`, so it is not
exported and takes `onscreens.csv` on import. Other `*_final` files are real
subtitle scenes.

### pipeline: orchestration

`internal/pipeline` walks the source tree, applies `source.files` glob and
`destination.folderNameMap`, and per file dispatches on mode:

- `translateRows` — each row independent.
- `translateReplicas` — group consecutive same-tag rows (`replicaSpans`), then:
  Masker => `translateReplicasBatched` (chunks of `BatchSize`, up to `Concurrency`
  chunks concurrent, per-string retry on chunk failure, progress logging);
  non-Masker => bounded worker pool.
- Per replica group the source text is resolved in order: `carryOver` file match
  (verbatim) -> `Verbatim` tag (keep source) -> `parasitizing.files` match (better
  source, still translated) -> the game's own source text. `placeReplicaWhole`
  puts the translation on the first non-empty row (or `g.start` if none) and
  blanks the rest.

### Lingvanex: two separate packages

- `internal/backend/lingvanex` — **process supervisor**. Spawns `py server.py`
  when `lingvanex.manage: true`, polls the TCP port until reachable
  (`healthTimeout` default 90s — ctranslate2 import is slow), streams child
  output to the logger at debug, kills the process tree on shutdown
  (`proc_windows.go` = `taskkill /T`, `proc_other.go` = process group). Forces
  `PYTHONUTF8=1` / `PYTHONIOENCODING=utf-8` in the child env (Windows pipe
  defaults to cp1252 and crashes on Cyrillic output).
- `internal/translate/lingvanex` — **HTTP client** that requests translations.

`third_party/lingvanex-server/server.py` is the CTranslate2 + SentencePiece
server. **Constraint (user-imposed): its decoding params must not change
translation output.** `BEAM_SIZE=4`, `LENGTH_PENALTY=1.1`, `COMPUTE_TYPE=int8`
are locked — verified byte-identical vs float32/beam 8 for ru→be. Only the CPU
knobs `INTER_THREADS` / `INTRA_THREADS` (and client-side `concurrency` /
`batchSize`) may be tuned for speed. `concurrency` must stay `<= INTER_THREADS`
or HTTP requests queue past `requestTimeout` and cascade into fallback retries.
Model files (`en_be/`, `ru_be/`) are gitignored.

## Conventions

- Logging: `log/slog` with a custom `cliHandler` in `cmd/gametranslator/log.go`
  (`HH:MM:SS  message  k=v`; level shown only for warn/error). Structured k/v,
  not formatted sentences.
- Registry `Register` panics on a duplicate id — that can only be a programming
  error (two inits, or a typo).
- Adding a game: package under `internal/game/<name>/`, implement `game.Game`
  (+ optional capabilities) and a `markup.Analyzer`, add an `extract.Format` only
  if the file layout is new, `game.Register(New())` in `init`, blank-import in
  `internal/plugins`. README "Adding a game" has the checklist.
