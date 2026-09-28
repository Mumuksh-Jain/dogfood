# DOGFOOD: Building the Platform That Judges You

An Engineering Retrospective on Hackathon Judging, Reproducibility, and Defensible Systems.

---

## 1. The Problem Everyone Thinks Is Simple

Every engineer who has participated in or organized a hackathon recognizes the scene.

Forty teams spend thirty-six sleepless hours building working prototypes. Submissions close. A panel of twenty volunteer judges arrives with varying backgrounds, distinct grading philosophies, and differing levels of scrutiny. Two hours later, a spreadsheet appears. Scores are sorted in descending order, prizes are handed out, and everyone leaves.

At first glance, the software requirement appears almost trivial. You need a form to collect submissions, a table to store integer ratings, an average formula, and a leaderboard sorted from highest to lowest. A junior developer could build a prototype in an afternoon.

The difficulty emerges when you ask a simple question that every disappointed participant wants to ask:

> *"Why did this project win, why did my project place fourth, and can you prove that your software calculated those numbers correctly?"*

At that moment, the apparent simplicity collapses. 

In practice, human judging is full of systemic noise. One judge considers a score of 3 out of 5 to be an enthusiastic endorsement for a prototype; another gives 5 out of 5 to anything that compiles. One judge evaluates ten projects and develops a calibrated baseline; another evaluates two projects before having to leave. If project A is reviewed by three lenient judges and project B is reviewed by three demanding judges, a naive arithmetic mean does not measure the quality of the projects. It measures the luck of the draw.

Worse, when platforms attempt to patch these problems on the fly—adjusting a rubric weight after judging starts, manually reassigning a dropped reviewer, or tweaking a database record—they silently corrupt the mathematical validity of the competition. The leaderboard changes, but nobody can trace why.

This is the problem everyone recognizes. It is the problem almost every hackathon platform avoids solving.

---

## 2. Where the Simple Version Breaks

When we began building Dogfood, our first step was not to start sketching user interfaces. It was to analyze where naive hackathon platforms structurally fail under operational pressure.

We identified five specific failure modes that recurringly undermine trust in hackathons:

1. **The Scale-Disparity Distortion**: A raw score of $4.0$ from a judge whose average is $4.5$ actually represents a below-average evaluation. Conversely, a raw score of $4.0$ from a judge whose average is $2.5$ represents an outstanding evaluation. Averaging these numbers together without normalization treats different measurement scales as identical units.
2. **The Peer-Anchoring Vulnerability**: When a platform allows judges to see what other judges have scored before submitting their own ballot, independent evaluation vanishes. Later reviewers naturally anchor their scores to earlier reviewers, destroying the statistical value of multiple independent evaluations.
3. **The Retroactive Rubric Mutation**: An organizer realizes midway through judging that "Innovation" should have been weighted at 40% instead of 20%, edits the rubric in place, and updates the database row. The database now evaluates ballots submitted under Version 1 using the criteria of Version 2, permanently destroying historical auditability.
4. **The Silent Overwrite**: A judge submits a ballot, discusses the project informally over lunch, opens the portal again, and modifies the score. If ballots are mutable in place, no one can prove which evaluation was official, whether deadlines were respected, or whether scores were altered after seeing partial standings.
5. **The Unverifiable Leaderboard**: A leaderboard displays a final score of `3.82`. When asked to show the exact arithmetic that produced `3.82`, organizers can only point to the final database record. The intermediate steps, normalization parameters, and tie-breaking choices are lost.

These are not superficial user-interface bugs. They are architectural defects in how data is modeled, stored, and calculated.

---

## 3. The Dilemma

Designing Dogfood forced us to confront a genuine engineering dilemma:

> **Do we build a wide platform with dozens of features, or a deep platform with mathematical integrity?**

In a 72-hour development cycle, the pressure to expand surface area is intense. It is tempting to quickly build community voting, social media sharing, automated certificates, webhooks, and complex team chat widgets. These features look impressive in screenshots.

