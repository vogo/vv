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
	"fmt"

	"github.com/vogo/vage/agent/taskagent"
	vctx "github.com/vogo/vage/context"
	"github.com/vogo/vage/session"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/traces/costtraces"
)

// buildMetricsStore constructs the per-session metrics archive,
// rooted at the same directory as FileSessionStore so a single
// SessionStore.Delete wipes everything. Returns (nil, nil) when the
// session subsystem is off — metrics persistence requires a stable
// session id and a shared root, neither exists when sessions are
// disabled.
func buildMetricsStore(cfg *configs.Config) (session.MetricsStore, error) {
	if cfg == nil || !cfg.Session.IsEnabled() {
		return nil, nil
	}

	root := sessionRootDir(cfg)
	store, err := session.NewFileMetricsStore(root)
	if err != nil {
		return nil, fmt.Errorf("metrics store: %w", err)
	}
	return store, nil
}

// buildBuildReportSink constructs the per-turn BuildReport archive.
// Returns (nil, nil) when:
//   - session subsystem is off (no shared root), or
//   - cfg.Session.PersistBuildReports is explicitly false.
//
// Otherwise reuses the session root with vage's
// FileBuildReportSink, honouring cfg.Session.BuildReportLimit when
// non-zero (zero falls back to vage's DefaultBuildReportLimit).
func buildBuildReportSink(cfg *configs.Config) (vctx.BuildReportSink, error) {
	if cfg == nil || !cfg.Session.IsEnabled() {
		return nil, nil
	}
	if !cfg.Session.PersistBuildReportsEnabled() {
		return nil, nil
	}

	opts := []vctx.FileBuildReportSinkOption{}
	if cfg.Session.BuildReportLimit > 0 {
		opts = append(opts, vctx.WithBuildReportLimit(cfg.Session.BuildReportLimit))
	}

	root := sessionRootDir(cfg)
	sink, err := vctx.NewFileBuildReportSink(root, opts...)
	if err != nil {
		return nil, fmt.Errorf("build_report sink: %w", err)
	}
	return sink, nil
}

// buildMetricsHook constructs the SessionMetricsHook bound to store,
// with pricing pulled from cfg.ModelPricing (custom overrides) +
// costtraces.LookupPricing (built-ins). Returns nil when store is nil.
//
// The PricingFunc adapter converts costtraces' "USD per million
// tokens" to session.PricingFunc's "USD per 1k tokens" so the metrics
// layer stays unit-agnostic.
func buildMetricsHook(cfg *configs.Config, store session.MetricsStore) *session.SessionMetricsHook {
	if store == nil {
		return nil
	}

	customPricing := configs.ConvertPricing(cfg.ModelPricing)
	pricing := func(model string) (float64, float64, bool) {
		p := costtraces.LookupPricing(model, customPricing)
		if p == nil {
			return 0, 0, false
		}
		// costtraces stores per-million; SessionMetricsHook expects per-1k.
		return p.InputPerMTokens / 1000.0, p.OutputPerMTokens / 1000.0, true
	}

	return session.NewSessionMetricsHook(store, session.WithMetricsPricing(pricing))
}

// getMetricsStore safely extracts the optional MetricsStore from
// opts. Returns nil when opts is nil or the field is unset.
func getMetricsStore(opts *Options) session.MetricsStore {
	if opts == nil {
		return nil
	}
	return opts.MetricsStore
}

// getBuildReportSink safely extracts the optional BuildReportSink from
// opts. Returns nil when opts is nil or the field is unset.
func getBuildReportSink(opts *Options) vctx.BuildReportSink {
	if opts == nil {
		return nil
	}
	return opts.BuildReportSink
}

// getMetricsHook safely extracts the optional SessionMetricsHook from
// opts. Returns nil when opts is nil or the field is unset.
func getMetricsHook(opts *Options) *session.SessionMetricsHook {
	if opts == nil {
		return nil
	}
	return opts.MetricsHook
}

// getCheckpointFailureCB safely extracts the optional checkpoint
// failure callback from opts. Returns nil when opts is nil or the
// field is unset; the agent factory then leaves
// WithCheckpointFailureCallback off (slog.Warn-only failure path).
func getCheckpointFailureCB(opts *Options) taskagent.CheckpointFailureCallback {
	if opts == nil {
		return nil
	}
	return opts.CheckpointFailureCB
}
