package traceobserversvc

// TraceListParams holds query parameters for listing/exporting traces.
type TraceListParams struct {
	ComponentUid   string
	EnvironmentUid string
	StartTime      string
	EndTime        string
	Limit          int
	Offset         int
	SortOrder      string
}

// TraceDetailsParams holds query parameters for fetching a specific trace.
type TraceDetailsParams struct {
	TraceID        string
	ComponentUid   string
	EnvironmentUid string
	SortOrder      string
	Limit          int
	StartTime      string
	EndTime        string
	ParentSpan     *bool
}