However, every hour spent building cosmetic features is an hour not spent securing the transaction boundaries of the judging engine. If a participant can overwrite another team's project because authorization checks were rushed, the platform fails. If a judge can query peer evaluations because API endpoints lack role isolation, the judging is compromised. If a division-by-zero error occurs when a judge awards identical scores across their cohort, the leaderboard crashes.

We made an explicit decision early in the process: **Dogfood would prioritize judging integrity, role isolation, and verifiable reproducibility over feature sprawl.**

---

## 4. Choosing What Must Be Trustworthy

Rather than attempting to satisfy every hypothetical feature request, we established four core non-negotiable guarantees:

1. **Deterministic Assignment**: The matching of judges to projects must respect individual reviewer capacities and track domain expertise while remaining completely reproducible from the input graph.
2. **Immutable Ballots and Rubrics**: Rubrics and ballots must never be updated in place. Once submitted, an evaluation is cryptographically sealed as an immutable version.
3. **Defensible Normalization**: Raw scores must be standardized against each judge's historical scoring distribution to mitigate grading bias, with explicit mathematical fallbacks for edge cases.
4. **Independent Cryptographic Replay**: Any participant, judge, or auditor must be able to download the raw input manifest, run an independent program, and reproduce the published leaderboard bit-for-bit.

---

## 5. Designing the Judging Engine

### Assignment Strategy
The assignment engine (`internal/assignment/engine.go`) treats reviewer allocation as a constrained bipartite matching problem. 

Judges declare a maximum review capacity and a set of eligible track domains. When the organizer initiates an assignment run:
* Projects are sorted deterministically by track and project identifier.
* Judges are sorted deterministically by unique user identifier.
* The engine performs a greedy allocation that balances workload while strictly enforcing track eligibility and preventing self-assignment or duplicate assignments.
* If the problem is mathematically infeasible—for example, if a specialized track has more projects than eligible reviewer capacity—the engine does not guess or silently drop projects. It generates a structured infeasibility record (`INSUFFICIENT_TRACK_JUDGES` or `INSUFFICIENT_CAPACITY`) that explains the exact bottleneck to the organizer.

### Versioned Rubrics and Ballot Lifecycles
In Dogfood, the `rubrics` and `ballots` tables are not mutable state buckets; they are append-only version ledgers.

When an organizer authors a rubric, the platform stores an immutable `rubric_versions` record containing the exact criteria, weights, point boundaries, and an automated SHA-256 `configuration_hash`. If the organizer later changes the weights, the system does not modify the existing record; it creates Version 2.

When a judge evaluates a project:
* They can save interim drafts (`save_kind = 'DRAFT'`), which are kept strictly confidential.
* When they click "Submit Ballot", the ballot transition is permanent. The record is stored with `save_kind = 'SUBMISSION'` and locked to the exact `rubric_version_id` active at the moment of evaluation.
* Any subsequent attempt by the judge to submit another ballot for that assignment returns `HTTP 409 Conflict`. A judge cannot alter a submitted score after the fact.

---

## 6. The Normalization Mathematics

To mitigate scale disparities between lenient and strict judges, Dogfood implements a z-score standardization engine (`internal/results/engine.go`).

### The Base Pipeline
For each judge $j$, the engine collects their cohort of finalized raw weighted scores $\mathcal{B}_j = \{x_{j,1}, x_{j,2}, \dots, x_{j,N}\}$.

It computes the sample mean $\mu_j$ and, using Bessel's correction $(N-1)$ for unbiased estimation, the sample standard deviation $\sigma_j$:

$$\mu_j = \frac{1}{N} \sum_{k=1}^N x_{j,k}, \qquad \sigma_j = \sqrt{\frac{1}{N-1} \sum_{k=1}^N (x_{j,k} - \mu_j)^2}$$

For each ballot, the standardized rating is computed as:

$$z_{j,p} = \frac{x_{j,p} - \mu_j}{\sigma_j}$$

Because raw z-scores typically range between $-3.0$ and $+3.0$, the engine rescales the score onto the familiar $0.0$ to $5.0$ hackathon rating scale centered at $2.5$:

$$S_{\text{norm}}(j,p) = \max\left(0.0, \min\left(5.0, 2.5 + 0.8 \cdot z_{j,p}\right)\right)$$

