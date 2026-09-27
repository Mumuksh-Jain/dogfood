# STATE

## Current phase
T2 JUDGING: CHECKPOINT 5 (AUDITABLE CSV EXPORT & API-FIRST CONSISTENCY) COMPLETE

## Last checkpoint passed
T2 Checkpoint 5: Auditable CSV Export & API-First Consistency implemented and verified:
- Defensible CSV export in `internal/httpapp/csv_export.go` supporting:
  - Default standings export (`/api/export.csv`, `/api/v1/export.csv`, `/api/v1/export/results.csv`): 17 stable reconciliation columns (`rank,project_id,title,team_id,team_name,track_id,track_name,final_score,raw_score,expected_reviews,completed_reviews,effective_reviews,fallback_count,tie_group,result_run_id,input_digest,published_at`).
  - Evaluations export (`/api/export/evaluations.csv`, `/api/v1/export/evaluations.csv`, `?type=evaluations`): 11 audit columns (`assignment_id,ballot_id,project_id,project_title,judge_user_id,judge_name,rubric_version_id,save_kind,scores,comment,created_at`).
  - Projects export (`/api/export/projects.csv`, `/api/v1/export/projects.csv`, `?type=projects`): project submissions with stable identifiers.
  - Clean project fallback if results run has not been computed yet, ensuring unconditional official acceptance test compatibility.
- Spreadsheet Formula Injection Defense (Failure Case F21):
  - `sanitizeCSVField` prefixes cells starting with formula triggers (`=`, `+`, `-`, `@`, `\t`, `\r`) with single quote (`'`), while preserving valid numbers.
- Strict Role Isolation:
  - Anonymous callers receive `401 Unauthorized`.
  - Non-admin personas (participants, judges) receive `403 Forbidden`.
  - Organizers and admins receive `200 OK` with `Content-Type: text/csv; charset=utf-8` and RFC 2616 `Content-Disposition: attachment`.
- API-First Consistency (Prompt 26):
  - Unified `/api/v1/...` routes in `internal/httpapp/server.go` sharing exact same service handlers and authorization checks as HTML routes.
  - Implemented `GET /api/v1/projects` and `GET /api/v1/teams`.
  - Authored valid, comprehensive `openapi.yaml` at repository root documenting all 32 implemented endpoints with zero docs-only imaginary routes.
- Automated Tests:
  - `internal/httpapp/csv_export_test.go`: `TestSanitizeCSVField_F21`, `TestCSVExport_AuthorizationAndFormat`, `TestCSVExport_Evaluations`, `TestCSVExport_ResultsPublishedWithFormulaSanitization`, `TestAPIFirst_Consistency`.
  - `internal/httpapp/human_acceptance_test.go`: `TestHumanAcceptance_FlowI_CSVExport_And_APIFirst` (Flow I).
  - Full repo test suite `go test -count=1 ./...` PASS.
- Container runtime verified: `docker compose build` and live HTTP probes passing.
- Official acceptance runner `python official/run.py .dogfood.toml`: 7/7 PASS (`claimed T1 T2, verified T1 T2`).

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
Review T2 Checkpoint 5 report. Human controls Git. Ready for final pre-freeze reviews, documentation polish, or tagging if requested.

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
- Auditable CSV Export & API-First consistency: complete and verified (internal/httpapp, openapi.yaml, Flow I)
- Final acceptance evidence: verified (7/7 PASS)

## Rule
Update this file after every meaningful checkpoint. The next AI session must read this file before proposing work.
