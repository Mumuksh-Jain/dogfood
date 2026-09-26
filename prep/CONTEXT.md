# CONTEXT

## What we are building
DOGFOOD 2026: an open-source, self-hostable hackathon submission and judging platform.

Primary target:
- Complete, correct T1
- Complete, defensible T2
- Replayable Result Receipt / "Explain this rank" as the default differentiator

Optional T3/T4 work activates only after the H48 stability gate.

## Frozen stack
- Go 1.27.1 toolchain if confirmed on the build machine at H0
- Go standard library HTTP layer (`net/http`) unless a tiny dependency is explicitly approved
- Pure-Go SQLite driver, pinned and vendored
- Embedded HTML/CSS/JavaScript; no CDN dependency
- REST API under `/api/v1`
- `openapi.yaml` maintained with the implementation
- Primary runtime image: `FROM scratch`
- Offline-first startup
- Primary target: Docker/Compose
- Build/package Linux amd64 and arm64 artifacts during the official event window
- Preloaded image tar fallback for unusual/offline Docker environments

## Offline rules
The final portal must not require:
- hosted database
- hosted authentication
- cloud account
- external API
- CDN
- runtime package download
- registry pull for the application runtime image

The final cold-start test must work with network disabled using only the repository, Docker/Compose, and the packaged local artifacts required by the documented path.

## Frozen schema principles
- Event-scoped authorization
- Published rubric versions are immutable
- Submitted/corrected ballot versions are immutable
- Every ballot version references the exact rubric version used
- Assignment generation has an `assignment_runs` record so infeasibility can be preserved even when zero assignments are produced
- Duplicate submissions are flagged/adjudicated, never silently merged
- Every published result identifies the exact ballot versions/input manifest used
- Published results are immutable; corrections create a new result run
- Expected, completed, and effective review counts are distinct
- Fixture source IDs are preserved
- Seed import is idempotent and must never silently overwrite post-seed edits

## Staged migration target

### 0001 — T1 core
- users
- sessions
- events
- event_roles
- tracks
- teams
- team_memberships
- team_invites
- projects
- submissions
- seed_imports

### 0002 — T2 judging
- judge_profiles
- judge_track_eligibility
- assignment_runs
- assignments
- rubric_versions
- ballot_versions

### 0003 — results/replay
- result_runs
- result_entries

Deferred tables are added only when required by a demonstrated behavior or after T1 + T2 + replay are stable.

## Ballot-version rule
For one assignment, the highest `version_no` is the latest working version.
The current committed ballot is the highest version whose save kind is `SUBMISSION` or `CORRECTION`.
Use an index equivalent to `(assignment_id, version_no DESC)`.
Do not introduce a second mutable "current" source of truth unless implementation evidence requires it.

## Result immutability rule
A result run may be recomputed/replaced while unpublished.
Once `published_at` is set, that result run is immutable.
Any correction creates a new result run with `supersedes_result_run_id`.

## Replay differentiator
A completed Replayable Result Receipt requires both:

1. UI:
   - visible "Explain this rank" path
   - exact rubric version
   - exact ballot/input versions
   - included/excluded inputs
   - fallback decisions
   - expected/completed/effective counts
   - arithmetic
   - tie policy
   - result run identity

2. Independent verification:
   - `make replay`, script, or equivalent command
   - recomputes the published result from immutable inputs
   - compares with frozen result
   - exits non-zero on mismatch

## The seven current checker cases

T1:
1. gallery is public
2. project from fixtures shown
3. closed event refuses submissions

T2:
4. judge sees own scores
5. judge cannot see peer scores
6. participant blocked
7. CSV export works

The authoritative kickoff checker may change. Re-read it at H0.

## Tier-claim rule
Claim only the highest contiguous tier set that the authoritative kickoff acceptance suite verifies on the exact clean submitted commit. Higher-tier features may be described as implemented/partial but are never claimed as checker-verified unless the suite verifies them.

## API rule
Every user-visible action should be backed by the same application/service logic exposed through `/api/v1`.
Do not implement duplicate business rules separately in HTML handlers and API handlers.

## Team-membership rule
"One active team per user per event" is an assumed product policy, not a confirmed Dogfood rule.
Re-check the authoritative H0 contract before encoding a uniqueness constraint.

## Fixture-derived cautions
- Preserve all fixture IDs exactly
- A team may have more than one project record
- Duplicate candidates must not be silently merged
- The fixture does not provide a full configurable rubric definition
- The fixture does not provide reliable original ballot submission timestamps
- Low-n and zero-variance judges require explicit normalization fallback handling

## Migration strategy
- Application-owned forward-only migrations
- Versioned SQL files embedded in the Go binary
- `schema_migrations` stores version, name, checksum, applied time
- Never edit an applied migration
- Checksum mismatch is a startup failure
- Migrations run before seeding

## Build discipline
Priority order:
1. failing test
2. implementation gap
3. documentation gap
4. optional enhancement

Do not start T3/T4 while T1/T2 are unstable.
Feature freeze at H60.

## No-code / no-drift rules
Before kickoff:
- no Dogfood project code
- no executable project migrations
- no Dockerfile/Compose/application implementation

During build:
- do not add features outside the current phase
- do not change a frozen invariant silently
- do not guess fixture or contract details when the official files can answer them
- do not introduce an external service dependency
- do not overclaim tiers
