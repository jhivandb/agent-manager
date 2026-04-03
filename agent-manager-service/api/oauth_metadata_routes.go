package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/wso2/ai-agent-management-platform/agent-manager-service/config"
)

type protectedResourceMetadata struct {
	ResourceName           string   `json:"resource_name"`
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers,omitempty"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
	ScopesSupported        []string `json:"scopes_supported,omitempty"`
}

func registerOAuthProtectedResourceMetadata(mux *http.ServeMux) {
	mux.HandleFunc("/.well-known/oauth-protected-resource", oauthProtectedResourceMetadata)
}

func oauthProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	cfg := config.GetConfig()
	metadata := protectedResourceMetadata{
		ResourceName:           "Agent Manager MCP Server",
		Resource:               requestBaseURL(r) + "/mcp",
		AuthorizationServers:   nil,
		BearerMethodsSupported: []string{"header"},
		ScopesSupported:        []string{},
	}

	if cfg != nil && len(cfg.KeyManagerConfigurations.Issuer) > 0 {
		metadata.AuthorizationServers = cfg.KeyManagerConfigurations.Issuer
	}

	data, err := json.Marshal(metadata)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = strings.TrimSpace(strings.Split(proto, ",")[0])
	}
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	return scheme + "://" + host	
}