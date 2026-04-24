# Supporting Data Models

These types appear as fields or return values in the main evaluator input types (Trace, AgentTrace, LLMSpan).

## Contents

- [Span Types](#span-types) — AgentSpan, RetrieverSpan, ToolSpan
- [Agent Step Types](#agent-step-types) — LLMReasoningStep, ToolExecutionStep, UserInputStep
- [Message Types](#message-types) — AssistantMessage, SystemMessage, ToolMessage, UserMessage
- [Metrics](#metrics) — AgentMetrics, LLMMetrics, RetrieverMetrics, TokenUsage, ToolMetrics, TraceMetrics
- [Other](#other) — RetrievedDoc, ToolCall, ToolCallInfo, ToolDefinition

## Span Types

**AgentSpan**

- `agentSpan.name`: `str` — Name of the agent
- `agentSpan.framework`: `str` — Framework (crewai, langchain, openai_agents, etc.)
- `agentSpan.model`: `str` — LLM model used by the agent
- `agentSpan.system_prompt`: `str` — System prompt / instructions
- `agentSpan.available_tools`: `List[ToolDefinition]` — Tools available to the agent
- `agentSpan.max_iterations`: `int | None` — Maximum iterations allowed
- `agentSpan.input`: `str` — Agent input
- `agentSpan.output`: `str` — Agent output
- `agentSpan.metrics`: `AgentMetrics` — Agent performance metrics

**RetrieverSpan**

- `retrieverSpan.query`: `str` — Retrieval query
- `retrieverSpan.documents`: `List[RetrievedDoc]` — Retrieved documents
- `retrieverSpan.vector_db`: `str` — Vector database used
- `retrieverSpan.top_k`: `int` — Number of documents requested
- `retrieverSpan.metrics`: `RetrieverMetrics` — Retrieval performance metrics

**ToolSpan**

- `toolSpan.name`: `str` — Tool name
- `toolSpan.arguments`: `Dict[str, Any]` — Arguments passed to the tool
- `toolSpan.result`: `Any` — Execution result

## Agent Step Types

**LLMReasoningStep**

- `lLMReasoningStep.content`: `str` — LLM response text
- `lLMReasoningStep.tool_calls`: `List[ToolCallInfo]` — Tool calls requested by the LLM
- `lLMReasoningStep.is_response`: `bool` — True if this is a final response (no tool calls requested)

**ToolExecutionStep**

- `toolExecutionStep.tool_name`: `str` — Name of the tool
- `toolExecutionStep.tool_input`: `Dict[str, Any] | None` — Input passed to the tool
- `toolExecutionStep.tool_output`: `Any | None` — Output returned by the tool
- `toolExecutionStep.content`: `str` — What was fed back to the LLM
- `toolExecutionStep.error`: `str | None` — Error message if failed
- `toolExecutionStep.duration_ms`: `float | None` — Execution duration in milliseconds
- `toolExecutionStep.nested_traces`: `List[LLMSpan | AgentTrace]` — Nested LLM calls or sub-agent traces

**UserInputStep**

- `userInputStep.content`: `str` — User message content

## Message Types

**AssistantMessage**

- `assistantMessage.content`: `str` — Response text
- `assistantMessage.tool_calls`: `List[ToolCall]` — Tool calls requested

**SystemMessage**

- `systemMessage.content`: `str` — System prompt text

**ToolMessage**

- `toolMessage.content`: `str` — Tool result text
- `toolMessage.tool_call_id`: `str` — ID of the originating tool call

**UserMessage**

- `userMessage.content`: `str` — User input text

## Metrics

**AgentMetrics**

- `agentMetrics.duration_ms`: `float` — Span duration in milliseconds
- `agentMetrics.error`: `bool` — Whether an error occurred
- `agentMetrics.error_type`: `str | None` — Error type if an error occurred
- `agentMetrics.error_message`: `str | None` — Error message if an error occurred
- `agentMetrics.token_usage`: `TokenUsage` — Token usage breakdown

**LLMMetrics**

- `lLMMetrics.duration_ms`: `float` — Span duration in milliseconds
- `lLMMetrics.error`: `bool` — Whether an error occurred
- `lLMMetrics.error_type`: `str | None` — Error type if an error occurred
- `lLMMetrics.error_message`: `str | None` — Error message if an error occurred
- `lLMMetrics.token_usage`: `TokenUsage` — Token usage breakdown
- `lLMMetrics.time_to_first_token_ms`: `float | None` — Time to first token in milliseconds

**RetrieverMetrics**

- `retrieverMetrics.duration_ms`: `float` — Span duration in milliseconds
- `retrieverMetrics.error`: `bool` — Whether an error occurred
- `retrieverMetrics.error_type`: `str | None` — Error type if an error occurred
- `retrieverMetrics.error_message`: `str | None` — Error message if an error occurred
- `retrieverMetrics.documents_retrieved`: `int` — Number of documents retrieved

**TokenUsage**

- `tokenUsage.input_tokens`: `int` — Number of input tokens
- `tokenUsage.output_tokens`: `int` — Number of output tokens
- `tokenUsage.total_tokens`: `int` — Total tokens (input + output)
- `tokenUsage.cache_read_tokens`: `int` — Cached prompt tokens (if supported)

**ToolMetrics**

- `toolMetrics.duration_ms`: `float` — Span duration in milliseconds
- `toolMetrics.error`: `bool` — Whether an error occurred
- `toolMetrics.error_type`: `str | None` — Error type if an error occurred
- `toolMetrics.error_message`: `str | None` — Error message if an error occurred

**TraceMetrics**

- `traceMetrics.total_duration_ms`: `float` — Total trace duration in milliseconds
- `traceMetrics.token_usage`: `TokenUsage` — Aggregated token usage across all LLM calls
- `traceMetrics.error_count`: `int` — Number of spans with errors
- `traceMetrics.has_errors`: `bool` — Check if any errors occurred in the trace

## Other

**RetrievedDoc**

- `retrievedDoc.id`: `str` — Document identifier
- `retrievedDoc.content`: `str` — Document content
- `retrievedDoc.score`: `float` — Relevance score
- `retrievedDoc.metadata`: `Dict[str, Any]` — Document metadata

**ToolCall**

- `toolCall.id`: `str` — Unique tool call identifier
- `toolCall.name`: `str` — Name of the tool
- `toolCall.arguments`: `Dict[str, Any]` — Arguments passed to the tool

**ToolCallInfo**

- `toolCallInfo.id`: `str` — Unique tool call identifier
- `toolCallInfo.name`: `str` — Name of the tool
- `toolCallInfo.arguments`: `Dict[str, Any]` — Arguments passed

**ToolDefinition**

- `toolDefinition.name`: `str` — Tool name
- `toolDefinition.description`: `str` — Tool description
- `toolDefinition.parameters`: `str` — JSON schema of parameters
