---
name: creating-custom-evals
description: Creates AMP custom evaluators (code or LLM-judge) via the create_custom_evaluator MCP tool. Builds trace, agent, or LLM-level evaluators in Agent Manager. Use when the user mentions evaluators, evals, scoring agents, judging responses, or quality checks for agent traces.
---

# Creating Custom Evaluators

## Instructions

Follow these steps when the user asks to create a custom evaluator:

1. **Clarify the goal** — Ask what behavior or quality to evaluate (e.g., helpfulness, tool usage, safety, coherence).
2. **Decide the evaluator type:**
   - `code` — Python function that programmatically analyzes trace data
   - `llm_judge` — Prompt template evaluated by an LLM
3. **Decide the evaluation level:**
   - `trace` — Once per trace (end-to-end assessment)
   - `agent` — Once per agent span (individual agent performance)
   - `llm` — Once per LLM call (per-call quality)
4. **Draft the full `source`** using the matching template from the Reference section below.
5. **Define `config_schema`** so it matches the code arguments or prompt placeholders.
6. **Call the `create_custom_evaluator` MCP tool** with the completed payload.

## Examples

### Example 1: Code Evaluator — Check Response is Non-Empty (Trace Level)

**User request:** "Create an evaluator that checks if the agent actually produced a response."

**What to produce:**

- Type: `code`, Level: `trace`
- Source:

```python
from amp_evaluation import EvalResult
from amp_evaluation.trace.models import Trace


def non_empty_response(
    trace: Trace,
    min_length: int = 10,
) -> EvalResult:
    """Check that the agent produced a non-empty, meaningful response."""
    agent_output = trace.output or ""

    if not agent_output.strip():
        return EvalResult.skip("No output to evaluate")

    if len(agent_output.strip()) < min_length:
        return EvalResult(
            score=0.25,
            passed=False,
            explanation=f"Response too short ({len(agent_output.strip())} chars, minimum {min_length})",
        )

    return EvalResult(
        score=1.0,
        passed=True,
        explanation=f"Response is {len(agent_output.strip())} chars",
    )
```

- Config schema: `min_length` (int, default 10)

### Example 2: LLM-Judge Evaluator — Helpfulness (Trace Level)

**User request:** "Create an evaluator that judges how helpful the agent's response is."

**What to produce:**

- Type: `llm_judge`, Level: `trace`
- Source:

```text
You are an expert evaluator. Your sole criterion is HELPFULNESS.

User Query:
{trace.input}

Agent Response:
{trace.output}

Evaluation Steps:
1. Identify what the user needs.
2. Assess whether the response provides actionable, useful content.
3. Check for empty helpfulness: does the response acknowledge the question without actually helping?
4. Assess whether the response would leave the user better off than before they asked.

Scoring Rubric:
  0.0  = Not helpful at all; ignores the user's need
  0.25 = Minimally helpful; touches on the topic but insufficient
  0.5  = Somewhat helpful; some useful content but gaps remain
  0.75 = Helpful; addresses the need well with only minor gaps
  1.0  = Highly helpful; directly and fully assists the user
```

- Config schema: none

### Example 3: Code Evaluator — Tool Usage (Agent Level)

**User request:** "Create an evaluator that checks whether an agent used the right tools."

**What to produce:**

- Type: `code`, Level: `agent`
- Source:

```python
from amp_evaluation import EvalResult
from amp_evaluation.trace.models import AgentTrace


def tool_usage_check(
    agent_trace: AgentTrace,
    required_tools: str = "",
) -> EvalResult:
    """Check that the agent used the expected tools."""
    tools_used = [s.tool_name for s in agent_trace.get_tool_steps()]

    if not tools_used:
        return EvalResult(score=0.0, passed=False, explanation="Agent did not use any tools")

    if required_tools:
        expected = [t.strip() for t in required_tools.split(",")]
        missing = [t for t in expected if t not in tools_used]
        if missing:
            return EvalResult(
                score=0.5,
                passed=False,
                explanation=f"Missing required tools: {', '.join(missing)}. Used: {', '.join(tools_used)}",
            )

    return EvalResult(
        score=1.0,
        passed=True,
        explanation=f"Agent used {len(tools_used)} tool(s): {', '.join(tools_used)}",
    )
```

