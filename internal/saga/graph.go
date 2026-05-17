package saga

import (
	"fmt"
	"sort"

	"github.com/s1liconcow/skiff/internal/state/schema"
)

func ReadyNodes(graph schema.SagaGraph, completed map[string]schema.StepResult) ([]schema.SagaNode, error) {
	if completed == nil {
		completed = map[string]schema.StepResult{}
	}
	nodesByID := make(map[string]schema.SagaNode, len(graph.Nodes))
	for _, node := range graph.Nodes {
		if _, exists := nodesByID[node.ID]; exists {
			return nil, fmt.Errorf("duplicate saga node %q", node.ID)
		}
		nodesByID[node.ID] = node
	}
	ready := make([]schema.SagaNode, 0)
	for _, node := range graph.Nodes {
		if resultSucceeded(completed[node.ID]) {
			continue
		}
		blocked := false
		for _, required := range requiresForNode(graph, node) {
			result, ok := completed[required]
			if !ok || !resultSucceeded(result) {
				blocked = true
				break
			}
		}
		if !blocked {
			ready = append(ready, node)
		}
	}
	sort.SliceStable(ready, func(i, j int) bool { return ready[i].ID < ready[j].ID })
	return ready, nil
}

func ReverseTopologicalCompleted(graph schema.SagaGraph, completed map[string]schema.StepResult) ([]schema.SagaNode, error) {
	if completed == nil {
		return nil, nil
	}
	ordered, err := TopologicalOrder(graph)
	if err != nil {
		return nil, err
	}
	out := make([]schema.SagaNode, 0)
	for i := len(ordered) - 1; i >= 0; i-- {
		if resultSucceeded(completed[ordered[i].ID]) {
			out = append(out, ordered[i])
		}
	}
	return out, nil
}

func TopologicalOrder(graph schema.SagaGraph) ([]schema.SagaNode, error) {
	nodes := make(map[string]schema.SagaNode, len(graph.Nodes))
	indegree := make(map[string]int, len(graph.Nodes))
	outgoing := make(map[string][]string, len(graph.Nodes))
	for _, node := range graph.Nodes {
		if _, exists := nodes[node.ID]; exists {
			return nil, fmt.Errorf("duplicate saga node %q", node.ID)
		}
		nodes[node.ID] = node
		indegree[node.ID] = 0
	}
	for _, node := range graph.Nodes {
		for _, required := range requiresForNode(graph, node) {
			if _, exists := nodes[required]; !exists {
				return nil, fmt.Errorf("saga node %q requires missing node %q", node.ID, required)
			}
			indegree[node.ID]++
			outgoing[required] = append(outgoing[required], node.ID)
		}
	}
	ready := make([]string, 0)
	for id, degree := range indegree {
		if degree == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)
	ordered := make([]schema.SagaNode, 0, len(graph.Nodes))
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		ordered = append(ordered, nodes[id])
		for _, next := range outgoing[id] {
			indegree[next]--
			if indegree[next] == 0 {
				ready = append(ready, next)
				sort.Strings(ready)
			}
		}
	}
	if len(ordered) != len(graph.Nodes) {
		return nil, fmt.Errorf("saga graph contains a dependency cycle")
	}
	return ordered, nil
}

func requiresForNode(graph schema.SagaGraph, node schema.SagaNode) []string {
	if len(node.Requires) > 0 {
		return append([]string(nil), node.Requires...)
	}
	requires := make([]string, 0)
	for _, edge := range graph.Edges {
		if edge.To == node.ID {
			requires = append(requires, edge.From)
		}
	}
	sort.Strings(requires)
	return requires
}

func resultSucceeded(result schema.StepResult) bool {
	return result.Status == string(stepsSucceededStatus())
}

func stepsSucceededStatus() string {
	return "succeeded"
}
