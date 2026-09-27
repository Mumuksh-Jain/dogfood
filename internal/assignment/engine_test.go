package assignment

import (
	"reflect"
	"testing"
)

func TestEngine_BasicFeasible(t *testing.T) {
	// 4 projects, 4 judges, capacity 3 each, target = 2 reviews/project
	projects := []ProjectInput{
		{ID: "prj_01", TrackID: "trk_01"},
		{ID: "prj_02", TrackID: "trk_01"},
		{ID: "prj_03", TrackID: "trk_01"},
		{ID: "prj_04", TrackID: "trk_01"},
	}

	judges := []JudgeInput{
		{UserID: "jdg_01", Capacity: 3, EligibleTracks: map[string]bool{"trk_01": true}},
		{UserID: "jdg_02", Capacity: 3, EligibleTracks: map[string]bool{"trk_01": true}},
		{UserID: "jdg_03", Capacity: 3, EligibleTracks: map[string]bool{"trk_01": true}},
		{UserID: "jdg_04", Capacity: 3, EligibleTracks: map[string]bool{"trk_01": true}},
	}

	res := RunDeterministicAssignment(projects, judges, 2)
	if !res.Feasible {
		t.Fatalf("expected feasible assignment, got infeasible: %s (%+v)", res.InfeasibilityCode, res.Details)
	}

	if len(res.Decisions) != 8 {
		t.Fatalf("expected 8 decisions (4 projects * 2 reviews), got %d", len(res.Decisions))
	}

	// Verify project coverage and distinct judges per project
	projectJudges := make(map[string]map[string]bool)
	judgeLoads := make(map[string]int)

	for _, d := range res.Decisions {
		if projectJudges[d.ProjectID] == nil {
			projectJudges[d.ProjectID] = make(map[string]bool)
		}
		if projectJudges[d.ProjectID][d.JudgeUserID] {
			t.Errorf("duplicate judge %s assigned to project %s", d.JudgeUserID, d.ProjectID)
		}
		projectJudges[d.ProjectID][d.JudgeUserID] = true
		judgeLoads[d.JudgeUserID]++
	}

	for _, p := range projects {
		if len(projectJudges[p.ID]) != 2 {
			t.Errorf("project %s expected 2 distinct judges, got %d", p.ID, len(projectJudges[p.ID]))
		}
	}

	for _, j := range judges {
		if judgeLoads[j.UserID] > j.Capacity {
			t.Errorf("judge %s exceeded capacity (%d > %d)", j.UserID, judgeLoads[j.UserID], j.Capacity)
		}
		// In balanced distribution: 8 / 4 = 2 reviews per judge
		if judgeLoads[j.UserID] != 2 {
			t.Errorf("expected judge %s to have balanced load 2, got %d", j.UserID, judgeLoads[j.UserID])
		}
	}
}

func TestEngine_DeterminismAcrossInputOrder(t *testing.T) {
	// Order 1
	p1 := []ProjectInput{
		{ID: "prj_01", TrackID: "trk_01"},
		{ID: "prj_02", TrackID: "trk_02"},
		{ID: "prj_03", TrackID: "trk_01"},
	}
	j1 := []JudgeInput{
		{UserID: "jdg_a", Capacity: 2, EligibleTracks: map[string]bool{"trk_01": true, "trk_02": true}},
		{UserID: "jdg_b", Capacity: 2, EligibleTracks: map[string]bool{"trk_01": true, "trk_02": true}},
		{UserID: "jdg_c", Capacity: 2, EligibleTracks: map[string]bool{"trk_01": true, "trk_02": true}},
	}
	res1 := RunDeterministicAssignment(p1, j1, 2)

	// Order 2: reversed projects and judges
	p2 := []ProjectInput{
		{ID: "prj_03", TrackID: "trk_01"},
		{ID: "prj_01", TrackID: "trk_01"},
		{ID: "prj_02", TrackID: "trk_02"},
	}
	j2 := []JudgeInput{
		{UserID: "jdg_c", Capacity: 2, EligibleTracks: map[string]bool{"trk_01": true, "trk_02": true}},
		{UserID: "jdg_a", Capacity: 2, EligibleTracks: map[string]bool{"trk_01": true, "trk_02": true}},
		{UserID: "jdg_b", Capacity: 2, EligibleTracks: map[string]bool{"trk_01": true, "trk_02": true}},
	}
	res2 := RunDeterministicAssignment(p2, j2, 2)

	if !res1.Feasible || !res2.Feasible {
		t.Fatalf("both runs should be feasible")
	}

	if !reflect.DeepEqual(res1.Decisions, res2.Decisions) {
		t.Fatalf("runs produced different decisions:\nRun1: %+v\nRun2: %+v", res1.Decisions, res2.Decisions)
	}
}

func TestEngine_TrackLocalBottleneck(t *testing.T) {
	// Track 1 has 2 projects needing 2 reviews each = 4 reviews needed
	// But Track 1 judges only have total capacity = 2!
	// Track 2 judges have huge capacity (10), so global capacity (12) > needed (4).
	// Max-flow must detect that Track 1 is locally infeasible!
	projects := []ProjectInput{
		{ID: "prj_t1_a", TrackID: "trk_01"},
		{ID: "prj_t1_b", TrackID: "trk_01"},
	}
	judges := []JudgeInput{
		{UserID: "jdg_t1_1", Capacity: 1, EligibleTracks: map[string]bool{"trk_01": true}},
		{UserID: "jdg_t1_2", Capacity: 1, EligibleTracks: map[string]bool{"trk_01": true}},
		{UserID: "jdg_t2_1", Capacity: 10, EligibleTracks: map[string]bool{"trk_02": true}},
	}

	res := RunDeterministicAssignment(projects, judges, 2)
	if res.Feasible {
		t.Fatalf("expected track-local bottleneck to be infeasible, but succeeded")
	}

	if res.InfeasibilityCode != CodeInsufficientCapacity {
		t.Errorf("expected %s, got %s", CodeInsufficientCapacity, res.InfeasibilityCode)
	}

	if res.Details == nil || res.Details.AchievableTotal != 2 {
		t.Errorf("expected achievable total 2, got %+v", res.Details)
	}
}
