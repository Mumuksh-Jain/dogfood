package assignment

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Machine-readable infeasibility codes
const (
	CodeInsufficientCapacity    = "INSUFFICIENT_CAPACITY"
	CodeNoEligibleJudge         = "NO_ELIGIBLE_JUDGE"
	CodeInsufficientTrackJudges = "INSUFFICIENT_TRACK_JUDGES"
	CodeNoProjects              = "NO_PROJECTS"
)

// InfeasibilityDetails provides structured diagnostic context for infeasible runs.
type InfeasibilityDetails struct {
	Code              string   `json:"code"`
	Message           string   `json:"message"`
	RequestedTotal    int      `json:"requested_total"`
	AchievableTotal   int      `json:"achievable_total"`
	AffectedProjects  []string `json:"affected_projects,omitempty"`
	AffectedTracks    []string `json:"affected_tracks,omitempty"`
	EligibleJudges    int      `json:"eligible_judges"`
	AvailableCapacity int      `json:"available_capacity"`
}

func (d *InfeasibilityDetails) ToJSON() string {
	if d == nil {
		return "{}"
	}
	b, err := json.Marshal(d)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// ProjectInput represents a project eligible for assignment.
type ProjectInput struct {
	ID                   string
	TrackID              string
	ActiveReviewsCount   int
	AssignedJudgeUserIDs map[string]bool
}

// JudgeInput represents a judge profile and eligibility.
type JudgeInput struct {
	UserID                 string
	Capacity               int
	ActiveAssignmentsCount int
	EligibleTracks         map[string]bool
	ExistingProjectIDs     map[string]bool
}

// AssignmentDecision represents a single proposed judge/project assignment.
type AssignmentDecision struct {
	JudgeUserID string
	ProjectID   string
	TrackID     string
}

// EngineResult is the output of the deterministic assignment engine.
type EngineResult struct {
	Feasible          bool
	InfeasibilityCode string
	Details           *InfeasibilityDetails
	Decisions         []AssignmentDecision
	RequestedTotal    int
	AchievableTotal   int
}

// RunDeterministicAssignment executes the constrained matching algorithm.
// All decisions are strictly deterministic: stable sorting is applied to projects and judges.
func RunDeterministicAssignment(projects []ProjectInput, judges []JudgeInput, targetReviews int) *EngineResult {
	if len(projects) == 0 {
		return &EngineResult{
			Feasible:          false,
			InfeasibilityCode: CodeNoProjects,
			Details: &InfeasibilityDetails{
				Code:    CodeNoProjects,
				Message: "no eligible projects found for assignment",
			},
		}
	}

	// 1. Sort projects deterministically by (TrackID ASC, ID ASC)
	sortedProjects := make([]ProjectInput, len(projects))
	copy(sortedProjects, projects)
	sort.Slice(sortedProjects, func(i, j int) bool {
		if sortedProjects[i].TrackID != sortedProjects[j].TrackID {
			return sortedProjects[i].TrackID < sortedProjects[j].TrackID
		}
		return sortedProjects[i].ID < sortedProjects[j].ID
	})

	// 2. Sort judges deterministically by UserID ASC
	sortedJudges := make([]JudgeInput, len(judges))
	copy(sortedJudges, judges)
	sort.Slice(sortedJudges, func(i, j int) bool {
		return sortedJudges[i].UserID < sortedJudges[j].UserID
	})

	// Calculate needed reviews per project
	totalRequested := 0
	type projectNeed struct {
		project ProjectInput
		needed  int
	}
	var needs []projectNeed
	affectedTracksMap := make(map[string]bool)
	var noJudgeProjects []string

	for _, p := range sortedProjects {
		needed := targetReviews - p.ActiveReviewsCount
		if needed < 0 {
			needed = 0
		}
		totalRequested += needed
		needs = append(needs, projectNeed{project: p, needed: needed})

		if needed > 0 {
			// Pre-check: count available distinct eligible judges for this project
			eligibleCount := 0
			for _, j := range sortedJudges {
				remCap := j.Capacity - j.ActiveAssignmentsCount
				if remCap <= 0 {
					continue
				}
				if !j.EligibleTracks[p.TrackID] {
					continue
				}
				if p.AssignedJudgeUserIDs != nil && p.AssignedJudgeUserIDs[j.UserID] {
					continue
				}
				if j.ExistingProjectIDs != nil && j.ExistingProjectIDs[p.ID] {
					continue
				}
				eligibleCount++
			}

			if eligibleCount == 0 {
				noJudgeProjects = append(noJudgeProjects, p.ID)
				affectedTracksMap[p.TrackID] = true
			} else if eligibleCount < needed {
				noJudgeProjects = append(noJudgeProjects, p.ID)
				affectedTracksMap[p.TrackID] = true
			}
		}
	}

	// If 0 reviews are needed (all projects already satisfied)
	if totalRequested == 0 {
		return &EngineResult{
			Feasible:        true,
			RequestedTotal:  0,
			AchievableTotal: 0,
			Decisions:       []AssignmentDecision{},
		}
	}

	// Compute total available capacity
	totalAvailableCap := 0
	eligibleJudgeCount := 0
	for _, j := range sortedJudges {
		remCap := j.Capacity - j.ActiveAssignmentsCount
		if remCap > 0 {
			totalAvailableCap += remCap
			eligibleJudgeCount++
		}
	}

	var affectedTracks []string
	for trk := range affectedTracksMap {
		affectedTracks = append(affectedTracks, trk)
	}
	sort.Strings(affectedTracks)

	// Pre-check failure: No eligible judge or insufficient distinct judges for a project
	if len(noJudgeProjects) > 0 {
		return &EngineResult{
			Feasible:          false,
			InfeasibilityCode: CodeNoEligibleJudge,
			RequestedTotal:    totalRequested,
			AchievableTotal:   0,
			Details: &InfeasibilityDetails{
				Code:              CodeNoEligibleJudge,
				Message:           fmt.Sprintf("project(s) %v have fewer eligible judges than the required %d reviews", noJudgeProjects, targetReviews),
				RequestedTotal:    totalRequested,
				AchievableTotal:   0,
				AffectedProjects:  noJudgeProjects,
				AffectedTracks:    affectedTracks,
				EligibleJudges:    eligibleJudgeCount,
				AvailableCapacity: totalAvailableCap,
			},
		}
	}

	// Pre-check failure: Global capacity shortage
	if totalRequested > totalAvailableCap {
		return &EngineResult{
			Feasible:          false,
			InfeasibilityCode: CodeInsufficientCapacity,
			RequestedTotal:    totalRequested,
			AchievableTotal:   totalAvailableCap,
			Details: &InfeasibilityDetails{
				Code:              CodeInsufficientCapacity,
				Message:           fmt.Sprintf("total requested review slots (%d) exceed available judge capacity (%d)", totalRequested, totalAvailableCap),
				RequestedTotal:    totalRequested,
				AchievableTotal:   totalAvailableCap,
				EligibleJudges:    eligibleJudgeCount,
				AvailableCapacity: totalAvailableCap,
			},
		}
	}

	// 3. Build Flow Network for Min-Cost Max-Flow
	// Nodes:
	// 0: Source
	// 1 .. M: Judges
	// M+1 .. M+N: Projects
	// M+N+1: Sink
	numJudges := len(sortedJudges)
	numProjects := len(sortedProjects)
	source := 0
	sink := numJudges + numProjects + 1

	net := newFlowNetwork(sink + 1)

	// Add edges Source -> Judges
	for i, j := range sortedJudges {
		judgeNode := 1 + i
		remCap := j.Capacity - j.ActiveAssignmentsCount
		if remCap <= 0 {
			continue
		}
		// To achieve load-balancing, add unit-capacity edges with increasing marginal cost
		for unit := 0; unit < remCap; unit++ {
			// Cost increases with each unit assigned to this judge.
			// Marginal cost = (current_load + unit) * 1000 + judge_deterministic_index
			marginalCost := (j.ActiveAssignmentsCount+unit)*1000 + i
			net.addEdge(source, judgeNode, 1, marginalCost)
		}
	}

	// Add edges Judges -> Projects
	for i, j := range sortedJudges {
		judgeNode := 1 + i
		for k, pn := range needs {
			if pn.needed == 0 {
				continue
			}
			p := pn.project
			// Check track eligibility
			if !j.EligibleTracks[p.TrackID] {
				continue
			}
			// Check duplicate active assignment
			if p.AssignedJudgeUserIDs != nil && p.AssignedJudgeUserIDs[j.UserID] {
				continue
			}
			if j.ExistingProjectIDs != nil && j.ExistingProjectIDs[p.ID] {
				continue
			}

			projectNode := numJudges + 1 + k
			net.addEdge(judgeNode, projectNode, 1, 0)
		}
	}

	// Add edges Projects -> Sink
	for k, pn := range needs {
		if pn.needed <= 0 {
			continue
		}
		projectNode := numJudges + 1 + k
		net.addEdge(projectNode, sink, pn.needed, 0)
	}

	// 4. Solve Min-Cost Max-Flow via Successive Shortest Path (SSP)
	maxFlow := net.minCostMaxFlow(source, sink, totalRequested)

	if maxFlow < totalRequested {
		// Identify which projects did not get enough reviews
		var shortProjects []string
		shortTracksMap := make(map[string]bool)
		for k, pn := range needs {
			if pn.needed <= 0 {
				continue
			}
			projectNode := numJudges + 1 + k
			flowReceived := 0
			for _, edgeIdx := range net.adj[projectNode] {
				e := net.edges[edgeIdx]
				if e.to == sink {
					flowReceived = e.flow
					break
				}
			}
			if flowReceived < pn.needed {
				shortProjects = append(shortProjects, pn.project.ID)
				shortTracksMap[pn.project.TrackID] = true
			}
		}

		var shortTracks []string
		for trk := range shortTracksMap {
			shortTracks = append(shortTracks, trk)
		}
		sort.Strings(shortTracks)

		return &EngineResult{
			Feasible:          false,
			InfeasibilityCode: CodeInsufficientCapacity,
			RequestedTotal:    totalRequested,
			AchievableTotal:   maxFlow,
			Details: &InfeasibilityDetails{
				Code:              CodeInsufficientCapacity,
				Message:           fmt.Sprintf("capacity constraints prevented satisfying all requested reviews (achieved %d of %d)", maxFlow, totalRequested),
				RequestedTotal:    totalRequested,
				AchievableTotal:   maxFlow,
				AffectedProjects:  shortProjects,
				AffectedTracks:    shortTracks,
				EligibleJudges:    eligibleJudgeCount,
				AvailableCapacity: totalAvailableCap,
			},
		}
	}

	// 5. Extract Decisions from flow
	var decisions []AssignmentDecision
	for i, j := range sortedJudges {
		judgeNode := 1 + i
		for _, edgeIdx := range net.adj[judgeNode] {
			e := net.edges[edgeIdx]
			if e.flow > 0 && e.to >= numJudges+1 && e.to <= numJudges+numProjects {
				projectIdx := e.to - (numJudges + 1)
				p := needs[projectIdx].project
				decisions = append(decisions, AssignmentDecision{
					JudgeUserID: j.UserID,
					ProjectID:   p.ID,
					TrackID:     p.TrackID,
				})
			}
		}
	}

	// Sort decisions deterministically: TrackID ASC, ProjectID ASC, JudgeUserID ASC
	sort.Slice(decisions, func(i, j int) bool {
		if decisions[i].TrackID != decisions[j].TrackID {
			return decisions[i].TrackID < decisions[j].TrackID
		}
		if decisions[i].ProjectID != decisions[j].ProjectID {
			return decisions[i].ProjectID < decisions[j].ProjectID
		}
		return decisions[i].JudgeUserID < decisions[j].JudgeUserID
	})

	return &EngineResult{
		Feasible:        true,
		Decisions:       decisions,
		RequestedTotal:  totalRequested,
		AchievableTotal: maxFlow,
	}
}

// --- Flow Network Implementation (Self-Contained) ---

type flowEdge struct {
	from     int
	to       int
	capacity int
	flow     int
	cost     int
	rev      int
}

type flowNetwork struct {
	n     int
	edges []flowEdge
	adj   [][]int
}

func newFlowNetwork(n int) *flowNetwork {
	return &flowNetwork{
		n:     n,
		edges: make([]flowEdge, 0),
		adj:   make([][]int, n),
	}
}

func (fn *flowNetwork) addEdge(from, to, cap, cost int) {
	idxForward := len(fn.edges)
	idxBackward := idxForward + 1

	fn.edges = append(fn.edges, flowEdge{
		from:     from,
		to:       to,
		capacity: cap,
		flow:     0,
		cost:     cost,
		rev:      idxBackward,
	})
	fn.adj[from] = append(fn.adj[from], idxForward)

	fn.edges = append(fn.edges, flowEdge{
		from:     to,
		to:       from,
		capacity: 0,
		flow:     0,
		cost:     -cost,
		rev:      idxForward,
	})
	fn.adj[to] = append(fn.adj[to], idxBackward)
}

const infDist = 1<<62 - 1

func (fn *flowNetwork) minCostMaxFlow(source, sink, maxTargetFlow int) int {
	totalFlow := 0

	for totalFlow < maxTargetFlow {
		// SPFA to find minimum cost augmenting path in residual graph
		dist := make([]int64, fn.n)
		for i := range dist {
			dist[i] = infDist
		}
		parentEdge := make([]int, fn.n)
		for i := range parentEdge {
			parentEdge[i] = -1
		}
		inQueue := make([]bool, fn.n)

		queue := make([]int, 0, fn.n)
		dist[source] = 0
		queue = append(queue, source)
		inQueue[source] = true

		for len(queue) > 0 {
			u := queue[0]
			queue = queue[1:]
			inQueue[u] = false

			for _, edgeIdx := range fn.adj[u] {
				e := fn.edges[edgeIdx]
				if e.capacity-e.flow > 0 {
					newDist := dist[u] + int64(e.cost)
					if newDist < dist[e.to] {
						dist[e.to] = newDist
						parentEdge[e.to] = edgeIdx
						if !inQueue[e.to] {
							queue = append(queue, e.to)
							inQueue[e.to] = true
						}
					}
				}
			}
		}

		if dist[sink] == infDist {
			// No augmenting path found
			break
		}

		// Find maximum flow we can push along this path (at least 1)
		push := maxTargetFlow - totalFlow
		curr := sink
		for curr != source {
			edgeIdx := parentEdge[curr]
			e := fn.edges[edgeIdx]
			rem := e.capacity - e.flow
			if rem < push {
				push = rem
			}
			curr = e.from
		}

		// Augment flow
		curr = sink
		for curr != source {
			edgeIdx := parentEdge[curr]
			fn.edges[edgeIdx].flow += push
			revIdx := fn.edges[edgeIdx].rev
			fn.edges[revIdx].flow -= push
			curr = fn.edges[edgeIdx].from
		}

		totalFlow += push
	}

	return totalFlow
}