- Config schema: `required_tools` (str, comma-separated list of expected tool names)

## Reference

### Evaluation Levels

| Level | Type Hint | Called |
|-------|-----------|--------|
| `trace` | `Trace` | Once per trace — end-to-end assessment |
| `agent` | `AgentTrace` | Once per agent span — individual agent performance |
| `llm` | `LLMSpan` | Once per LLM call — per-call quality |

### Code Evaluators

Code evaluators are Python **functions** (not classes) that return an `EvalResult`.

#### Rules

- Write a **function** (not a class)
- Type-hint the first parameter to set the evaluation level
- Define configurable parameters as function arguments with plain defaults
- Return `EvalResult(score=0.0-1.0, explanation="...")` — higher is better
- Use `EvalResult.skip("reason")` when evaluation cannot be performed

#### Code Template — Trace Level

```python
from amp_evaluation import EvalResult
from amp_evaluation.trace.models import Trace


def my_evaluator(trace: Trace, threshold: float = 0.5) -> EvalResult:
    agent_output = trace.output or ""
    if not agent_output.strip():
        return EvalResult.skip("No output to evaluate")
    score = 1.0
    return EvalResult(score=score, passed=score >= threshold, explanation="...")
```

**Trace fields:** `trace.input`, `trace.output`, `trace.spans`, `trace.metrics`, `trace.format_evidence()`, `trace.format_spans()`, `trace.get_agents()`, `trace.get_llm_calls()`, `trace.get_retrievals()`, `trace.get_tool_calls()`

#### Code Template — Agent Level

```python
from amp_evaluation import EvalResult
from amp_evaluation.trace.models import AgentTrace


def my_evaluator(agent_trace: AgentTrace, threshold: float = 0.5) -> EvalResult:
    tools_used = [s.tool_name for s in agent_trace.get_tool_steps()]
    if not tools_used:
        return EvalResult(score=0.5, explanation="Agent did not use any tools")
    score = 1.0
    return EvalResult(score=score, passed=score >= threshold, explanation=f"Used {len(tools_used)} tool(s)")
```

**AgentTrace fields:** `agent_trace.input`, `agent_trace.output`, `agent_trace.steps`, `agent_trace.agent_name`, `agent_trace.model`, `agent_trace.system_prompt`, `agent_trace.available_tools`, `agent_trace.metrics`, `agent_trace.format_steps()`, `agent_trace.get_error_steps()`, `agent_trace.get_llm_steps()`, `agent_trace.get_sub_agents()`, `agent_trace.get_tool_steps()`

#### Code Template — LLM Level

```python
from amp_evaluation import EvalResult
from amp_evaluation.trace.models import LLMSpan


def my_evaluator(llm_span: LLMSpan, threshold: float = 0.5) -> EvalResult:
    output = llm_span.output or ""
    if not output.strip():
        return EvalResult.skip("Empty LLM output")
    score = 1.0
    return EvalResult(score=score, passed=score >= threshold, explanation=f"LLM ({llm_span.model}) responded")
```

**LLMSpan fields:** `llm_span.input`, `llm_span.output`, `llm_span.available_tools`, `llm_span.model`, `llm_span.vendor`, `llm_span.temperature`, `llm_span.metrics`, `llm_span.format_messages()`, `llm_span.get_assistant_messages()`, `llm_span.get_system_messages()`, `llm_span.get_tool_messages()`, `llm_span.get_user_messages()`

### LLM-Judge Evaluators

LLM-judge evaluators are **prompt template strings** (not Python code). Use `{expression}` syntax to access trace data. Python expressions (including comprehensions) are supported inside `{ }` — loop statements are not.

The framework auto-appends JSON scoring instructions — **do NOT include scoring/output format instructions**.

#### Rules

- Use `{variable.field}` to access trace data (Python f-string syntax)
- Expressions supported: `{len(trace.spans)}`, `{', '.join(s.tool_name for s in agent_trace.get_tool_steps())}`
- Include a **scoring rubric** (0.0 to 1.0 scale)
- Do NOT include output format instructions
- Variables by level: `trace` (Trace), `agent_trace` (AgentTrace), `llm_span` (LLMSpan)

#### LLM-Judge Template — Trace Level

Variable: `trace` (Trace)

