/*
 * Licensed to the Apache Software Foundation (ASF) under one or more
 * contributor license agreements.  See the NOTICE file distributed with
 * this work for additional information regarding copyright ownership.
 * The ASF licenses this file to You under the Apache License, Version 2.0
 * (the "License"); you may not use this file except in compliance with
 * the License.  You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package setup

import (
	"errors"
	"fmt"

	"github.com/vogo/vage/agent"
	"github.com/vogo/vv/agents"
)

// ErrSessionDisabled is returned when a resume call is attempted but the
// session subsystem is off (cfg.Session.Enabled == false). Resume needs
// a stable session id and a persistent checkpoint store; neither exists
// without the session subsystem.
var ErrSessionDisabled = errors.New("vv: session subsystem is disabled (set session.enabled: true)")

// ErrNoIterationStore is returned when the session subsystem is on but
// the InitResult carries no IterationStore — indicates a setup wiring
// regression rather than user error. Surfaces as 503 from HTTP and exit
// 1 with diagnostic from CLI.
var ErrNoIterationStore = errors.New("vv: iteration checkpoint store not configured")

// ErrAgentNotFound is returned when the latest checkpoint references an
// agent id that the current registry does not know about — happens when
// the agent was renamed, removed, or never registered between checkpoint
// write and resume call. Both transports surface this as 404.
var ErrAgentNotFound = errors.New("vv: checkpoint references an agent that is no longer registered")

// ResumeAgent resolves the agent that wrote the resumed checkpoint.
// Sub-agents (Dispatchable: true) are looked up via Result.Agent; the
// Primary Assistant (Dispatchable: false, not in subAgents) is fetched
// from Dispatcher.Primary(). Both transports (CLI / HTTP) call into this
// method so the resolution rules stay identical.
//
// Returns ErrAgentNotFound (wrapped) when the id is unknown or the
// requested handle is missing — caller maps to 404 / exit 1.
func (ir *InitResult) ResumeAgent(agentID string) (agent.Agent, error) {
	if ir == nil {
		return nil, fmt.Errorf("%w: init result is nil", ErrAgentNotFound)
	}

	if agentID == agents.PrimaryAgentID {
		if ir.SetupResult == nil || ir.SetupResult.Dispatcher == nil {
			return nil, fmt.Errorf("%w: primary requested but dispatcher is nil", ErrAgentNotFound)
		}
		p := ir.SetupResult.Dispatcher.Primary()
		if p == nil {
			return nil, fmt.Errorf("%w: primary not attached to dispatcher", ErrAgentNotFound)
		}
		return p, nil
	}

	if ir.SetupResult == nil {
		return nil, fmt.Errorf("%w: setup result is nil for agent %q", ErrAgentNotFound, agentID)
	}
	a := ir.SetupResult.Agent(agentID)
	if a == nil {
		return nil, fmt.Errorf("%w: agent_id=%q", ErrAgentNotFound, agentID)
	}
	return a, nil
}
