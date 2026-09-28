# Dogfood 2026 — Threat Model & Security Architecture

**Status**: Verified & Enforced  
**Reference Implementations**: `internal/auth/`, `internal/httpapp/`, `internal/judging/`, `internal/results/`  
**Automated Verification**: `internal/httpapp/adversarial_test.go` (Failure Modes F02–F21)

---

## 1. System Assets

1. **Ballots & Criterion Scores**: Private raw evaluations submitted by assigned judges.
2. **Judge Identities & Review Queues**: Confidential judge profiles, queue assignments, and unfinalized draft evaluations.
3. **Participant Identities & Team Formations**: User accounts, team rosters, and invite tokens.
4. **Project Submissions**: Code repository links, demonstration URLs, abstracts, and versioned drafts.
5. **Result Runs & Standings**: Canonical input manifests, SHA-256 digests, aggregated final scores, and leaderboard rankings.
6. **Rubric Versions**: Criteria definitions, point boundaries, and weight configurations.
7. **Exported CSV Files**: Public leaderboard exports, administrative reconciliation tables, and judge audit spreadsheets.
8. **Audit Receipts ("Explain This Rank")**: Transparent cryptographic receipts showing arithmetic breakdowns and fallback traces.

---

## 2. Threat Actors & Capabilities

| Threat Actor | Motivation | Capabilities |
| :--- | :--- | :--- |
| **Malicious Participant** | Inflate own standing, sabotage peer projects, tamper with team rosters, bypass deadlines. | Authenticated HTTP client with participant session token; network access to public routes. |
| **Malicious Judge** | Collude with favored teams, discover peer judges' scores, alter ballots post-publication. | Authenticated HTTP client with judge session token; access to assigned projects. |
| **Compromised Judge Session** | Session token exfiltration via client-side leak or network intercept. | Bearer token access attempting to spoof judge evaluations. |
| **Malicious / Compromised Organizer** | Rewrite historical competition results, mutate rubrics retroactively. | Administrative session cookie with organizer authorization. |
| **Automated Scraper** | Exfiltrate private unsubmitted drafts, enumerate unassigned projects, flood endpoints. | Unauthenticated HTTP crawler executing high-velocity queries. |
| **Accidental Operator Error** | Misconfigure rubric weights, prematurely publish incomplete scores, trigger race conditions. | Authorized administrator operating web interface under operational pressure. |

---

## 3. Comprehensive Threat Analysis Matrix

### 3.1 Ballot & Scoring Integrity

#### Threat: Finalized Ballot Overwrite (Tampering with Submitted Scores)
* **Attack Surface**: `POST /api/v1/assignments/{id}/ballot/submit` and `POST /api/v1/assignments/{id}/ballot`
* **Threat Mechanism**: A judge submits a score, waits to see other projects or standings, and attempts to alter their evaluation.
* **Existing Defense**: Hard immutability invariant in `internal/judging/service.go`. Once a ballot version is saved with `save_kind IN ('SUBMISSION', 'SUBMITTED', 'CORRECTION')`, the assignment is locked. Any subsequent save or submit attempt returns `409 Conflict` (`ErrBallotSubmitted`).
* **Verified Evidence**: Adversarial Test Case `F13` in `internal/httpapp/adversarial_test.go` and live verification returning HTTP `409 Conflict`.
* **Residual Risk**: Organizer manual database override (mitigated by immutable result run input digests).

#### Threat: Ballot Stuffing & Unauthorized Scoring
* **Attack Surface**: `POST /api/v1/assignments/{id}/ballot/submit`
* **Threat Mechanism**: An attacker attempts to submit a ballot for a project without an official assignment.
* **Existing Defense**: `SaveDraftBallot` and `SubmitBallot` verify that an assignment record exists, is assigned to the authenticated user ID, and matches the target project. Submissions without valid assignments return `404 Not Found` or `403 Forbidden`.
* **Verified Evidence**: Unit test suite `internal/judging/service_test.go`.
* **Residual Risk**: Low; strictly bounded by assignment table constraints.

