package dispatches

// appendEnvironment appends the runtime environment block to a base system
// prompt. Package-local copy of agents.AppendEnvironment for the same reason
// appendProjectInstructions is duplicated below: agents imports dispatches.
func appendEnvironment(basePrompt, env string) string {
	if env == "" {
		return basePrompt
	}

	return basePrompt + "\n\n# Environment\n\n" + env
}

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
