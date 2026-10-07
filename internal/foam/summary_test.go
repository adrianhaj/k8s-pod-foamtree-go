package foam

import (
	"reflect"
	"testing"
)

func TestSummarize(t *testing.T) {
	memLimit := int64(2_000_000_000)
	nodes := []Node{
		{Name: "n3", CPU: 8000, Memory: 16_000_000_000, Zone: "b", Conditions: map[string]bool{"Ready": false}},
		{Name: "n1", CPU: 4000, Memory: 8_000_000_000, Pool: "general", Zone: "a"},
		{Name: "n2", CPU: 2000, Memory: 4_000_000_000, Pool: "general", Zone: "a", Unschedulable: true},
	}
	pods := []Pod{
		{Name: "web", NodeName: "n1", CPU: 1000, Memory: 2_000_000_000,
			Containers: []Container{{Name: "web", CPU: 1000, Memory: 2_000_000_000, MemoryLimit: &memLimit}}},
		{Name: "batch", NodeName: "n3", CPU: 500, Containers: []Container{{Name: "batch", CPU: 500}}},
		// A gone node's pod counts toward findings, never toward a group.
		{Name: "orphan", NodeName: "gone", CPU: 300, Memory: 300,
			Containers: []Container{{Name: "orphan", CPU: 300, Memory: 300, MemoryLimit: &memLimit}}},
		{Name: "queued", CPU: 100, Memory: 100, Containers: []Container{{Name: "queued", CPU: 100, Memory: 100}}},
	}
	got := Summarize(nodes, pods, without("image-pull"))
	want := Summary{
		Groups: []GroupSummary{
			{Pool: "", Zone: "b", CPU: 8000, CPURequested: 500, Memory: 16_000_000_000},
			{Pool: "general", Zone: "a", CPU: 6000, CPURequested: 1000, Memory: 12_000_000_000, MemoryRequested: 2_000_000_000},
		},
		Findings: map[string]int{"missing-requests": 1, "missing-limits": 2, "monolith": 0, "ratio-asymmetry": 0,
			"crashloop": 0, "oom-killed": 0, "resize-deferred": 0, "resize-infeasible": 0},
		Warnings: map[string]int{"cordoned": 1, "not-ready": 1, "memory-pressure": 0, "disk-pressure": 0, "pid-pressure": 0, "tainted": 0},
		Pending:  1,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}
