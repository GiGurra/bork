# Docs audit — merged plan (bork-i2xvwp, phase 2)

Sources: auditA.md and auditB.md, attached to bork-i2xvwp, both checked against main 62ec720e (after #406). auditA drafted this plan; auditB reviewed and added evidence corrections, ownership boundaries, validation details and generated-artifact limits.

Both audits agree that **the checked code blocks are healthy**: TestDocSnippets passes, and both auditors' tour/testing runs succeeded. The problems are coverage, prose, structure, and staleness in prose and unchecked pages. So the plan adds and reorganizes; it does not mass-fix snippets.

Tags: **[A]** found by auditA, **[B]** by auditB, **[A+B]** by both.

---

## 1. Combined Top 15 (by newcomer impact)

| # | Item | Found by | P |
| --- | --- | --- | --- |
| 1 | **No signature-level reference for built-ins.** String/List/Map/Option/Seq/Bytes/Channel/Task/Atom/Scope/Mock methods appear as name lists only. `bork doc` can't render the prelude. Several functions are undocumented (`prepend`, `onClose`, `scopeOf`, `withTimeoutDo`, `Mock.waitFor`…). | A+B | P1 |
| 2 | **Std pages lack signatures and examples, and read like changelogs.** 12 of 29 have no example. Pages open with run-on sentences. Paragraphs are duplicated (http.md 9≡44, 11≡46, 13≡48; encoding CSV ×3; math summary ×2). Migration voice ("moved from the prelude", "(implemented)"). APIs left unnamed (env Get/Require/All, log Config fields). | A+B | P1 |
| 3 | **No HTTP + database walkthrough.** http.md has no minimal server; sql.md has no open/insert/query/tx example. Nothing composes them with config, union-to-status mapping, handler tests, or shutdown. Both auditors built the service only by guessing or reading example source. | A+B | P1 |
| 4 | **Scope exit is described wrongly.** Leaving a scope *cancels* its tasks and then joins them. "Fire-and-forget… the scope still waits" suggests the work runs to completion (verified: a delayed task gets `Cancelled { reason: "the scope ended" }`). The `taskTimeout` exception also needs a mention. | B | P1 |
| 5 | **No minimal CLI recipe.** std/cli.md runs 80 lines of mapping policy before any code. The intro omits `Run`'s required name/description arguments. `cli.Error`'s shape, printing it (`eprintln` takes only String), and exit codes are never shown. | A+B | P1 |
| 6 | **No cheat sheet and no single-file agent reference** (`llms.txt` / `llms-full.txt`). grammar.md does have an EBNF block (l.24ff), but it is contributor-facing, headed "M0 so far", and mixed with non-grammar sections. | A+B | P1 |
| 7 | **Loops and rebinding are hard to find and contradictory.** Loops live only under Collections. tour.md:66 says names can't be reused (wrong). Same-block rebinding is OK, nested shadowing is an error, and loop "carrying" is allowed; this is explained only in dense prose. | A+B | P1 |
| 8 | **Content after the Previous/Next footer**: matching.md:272 (all of `is`), facts.md:283, comptime.md:114, packages.md:292. | A+B | P1 |
| 9 | **No "coming from Go/TS/Rust/Python" page, FAQ, pitfalls, or error explanations**: `Some(x)` undefined, `:` named args, unused locals, unused declared effects on private functions, discarded unions, `?` in `main`, `eprintln` takes only String. | A+B | P2 |
| 10 | **Resource contracts have no reader docs**: `in` parameter lifetimes, resource declarations, and how values that retain a resource, or must outlive another, are returned from helpers (retention and outliving are concepts, not keywords). `attach` and `openScope`/`closeScope` have no examples. `OwnedScope` is undefined. | A+B | P1 |
| 11 | **Lazy/computed record fields are implemented but missing from Types**: copy semantics, printing, and codec omission are documented only in design/lazy.md. | B | P1 |
| 12 | **Page order and structure.** types.md opens with tuples. matching.md puts positional payloads and their JSON wire format second. packages.md mixes modules, library publishing, legal proxy text, a `providers` migration, type classes, and assembly. The index puts derivation before type classes, and Previous/Next skips derivation. | A+B | P2 |
| 13 | **Stale status and spec claims.** design/channels.md and design/derive.md read as current proposals (and use `launch`). roadmap/lsp/describe report outdated limits. grammar.md:216 forbids generic equality (wrong). Its "result caching planned" line (l.378) needs checking with the cache owner: the whole-artifact cache may not reuse comptime values on its own. diagnostics.md quotes the obsolete `syntax.single-ampersand`. time.md says a slow ticker receiver gets "the latest" tick, but it retains the earlier pending tick and drops subsequent ticks while full. process.md/strconv write defaults as `Stop(grace = 5s)`, `Hex(width = 0)`. | A+B | P2 |
| 14 | **Examples index is incomplete**: 14 examples unlisted (all `cli_*`, `http_multi`, `package_values`, `scripts`, `script_uuid`, `tuples`). It also lacks run hints (long-running service, curl, fixture cwd). | A+B | P2 |
| 15 | **Testing gaps**: `_test.bork` files and running every package; failure output (shrunk property, seed replay, unmet mock expectation, hermetic rejection); undocumented mock handle methods (`expectWhere`, `waitFor`, `waitForWhere`). | A+B | P2 |

---

## 2. Work breakdown (parallel PRs)

Ownership rules (agreed by A and B):
- Concurrently active PRs have **exactly one owner per file**. PR 0 is a sequential foundation; chapter owners branch after it merges. PR 9 changes navigation only after chapter owners land. Page owners fix their own stale items; no separate PR edits that page at the same time.
- The **shared index and test files** belong to PR 0 (lands first) and PR 9 (lands last), and to no one else: docs/README.md, docs/std/README.md, the Previous/Next footers, the README's docs list, internal/driver/docs_snippets_test.go and scripts/test_docs_site.py. A content PR that adds a page notes in its description which index link PR 9 should add.
- Write for users in the present tense. No migration or history notes in reader pages (they go to design docs or release notes). Keep user-visible guarantees, limits and cancellation semantics on the page or behind a canonical link. Move only implementation detail.
- Bork blocks must be complete and checked. Multi-file examples get a fixture (an example dir or testdata case), not more `fragment` blocks. A `bork fails` block must be followed by the `text` block quoting the intended diagnostic.
- Each PR stays under ~1,500 changed lines, including tracked generated output. Split large page groups or generated publication work further before parallel work starts. Locally run focused doc/package tests and required lint/gofmt for code changes; CI is the full gate.

### Std page template (used by PRs 4a–4e)
1. One-sentence purpose.
2. **A minimal runnable example in the first screen**, with its output (happy path and one failure).
3. An API table: signature (effects, defaults as `name: Type = default`, full result union, record fields) and a one-line meaning.
4. Task sections, each with an example. State each guarantee and limit once.
5. Links to larger examples, `bork doc bork/<pkg>` (once bork-2fq39h is fixed), and the design doc.

### PR 0 — Doc-test guards and footer moves (first, small)
- **Files:** internal/driver/docs_snippets_test.go (reader pages = every `docs/*.md` except an explicit contributor list, so new pages are checked automatically; support legitimate non-bork fence languages on existing editor pages without silently skipping bork blocks; fail on content after the Previous/Next footer; fail on identical paragraphs within a page, with an allowlist for current offenders that PR 9 empties after std owners remove duplicates). Also the four after-footer blocks only (matching.md:272, facts.md:283, comptime.md:114, packages.md:292), moved into the right sections.
- **Acceptance:** the tests pass on main. The footer check fails on a page that has text after its footer. Chapter PRs branch after this merges.

### PR 1 — Stale references in spec/contributor/design docs
- **Files:** grammar.md and targeted requirements.md sections (the "M0" header; l.216 generic `Eq`; move non-grammar implementation/reference sections only where appropriate, preserving current cross-links; l.378 caching only after confirming with the cache owner), diagnostics.md (regenerate the JSON example; `&` on Bool is now `type.error`), describe.md:46, roadmap.md, contributing.md (historical-docs note), docs/design/** (except the two files owned by 4a): a status banner on each ("Implemented; current docs: …" or "Historical proposal"), no `launch`/`spawn`/`providers` presented as current, failing sketches such as design/comptime.md:23 marked as sketches or fixed.
- **Acceptance:** every design doc identifies its real status; historical language proposals link current reader docs, and compiler-internal designs link appropriate implementation/reference pages when no reader page exists. Preserve current useful rationale; do not label every design historical. The diagnostics example matches real `bork check --json` output.

### PR 2a — `bork doc builtin` (code)
- **Files:** the `bork doc` implementation and its tests.
- **Goal:** render every exported prelude declaration with signature and doc comment. Hide internal helpers (`compilerSelect*`, and so on).
- **Depends on:** nothing. It must not depend on the bork-2fq39h fix (that bug is in std packages with Go deps).

### PR 2b — Built-ins reference page
- **Files:** new docs/std/builtins.md reference entry point, generation support and a staleness test; missing prelude doc comments. Generate the full API reproducibly from 2a. If full tracked output would exceed ~1,500 lines, publish it as build output or split publication into separate per-topic generated-reference PRs; no oversized generated diff.
- **Acceptance:** every name in the basics.md/collections.md/types.md method lists appears in the generated reference with signature, effects, facts, defaults and concise comments; the test detects source/reference drift and excludes compiler-internal helpers. Published links resolve, including in llms output. **Depends on:** 2a.

### PR 3a — Language core: basics, types, matching, collections
- **Files:** basics.md, types.md, matching.md, collections.md.
- **Content:**
  - basics: a "Control flow" section (if / match / every loop form / early return / tail calls); ✅/❌ examples for same-block rebinding, nested shadowing and loop carrying; printing (`println` vs `eprintln(String)`, `toString`); pitfalls inline.
  - collections: keep `for (x in xs)` and Seq; the carry rules move to basics as examples, with a link back.
  - types: **lazy/computed record fields** (supplied vs computed; copy recomputation; Show and codec behavior; validation). Trim the codec/GoStruct prose in the tuples section. Order: reorder only where it helps the learning path (A proposes tuples after Option; B considers that subjective). The PR author decides and justifies the order in the PR.
  - matching: integrate `is` (moved by PR 0). Link the positional-payload JSON encoding to codec.md. Cut the spec prose at l.8–10, 35–37 and 288–299.
- **Acceptance:** every loop form has a runnable example. There is a `bork fails` nested-shadowing example with its quoted error. The lazy-field example shows copy recomputation and encoded output.

### PR 3b — Language: scopes, channels, effects, facts, comptime
- **Files:** scopes.md, channels.md, effects.md, facts.md, comptime.md.
- **Content:**
  - scopes: scope exit runs **cancel → join → cleanup**, shown with paired runnable examples (await inside the scope vs. letting exit cancel), plus the `taskTimeout` exception. Examples for `attach` and `openScope`/`closeScope`. A "Functions that keep resources" section: `in` parameter lifetimes, resource declarations, and returning values that retain a resource or must outlive another (concepts only; teach no invented syntax). Define OwnedScope or drop the term. Fix the "Two tools" count. Shrink the signal section to a summary plus a link to std/signal.md (4c adds the detail there).
  - channels: split the glued bullet at l.309.
  - effects: move the signal note out. Add the private-function unused-effect rule with a `bork fails` example. Inline examples for ambient `logged`/`propagated`/`needs x?`.
  - facts: a "the compiler can't prove my fact" troubleshooting recipe (guard → validated return → alias/rule → `bork describe`; `trust` plus a test).
  - comptime: tuples in the supported results; a note on ReadBytes returning `List[Byte]`.
- **Acceptance:** the scope-exit example's output matches a real run, and each contract concept has one checked example. The signal detail removed from scopes.md is preserved on std/signal.md (coordinate with 4c).

### PR 3c — Language: packages, libraries, derivation, interop, testing
- **Files:** packages.md, new language/libraries.md, derivation.md, go-interop.md, testing.md.
- **Content:**
  - packages: packages, modules, imports, type classes, derived instances, assembly (document `assembleAll`/`assembleRecord` and a two-package `instances` bundle example).
  - libraries.md: deps, publishing, private modules. The legal proxy text goes to design/library-dependencies.md (link only). The `providers` migration is dropped.
  - derivation: split "first template" from the advanced reference, and add a second worked example (a small record encoder with a rejection diagnostic).
  - go-interop: a GoStruct/go-tags example.
  - testing: test files and `_test.bork`; running every package (with a CI recipe, no invented flags); real failure transcripts (shrunk property plus seed replay, generator exhaustion or skipped parameters, unmet mock expectation, hermetic rejection); the complete mock handle table; an inline HTTP mock.
- **Acceptance:** no migration or legal text remains in packages.md, and the failure transcripts come from real runs.

### PR 4a–4e — Std pages (apply the template; each batch owns its files)
- **4a HTTP and SQL:** std/http.md, std/sql.md, design/http-propagation.md, design/interpolators.md (they receive the protocol and render-check detail; the user-visible constraints stay on the std page).
- **4b Data and config:** codec.md (receives the positional-payload encoding), json.md, yaml.md, env.md (Get/Require/All), log.md (Config fields), encoding.md (one CSV round-trip example), embed.md.
- **4c System:** fs.md, process.md (typed `cancelGrace: time.Duration` signatures and examples), time.md (the ticker keeps the earliest pending tick), tasks.md (fix the stale "follow-ups" link), net.md, signal.md (receives the scopes detail).
- **4d1 Numbers and randomness:** crypto.md, rand.md, uuid.md, math.md (remove the duplicate summary), strconv.md (defaults syntax), bits.md, binary.md.
- **4d2 Archives and advanced helpers:** archive.md, compress.md, url.md, regex.md, test.md (drop the `providers` migration), new shape.md. Each subgroup is a separate focused PR within the size limit; shape.md owns the API reference, while PR 3c owns the derivation tutorial.
- **4e CLI:** cli.md (lead with a 20–30-line app: Parse/Run with name and description, the `cli.Error { errors }` shape, `eprintln(toString(e))`, a nonzero exit, `--help`/missing-flag transcripts), cli-cookbook.md (minimal recipe first, fleet as the advanced walkthrough). Coordinate with **boacli**.
- **Acceptance (each):** every page follows the template, with its first example within 15 lines; examples compile and representative output and error handling are run and quoted. No duplicates remain in the batch's pages; PR 9 removes completed entries from PR 0's shared duplicate-paragraph allowlist, so std workers do not edit the common test file concurrently. `rg "\(implemented\)|moved from the prelude|former prelude"` finds nothing in the batch's files.

### PR 5 — Service tour and cookbook
- **Files:** tour.md (l.66 rebinding fix; l.7 Go wording aligned with install.md, i.e. 1.21+ auto-switches to 1.26; a "Next" link), new docs/tour-service.md, new docs/cookbook.md, new examples/service_tour/ plus testdata/examples transcript.
- **Content:**
  - tour-service.md: a SQLite-backed JSON API with env config, fact-validated input, typed SQL, union → status mapping, application vs. request scopes, logging, graceful shutdown, direct handler tests, run/curl.
  - cookbook.md: about 25 checked recipes: errors in `main` with exit codes, stdin, sleep/timeouts, loop N times, files, JSON, env config, subprocess, parallel map, retry, CLI skeleton, mock-based test, …
- **Acceptance:** every block compiles, and the service example's transcript is checked by the examples test. Run valid/invalid handler tests, setup failure/exit paths and cleanup without requiring an external database service. Cookbook fragments requiring files/packages have runnable fixtures.

### PR 6 — Quick references
- **Files:** new docs/cheatsheet.md, new docs/coming-from.md (one page with Go / TS / Rust / Python tables), new docs/faq.md (pitfalls plus the top ~15 diagnostics, each a `bork fails` block with its quoted message and a fix).
- **Acceptance:** blocks compile, and the FAQ messages match the compiler. Can start at any time; independent of the other PRs.

### PR 7 — Examples index
- **Files:** docs/examples.md (all 14 missing examples; a "Run" column covering demo args, long-running servers, cwd/fixtures), examples/http_server/main.bork (header comment above the imports), and a new test file checking that every public runnable `examples/*` dir is linked. Finish after PR 5 adds service_tour, or reserve its actual run instructions with that owner before landing.
- **Acceptance:** all public examples have accurate first-run commands and requirements; the coverage test catches an unlisted public example without requiring internal fixtures to be promoted.

### PR 8 — Agent reference (llms)
- **Files:** scripts/gen_llms.py and the docs site build (mkdocs hook), producing `llms.txt` (index) and `llms-full.txt` from the README, tour, language pages, cheat sheet, builtins.md and std pages as **site build output, not committed**, plus a test of the generator (paths and links resolve, every reader page is included).
- **Acceptance:** the deployed site serves /llms.txt and /llms-full.txt, and the generator test passes. **Depends on:** content PRs and generated builtin reference settling (build it last; it regenerates automatically after that). Test deterministic output, inclusion of all canonical source sections, completeness and resolved file/anchor links; do not truncate to meet the tracked PR size limit.

### PR 9 — Navigation integration (last)
- **Files:** docs/README.md (Quick references block: cheat sheet, cookbook, FAQ, coming-from, builtins; index descriptions mention loops; language order), docs/std/README.md (builtins and shape rows; a `bork doc` note), every language page's Previous/Next footer (one chain: Basics → Types → Matching → Collections → Facts → Effects → Scopes → Channels → Packages → Libraries → Comptime → Interpolation → Derivation → Go → Testing), the README docs list (agent pointer to the published /llms.txt and /llms-full.txt), scripts/test_docs_site.py; final common doc-test coverage/duplicate allowlist cleanup in internal/driver/docs_snippets_test.go.
- **Acceptance:** Previous/Next forms one chain over all language pages, and site nav includes every new page. All reader pages and multi-file fixtures are checked; all relative/anchor links and published agent/builtin-reference URLs resolve. The duplicate allowlist is empty after content owners finish.

**Order:** PR 0 first. Then 1, 2a, 3a/3b/3c, 4a/4b/4c/4d1/4d2/4e, 5 and 6 in parallel (2b after 2a; 7 finishes after 5). PRs 8 and 9 last.

## 3. Dropped or deferred

| Item | Decision | Reason |
| --- | --- | --- |
| Trimming requirements.md (4,146 lines) | Defer | Contributor spec, not reader-facing. |
| Separate per-language tours | Drop | One coming-from page with tables avoids four duplicated tours (auditB). |
| Fixing all 28 failing design-doc sketches | Drop | They are historical sketches. Banners plus marking them as fragments is enough (auditB). |
| Plain-text `bork doc` terminal mode (no anchors) | Defer | Tooling polish. Revisit after bork-2fq39h. |
| "Coming from" tables at the end of every language page | Defer | coming-from.md covers it. Add per-page tables later if needed. |
| Grammar-based linting for agents | Defer | llms-full plus the checked cheat sheet first. |
| Committing llms-full.txt to the repo | Drop | It would exceed the PR size limit and go stale. Generate it at site build time instead (auditB). |
| Expanding resource/callback Go interop beyond current syntax | Defer | Document what exists first (auditB). |
| Flipping the comptime caching status in grammar.md | Defer | Verify with the cache owner first. Whole-artifact reuse is not the same as reusing comptime values (auditB). |

## 4. Non-doc bugs (route, don't fix in doc PRs)

| Bug | Status / owner |
| --- | --- |
| `bork doc` fails for cli/crypto/sql/uuid/yaml ("documentation inputs changed; retry") | Filed **bork-2fq39h** |
| `bork/cli` `--help` shows Int fields as `string`; parse errors show "invalid JSON at line 1" | Worker **boacli** |
| `bork doc` signatures show the internal alias `codecs.JsonError` (http.Body, env.LoadJson) | Worker **codecnames** |
| `eprintln(text: String)` vs. variadic `println(any…)` | Document current behavior in PR 3a/CLI recipes. API expansion is optional product work, not a blocker or implied compiler bug. |
| `compilerSelect*` helpers are exported from the prelude (may show in completion and in `bork doc builtin`) | **New**, small. PR 2a must hide them in docs regardless. |
| ticker: earlier pending tick retained while later ticks drop | Confirmed current implementation/runtime behavior; doc fix in 4c. No semantic-change request or lead decision needed for this audit. |
