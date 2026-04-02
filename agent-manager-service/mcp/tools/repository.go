package tools

// import (
// 	"context"
// 	"fmt"
// 	"time"

// 	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

// 	"github.com/wso2/agent-manager/agent-manager-service/spec"
// 	"github.com/wso2/agent-manager/agent-manager-service/utils"
// )

// type listCommitsInput struct {
// 	Owner  string  `json:"owner"`
// 	Repo   string  `json:"repo"`
// 	Branch *string `json:"branch,omitempty"`
// 	Path   *string `json:"path,omitempty"`
// 	Author *string `json:"author,omitempty"`
// 	Since  *string `json:"since,omitempty"`
// 	Until  *string `json:"until,omitempty"`
// 	Limit  *int    `json:"limit,omitempty"`
// 	Offset *int    `json:"offset,omitempty"`
// }

// func (t *Toolsets) registerRepositoryTools(server *gomcp.Server) {
// 	gomcp.AddTool(server, &gomcp.Tool{
// 		Name:        "list_commits",
// 		Description: "List recent commits for a GitHub repository with optional filters.",
// 		InputSchema: createSchema(map[string]any{
// 			"owner":  stringProperty("Required. Repository owner (org or user)."),
// 			"repo":   stringProperty("Required. Repository name."),
// 			"branch": stringProperty("Optional. Branch name or SHA."),
// 			"path":   stringProperty("Optional. Filter commits affecting this path."),
// 			"author": stringProperty("Optional. Filter commits by author."),
// 			"since":  stringProperty("Optional. RFC3339 start time for commits."),
// 			"until":  stringProperty("Optional. RFC3339 end time for commits."),
// 			"limit":  intProperty(fmt.Sprintf("Optional. Max commits to return (default %d, min %d, max %d).", utils.DefaultLimit, utils.MinLimit, utils.MaxLimit)),
// 			"offset": intProperty(fmt.Sprintf("Optional. Pagination offset (default %d, min %d).", utils.DefaultOffset, utils.MinOffset)),
// 		}, []string{"owner", "repo"}),
// 	}, listCommits(t.RepositoryToolset))
// }

// func listCommits(handler RepositoryToolsetHandler) func(context.Context, *gomcp.CallToolRequest, listCommitsInput) (*gomcp.CallToolResult, any, error) {
// 	return func(ctx context.Context, _ *gomcp.CallToolRequest, input listCommitsInput) (*gomcp.CallToolResult, any, error) {
// 		if input.Owner == "" {
// 			return nil, nil, fmt.Errorf("owner is required")
// 		}
// 		if input.Repo == "" {
// 			return nil, nil, fmt.Errorf("repo is required")
// 		}

// 		limit := utils.DefaultLimit
// 		if input.Limit != nil {
// 			limit = *input.Limit
// 		}
// 		if limit < utils.MinLimit || limit > utils.MaxLimit {
// 			return nil, nil, fmt.Errorf("limit must be between %d and %d", utils.MinLimit, utils.MaxLimit)
// 		}

// 		offset := utils.DefaultOffset
// 		if input.Offset != nil {
// 			offset = *input.Offset
// 		}
// 		if offset < utils.MinOffset {
// 			return nil, nil, fmt.Errorf("offset must be >= %d", utils.MinOffset)
// 		}

// 		req := spec.ListCommitsRequest{
// 			Owner: input.Owner,
// 			Repo:  input.Repo,
// 		}
// 		if input.Branch != nil {
// 			req.Branch = input.Branch
// 		}
// 		if input.Path != nil {
// 			req.Path = input.Path
// 		}
// 		if input.Author != nil {
// 			req.Author = input.Author
// 		}
// 		if input.Since != nil {
// 			parsed, err := time.Parse(time.RFC3339, *input.Since)
// 			if err != nil {
// 				return nil, nil, fmt.Errorf("invalid since format (use RFC3339)")
// 			}
// 			req.Since = &parsed
// 		}
// 		if input.Until != nil {
// 			parsed, err := time.Parse(time.RFC3339, *input.Until)
// 			if err != nil {
// 				return nil, nil, fmt.Errorf("invalid until format (use RFC3339)")
// 			}
// 			req.Until = &parsed
// 		}

// 		result, err := handler.ListCommits(ctx, req, limit, offset)
// 		if err != nil {
// 			return nil, nil, wrapToolError("list_commits", err)
// 		}
// 		return handleToolResult(result, nil)
// 	}
// }
