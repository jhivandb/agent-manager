package amcmd

import (
	"github.com/wso2/agent-manager/internal/am/clierr"
	"github.com/wso2/agent-manager/internal/am/cmd"
	"github.com/wso2/agent-manager/internal/am/cmdutil"
	"github.com/wso2/agent-manager/internal/am/config"
	"github.com/wso2/agent-manager/internal/am/iostreams"
	"github.com/wso2/agent-manager/internal/am/render"
)

// Main loads config, builds the root command, and executes it. Returns the
// process exit code.
func Main() int {
	io := iostreams.System()

	path, err := config.DefaultPath()
	if err != nil {
		_ = render.Error(io, render.Scope{}, clierr.Newf(clierr.ConfigNotLoaded, "%v", err))
		return 1
	}
	cfg, err := config.Load(path)
	if err != nil {
		_ = render.Error(io, render.Scope{}, clierr.Newf(clierr.ConfigNotLoaded, "%v", err))
		return 1
	}

	root, err := cmd.NewRootCmd(cmdutil.NewFactory(cfg, io))
	if err != nil {
		return 1
	}
	if err := root.Execute(); err != nil {
		return 1
	}
	return 0
}
