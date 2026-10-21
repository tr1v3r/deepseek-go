package deepseek

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
)

// Accumulator combines streamed choices and indexed tool arguments. Its zero
// value is ready to use; it is not concurrency-safe. It retains all content in
// memory, so use Recv directly for unbounded output. It does not execute tools.
type Accumulator struct {
	result  ChatCompletion
	choices map[int]*accumulatedChoice
}

type accumulatedChoice struct {
	choice     Choice
	tools      map[int]*accumulatedTool
	content    strings.Builder
	reasoning  strings.Builder
	hasContent bool
}

type accumulatedTool struct {
	call      ToolCall
	name      strings.Builder
	arguments strings.Builder
}

// Add validates completion IDs, indexes, and tool identities before changing
// state. A rejected chunk leaves the accumulator unchanged. Only Recv's io.EOF
// confirms that the stream has completed; Add does not track [DONE].
func (a *Accumulator) Add(chunk ChatCompletionChunk) error {
	if err := a.validateChunk(chunk); err != nil {
		return err
	}

	if a.choices == nil {
		a.choices = make(map[int]*accumulatedChoice)
	}

	a.updateMetadata(chunk)

	for _, delta := range chunk.Choices {
		choice := a.choices[delta.Index]
		if choice == nil {
			choice = &accumulatedChoice{
				choice: Choice{
					Index:   delta.Index,
					Message: Message{Role: RoleAssistant},
				},
				tools: make(map[int]*accumulatedTool),
			}

			a.choices[delta.Index] = choice
		}

		choice.add(delta)
	}

	return nil
}

func (a *Accumulator) validateChunk(chunk ChatCompletionChunk) error {
	if a.result.ID != "" && chunk.ID != "" && a.result.ID != chunk.ID {
		return &ProtocolError{Message: "cannot combine different completion IDs"}
	}

	// Track identities locally so conflicts inside this chunk are also rejected
	// before any accumulated content is changed.
	type toolKey struct{ choice, tool int }
	type identity struct{ id, kind string }
	seen := make(map[toolKey]identity)

	for _, choice := range chunk.Choices {
		if choice.Index < 0 {
			return &ProtocolError{Message: "negative choice index"}
		}

		for _, tool := range choice.Delta.ToolCalls {
			if tool.Index == nil || *tool.Index < 0 {
				return &ProtocolError{Message: "tool delta requires a nonnegative index"}
			}

			key := toolKey{choice: choice.Index, tool: *tool.Index}
			previous, exists := seen[key]
			if !exists {
				if accumulated := a.choices[choice.Index]; accumulated != nil {
					if existing := accumulated.tools[*tool.Index]; existing != nil {
						previous = identity{id: existing.call.ID, kind: existing.call.Type}
					}
				}
			}

			if previous.id != "" && tool.ID != "" && previous.id != tool.ID {
				return &ProtocolError{Message: "conflicting tool IDs at the same index"}
			}

			if previous.kind != "" && tool.Type != "" && previous.kind != tool.Type {
				return &ProtocolError{Message: "conflicting tool types at the same index"}
			}

			if tool.ID != "" {
				previous.id = tool.ID
			}

			if tool.Type != "" {
				previous.kind = tool.Type
			}

			seen[key] = previous
		}
	}

	return nil
}

func (a *Accumulator) updateMetadata(chunk ChatCompletionChunk) {
	if chunk.ID != "" {
		a.result.ID = chunk.ID
	}

	if chunk.Model != "" {
		a.result.Model = chunk.Model
	}

	if chunk.Created != 0 {
		a.result.Created = chunk.Created
	}

	if chunk.SystemFingerprint != "" {
		a.result.SystemFingerprint = chunk.SystemFingerprint
	}

	if chunk.Usage != nil {
		a.result.Usage = cloneUsage(chunk.Usage)
	}

	a.result.Object = "chat.completion"
}

func (c *accumulatedChoice) add(delta ChunkChoice) {
	if delta.Delta.Role != "" {
		c.choice.Message.Role = delta.Delta.Role
	}

	if delta.Delta.Content != nil {
		c.hasContent = true
		c.content.WriteString(*delta.Delta.Content)
	}

	if delta.Delta.ReasoningContent != nil {
		c.reasoning.WriteString(*delta.Delta.ReasoningContent)
	}

	if delta.FinishReason != nil {
		c.choice.FinishReason = Ptr(*delta.FinishReason)
	}

	// Preserve the latest non-null chunk; token-level merging is unsupported.
	logprobs := bytes.TrimSpace(delta.Logprobs)
	if len(logprobs) > 0 && !bytes.Equal(logprobs, []byte("null")) {
		c.choice.Logprobs = append(json.RawMessage(nil), logprobs...)
	}

	for _, toolDelta := range delta.Delta.ToolCalls {
		tool := c.tools[*toolDelta.Index]
		if tool == nil {
			tool = &accumulatedTool{}
			c.tools[*toolDelta.Index] = tool
		}

		if toolDelta.ID != "" {
			tool.call.ID = toolDelta.ID
		}

		if toolDelta.Type != "" {
			tool.call.Type = toolDelta.Type
		}

		tool.name.WriteString(toolDelta.Function.Name)
		tool.arguments.WriteString(toolDelta.Function.Arguments)
	}
}

// Completion returns a detached snapshot, sorted by choice and tool indexes.
func (a *Accumulator) Completion() ChatCompletion {
	result := a.result
	result.Usage = cloneUsage(a.result.Usage)
	result.Choices = make([]Choice, 0, len(a.choices))

	for _, choice := range a.choices {
		result.Choices = append(result.Choices, choice.snapshot())
	}

	sort.Slice(result.Choices, func(i, j int) bool {
		return result.Choices[i].Index < result.Choices[j].Index
	})

	return result
}

func (c *accumulatedChoice) snapshot() Choice {
	result := c.choice
	if c.hasContent {
		result.Message.Content = Text(c.content.String())
	}

	result.Message.ReasoningContent = c.reasoning.String()
	result.Logprobs = append(json.RawMessage(nil), result.Logprobs...)
	if result.FinishReason != nil {
		result.FinishReason = Ptr(*result.FinishReason)
	}

	indexes := make([]int, 0, len(c.tools))
	for index := range c.tools {
		indexes = append(indexes, index)
	}

	sort.Ints(indexes)
	result.Message.ToolCalls = make([]ToolCall, 0, len(indexes))

	for _, index := range indexes {
		tool := c.tools[index]
		call := tool.call
		call.Function = FunctionCall{
			Name:      tool.name.String(),
			Arguments: tool.arguments.String(),
		}

		result.Message.ToolCalls = append(result.Message.ToolCalls, call)
	}

	return result
}

func cloneUsage(usage *Usage) *Usage {
	if usage == nil {
		return nil
	}

	result := *usage
	if usage.PromptTokensDetails != nil {
		result.PromptTokensDetails = Ptr(*usage.PromptTokensDetails)
	}

	if usage.CompletionTokensDetails != nil {
		result.CompletionTokensDetails = Ptr(*usage.CompletionTokensDetails)
	}

	return &result
}