#### Threat: Sybil Community Voting
* **Status**: **NOT APPLICABLE (FEATURE INTENTIONALLY DORMANT)**
* **Analysis**: Tier 3 community voting was evaluated at the H48 Scope Gate and intentionally left unstarted to eliminate Sybil vote-stuffing attack vectors entirely.

---

### 3.2 Peer Isolation & Confidentiality

#### Threat: Peer Judge Score Leakage (Score Anchoring / Bias)
* **Attack Surface**: `GET /api/judge/scores?judge={other_judge}` and `GET /api/v1/assignments/{id}/ballot`
* **Threat Mechanism**: Judge B queries Judge A's scores or assignments before evaluating the same project, biasing their judgment.
* **Existing Defense**: `handleJudgeScores` extracts the authenticated session token from SQLite. If the caller requests another judge's ID and lacks organizer privileges, it strictly returns `403 Forbidden`. Judge B querying Judge A's assignment ballot receives `403 Forbidden`.
* **Verified Evidence**: Adversarial Test Case `F04` (`internal/httpapp/adversarial_test.go`) and official test harness `T2 judge cannot see peer scores` (**PASS**).
* **Residual Risk**: Out-of-band communication between judges in real life (outside software boundary).

#### Threat: Draft Leakage to Public Gallery
* **Attack Surface**: `GET /projects` and `GET /api/v1/projects`
* **Threat Mechanism**: An attacker enumerates draft projects or unsubmitted work before the deadline.
* **Existing Defense**: Public gallery queries join on `submissions` with explicit filter `s.state = 'SUBMITTED'`. Unsubmitted drafts (`state = 'DRAFT'`) are never returned to anonymous or peer users.
* **Verified Evidence**: Adversarial Test Case `F11` in `internal/httpapp/adversarial_test.go`.
* **Residual Risk**: None; database queries filter on submission state at the SQL projection layer.

---

### 3.3 Access Control & Team Integrity

#### Threat: Cross-Team Project Mutation
* **Attack Surface**: `POST /projects/{id}` and `POST /api/v1/projects/{id}`
* **Threat Mechanism**: Participant from Team Beta attempts to edit or withdraw Team Alpha's project.
* **Existing Defense**: `handleProjectDetail` and update handlers check active team membership in `team_memberships` table (`WHERE team_id = ? AND user_id = ? AND left_at IS NULL`). Non-members receive `403 Forbidden` or `405 Method Not Allowed`.
* **Verified Evidence**: Adversarial Test Case `F06` in `internal/httpapp/adversarial_test.go`.
* **Residual Risk**: None; enforced inside transaction boundary.

#### Threat: Team Capacity Circumvention (Concurrent Join Race)
* **Attack Surface**: `POST /teams/{id}/join`
* **Threat Mechanism**: Attackers send concurrent HTTP requests to exceed the 4-member limit.
* **Existing Defense**: SQLite write transactions enforce a strict count check `SELECT COUNT(*) FROM team_memberships WHERE team_id = ? AND left_at IS NULL`. If count $\ge 4$, join is rejected with `400 Bad Request`.
* **Verified Evidence**: Adversarial Test Case `F09` in `internal/httpapp/adversarial_test.go`.
* **Residual Risk**: None; serialized by SQLite single-writer transactional lock.

#### Threat: Deadline Gaming / Late Submission
* **Attack Surface**: `POST /projects/new` and `POST /projects/{id}/submit`
* **Threat Mechanism**: Submitting projects after the event submission window has closed.
* **Existing Defense**: `isSubmissionOpen(ctx)` validates server UTC time against `events.submissions_close_at`. Requests after closure return `403 Forbidden`.
* **Verified Evidence**: Adversarial Test Case `F07` and official check `T1 closed event refuses submissions` (**PASS**).
* **Residual Risk**: Clock skew on host OS (mitigated by containerizing time with standard UTC clock).

---

### 3.4 Data Portability & Client-Side Execution

