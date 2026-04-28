package openai

import "context"

type Agent struct {
	Name  string
	Focus string
}

type DraftRequest struct {
	Model                    string
	Plan                     string
	Agent                    Agent
	TaskProposalMode         string
	ResolvedTaskProposalMode string
	TaskProposalInstruction  string
}

type MergeRequest struct {
	Model                    string
	Plan                     string
	Drafts                   []PlanningDraft
	TaskProposalMode         string
	ResolvedTaskProposalMode string
	TaskProposalInstruction  string
}

type ReconcileSpecRequest struct {
	Model             string
	PreviousSpec      string
	PlannedSpec       string
	SelectedTask      string
	CodexSummary      string
	GitDiffSummary    string
	ValidationSummary string
}

type PlanningDraft struct {
	Agent              string   `json:"agent"`
	Summary            string   `json:"summary"`
	SpecMarkdown       string   `json:"spec_markdown"`
	TaskMarkdown       string   `json:"task_markdown"`
	SpecDraft          string   `json:"spec_draft"`
	TaskDraft          string   `json:"task_draft"`
	Risks              []string `json:"risks"`
	Assumptions        []string `json:"assumptions"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	TestPlan           []string `json:"test_plan"`
	TestingGuidance    []string `json:"testing_guidance"`
}

type MergeResult struct {
	Spec  string   `json:"spec"`
	Task  string   `json:"task"`
	Notes []string `json:"notes"`
}

type ReconcileSpecResult struct {
	Spec  string   `json:"spec"`
	Notes []string `json:"notes"`
}

type Planner interface {
	Draft(context.Context, DraftRequest) (PlanningDraft, []byte, error)
	Merge(context.Context, MergeRequest) (MergeResult, []byte, error)
	ReconcileSpec(context.Context, ReconcileSpecRequest) (ReconcileSpecResult, []byte, error)
}