### Where Naive Mathematics Fails: The Edge Cases
Real-world data breaks idealized formulas. A robust engine must account for situations where standard statistics are undefined:

1. **The Constant Judge ($\sigma = 0$)**: Suppose a judge evaluates four projects and awards a score of $3.0$ to all of them. The standard deviation is zero. A naive z-score formula attempts to compute $\frac{3.0 - 3.0}{0}$, resulting in `NaN` or a server panic. Dogfood detects this condition ($\sigma < 10^{-9}$), records fallback code `ZERO_VARIANCE`, and falls back to the judge's raw score without throwing an error.
2. **The Small Sample ($N < 2$)**: When a judge only completes a single review before dropping out, sample variance cannot be estimated because the denominator $(N-1)$ is zero. The engine detects $N < 2$, records fallback code `LOW_N`, and falls back to the raw score.
3. **Transparent Audit Receipts**: Rather than hiding these edge cases, Dogfood documents every fallback on the project's public "Explain This Rank" page, listing the exact fallback code and reason for every individual ballot.

### Deterministic Tie-Breaking
When two projects finish with identical normalized averages, tie-breaking must not depend on database insertion order or random memory layout. Dogfood applies a deterministic four-tier lexicographical comparator:

$$\text{Final Score (DESC)} \longrightarrow \text{Raw Score Average (DESC)} \longrightarrow \text{Review Count (DESC)} \longrightarrow \text{Project ID (ASC)}$$

Tied projects share an identical `tie_group`, but ranking is strictly total and reproducible across any computing architecture.

---

## 7. Making the Boundaries Defensible

A judging engine is only as defensible as its authorization model. A single leaked endpoint can compromise an entire event.

Dogfood enforces hard authorization boundaries at the database and HTTP middleware layers:

* **Peer Judge Isolation**: In `server.go`, `handleJudgeScores` verifies caller identity against the session table. If Judge B attempts to query `GET /api/judge/scores?judge=judge_a`, the server returns `403 Forbidden`. If Judge B attempts to submit a ballot for Judge A's assignment, the server returns `403 Forbidden`.
* **Participant Restrictions**: Participants are blocked with `403 Forbidden` from all judging endpoints, organizer progress rooms, and administrative APIs.
* **Team Isolation**: Team members can only modify projects associated with their own active team. Cross-team mutation attempts return `403 Forbidden`.
* **Spreadsheet Injection Defense**: Hackathon organizers frequently export leaderboards to Excel or Google Sheets. If a malicious participant titles their project `=cmd|' /C calc'!A0`, opening the CSV can execute arbitrary commands on the organizer's machine. Dogfood's CSV export engine scans every cell; if the first character is an execution trigger (`=`, `+`, `-`, `@`), it automatically prepends an apostrophe `'` to neutralize the payload (Failure Mode F21).

---

## 8. Making Results Reproducible

Most hackathon platforms treat the leaderboard as transient application state. In Dogfood, publishing results is an immutable cryptographic event.

When an organizer publishes official standings:
1. The engine constructs a **Canonical Input Manifest** containing every contributing ballot, criterion score, and judge identifier, sorted in deterministic order.
2. The platform hashes the manifest using SHA-256 to create an immutable `input_digest`.
3. The result run is stored with `status = 'PUBLISHED'`.
4. If a correction is necessary—such as accommodating an emergency late ballot—the existing run is never mutated. A new run is created that explicitly records `supersedes_result_run_id`, preserving the complete historical audit trail.

### Independent Verification via CLI Replay
To prove that our software does not hide arbitrary adjustments, Dogfood ships with an independent verification command:

```bash
docker compose exec dogfood /dogfood replay
```

The command loads the immutable manifest, discards the stored results, recomputes all means, standard deviations, fallbacks, z-scores, project averages, and tie-breaks from scratch, and verifies that the computed SHA-256 digest and numerical ranks match the published record with 100% mathematical equality.

In our final release candidate, verifying the 41 projects from the canonical fixture produced:

