package results

import (
	"math"
	"testing"
)

func TestEngine_ComputeJudgeCohortStats(t *testing.T) {
	ballots := []RawBallotRecord{
		{JudgeUserID: "j1", RawScore: 3.0},
		{JudgeUserID: "j1", RawScore: 4.0},
		{JudgeUserID: "j1", RawScore: 5.0},
		{JudgeUserID: "j2", RawScore: 4.5},
		{JudgeUserID: "j3", RawScore: 2.0},
		{JudgeUserID: "j3", RawScore: 2.0},
	}

	stats := ComputeJudgeCohortStats(ballots)

	// j1: n=3, mean = 4.0, var = ((1 + 0 + 1) / 2) = 1.0, stddev = 1.0
	s1, ok := stats["j1"]
	if !ok {
		t.Fatalf("expected stats for j1")
	}
	if s1.Count != 3 {
		t.Errorf("j1 count: expected 3, got %d", s1.Count)
	}
	if math.Abs(s1.Mean-4.0) > 1e-4 {
		t.Errorf("j1 mean: expected 4.0, got %f", s1.Mean)
	}
	if math.Abs(s1.StdDev-1.0) > 1e-4 {
		t.Errorf("j1 stddev: expected 1.0, got %f", s1.StdDev)
	}

	// j2: n=1, mean = 4.5, stddev = 0
	s2, ok := stats["j2"]
	if !ok {
		t.Fatalf("expected stats for j2")
	}
	if s2.Count != 1 || math.Abs(s2.Mean-4.5) > 1e-4 || s2.StdDev != 0 {
		t.Errorf("j2 stats incorrect: %+v", s2)
	}

	// j3: n=2, mean = 2.0, zero variance
	s3, ok := stats["j3"]
	if !ok {
		t.Fatalf("expected stats for j3")
	}
	if s3.Count != 2 || math.Abs(s3.Mean-2.0) > 1e-4 || s3.StdDev != 0 {
		t.Errorf("j3 stats incorrect: %+v", s3)
	}
}

func TestEngine_NormalizeBallot_FallbacksAndStandardization(t *testing.T) {
	// Case 1: n=1 -> FallbackLowN
	stats1 := JudgeCohortStats{
		JudgeUserID: "j1",
		Count:       1,
		Mean:        4.0,
		StdDev:      0.0,
	}
	normScore, zScore, code, _ := NormalizeBallot(4.0, stats1)
	if code != FallbackLowN || zScore != nil || normScore != 4.0 {
		t.Errorf("expected FallbackLowN, got code=%s, z=%v, norm=%f", code, zScore, normScore)
	}

	// Case 2: n >= 2 with zero variance -> FallbackZeroVariance
	statsZeroVar := JudgeCohortStats{
		JudgeUserID: "j2",
		Count:       3,
		Mean:        3.5,
		StdDev:      0.0,
	}
	normScore, zScore, code, _ = NormalizeBallot(3.5, statsZeroVar)
	if code != FallbackZeroVariance || zScore != nil || normScore != 3.5 {
		t.Errorf("expected FallbackZeroVariance, got code=%s, z=%v, norm=%f", code, zScore, normScore)
	}

	// Case 3: n >= 2 with positive variance -> Standard Normalization
	// mean=3.0, stddev=1.0. score=4.0 -> z=+1.0 -> norm = 2.5 + (1.0 * 0.8) = 3.3
	statsNorm := JudgeCohortStats{
		JudgeUserID: "j3",
		Count:       4,
		Mean:        3.0,
		StdDev:      1.0,
	}
	normScore, zScore, code, _ = NormalizeBallot(4.0, statsNorm)
	if code != FallbackNone || zScore == nil {
		t.Fatalf("expected FallbackNone, got code=%s, z=%v", code, zScore)
	}
	if math.Abs(*zScore-1.0) > 1e-4 {
		t.Errorf("expected z=1.0, got %f", *zScore)
	}
	if math.Abs(normScore-3.3) > 1e-4 {
		t.Errorf("expected normScore=3.3, got %f", normScore)
	}

	// Case 4: Clamping at scale boundaries [0, 5]
	normScoreLow, _, _, _ := NormalizeBallot(-10.0, statsNorm)
	if normScoreLow != 0.0 {
		t.Errorf("expected lower clamp at 0.0, got %f", normScoreLow)
	}
	normScoreHigh, _, _, _ := NormalizeBallot(100.0, statsNorm)
	if normScoreHigh != 5.0 {
		t.Errorf("expected upper clamp at 5.0, got %f", normScoreHigh)
	}
}