```text
You are an expert evaluator. Your sole criterion is HELPFULNESS: does the response actually help the user with what they asked for?

User Query:
{trace.input}

Agent Response:
{trace.output}

Execution Summary:
- Total spans: {len(trace.spans)}
- Agents involved: {', '.join(a.agent_name or 'unnamed' for a in trace.get_agents()) or 'none'}

Evaluation Steps:
1. Identify what the user needs.
2. Assess whether the response provides actionable, useful content.
3. Check for empty helpfulness: does the response acknowledge the question without actually helping?
4. Assess whether the response would leave the user better off than before they asked.

Scoring Rubric:
  0.0  = Not helpful at all; ignores the user's need
  0.25 = Minimally helpful; touches on the topic but insufficient
  0.5  = Somewhat helpful; some useful content but gaps remain
  0.75 = Helpful; addresses the need well with only minor gaps
  1.0  = Highly helpful; directly and fully assists the user
```

#### LLM-Judge Template — Agent Level

Variable: `agent_trace` (AgentTrace)

```text
You are an expert evaluator. Your sole criterion is TOOL USAGE: does the agent choose and use the right tools effectively?

Agent: {agent_trace.agent_name or 'agent'}
Model: {agent_trace.model}

Goal:
{agent_trace.input}

Final Response:
{agent_trace.output}

Tools Available: {', '.join(t.name for t in agent_trace.available_tools)}
Tools Used: {', '.join(s.tool_name for s in agent_trace.get_tool_steps())}
Total Steps: {len(agent_trace.steps)}

Evaluation Steps:
1. Were the right tools selected for the task?
2. Were tool inputs well-formed and effective?
3. Were there unnecessary tool calls or tools that should have been used but weren't?
4. Did the agent use tool results effectively in its final response?

Scoring Rubric:
  0.0  = Tools used incorrectly or not at all despite being needed
  0.25 = Some tools used but with major errors in selection or usage
  0.5  = Tools used adequately but with unnecessary calls or missed opportunities
  0.75 = Good tool usage with only minor inefficiencies
  1.0  = Optimal tool usage; right tools, right inputs, no waste
```

#### LLM-Judge Template — LLM Level

Variable: `llm_span` (LLMSpan)

```text
You are an expert evaluator. Your sole criterion is COHERENCE: is this LLM response well-structured, logical, and easy to follow?

Model: {llm_span.model}
Vendor: {llm_span.vendor}
Messages in conversation: {len(llm_span.input)}

LLM Response:
{llm_span.output}

Evaluation Steps:
1. Does the response have a clear structure with logical flow?
2. Are ideas connected coherently, or are there abrupt jumps or contradictions?
3. Is the level of detail appropriate and consistent throughout?
4. Would a reader understand the response on first reading?

Scoring Rubric:
  0.0  = Incoherent; disorganized, contradictory, or impossible to follow
  0.25 = Poorly structured; significant logical gaps or confusing organization
  0.5  = Understandable but with structural issues or unclear passages
  0.75 = Well-structured and clear with only minor areas that could be tighter
  1.0  = Exceptionally coherent; perfectly organized, logical, and easy to follow
```

### EvalResult

```python
EvalResult(score=0.85, explanation="Response covers 4 of 5 topics")           # success
EvalResult(score=0.3, passed=False, explanation="Below threshold")            # explicit fail
EvalResult.skip("No output to evaluate")                                      # skip
```

- `score`: float, 0.0 to 1.0 (mandatory). Higher is always better.
- `explanation`: str (recommended).
- `passed`: bool (optional). Defaults to `score >= 0.5`.
- Use `EvalResult.skip(reason)` for missing data — do NOT return score=0.0.

### Common Mistakes

```python
# DON'T: Return score outside 0-1 range
EvalResult(score=5.0, ...)  # ValueError!

# DON'T: Return 0.0 for missing data — use skip
if not trace.output:
    return EvalResult(score=0.0, explanation="No output")  # Wrong
    return EvalResult.skip("No output to evaluate")         # Correct

# DON'T: Include scoring instructions in LLM-judge prompts
# The framework appends them automatically.
```

### Detailed Data Models

For full field definitions of span types, agent step types, message types, and metrics, see [data-models.md](data-models.md).
