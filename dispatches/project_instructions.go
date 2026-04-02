package dispatches

// appendProjectInstructions appends project instructions to a base system
// prompt. If instructions is empty, the base prompt is returned unchanged.
// This is a package-local copy of the same logic in agents.AppendProjectInstructions
// to avoid a circular import (agents imports dispatches).
func appendProjectInstructions(basePrompt, instructions string) string {
	if instructions == "" {
		return basePrompt
	}

	return basePrompt + "\n\n# Project Instructions\n\n" +
		"IMPORTANT: The following are project-specific instructions provided by the user. " +
		"These instructions should be followed and take precedence over default behaviors when applicable.\n\n" +
		instructions
}
