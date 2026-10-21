package deepseek

import (
	"fmt"
	"strings"
)

// Validate checks structural invariants, not model-specific limits that may change.
func (r ChatCompletionRequest) Validate() error {
	if strings.TrimSpace(r.Model) == "" {
		return &ValidationError{"model", "required"}
	}

	if len(r.Messages) == 0 {
		return &ValidationError{"messages", "must not be empty"}
	}

	if r.MaxTokens != nil && *r.MaxTokens <= 0 {
		return &ValidationError{"max_tokens", "must be positive"}
	}

	for i, m := range r.Messages {
		field := fmt.Sprintf("messages[%d]", i)
		switch m.Role {
		case RoleSystem, RoleUser, RoleTool:
			if m.Content.IsNull() {
				return &ValidationError{field + ".content", "required"}
			}
		case RoleAssistant:
		default:
			return &ValidationError{field + ".role", "must be system, user, assistant, or tool"}
		}

		if m.Role == RoleTool && m.ToolCallID == "" {
			return &ValidationError{field + ".tool_call_id", "required"}
		}
	}

	return nil
}
