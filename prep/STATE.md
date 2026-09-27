# STATE

## Current phase
T2 JUDGING: CHECKPOINT 3 (JUDGE RUBRIC + BALLOT LIFECYCLE) COMPLETE

## Last checkpoint passed
T2 Checkpoint 3: Complete Rubric + Ballot lifecycle implemented and verified:
- Service package `internal/judging` with cryptographic canonical hashing, rubric versioning, and immutable ballot revisions.
- Warm human evaluation interface (`web/templates/evaluation.html`) with real-time score calculations and read-only locked ballot views.
- HTTP endpoints: `/evaluations/{id}` (GET/POST), `/api/assignments/{id}/ballot` (GET/POST draft/POST submit), `/api/events/{id}/rubric`, `/api/events/{id}/rubrics`, `/api/rubrics/{id}/publish`, and `/api/organizer/assignments/run`.
- Real ballot retrieval in `/api/judge/scores` with strict peer isolation and 403 enforcement.
- Full automated test suite passing (`go test -count=1 ./...`), new Flow G human acceptance testing complete, official checker passes 7/7.

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
Review T2 Checkpoint 3 report. Human controls Git. Ready for T2 Checkpoint 4 / Leaderboard / Differentiators if requested.

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
- Clean/reviewer-environment offline release proof: PENDING dedicated release checkpoint
- T2 judging schema: complete and verified (Migration 0002)
- T2 judging engine: complete and verified (internal/assignment)
- T2 rubric & ballot lifecycle: complete and verified (internal/judging, templates, HTTP API)
- Replay differentiator: not started
- Final acceptance evidence: verified (7/7 PASS)

## Rule
Update this file after every meaningful checkpoint. The next AI session must read this file before proposing work.
