# Mathematical Specification & Normalization Proof

**Implementation Reference**: `internal/results/engine.go` and `internal/results/service.go`  
**Algorithm Key**: `Z_SCORE_STANDARDIZATION`  
**Algorithm Version**: `v1`  
**Approved Claim Language**: *"Mitigates judge scoring-scale disparities across evaluation cohorts."* (Does not state *"guarantees fairness"*).

---

## 1. Definitions & Symbols

* $\mathcal{P} = \{p_1, p_2, \dots, p_M\}$: The set of all eligible project submissions in the event.
* $\mathcal{J} = \{j_1, j_2, \dots, j_K\}$: The set of all active judges assigned to the event.
* $\mathcal{C} = \{c_1, c_2, \dots, c_D\}$: The set of evaluation criteria defined on the immutable rubric version.
* $w_c \in \mathbb{R}^+$: The positive weight assigned to criterion $c \in \mathcal{C}$ such that $\sum_{c \in \mathcal{C}} w_c > 0$.
* $r_{j,p,c} \in [\min_c, \max_c]$: The raw scalar rating assigned by judge $j$ to project $p$ under criterion $c$.
* $x_{j,p}$: The composite raw weighted score awarded by judge $j$ to project $p$.
* $\mathcal{B}_j$: The evaluation cohort of judge $j$, consisting of all finalized ballots submitted by judge $j$ within the active result run scope.
* $N_j = |\mathcal{B}_j|$: The sample size of judge $j$'s evaluation cohort.
* $\mu_j \in \mathbb{R}$: The sample mean of raw scores assigned by judge $j$.
* $s_j^2 \in \mathbb{R}_{\ge 0}$: The sample variance of raw scores assigned by judge $j$ computed using Bessel's correction.
* $\sigma_j \in \mathbb{R}_{\ge 0}$: The sample standard deviation of raw scores assigned by judge $j$.
* $z_{j,p} \in \mathbb{R}$: The standardized z-score of raw score $x_{j,p}$ relative to judge $j$'s cohort.
* $S_{\text{norm}}(j,p) \in [0.0, 5.0]$: The rescaled normalized score mapped to the standard competition interval.
* $FinalScore(p) \in [0.0, 5.0]$: The aggregated final competition score for project $p$.

---

## 2. Input Domain

1. **Criterion Bounds**: For each criterion $c$, scores must satisfy $\min_c \le r_{j,p,c} \le \max_c$, with $\min_c \ge 0$ and $\max_c > \min_c$.
2. **Criteria Weights**: All weights are non-negative $w_c \ge 0$, and the sum of weights is strictly positive:
   $$\sum_{c \in \mathcal{C}} w_c > 0$$
3. **Ballot State Invariant**: A ballot enters the scoring cohort if and only if its lifecycle status is `SUBMISSION`, `SUBMITTED`, or `CORRECTION`. Unsubmitted drafts (`save_kind = 'DRAFT'`) are excluded.

---

## 3. Judge-Level Weighted Score Construction

For each finalized ballot submitted by judge $j$ on project $p$, the composite raw weighted score $x_{j,p}$ is computed as:

$$x_{j,p} = \frac{\sum_{c \in \mathcal{C}} w_c \cdot r_{j,p,c}}{\sum_{c \in \mathcal{C}} w_c}$$

*Implementation note*: If criterion weights are uniform, $x_{j,p}$ simplifies to the arithmetic mean of criteria scores. The value is rounded to 2 decimal places in database representation and maintained as IEEE 754 double precision (`float64`) in arithmetic calculations.

---

## 4. Cohort Definition

For each judge $j \in \mathcal{J}$, the cohort $\mathcal{B}_j$ is defined as the multiset of all finalized raw scores:

$$\mathcal{B}_j = \{ x_{j,p} \mid \text{ballot}(j, p) \text{ finalized in active event run} \}$$

The cohort sample size is $N_j = |\mathcal{B}_j|$.

---

## 5. Sample Mean Calculation

The sample mean $\mu_j$ for judge $j$'s cohort is the first sample moment:

$$\mu_j = \frac{1}{N_j} \sum_{k=1}^{N_j} x_{j,k}$$

In `internal/results/engine.go` (line 56), the computed mean is rounded to 3 decimal places for numerical stability:

$$\mu_j^* = \frac{\lfloor 1000 \cdot \mu_j + 0.5 \rfloor}{1000}$$

