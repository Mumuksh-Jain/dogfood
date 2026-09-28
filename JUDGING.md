# Judging, Rubrics & Defensible Scoring Guide — Dogfood 2026

Dogfood 2026 implements a defensible, mathematically transparent judging architecture. Every score is derived through an auditable, reproducible pipeline that eliminates reviewer leniency bias while preserving cryptographic provenance.

---

## 1. Judging Lifecycle

```
[Organizer: Configure Rubric & Weights]
                    │
                    ▼
[Organizer: Deterministic Assignment Engine] ───► Assigns Judges (Track & Capacity Constrained)
                    │
                    ▼
[Judge: Evaluates Assigned Projects] ───────────► Saves DRAFT Ballot Revisions
                    │
                    ▼
[Judge: Final Ballot Submission] ───────────────► Immutable SUBMISSION (Assignment COMPLETED)
                    │
                    ▼
[Organizer: Compute Defensible Standings] ───────► Z-Score Normalization + Fallback Policy
                    │
                    ▼
[Public: Leaderboard & "Explain This Rank"] ────► Mathematical Audit Receipts at /results
                    │
                    ▼
[CLI: Independent Replay Verification] ─────────► 'dogfood replay' matches SHA-256 Digest
```

---

## 2. Rubric Structure & Criteria Weighting

### Canonical Criteria Dimensions
By default, projects are evaluated across four dimensions on a 0.0 to 5.0 scale:

| Criterion ID | Name | Description | Default Weight | Range |
| :--- | :--- | :--- | :--- | :--- |
| `functionality` | **Functionality & Feature Completeness** | Core feature completeness, stability, and robustness. | 1.0x | 0.0 – 5.0 |
| `quality` | **Code Quality & Technical Design** | Clean architecture, documentation, test suite, and repo structure. | 1.0x | 0.0 – 5.0 |
| `innovation` | **Innovation & Creativity** | Technical novelty, original concept, and unique problem solving. | 1.0x | 0.0 – 5.0 |
| `impact` | **Impact & Practical Usability** | Real-world utility, user experience, and market potential. | 1.0x | 0.0 – 5.0 |

### Weighted Score Formula
The total weighted score for a ballot is calculated as:
$$\text{Weighted Score} = \frac{\sum_{i=1}^M (\text{score}_i \times \text{weight}_i)}{\sum_{i=1}^M \text{weight}_i}$$

### Immutability & Configuration Hashing
Every rubric revision is hashed using canonical SHA-256:
* All criteria are sorted alphabetically by criterion ID.
* The normalized JSON string is hashed: $\text{SHA-256}(\text{criteria\_json}) \rightarrow \text{configuration\_hash}$.
* When an organizer updates weights, a new version (e.g. `v2`) is published.
* **Hard Invariant:** Ballots submitted under version `v1` remain bound to `v1`. Rubrics are strictly append-only and never mutated in place.

---

## 3. Deterministic Judge Assignment Engine

Judge allocations are generated using a deterministic matching engine (`internal/assignment`):
1. **Constraints Enforced**:
   * **Reviewer Capacity:** A judge is never assigned more projects than their configured `judge_profiles.capacity`.
   * **Track Eligibility:** A judge is only assigned projects belonging to tracks where `judge_track_eligibility.eligible = 1`.
   * **Conflict of Interest Avoidance:** A participant can never be assigned to evaluate their own team's project.
2. **Deterministic Seed:** Sorting of projects and judges incorporates an event-scoped deterministic seed. Running the assignment engine repeatedly on identical data produces identical assignments.
3. **Structured Infeasibility:** If the collective judge capacity or track distribution cannot satisfy the target review count (e.g. 3 reviews per project), the run completes with status `INFEASIBLE` and records structured diagnostic details.

---

## 4. Ballot Lifecycle & Peer Isolation

1. **Draft Autosave (`save_kind = 'DRAFT'`)**:
   * Judges can save in-progress evaluations at any time via `POST /api/v1/assignments/{id}/ballot/draft`.
   * Scores may be partially filled; draft revisions are recorded with monotonic version numbers in `ballot_versions`.
   * Assignment status transitions from `ASSIGNED` to `STARTED`.
2. **Final Submission (`save_kind = 'SUBMISSION'`)**:
   * All required criteria must be present and within bounds $[0.0, 5.0]$.
   * Total and weighted scores are computed and frozen.
   * Assignment status transitions to `COMPLETED`.