```text
=== DOGFOOD 2026 AUDITABLE REPLAY ENGINE ===
Verifying Result Run: run_573e0de3fc3f78c2
Canonical Input Digest Expected: 8eb32db389c6ee8adab8f044a8e311b13f7003ced1bbbb3eaab9e09c1e5aa33c
Canonical Input Digest Computed: 8eb32db389c6ee8adab8f044a8e311b13f7003ced1bbbb3eaab9e09c1e5aa33c
Digest Integrity: PASS (Cryptographically Identical)
Projects Evaluated: 41
FINAL VERDICT: PASS (100% Mathematical Equality Verified)
```

---

## 9. The Decision to Stop Building: The Hour-60 Lesson

At Hour 60 of the hackathon, we reached a critical project crossroad.

Tier 1 (Core Portal) and Tier 2 (Judging Engine, Normalization, Replay) were fully functional and passing all automated tests. We had twelve hours remaining. We could have used those twelve hours to rush an initial implementation of Tier 3 (Community Voting) or Tier 4 (Webhooks and Certificates).

We chose not to.

We recognized that adding uncontracted features at Hour 60 would introduce fresh regression risks to a judging engine that was already mathematically sound and stable. Instead, we executed a formal **H60 Feature Freeze**:
* We locked the schema and sealed the API routes.
* We eliminated all external network and CDN dependencies, ensuring all assets are embedded into the Go binary.
* We packaged the runtime into a minimal `FROM scratch` Docker image with a native, shell-free `/dogfood healthcheck` command.
* We authored a standalone operability fallback configuration (`compose.preloaded.yaml`) with `pull_policy: never` and pre-built image tarballs for air-gapped evaluation environments.
* We implemented an adversarial test campaign covering 11 critical failure modes (F02–F21).

Knowing **what not to build** was the most consequential engineering decision of the project. It allowed us to deliver a platform that is verifiably correct rather than superficially broad.

---

## 10. What We Cut and What We Would Build Next

Honest engineering requires transparency about what was left out:

* **Tier 3 (Community Voting & Comments)**: Intentionally declared dormant. We chose not to ship a partial voting implementation that lacked robust Sybil resistance.
* **Tier 4 (Webhooks & Certificates)**: Intentionally declared dormant. Webhooks introduce asynchronous retry queues and outbound network failure modes that conflict with self-contained, offline-first guarantees.
* **Pairwise Judging Mode**: Deferred. While pairwise comparisons offer interesting theoretical properties for small project sets, criteria-weighted rubric evaluation with z-score normalization provided stronger auditability for large multi-track cohorts.

If we had another development cycle, our first priority would be implementing **formal cryptographic ballot signing** using asymmetric key pairs generated per judge, allowing third-party auditors to verify not only that the calculation was correct, but that individual ballots were signed by specific verified hardware tokens.

---

## 11. Final Verification Evidence

The released candidate (`268ac7a`) has been independently validated:

| Verification Target | Command / Harness | Result |
| :--- | :--- | :--- |
| **Go Test Suite** | `go test -count=1 ./...` | **8/8 Packages PASS** (~1.3s) |
| **Official Acceptance** | `python official/run.py .dogfood.toml` | **7/7 Checks PASS** (`claimed T1 T2, verified T1 T2`) |
| **Scratch Healthcheck** | `docker inspect --format "{{json .State.Health}}"` | **HEALTHY** (Native exec probe, 0 failing streak) |
| **Replay Parity** | `docker compose exec dogfood /dogfood replay` | **100% Mathematical Equality Verified** |
| **Offline Operability** | `docker compose -f compose.preloaded.yaml up -d` | **7/7 Checks PASS** (`pull_policy: never`) |

---

## 12. The Final Lesson

The central lesson of building Dogfood is that the difficult part of a hackathon platform was never the visual interface.

The difficult part was building a judging engine that respects its own constraints under pressure: an engine that refuses to leak peer scores, refuses to overwrite finalized ballots, refuses to distort unequal grading scales, and can independently prove to every participant why the leaderboard looks the way it does.

We did not build another hackathon gallery. We built the platform that can judge you—and prove that it did so fairly.
