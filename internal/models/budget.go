package models

// OutputBudget reserves most of the total window for the prompt and history.
// Small windows must never get an output limit equal to the entire context.
func OutputBudget(contextWindow int) int {
	return max(1, min(contextWindow/4, 32768))
}