3. **Hard Immutability**:
   * Once a `SUBMISSION` ballot is committed, any subsequent draft or submit requests on that assignment are rejected with `409 Conflict` (`ErrBallotSubmitted`).
4. **Peer Isolation & Role Guards**:
   * Judges can only view and evaluate assignments explicitly assigned to their `user_id`.
   * Invariant: A judge attempting to access or submit another judge's assignment receives `403 Forbidden`.
   * Invariant: Participants attempting to access judge routes receive `403 Forbidden`.

---

## 5. Defensible Score Aggregation & Normalization

### The Normalization Problem
Raw judge scores cannot be directly averaged without introducing bias:
* Some judges score generously (mean 4.5), while others score strictly (mean 2.5).
* Some judges use the entire scale (high variance), while others assign similar scores to all projects (low variance).

### Z-Score Standardization
For each judge $j$ who submitted scores across $N$ projects $x_1, \dots, x_N$:

1. **Judge Mean ($\mu_j$)**:
   $$\mu_j = \frac{1}{N} \sum_{k=1}^N x_k$$

2. **Judge Sample Standard Deviation ($\sigma_j$)**:
   $$\sigma_j = \sqrt{\frac{1}{N} \sum_{k=1}^N (x_k - \mu_j)^2}$$

3. **Standardized Z-Score ($z_{jk}$)**:
   $$z_{jk} = \frac{x_{jk} - \mu_j}{\sigma_j}$$

4. **Normalized Score ($S_{\text{norm}}$)**:
   $$S_{\text{norm}} = \text{clamp}\left(50.0 + 15.0 \times z_{jk},\ 0.0,\ 100.0\right)$$

### Explicit Fallback Policies
To prevent mathematical anomalies (such as division by zero), the scoring engine implements explicit fallback behaviors:

| Fallback Code | Condition | Behavior & Rationale |
| :--- | :--- | :--- |
| `STANDARD` | $N \ge 3$ and $\sigma_j \ge 10^{-4}$ | Full z-score standardization applied. |
| `CONSTANT_SCORE_FALLBACK` | $\sigma_j < 10^{-4}$ (Constant Judge) | Z-score division by zero is avoided. Score is normalized directly to percentage scale: $\frac{x}{5.0} \times 100.0$. |
| `SMALL_SAMPLE_FALLBACK` | $N < 3$ reviews | Sample size is too small for meaningful variance calculation. Falls back to raw percentage: $\frac{x}{5.0} \times 100.0$. |

The final score for a project is the arithmetic mean of its effective normalized scores:
$$\text{Final Score} = \frac{1}{K} \sum_{m=1}^K S_{\text{norm}, m}$$

---

## 6. Deterministic Ranking & Tie Resolution

1. **Primary Ordering:** Projects are sorted in descending order of `final_score`.
2. **Tie Tolerance:** Scores differing by less than $10^{-4}$ ($0.0001$) are treated as mathematical ties.
3. **Tie Groups:** Tied projects share the exact same `rank` and are assigned a shared `tie_group` integer.
4. **Deterministic Tie-Breaking:** Secondary sort order within a tie group is determined deterministically by `raw_score DESC`, followed by stable `project_id ASC`.

---

## 7. "Explain This Rank" Audit Receipts

Every published result entry includes a comprehensive mathematical receipt stored in `result_entries.explanation_json` and displayed at `/results/{run_id}/explain/{project_id}`:
* Itemized list of all judge ballots contributing to the score.
* Per-judge $\mu_j$, $\sigma_j$, and ballot count.
* Raw score and standardized z-score per ballot.
* Explicit `fallback_code` and human-readable `fallback_reason`.
* Step-by-step formula breakdown showing both raw and normalized averages.
* SHA-256 `input_digest` proving data provenance.

---

## 8. Cryptographic Replay Verification

To independently verify results without trusting server state:
```bash
go run ./cmd/dogfood replay <run_id>
```
The replay command:
1. Re-assembles the canonical `InputManifest` from raw database ballots.
2. Re-computes the SHA-256 digest and asserts `digest_matches == true`.
3. Runs the complete normalization and ranking arithmetic independently.
4. Asserts that all final scores, ranks, and tie groups match with zero discrepancies.
