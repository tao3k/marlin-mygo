// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package kit_test

import (
	"testing"

	"github.com/tao3k/marlin-mygo/runtime/kit"
)

func TestValidateWorkflowGraphJSON(t *testing.T) {
	valid := `{"schemaVersion":"marlin.workflow.graph.v1","nodes":[{"id":"a","title":"Plan"},{"id":"b","title":"Run"}],"edges":[["a","b"]]}`
	g, err := kit.ValidateWorkflowGraphJSON(valid)
	if err != nil || len(g.Nodes) != 2 || len(g.Edges) != 1 {
		t.Fatalf("expected valid workflow graph, got g=%+v err=%v", g, err)
	}
	brokenEdge := `{"nodes":[{"id":"a"}],"edges":[["a","missing"]]}`
	if _, err := kit.ValidateWorkflowGraphJSON(brokenEdge); err == nil {
		t.Fatalf("expected broken edge target rejected fail-closed")
	}
	emptyNodes := `{"nodes":[],"edges":[]}`
	if _, err := kit.ValidateWorkflowGraphJSON(emptyNodes); err == nil {
		t.Fatalf("expected empty node list rejected fail-closed")
	}
}