---

## 6. Sample Standard Deviation Calculation

To eliminate estimation bias for small sample cohorts, the sample variance $s_j^2$ is computed using Bessel's correction $(N_j - 1)$:

$$s_j^2 = \frac{1}{N_j - 1} \sum_{k=1}^{N_j} (x_{j,k} - \mu_j)^2 \quad \text{for } N_j \ge 2$$

The sample standard deviation $\sigma_j$ is:

$$\sigma_j = \sqrt{s_j^2}$$

Rounded to 3 decimal places:

$$\sigma_j^* = \frac{\lfloor 1000 \cdot \sigma_j + 0.5 \rfloor}{1000}$$

---

## 7. Z-Score Standardization & Scale Rescaling

When $N_j \ge 2$ and $\sigma_j \ge 10^{-9}$, the standard z-score transformation is applied:

$$z_{j,p} = \frac{x_{j,p} - \mu_j^*}{\sigma_j^*}$$

### Mapping to the Standard Competition Interval $[0.0, 5.0]$
A standard normal variable $Z \sim \mathcal{N}(0, 1)$ typically falls within $[-3.0, +3.0]$. To map standardized scores back to the familiar 5-point hackathon rating scale without distortion:
* Center of distribution: $C = 2.5$
* Scaling factor: $\lambda = 0.8$ (mapping $\pm 3\sigma \to 2.5 \pm 2.4 \in [0.1, 4.9]$)

The normalized score is:

$$S_{\text{raw\_norm}}(j,p) = 2.5 + 0.8 \cdot z_{j,p}$$

With boundary clamping enforced:

$$S_{\text{norm}}(j,p) = \max\left(0.0, \min\left(5.0, \text{round}_3(S_{\text{raw\_norm}}(j,p))\right)\right)$$

Fallback code: `NONE` (`FallbackNone`).

---

## 8. Constant-Score Cohort Fallback (`ZERO_VARIANCE`)

### Theorem 1 (Singularity of Zero Variance)
*If a judge assigns identical scores to all evaluated projects ($x_{j,1} = x_{j,2} = \dots = x_{j,N}$), the sample standard deviation is identically zero ($\sigma_j = 0$), and the z-score transformation $z = \frac{x - \mu}{\sigma} = \frac{0}{0}$ is undefined.*

### Engineering Defense
In `internal/results/engine.go` (line 73):
```go
if stats.StdDev < 1e-9 {
    return rawScore, nil, FallbackZeroVariance, "Judge assigned identical scores across all evaluations (zero variance); z-score is undefined."
}
```
* **Policy**: When $\sigma_j < 10^{-9}$, the engine bypasses division by zero, assigns $z = \text{null}$, returns the unmodified raw weighted score $x_{j,p}$, and records fallback code `ZERO_VARIANCE`.
* **Receipt Tracking**: The event is recorded in the project's audit receipt, incrementing `fallback_count`.

---

## 9. Small-Sample Cohort Fallback (`LOW_N`)

### Theorem 2 (Undefined Degrees of Freedom at $N=1$)
*Sample dispersion cannot be estimated from a single observation ($N_j = 1$), as the denominator $N_j - 1 = 0$.*

### Engineering Defense
In `internal/results/engine.go` (line 69):
```go
if stats.Count < 2 {
    return rawScore, nil, FallbackLowN, "Judge evaluated fewer than 2 projects; sample dispersion cannot be estimated."
}
```
* **Policy**: When a judge has evaluated $N_j < 2$ projects, sample dispersion cannot be determined. The engine assigns $z = \text{null}$, returns the raw score $x_{j,p}$, and records fallback code `LOW_N`.
* **Receipt Tracking**: Increments `fallback_count` and documents the reason in `ballot_audits`.

---

## 10. Project Score Aggregation

For each project $p \in \mathcal{P}$, let $\mathcal{B}_p = \{b_1, b_2, \dots, b_m\}$ denote the set of finalized ballots submitted by all assigned judges. Let $m = |\mathcal{B}_p|$ denote completed reviews.

If $m > 0$:

$$FinalScore(p) = \frac{1}{m} \sum_{k=1}^m S_{\text{norm}}(b_k)$$

$$RawScore(p) = \frac{1}{m} \sum_{k=1}^m x_{b_k}$$

Both values are rounded to 2 decimal places:

