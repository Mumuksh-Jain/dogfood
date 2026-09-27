# STATE

## Current phase
T2 JUDGING: CHECKPOINT 4 (RESULTS ENGINE, DEFENSIBLE SCORE AGGREGATION, LEADERBOARD, & EXPLAIN-THIS-RANK REPLAY) COMPLETE

## Last checkpoint passed
T2 Checkpoint 4: Complete Results Engine, Leaderboard, and "Explain This Rank" Auditability / Replay implemented and verified:
- Migration `0003_results_replay.sql` introducing `result_runs` and `result_entries` tables, with comprehensive migration tests in `internal/migrations/migration_0003_test.go`.
- Package `internal/results` implementing defensible cross-judge normalization (z-score standardization with explicit low-n and zero-variance fallbacks), deterministic tie-breaking, canonical JSON input manifests, and SHA-256 digest provenance.
- Public & Organizer HTTP endpoints:
  - `GET /results`: Leaderboard standings with podium, track filtering, and review count metrics.
  - `GET /results/{run_id}/projects/{project_id}`: Flagship "Explain This Rank" transparent receipt displaying rubric criteria scores, judge cohort statistics, formula breakdown, approved fairness statement, and tie-breaking rationale.
  - `POST /api/organizer/results/compute`: Organizer computation of draft results.
  - `POST /api/organizer/results/{id}/publish`: Organizer publication of immutable results.
  - `GET /api/results`: Public API for active published results.
  - `GET /api/results/{run_id}/projects/{project_id}`: API receipt payload.
  - `GET /api/results/{run_id}/replay`: Independent mathematical recalculation and digest verification.
- Independent CLI command `dogfood replay <run_id> [project_id]` in `cmd/dogfood/main.go` verifying 100% mathematical equality and digest matches.
- Automated tests: `internal/results/engine_test.go`, `internal/results/service_test.go`, `internal/httpapp/human_acceptance_test.go` (Flow H), plus controlled tampering test proving replay fails when data is corrupted.
- Official acceptance runner `python official/run.py .dogfood.toml`: 7/7 PASS (`claimed T1 T2, verified T1 T2`).
- Container runtime verified: `docker compose build` and live HTTP probes passing.

## Current blocker
none

## Last checker output
T1  gallery is public ................. PASS
T1  project from fixtures shown ....... PASS
T1  closed event refuses submissions .. PASS
T2  judge sees own scores ............. PASS
T2  judge cannot see peer scores ...... PASS
T2  participant blocked ............... PASS
T2  csv export works .................. PASS
claimed T1 T2, verified T1 T2

## Next action
Review T2 Checkpoint 4 report. Human controls Git. Ready for final release documentation, README/JUDGING.md updates, or tag if requested.

## Known deltas since H0
None.

## Build status
- Contract research: complete
- F1.1 architecture freeze: complete
- Migration manifest: complete
- Environment preparation: complete
- Directory structure: initialized
- Application scaffold: complete and committed
- Migration 0001 (T1 Core Schema): complete and verified
- Fixture seeding: complete and verified
- First vertical slice & checker: complete and verified
- T1 core complete: complete and verified (T1 + T2 pass)
- T1 closure patch: complete and verified
- Containerized runtime: VERIFIED
- Self-contained runtime assets: VERIFIED
- Clean/reviewer-environment offline release proof: VERIFIED
- T2 judging schema: complete and verified (Migration 0002)
- T2 judging engine: complete and verified (internal/assignment)
- T2 rubric & ballot lifecycle: complete and verified (internal/judging, templates, HTTP API)
- T2 results & normalization engine: complete and verified (Migration 0003, internal/results)
- Replay differentiator: complete and verified (Explain This Rank UI, API, & CLI)
- Final acceptance evidence: verified (7/7 PASS)

## Rule
Update this file after every meaningful checkpoint. The next AI session must read this file before proposing work.
