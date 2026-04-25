package openai

import "context"

type Agent struct {
	Name  string
	Focus string
}

type DraftRequest struct {
	Model string
	Plan  string
	Agent Agent
}

type MergeRequest struct {
	Model  string
	Plan   string
	Drafts []PlanningDraft
}

type EvaluationRequest struct {
	Model        string
	Plan         string
	Spec         string
	Task         string
	CodexSummary string
	CodexEvents  string
	GitDiff      string
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

type EvaluationResult struct {
	Result               string   `json:"result"`
	Score                int      `json:"score"`
	Summary              string   `json:"summary"`
	WhatChanged          []string `json:"what_changed"`
	RequirementsCoverage []string `json:"requirements_coverage"`
	TestCoverage         []string `json:"test_coverage"`
	Risks                []string `json:"risks"`
	Regressions          []string `json:"regressions"`
	RecommendedFollowups []string `json:"recommended_followups"`
	Verdict              string   `json:"verdict,omitempty"`
	Reasons              []string `json:"reasons,omitempty"`
	TestResults          []string `json:"test_results,omitempty"`
}

type Planner interface {
	Draft(context.Context, DraftRequest) (PlanningDraft, []byte, error)
	Merge(context.Context, MergeRequest) (MergeResult, []byte, error)
	Evaluate(context.Context, EvaluationRequest) (EvaluationResult, []byte, error)
}
