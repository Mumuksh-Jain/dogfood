package results

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

// ComputeJudgeCohortStats computes mean, sample variance, and sample std dev for each judge.
func ComputeJudgeCohortStats(ballots []RawBallotRecord) map[string]JudgeCohortStats {
	byJudge := make(map[string][]float64)
	for _, b := range ballots {
		byJudge[b.JudgeUserID] = append(byJudge[b.JudgeUserID], b.RawScore)
	}

	stats := make(map[string]JudgeCohortStats, len(byJudge))
	for judgeID, scores := range byJudge {
		n := len(scores)
		if n == 0 {
			continue
		}

		sum := 0.0
		minVal := scores[0]
		maxVal := scores[0]
		for _, s := range scores {
			sum += s
			if s < minVal {
				minVal = s
			}
			if s > maxVal {
				maxVal = s
			}
		}
		mean := sum / float64(n)

		variance := 0.0
		stdDev := 0.0
		if n >= 2 {
			varSum := 0.0
			for _, s := range scores {
				diff := s - mean
				varSum += diff * diff
			}
			// Sample variance with Bessel's correction (n - 1)
			variance = varSum / float64(n-1)
			stdDev = math.Sqrt(variance)
		}

		stats[judgeID] = JudgeCohortStats{
			JudgeUserID: judgeID,
			Count:       n,
			Mean:        math.Round(mean*1000) / 1000,
			Variance:    math.Round(variance*1000) / 1000,
			StdDev:      math.Round(stdDev*1000) / 1000,
			MinScore:    minVal,
			MaxScore:    maxVal,
		}
	}

	return stats
}

// NormalizeBallot applies z-score standardization with explicit low-n and zero-variance fallbacks.
func NormalizeBallot(rawScore float64, stats JudgeCohortStats) (float64, *float64, string, string) {
	if stats.Count < 2 {
		return rawScore, nil, FallbackLowN, "Judge evaluated fewer than 2 projects; sample dispersion cannot be estimated."
	}

	if stats.StdDev < 1e-9 {
		return rawScore, nil, FallbackZeroVariance, "Judge assigned identical scores across all evaluations (zero variance); z-score is undefined."
	}

	z := (rawScore - stats.Mean) / stats.StdDev
	zRounded := math.Round(z*1000) / 1000

	// Map standard normal scale to [0, 5] rating scale with 2.5 center
	normalized := 2.5 + (z * 0.8)
	if normalized < 0.0 {
		normalized = 0.0
	} else if normalized > 5.0 {
		normalized = 5.0
	}

	normalized = math.Round(normalized*1000) / 1000
	return normalized, &zRounded, FallbackNone, "Standardized using judge cohort z-score mapped to standard 0-5 scale."
}

// BuildInputManifest creates a canonical, deterministically ordered input manifest and computes its SHA-256 digest.
func BuildInputManifest(eventID string, algKey, algVer string, ballots []RawBallotRecord) (InputManifest, string, string, error) {
	// Sort ballots deterministically
	sortedBallots := make([]RawBallotRecord, len(ballots))
	copy(sortedBallots, ballots)
	sort.Slice(sortedBallots, func(i, j int) bool {
		if sortedBallots[i].ProjectID != sortedBallots[j].ProjectID {
			return sortedBallots[i].ProjectID < sortedBallots[j].ProjectID
		}
		if sortedBallots[i].JudgeUserID != sortedBallots[j].JudgeUserID {
			return sortedBallots[i].JudgeUserID < sortedBallots[j].JudgeUserID
		}
		return sortedBallots[i].BallotVersionID < sortedBallots[j].BallotVersionID
	})

	manifestItems := make([]ManifestBallotItem, len(sortedBallots))
	for i, b := range sortedBallots {
		manifestItems[i] = ManifestBallotItem{
			AssignmentID:    b.AssignmentID,
			JudgeUserID:     b.JudgeUserID,
			ProjectID:       b.ProjectID,
			RubricVersionID: b.RubricVersionID,
			BallotVersionID: b.BallotVersionID,
			BallotVersionNo: b.BallotVersionNo,
			RawScore:        b.RawScore,
			CriteriaScores:  b.CriteriaScores,
			SubmittedAt:     b.SubmittedAt,
		}
	}

	manifest := InputManifest{
		EventID:          eventID,
		AlgorithmKey:     algKey,
		AlgorithmVersion: algVer,
		CohortPolicy:     CohortAllCompleted,
		TiePolicy:        TieDeterministicFallback,
		Config: map[string]any{
			"scale_center": 2.5,
			"scale_factor": 0.8,
			"min_scale":    0.0,
			"max_scale":    5.0,
		},
		Ballots: manifestItems,
	}

	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return InputManifest{}, "", "", fmt.Errorf("failed to marshal input manifest: %w", err)
	}

	h := sha256.Sum256(manifestBytes)
	digest := hex.EncodeToString(h[:])

	return manifest, string(manifestBytes), digest, nil
}

