package tools

import gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

// Register wires all MCP tools to the server.
func (t *Toolsets) Register(server *gomcp.Server) {
	if t == nil {
		return
	}
	if t.AgentToolset != nil {
		t.registerAgentTools(server)
		t.registerMetricsTools(server)
	}

	t.registerDocTools(server)

	if t.ProjectToolset != nil {
		t.registerProjectTools(server)
	}
	// if t.RepositoryToolset != nil {
	// 	t.registerRepositoryTools(server)
	// }

	if t.BuildToolset != nil {
		t.registerBuildTools(server)
	}
	if t.DeploymentToolset != nil {
		t.registerDeploymentTools(server)
	}
	// if t.TraceToolset != nil {
	// 	t.registerTraceTools(server)
	// }
	if t.RuntimeLogToolset != nil {
		t.registerRuntimeLogTools(server)
	}
	if t.EvaluatorToolset != nil {
		t.registerEvaluatorTools(server)
	}
	if t.MonitorToolset != nil {
		t.registerMonitorTools(server)
	}
	if t.MonitorScoresToolset != nil {
		t.registerMonitorScoresTools(server)
	}
}
