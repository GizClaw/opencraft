package bindings

import "testing"

func TestParseAgentGraph(t *testing.T) {
	raw := `{"name":"sub","entry":"llm","nodes":[{"id":"llm","type":"inference","config":{"system_prompt":"SP"}}],"edges":[{"from":"llm","to":"__end__","condition":"tool_pending == false"}]}`
	graph, err := parseAgentGraph(raw)
	if err != nil {
		t.Fatal(err)
	}
	if graph.Name != "sub" || graph.Entry != "llm" ||
		len(graph.Nodes) != 1 || len(graph.Edges) != 1 {
		t.Fatalf("graph = %+v", graph)
	}
	if got := string(graph.Nodes[0].Config); got != `{"system_prompt":"SP"}` {
		t.Fatalf("node config = %s", got)
	}
}
