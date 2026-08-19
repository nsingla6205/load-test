package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
)

func TestIsICLifSetComplete(t *testing.T) {
	nodeIDs := map[string]int64{"n1": 1, "n2": 2}

	tests := []struct {
		name  string
		lifs  []*datamodel.Lif
		nodes map[string]int64
		want  bool
	}{
		{name: "empty", lifs: nil, nodes: nodeIDs, want: false},
		{name: "complete", lifs: []*datamodel.Lif{{NodeID: 1}, {NodeID: 2}}, nodes: nodeIDs, want: true},
		{name: "partial", lifs: []*datamodel.Lif{{NodeID: 1}}, nodes: nodeIDs, want: false},
		{name: "duplicate", lifs: []*datamodel.Lif{{NodeID: 1}, {NodeID: 1}, {NodeID: 2}}, nodes: nodeIDs, want: false},
		{name: "extra_stale_node", lifs: []*datamodel.Lif{{NodeID: 1}, {NodeID: 2}, {NodeID: 3}}, nodes: nodeIDs, want: false},
		{name: "nil_lif", lifs: []*datamodel.Lif{{NodeID: 1}, nil}, nodes: nodeIDs, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsICLifSetComplete(tt.lifs, tt.nodes))
		})
	}
}