$$FinalScore^*(p) = \frac{\lfloor 100 \cdot FinalScore(p) + 0.5 \rfloor}{100}$$

If $m = 0$ (unreviewed project), $FinalScore^*(p) = 0.00$ and $RawScore^*(p) = 0.00$.

---

## 11. Deterministic Tie-Breaking (Total Strict Ordering)

To eliminate non-deterministic rank ordering across database implementations, the platform enforces a strict lexicographical total ordering relation $\succ$:

$$p_a \succ p_b \iff \begin{cases}
FinalScore(p_a) > FinalScore(p_b) \\
\text{or } (FinalScore(p_a) = FinalScore(p_b) \land RawScore(p_a) > RawScore(p_b)) \\
\text{or } (FinalScore(p_a) = FinalScore(p_b) \land RawScore(p_a) = RawScore(p_b) \land CompletedReviews(p_a) > CompletedReviews(p_b)) \\
\text{or } (FinalScore(p_a) = FinalScore(p_b) \land RawScore(p_a) = RawScore(p_b) \land CompletedReviews(p_a) = CompletedReviews(p_b) \land ID(p_a) < ID(p_b))
\end{cases}$$

### Tie Groups
* Projects sharing identical $FinalScore$, $RawScore$, and $CompletedReviews$ are assigned the identical integer `tie_group`.
* Numerical rank numbers increment strictly sequentially ($1, 2, 3, \dots, M$) via the deterministic sort order.

---

## 12. Proof of Determinism

### Theorem 3 (Deterministic Output Parity)
*Given identical set of finalized ballots $\mathcal{B}$, rubric criteria $\mathcal{C}$, and projects $\mathcal{P}$, the algorithm produces bit-for-bit identical final scores, ranks, and tie groups across all computing platforms.*

### Proof
1. **Sorted Pre-Image**: Ballots are sorted by primary key triplet $(ProjectID \text{ ASC}, JudgeUserID \text{ ASC}, BallotVersionID \text{ ASC})$ before manifest computation (`internal/results/engine.go` lines 97–105).
2. **Pure Functional Transformations**: The mapping $x_{j,p} \mapsto S_{\text{norm}}(j,p)$ relies solely on IEEE 754 64-bit floating point arithmetic with deterministic rounding thresholds ($\text{round}_3$ for statistics and $\text{round}_2$ for final scores).
3. **Total Order Tie Resolution**: Because project IDs $ID(p)$ are unique alphanumeric strings, the final comparator step $ID(p_a) < ID(p_b)$ guarantees that no two distinct projects evaluate as equal under the ordering relation.
4. **Conclusion**: The function $f(\mathcal{B}, \mathcal{P}) \to \text{Results}$ is a deterministic mathematical bijection. $\blacksquare$

---

## 13. Cryptographic Verification & Replay

To verify that published rankings are immutable and have not suffered mathematical drift:
1. **Canonical Manifest**: An immutable JSON document contains all contributing raw ballots sorted in deterministic order.
2. **SHA-256 Digest**:
   $$H = \text{SHA-256}(\text{CanonicalManifestBytes})$$
3. **Independent Replay CLI**:
   The independent command:
   ```bash
   dogfood replay <run_id>
   ```
   loads the stored manifest from SQLite, independently re-computes all means, standard deviations, fallbacks, z-scores, project averages, and tie-breaks from source ballots, recomputes $H'$, and checks:
   $$H' \equiv H \quad \land \quad \max_{p} |FinalScore_{\text{stored}}(p) - FinalScore_{\text{recomputed}}(p)| \le 10^{-4}$$
   Exits with status `0` only upon 100% mathematical equality.

---

## 14. Empirical Boundaries & Limitations

1. **Cohort Scale vs. Individual Judges**: Z-score normalization mitigates scale differences (e.g., a "harsh" judge averaging 2.0 vs. a "generous" judge averaging 4.5). It does not alter relative order within a single judge's queue.
2. **Small-n Variance**: When judges evaluate small numbers of projects ($N < 5$), sample standard deviation has higher estimation error. The engine explicitly flags these reviews with audit codes (`LOW_N`, `ZERO_VARIANCE`) on the public receipt.
3. **No Claim of "Guaranteed Fairness"**: In accordance with the project specification guidelines, Dogfood documents that its pipeline *mitigates scoring-scale differences* rather than making unprovable claims of absolute fairness.
