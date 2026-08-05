package agents

// AppendEnvironment appends the runtime environment block to a base system
// prompt. If env is empty, the base prompt is returned unchanged.
//
// The block carries facts an agent cannot derive on its own and would
// otherwise have to discover by trial and error: the absolute working
// directory (file tools reject relative paths), the platform, the date, and
// the tool-iteration budget for the run. A front-door agent that does not
// know its working directory burns its first iteration on a failed `read(".")`
// and never learns how much budget it has left.
func AppendEnvironment(basePrompt, env string) string {
	if env == "" {
		return basePrompt
	}

	return basePrompt + "\n\n# Environment\n\n" + env
}

// ComposeSystemPrompt assembles the final system prompt from the agent's base
// prompt, the runtime environment block, and the project instructions, in that
// order. Environment before project instructions so the user-authored rules
// stay closest to the end of the prompt, where they are read last and carry
// the most weight.
func ComposeSystemPrompt(basePrompt, env, instructions string) string {
	return AppendProjectInstructions(AppendEnvironment(basePrompt, env), instructions)
}