#### Threat: CSV Formula Injection (Spreadsheet Execution Vector)
* **Attack Surface**: `GET /api/v1/export.csv` and `GET /api/v1/export/evaluations.csv`
* **Threat Mechanism**: A malicious participant titles their project `=CMD|' /C calc'!A0` or `+SUM(A1:A10)`. When an organizer opens the exported CSV in Microsoft Excel or Google Sheets, the formula executes.
* **Existing Defense**: `sanitizeCSVField` in `internal/httpapp/server.go` inspects every exported cell. If the first character is an execution trigger (`=`, `+`, `-`, `@`, `\t`, `\r`), it prepends an apostrophe `'`, converting the payload to a harmless text literal.
* **Verified Evidence**: Adversarial Test Case `F21` in `internal/httpapp/adversarial_test.go`.
* **Residual Risk**: None; neutralized across all CSV export paths.

---

### 3.5 Results Tampering & Cryptographic Provability

#### Threat: Retroactive Result Manipulation / Undetected Calculation Drift
* **Attack Surface**: Published leaderboard and results database tables.
* **Threat Mechanism**: An administrator or attacker modifies scores directly in the database or alters the calculation algorithm.
* **Existing Defense**:
  1. **Canonical Manifest**: Every result run stores `input_manifest_json` listing all contributing raw ballots in deterministic sorted order.
  2. **SHA-256 Digest**: `input_digest` cryptographically locks the exact input dataset.
  3. **Independent Replay CLI**: `dogfood replay <run_id>` reconstructs results from scratch, recomputes the SHA-256 digest, and validates 100% mathematical equality. Any altered ballot causes immediate digest mismatch and nonzero exit code.
* **Verified Evidence**: Live CLI replay verification (`run_573e0de3fc3f78c2`, digest `8eb32db3...`, 41 projects, **100% Mathematical Equality Verified**).
* **Residual Risk**: None; mathematical tampering is cryptographically detectable by any auditor.

---

## 4. Threat Matrix Summary

| Threat ID | Threat Description | Attack Vector | Enforced Defense | Verified Test | Status |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **T-01** | Finalized Ballot Overwrite | `POST /api/v1/assignments/{id}/ballot/submit` | State lock in `ballot_versions` returning HTTP 409 | `F13` | **DEFENDED** |
| **T-02** | Peer Judge Score Leak | `GET /api/judge/scores?judge=X` | Session user matching returning HTTP 403 | `F04`, Harness T2 | **DEFENDED** |
| **T-03** | Cross-Team Project Edit | `POST /projects/{id}` | Active membership verification returning HTTP 403 | `F06` | **DEFENDED** |
| **T-04** | Post-Deadline Submission | `POST /projects/new` | UTC window comparison returning HTTP 403 | `F07`, Harness T1 | **DEFENDED** |
| **T-05** | Team Size Exceeded | `POST /teams/{id}/join` | Transactional capacity check returning HTTP 400 | `F09` | **DEFENDED** |
| **T-06** | Draft Project Leakage | `GET /projects` | Projection query filtering `state = 'SUBMITTED'` | `F11` | **DEFENDED** |
| **T-07** | Invalid Rubric Weights | `POST /api/v1/organizer/rubrics` | Criteria validation returning HTTP 400 | `F12` | **DEFENDED** |
| **T-08** | Constant Judge Div-by-Zero | Normalization Engine | Zero-variance check with fallback code | `F14` | **DEFENDED** |
| **T-09** | Small-n Sample Error | Normalization Engine | $N < 2$ low-n check with fallback code | `F15` | **DEFENDED** |
| **T-10** | CSV Formula Injection | `GET /api/v1/export.csv` | Prepended apostrophe escaping (`=`, `+`, `-`, `@`) | `F21` | **DEFENDED** |
| **T-11** | Covert Result Tampering | Database direct write | SHA-256 canonical digest & independent CLI replay | Replay CLI | **DEFENDED** |
| **T-12** | Sybil Community Voting | N/A | Feature formally declared dormant | Scope Gate H48 | **NOT APPLICABLE** |
