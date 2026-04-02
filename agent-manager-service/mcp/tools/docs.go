package tools

import (
	"context"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type docsSitemapOutput struct {
	Site       string `json:"site"`
	SitemapURL string `json:"sitemap_url"`
	Note       string `json:"note"`
}

func (t *Toolsets) registerDocTools(server *gomcp.Server) {
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "get_docs_sitemap",
		Description: "Return the Agent Manager documentation sitemap URL. use when further details about the agent manager platform or its capabilities are needed.",
		InputSchema: createSchema(map[string]any{}, nil),
	}, getDocsSitemap())
}

func getDocsSitemap() func(context.Context, *gomcp.CallToolRequest, map[string]any) (*gomcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, _ map[string]any) (*gomcp.CallToolResult, any, error) {
		_ = ctx
		response := docsSitemapOutput{
			Site:       "agent-manager",
			SitemapURL: "https://wso2.github.io/agent-manager/llms.txt",
			Note:       "Use this URL to get answers related to Agent Manager's capabilities.",
		}
		return handleToolResult(response, nil)
	}
}
