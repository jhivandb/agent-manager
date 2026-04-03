package mcp

import "net/http"

// responseWriter401Interceptor wraps http.ResponseWriter to intercept 401 status codes.
type responseWriter401Interceptor struct {
	http.ResponseWriter
	statusCode          int
	headerWritten       bool
	resourceMetadataURL string
}

// WriteHeader intercepts the status code and adds WWW-Authenticate header on 401.
func (rw *responseWriter401Interceptor) WriteHeader(statusCode int) {
	if rw.headerWritten {
		return
	}

	rw.statusCode = statusCode
	rw.headerWritten = true

	if statusCode == http.StatusUnauthorized {
		rw.ResponseWriter.Header().Set("WWW-Authenticate", "Bearer resource_metadata=\""+rw.resourceMetadataURL+"\"")
	}

	rw.ResponseWriter.WriteHeader(statusCode)
}

// Write intercepts the write to ensure WriteHeader is called.
func (rw *responseWriter401Interceptor) Write(b []byte) (int, error) {
	if !rw.headerWritten {
		rw.WriteHeader(http.StatusOK)
	}
	return rw.ResponseWriter.Write(b)
}

// Auth401Interceptor adds the OAuth resource metadata hint header for 401 responses.
func Auth401Interceptor() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			interceptor := &responseWriter401Interceptor{
				ResponseWriter:      w,
				statusCode:          http.StatusOK,
				headerWritten:       false,
				resourceMetadataURL: buildResourceMetadataURL(r),
			}
			next.ServeHTTP(interceptor, r)
		})
	}
}

func buildResourceMetadataURL(r *http.Request) string {
	return requestBaseURL(r) + "/.well-known/oauth-protected-resource"
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	return scheme + "://" + host
}