// ComputeProjectResults performs defensible score aggregation, normalization, and ranking.
func ComputeProjectResults(
	runID string,
	eventID string,
	projects []ProjectInfo,
	ballots []RawBallotRecord,
	expectedPerProject map[string]int,
	inputDigest string,
	status string,
	publishedAt string,
) ([]ResultEntry, error) {
	cohortStats := ComputeJudgeCohortStats(ballots)

	// Group ballots by project
	ballotsByProject := make(map[string][]RawBallotRecord)
	for _, b := range ballots {
		ballotsByProject[b.ProjectID] = append(ballotsByProject[b.ProjectID], b)
	}

	var entries []ResultEntry

	for _, p := range projects {
		projBallots := ballotsByProject[p.ID]
		completedCount := len(projBallots)

		expectedCount := expectedPerProject[p.ID]
		if expectedCount == 0 {
			expectedCount = completedCount
		}

		var (
			rawScores        []float64
			rawSum           float64
			normalizedScores []float64
			normalizedSum    float64
			fallbackCount    int
			ballotAudits     []BallotAudit
			rubricVersionID  string
		)

		for _, b := range projBallots {
			if rubricVersionID == "" {
				rubricVersionID = b.RubricVersionID
			}

			rawScores = append(rawScores, b.RawScore)
			rawSum += b.RawScore

			stats := cohortStats[b.JudgeUserID]
			normScore, zScore, code, reason := NormalizeBallot(b.RawScore, stats)

			if code != FallbackNone {
				fallbackCount++
			}

			normalizedScores = append(normalizedScores, normScore)
			normalizedSum += normScore

			ballotAudits = append(ballotAudits, BallotAudit{
				BallotVersionID:  b.BallotVersionID,
				BallotVersionNo:  b.BallotVersionNo,
				AssignmentID:     b.AssignmentID,
				JudgeUserID:      b.JudgeUserID,
				JudgeName:        b.JudgeName,
				RubricVersionID:  b.RubricVersionID,
				CriteriaScores:   b.CriteriaScores,
				RawScore:         b.RawScore,
				JudgeMean:        stats.Mean,
				JudgeStdDev:      stats.StdDev,
				JudgeBallotCount: stats.Count,
				ZScore:           zScore,
				NormalizedScore:  normScore,
				FallbackCode:     code,
				FallbackReason:   reason,
				SubmittedAt:      b.SubmittedAt,
			})
		}

		rawAvg := 0.0
		normalizedAvg := 0.0
		if completedCount > 0 {
			rawAvg = math.Round((rawSum/float64(completedCount))*100) / 100
			normalizedAvg = math.Round((normalizedSum/float64(completedCount))*100) / 100
		}

		finalScore := normalizedAvg

		formulaBreakdown := FormulaBreakdown{
			RawScores:         rawScores,
			RawAverage:        rawAvg,
			NormalizedScores:  normalizedScores,
			NormalizedAverage: normalizedAvg,
			FinalScore:        finalScore,
			RoundingPolicy:    "Arithmetic mean rounded to 2 decimal places",
			ApprovedLanguage:  ApprovedFairnessStatement,
		}

		explanation := ExplanationPayload{
			RunID:            runID,
			ProjectID:        p.ID,
			ProjectTitle:     p.ProjectTitle,
			TeamName:         p.TeamName,
			TrackID:          p.TrackID,
			TrackName:        p.TrackName,
			RubricVersionID:  rubricVersionID,
			ExpectedReviews:  expectedCount,
			CompletedReviews: completedCount,
			EffectiveReviews: completedCount,
			FallbackCount:    fallbackCount,
			BallotAudits:     ballotAudits,
			FormulaBreakdown: formulaBreakdown,
			TieBreak: TieBreakAudit{
				TiePolicy:     TieDeterministicFallback,
				SortOrderRule: "final_score DESC, raw_score DESC, completed_reviews DESC, project_id ASC",
			},
			InputDigest: inputDigest,
			RunStatus:   status,
			PublishedAt: publishedAt,
		}

		entries = append(entries, ResultEntry{
			ResultRunID:      runID,
			ProjectID:        p.ID,
			RawScore:         rawAvg,
			NormalizedScore:  normalizedAvg,
			FinalScore:       finalScore,
			ExpectedReviews:  expectedCount,
			CompletedReviews: completedCount,
			EffectiveReviews: completedCount,
			FallbackCount:    fallbackCount,
			Explanation:      &explanation,
			ProjectTitle:     p.ProjectTitle,
			ProjectSummary:   p.ProjectSummary,
			TrackID:          p.TrackID,
			TrackName:        p.TrackName,
			TeamName:         p.TeamName,
			RepoURL:          p.RepoURL,
			DemoURL:          p.DemoURL,
		})
	}

	// Deterministic sorting
	sort.Slice(entries, func(i, j int) bool {
		// 1. final_score DESC
		if entries[i].FinalScore != entries[j].FinalScore {
			return entries[i].FinalScore > entries[j].FinalScore
		}
		// 2. raw_score DESC
		if entries[i].RawScore != entries[j].RawScore {
			return entries[i].RawScore > entries[j].RawScore
		}
		// 3. completed_reviews DESC
		if entries[i].CompletedReviews != entries[j].CompletedReviews {
			return entries[i].CompletedReviews > entries[j].CompletedReviews
		}
		// 4. project_id ASC
		return entries[i].ProjectID < entries[j].ProjectID
	})

	// Assign Ranks and Tie Groups
	for i := range entries {
		rank := i + 1
		entries[i].Rank = rank

		if i > 0 {
			prev := entries[i-1]
			curr := entries[i]
			if prev.FinalScore == curr.FinalScore &&
				prev.RawScore == curr.RawScore &&
				prev.CompletedReviews == curr.CompletedReviews {
				entries[i].TieGroup = entries[i-1].TieGroup
			} else {
				entries[i].TieGroup = rank
			}
		} else {
			entries[i].TieGroup = 1
		}

		// Update explanation with computed rank & tie group
		if entries[i].Explanation != nil {
			entries[i].Explanation.TieBreak.Rank = entries[i].Rank
			entries[i].Explanation.TieBreak.TieGroup = entries[i].TieGroup

			expBytes, _ := json.Marshal(entries[i].Explanation)
			entries[i].ExplanationJSON = string(expBytes)
		}
	}

	return entries, nil
}