func TestEngine_DeterministicInputManifestAndDigest(t *testing.T) {
	ballots := []RawBallotRecord{
		{ProjectID: "prj_02", JudgeUserID: "j1", BallotVersionID: "b2", RawScore: 4.0},
		{ProjectID: "prj_01", JudgeUserID: "j2", BallotVersionID: "b1", RawScore: 3.5},
		{ProjectID: "prj_01", JudgeUserID: "j1", BallotVersionID: "b3", RawScore: 4.5},
	}

	// First pass
	m1, json1, d1, err1 := BuildInputManifest("evt_01", AlgorithmZScoreStandardization, AlgorithmVersionV1, ballots)
	if err1 != nil {
		t.Fatalf("build manifest 1 failed: %v", err1)
	}

	// Second pass with shuffled input order
	shuffled := []RawBallotRecord{ballots[1], ballots[2], ballots[0]}
	m2, json2, d2, err2 := BuildInputManifest("evt_01", AlgorithmZScoreStandardization, AlgorithmVersionV1, shuffled)
	if err2 != nil {
		t.Fatalf("build manifest 2 failed: %v", err2)
	}

	if d1 != d2 {
		t.Errorf("digests must be deterministic: d1=%s d2=%s", d1, d2)
	}
	if json1 != json2 {
		t.Errorf("canonical JSON must match: \n%s\nvs\n%s", json1, json2)
	}
	if len(m1.Ballots) != 3 || len(m2.Ballots) != 3 {
		t.Errorf("expected 3 ballots in manifest")
	}
}

func TestEngine_DeterministicTiePolicyAndRanking(t *testing.T) {
	projects := []ProjectInfo{
		{ID: "prj_B", ProjectTitle: "Project B"},
		{ID: "prj_A", ProjectTitle: "Project A"},
		{ID: "prj_C", ProjectTitle: "Project C"},
		{ID: "prj_D", ProjectTitle: "Project D"},
	}

	// Both prj_A and prj_B receive identical raw and normalized scores from single judge (n=1 fallback)
	ballots := []RawBallotRecord{
		{ProjectID: "prj_A", JudgeUserID: "j1", BallotVersionID: "b_a", RawScore: 4.0},
		{ProjectID: "prj_B", JudgeUserID: "j1", BallotVersionID: "b_b", RawScore: 4.0},
		{ProjectID: "prj_C", JudgeUserID: "j1", BallotVersionID: "b_c", RawScore: 5.0},
		{ProjectID: "prj_D", JudgeUserID: "j1", BallotVersionID: "b_d", RawScore: 2.0},
	}

	expectedCounts := map[string]int{"prj_A": 1, "prj_B": 1, "prj_C": 1, "prj_D": 1}

	entries, err := ComputeProjectResults("run_test", "evt_01", projects, ballots, expectedCounts, "digest123", StatusDraft, "")
	if err != nil {
		t.Fatalf("compute results failed: %v", err)
	}

	if len(entries) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(entries))
	}

	// Rank 1 must be prj_C (score 5.0)
	if entries[0].ProjectID != "prj_C" || entries[0].Rank != 1 || entries[0].TieGroup != 1 {
		t.Errorf("rank 1 should be prj_C, got %+v", entries[0])
	}

	// Ranks 2 and 3 are prj_A and prj_B (score 4.0, completed 1)
	// Tie breaker: project_id ASC -> prj_A comes before prj_B
	if entries[1].ProjectID != "prj_A" || entries[1].Rank != 2 || entries[1].TieGroup != 2 {
		t.Errorf("rank 2 should be prj_A with tie_group 2, got %+v", entries[1])
	}
	if entries[2].ProjectID != "prj_B" || entries[2].Rank != 3 || entries[2].TieGroup != 2 {
		t.Errorf("rank 3 should be prj_B sharing tie_group 2, got %+v", entries[2])
	}

	// Rank 4 must be prj_D (score 2.0)
	if entries[3].ProjectID != "prj_D" || entries[3].Rank != 4 || entries[3].TieGroup != 4 {
		t.Errorf("rank 4 should be prj_D, got %+v", entries[3])
	}
}
