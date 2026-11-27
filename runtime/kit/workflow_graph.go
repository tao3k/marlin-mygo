// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package kit

import (
	"encoding/json"
	"fmt"
)

// WorkflowNode describes one interactive BPMN / Agent DAG node synchronized
// bidirectionally between Go (marlin-mygo / marlin-ui) and the Tier-0
// transparent WebView canvas.
type WorkflowNode struct {
	ID    string  `json:"id"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	W     float64 `json:"w"`
	H     float64 `json:"h"`
	Tag   string  `json:"tag"`
	Title string  `json:"title"`
	Sub   string  `json:"sub"`
	Color string  `json:"color"`
	Desc  string  `json:"desc"`
}

// WorkflowGraph is the typed, bounded JSON state envelope exchanged over the
// Tier-0 Hybrid GPU <-> WebView RPC bridge.
type WorkflowGraph struct {
	SchemaVersion string         `json:"schemaVersion"`
	Nodes         []WorkflowNode `json:"nodes"`
	Edges         [][2]string    `json:"edges"`
}

// MaxWorkflowGraphBytes bounds Tier-0 BPMN graph payloads fail-closed.
const MaxWorkflowGraphBytes = 128 * 1024

// ValidateWorkflowGraphJSON decodes and validates a BPMN / Agent DAG JSON
// payload from the Tier-0 WebView bridge (enforcing size, schema, node/edge
// bounds, and endpoint referential integrity).
func ValidateWorkflowGraphJSON(raw string) (WorkflowGraph, error) {
	if len(raw) > MaxWorkflowGraphBytes {
		return WorkflowGraph{}, fmt.Errorf("workflow graph exceeds max bytes (%d > %d)", len(raw), MaxWorkflowGraphBytes)
	}
	var g WorkflowGraph
	if err := json.Unmarshal([]byte(raw), &g); err != nil {
		return WorkflowGraph{}, fmt.Errorf("invalid workflow graph json: %w", err)
	}
	if g.SchemaVersion != "" && g.SchemaVersion != "marlin.workflow.graph.v1" {
		return WorkflowGraph{}, fmt.Errorf("unsupported workflow schemaVersion %q", g.SchemaVersion)
	}
	if len(g.Nodes) == 0 || len(g.Nodes) > 128 {
		return WorkflowGraph{}, fmt.Errorf("workflow node count %d out of bounds [1, 128]", len(g.Nodes))
	}
	if len(g.Edges) > 256 {
		return WorkflowGraph{}, fmt.Errorf("workflow edge count %d exceeds limit 256", len(g.Edges))
	}
	ids := make(map[string]struct{}, len(g.Nodes))
	for _, n := range g.Nodes {
		if n.ID == "" {
			return WorkflowGraph{}, fmt.Errorf("workflow node requires non-empty id")
		}
		ids[n.ID] = struct{}{}
	}
	for _, e := range g.Edges {
		if _, ok := ids[e[0]]; !ok {
			return WorkflowGraph{}, fmt.Errorf("workflow edge source %q does not exist", e[0])
		}
		if _, ok := ids[e[1]]; !ok {
			return WorkflowGraph{}, fmt.Errorf("workflow edge target %q does not exist", e[1])
		}
	}
	if g.SchemaVersion == "" {
		g.SchemaVersion = "marlin.workflow.graph.v1"
	}
	return g, nil
}
