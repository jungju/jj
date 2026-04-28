package openai

func planningDraftSchema() map[string]any {
	return objectSchema(map[string]any{
		"agent":               stringSchema(),
		"summary":             stringSchema(),
		"spec_markdown":       stringSchema(),
		"task_markdown":       stringSchema(),
		"risks":               stringArraySchema(),
		"assumptions":         stringArraySchema(),
		"acceptance_criteria": stringArraySchema(),
		"test_plan":           stringArraySchema(),
	}, []string{"agent", "summary", "spec_markdown", "task_markdown", "risks", "assumptions", "acceptance_criteria", "test_plan"})
}

func mergeSchema() map[string]any {
	return objectSchema(map[string]any{
		"spec":  stringSchema(),
		"task":  stringSchema(),
		"notes": stringArraySchema(),
	}, []string{"spec", "task", "notes"})
}

func specReconcileSchema() map[string]any {
	return objectSchema(map[string]any{
		"spec":  stringSchema(),
		"notes": stringArraySchema(),
	}, []string{"spec", "notes"})
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
		"required":             required,
	}
}

func stringSchema() map[string]any {
	return map[string]any{"type": "string"}
}

func stringArraySchema() map[string]any {
	return map[string]any{
		"type":  "array",
		"items": stringSchema(),
	}
}
