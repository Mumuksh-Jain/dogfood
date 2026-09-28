# Dogfood 2026 — Hackathon Platform

An auditable, offline-first hackathon lifecycle platform written in Go and modern SQLite. Implements end-to-end team collaboration, submission management, deterministic judge allocation, versioned weighted scoring rubrics, statistical score normalization, cryptographic replay verification, and organizer progress operations.

---

## Tier Implementation Status

* **Tier 1 (Core Portal & Submissions)**: **COMPLETE & VERIFIED**
  * Event lifecycle time windows (Registration, Submissions, Judging) with strict enforcement.
  * Participant authentication, session management, and role-based access control.
  * Team formation, invite tokens, single-team exclusivity, and max capacity (4 members).
  * Project submissions with versioned drafts, submitted state immutability, and public gallery.
  * Tracks and prize configuration.
* **Tier 2 (Judging, Defensible Scoring & Replay)**: **COMPLETE & VERIFIED**
  * Versioned scoring rubrics with customizable weights and SHA-256 configuration hashes.
  * Deterministic judge-to-project allocation engine with capacity and track eligibility graphs.
  * Draft-to-final ballot lifecycle with hard immutability once submitted.
  * Strict judge peer isolation and participant blocking.
  * Defensible score aggregation with z-score standardization and explicit fallback policies (`CONSTANT_SCORE_FALLBACK`, `SMALL_SAMPLE_FALLBACK`, `STANDARD`).
  * Immutable published results with deterministic ranking and tie groups.
  * Public leaderboard at `/results` with transparent "Explain This Rank" mathematical audit receipts.
  * Independent CLI replay command (`dogfood replay`) verifying SHA-256 input manifest and score reproduction.
  * Live Organizer Judging Operations Control Room with real-time review velocity, judge roster administration, project review coverage, and track progress breakdown.
  * Auditable RFC 4180 CSV exports with Spreadsheet Formula Injection Defense (Failure Case F21).

---

## Offline-First Architecture

Dogfood operates with **zero external internet dependencies at runtime**:
* Embedded static assets via Go `embed.FS` (`/static/app.css`).
* Pure system font stack (`-apple-system`, `BlinkMacSystemFont`, `Segoe UI`, `Roboto`, `Helvetica`, `Arial`).
* Zero CDNs, Google Fonts, external JavaScript libraries, or remote APIs.
* Embedded, self-contained SQLite storage with WAL mode, foreign key enforcement, and automated migrations.

---

## Quickstart

### Running with Docker

```bash
# Build and run container
docker compose up --build

# Access portal
http://localhost:8080
```

### Offline Fallback / Unusual Docker Environments

If running in an environment without Docker build capabilities or on an isolated machine with pre-imported image archives:

```bash
# 1. Load the pre-packaged image archive (if using offline tar distribution)
docker load < dist/dogfood-image-linux-amd64.tar.gz

# 2. Run with the preloaded Compose configuration (never pulls, no build required)
docker compose -f compose.preloaded.yaml up -d
```

### Running Natively with Go

```bash
# Requires Go 1.22+
go run ./cmd/dogfood --port 8080 --db dogfood.db
```

The portal automatically runs database migrations and seeds canonical fixture data on first startup.

---

## Pre-Seeded Demo Accounts & Personas

Quick login is available via the persona selector on the `/login` page:

| Persona | Role | Email | Seeded Bearer Token |
| :--- | :--- | :--- | :--- |
| **Organizer** | Organizer / Admin | `organizer@dogfood.internal` | `org_7f2a` |
| **Alex Chen** | Participant (Team Alpha) | `alex@dogfood.internal` | `prt_2e88` |
| **Blair Taylor** | Participant (Team Beta) | `blair@dogfood.internal` | *(session login)* |
| **Tomas Varga** | Official Judge | `tomas@dogfood.internal` | `jdg_a_91bc` |
| **Wei Lindqvist** | Official Judge | `wei@dogfood.internal` | `jdg_b_44de` |

---

## Replay Verification CLI

Dogfood provides an independent deterministic replay verification command:

```bash
# Verify entire result run
go run ./cmd/dogfood replay <run_id>

# Verify specific project within a run
go run ./cmd/dogfood replay <run_id> <project_id>
```

The replay engine parses the immutable SHA-256 input manifest, independently recomputes all normalization and tie-breaking arithmetic, and validates exact bit-for-bit parity.

---

## Verification & Test Commands

```bash
# 1. Run all unit and integration tests
go test -count=1 ./...

# 2. Run official acceptance test harness
python official/run.py .dogfood.toml
```

**Official Harness Result:** `claimed T1 T2, verified T1 T2` (7/7 PASS).

---

## Key Web & API Endpoints

### Public Web Routes
* `GET /`: Landing page and event overview.
* `GET /projects`: Public gallery of submitted projects.
* `GET /projects/{id}`: Detailed project submission view.
* `GET /results`: Public leaderboard and standings.
* `GET /results/{run_id}/explain/{project_id}`: Transparent "Explain This Rank" arithmetic receipt.
* `GET /login`: Persona switcher and session login.
* `GET /dashboard`: Role-adaptive workspace for participants, judges, and organizers.

### Programmatic REST API (`/api/v1`)
* `GET /api/v1/projects`: List public projects (with optional `?track=` filter).
* `GET /api/v1/teams`: List registered teams.
* `GET /api/v1/tracks`: List event tracks with submission counts.
* `GET /api/v1/prizes`: List event prizes.
* `GET /api/v1/results`: Active published result run and project standings.
* `GET /api/v1/results/{run_id}/replay/{project_id}`: Itemized replay verification report.
* `GET /api/v1/organizer/progress`: Real-time judging metrics, judge velocity, and project coverage.
* `POST /api/v1/organizer/judges`: Invite judge and set capacity / track eligibility.
* `POST /api/v1/organizer/rubrics`: Author and publish a new rubric version.
* `GET /api/v1/export.csv`: RFC 4180 standings CSV with F21 formula injection defense.
* `GET /api/v1/export/evaluations.csv`: Per-judge ballot audit export.