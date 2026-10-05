package configs

import "testing"

func TestAgentsConfig_EffectiveInterruptLeaseTTL(t *testing.T) {
	if got := (*AgentsConfig)(nil).EffectiveInterruptLeaseTTL(); got != 300 {
		t.Errorf("nil = %d, want 300", got)
	}

	c := &AgentsConfig{}
	if got := c.EffectiveInterruptLeaseTTL(); got != 300 {
		t.Errorf("zero = %d, want 300", got)
	}

	c.AskUserTimeout = 90
	if got := c.EffectiveInterruptLeaseTTL(); got != 90 {
		t.Errorf("from ask_user_timeout = %d, want 90", got)
	}

	c.InterruptLeaseTTL = 15
	if got := c.EffectiveInterruptLeaseTTL(); got != 15 {
		t.Errorf("explicit = %d, want 15", got)
	}
}
