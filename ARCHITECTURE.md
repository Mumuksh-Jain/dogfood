# Architecture & System Design — Dogfood 2026

## 1. System Overview

Dogfood 2026 is designed as a standalone, offline-first hackathon lifecycle platform. It combines a server-rendered web application with an API-first JSON backend and an independent CLI replay engine.

```
                  ┌──────────────────────────────────────────────┐
                  │          HTTP / Browser / API Clients        │
                  └──────────────────────┬───────────────────────┘
                                         │
                                         ▼
┌────────────────────────────────────────────────────────────────────────┐
│                        cmd/dogfood & internal/httpapp                  │
│  - Go 1.22 net/http Standard Library Multiplexer                       │
│  - Dual-mode Handlers (HTML browser templates + JSON /api/v1 endpoints)│
│  - Session & Bearer Token Authentication (internal/auth)               │
│  - RFC 4180 CSV Exporter with Formula Injection Defense (F21)          │
└────────────┬───────────────────────────┬──────────────────────┬────────┘
             │                           │                      │
             ▼                           ▼                      ▼
┌────────────────────────┐  ┌────────────────────────┐  ┌───────────────┐
│   internal/assignment  │  │    internal/judging    │  │internal/results│
│ - Deterministic Solver │  │ - Rubric Versioning    │  │ - Z-Score Norm│
│ - Track Eligibility    │  │ - Immutability Guard   │  │ - Fallback Ctr│
│ - Capacity Constraints │  │ - Ballot Lifecycle     │  │ - Replay Eng  │
└────────────┬───────────┘  └────────────┬───────────┘  └───────┬───────┘
             │                           │                      │
             └───────────────────────────┼──────────────────────┘
                                         ▼
┌────────────────────────────────────────────────────────────────────────┐
│                    SQLite 3 Embedded Engine (internal/db)              │
│  - PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL;                │
│  - Strict Referencing Schemas & Append-Only Immutable Records          │
└────────────────────────────────────────────────────────────────────────┘
```

---

## 2. Package Boundaries & Responsibilities

The codebase follows idiomatic Go package separation with clear domain boundaries:

| Package | Purpose & Guarantees |
| :--- | :--- |
| `cmd/dogfood` | Main binary entrypoint. Handles flag parsing (`--port`, `--db`, `--fixtures`) and the CLI subcommand `dogfood replay <run_id> [project_id]`. |
| `internal/auth` | Session token generation (`crypto/rand`), SHA-256 token hashing, session lifecycle, role evaluation (`organizer`, `admin`, `judge`, `participant`), and preservation of official acceptance tokens. |
| `internal/db` | SQLite database initialization with production pragmas: `journal_mode = WAL`, `foreign_keys = ON`, `busy_timeout = 5000`, `synchronous = NORMAL`. |
| `internal/migrations` | Ordered, transactional schema migrations (`0001_t1_core.sql`, `0002_t2_judging.sql`, `0003_results_replay.sql`, `0010_t1_prizes.sql`). |
| `internal/seed` | Loads and verifies canonical fixtures (`official/fixtures.json`) against the frozen SHA-256 fixture checksum. |
| `internal/assignment` | Deterministic judge-to-project allocation engine. Respects judge capacity, track eligibility graphs, and conflict-of-interest constraints with seed-based reproducibility. |
| `internal/judging` | Scoring rubric versioning, criteria validation (positive weights, min < max), draft-to-final ballot lifecycle, and submitted ballot immutability. |
| `internal/results` | Score aggregation pipeline: judge mean/variance calculation, z-score standardization, explicit fallbacks (`CONSTANT_SCORE_FALLBACK`, `SMALL_SAMPLE_FALLBACK`), deterministic tie resolution, input manifest serialization, and independent replay verification. |
| `internal/httpapp` | HTTP routing, template rendering with Go `html/template`, JSON REST endpoints, organizer judging operations, and RFC 4180 CSV export. |
| `web` | Static CSS and HTML template assets embedded into the binary via `embed.FS`. |

---

## 3. Storage Architecture & Concurrency

### SQLite Configuration & Concurrency Pragmas
To provide resilient local storage without requiring external database servers, SQLite is initialized with:
* `PRAGMA journal_mode = WAL;`: Write-Ahead Logging allows concurrent readers while a write transaction is executing.
* `PRAGMA foreign_keys = ON;`: Hard referential integrity enforced on all relationships (`events`, `tracks`, `teams`, `projects`, `assignments`, `rubric_versions`, `ballot_versions`, `result_runs`).
* `PRAGMA busy_timeout = 5000;`: Waits up to 5 seconds during lock contention before returning `busy`.
* `PRAGMA synchronous = NORMAL;`: Ensures durability across crashes while maintaining high throughput.

### Transactional Boundaries
Mutations that span multiple tables or require strict state invariants execute in atomic database transactions (`tx.BeginTx`):
1. **Ballot Submission (`judging.SubmitBallot`)**: Validates rubric criteria, verifies assignment ownership, locks ballot against existing submissions, increments version, inserts immutable `SUBMISSION` row, and transitions assignment status to `COMPLETED`.
2. **Result Generation (`results.ComputeResults`)**: Serializes input manifest, hashes SHA-256 digest, computes normalization and rankings, and inserts `result_runs` and `result_entries` in a single transaction.
3. **Rubric Versioning (`judging.CreateAndPublishRubric`)**: Inserts new version `v(N+1)` with cryptographic configuration hash without altering existing published rubrics.

---

## 4. Security & Role-Based Access Control

### Role Isolation Matrix
Authorization is enforced both at the HTTP router layer and within domain services:

| Role | Permitted Actions | Prohibited Actions (403 Forbidden) |
| :--- | :--- | :--- |
| **Anonymous** | View landing page, public gallery, leaderboard, explain-rank receipts, health check. | Edit projects, submit ballots, view drafts, access organizer controls. |
| **Participant** | Create team, invite members, draft/edit own team projects, submit project before deadline. | Access organizer progress/controls, evaluate projects, view other teams' drafts, export CSV. |
| **Judge** | View assigned projects, save draft ballots, submit final ballots for assigned projects. | View peer judge ballots, alter organizer config, create teams, export CSV. |
| **Organizer / Admin** | Configure event windows, tracks, prizes, run assignment engine, invite judges, author rubrics, compute/publish results, export CSV. | Cannot mutate submitted ballots directly; all operations are auditable. |

### Spreadsheet Formula Injection Defense (Failure Case F21)
All CSV export endpoints apply RFC 4180 escaping and prefix sanitization. If any cell starts with spreadsheet execution trigger characters (`=`, `+`, `-`, `@`, `\t`, `\r`), it is prefixed with a single quote (`'`) to neutralize remote code execution vulnerabilities in Excel and Google Sheets.

---

## 5. Replay Verification Architecture

The scoring system provides mathematical auditability through cryptographic replay verification:
1. **Input Manifest**: When an official result run is computed, all submitted ballots are collected and serialized into canonical JSON (`InputManifest`).
2. **SHA-256 Digest**: The SHA-256 hash of the canonical manifest is stored in `result_runs.input_digest`.
3. **Verification**: The `dogfood replay <run_id>` CLI command reads the source ballots from the database, re-hashes the input manifest to verify tamper-freedom, recomputes normalization and rankings from scratch, and verifies bit-for-bit parity against the published standings.
