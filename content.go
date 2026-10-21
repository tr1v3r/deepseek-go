package deepseek

import (
	"bytes"
	"encoding/json"
	"fmt"
)

const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Content is the string/parts/null union used by the API. Its zero value is null.
// Use Text("") for an explicit empty string, Parts for multimodal input.
type Content struct {
	text  *string
	parts []ContentPart
}

// Text constructs a text value, preserving an explicit empty string.
func Text(s string) Content { return Content{text: &s} }

// Parts copies the input slice. Nested ImageURL pointers remain shared; do not mutate them during a request.
func Parts(parts ...ContentPart) Content { return Content{parts: append([]ContentPart{}, parts...)} }

// Text returns the textual value, or "" for null/multimodal content.
func (c Content) Text() string {
	if c.text == nil {
		return ""
	}

	return *c.text
}

// IsNull reports whether content is null rather than a string or parts array.
func (c Content) IsNull() bool {
	return c.text == nil && c.parts == nil
}

// Parts returns a copy of the content part slice; nested ImageURL values are shared.
func (c Content) Parts() []ContentPart {
	return append([]ContentPart(nil), c.parts...)
}

func (c Content) MarshalJSON() ([]byte, error) {
	if c.text != nil {
		return json.Marshal(*c.text)
	}

	if c.parts != nil {
		return json.Marshal(c.parts)
	}

	return []byte("null"), nil
}

func (c *Content) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	var next Content
	if bytes.Equal(b, []byte("null")) {
		*c = next
		return nil
	}

	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}

		next = Text(s)
	} else if len(b) > 0 && b[0] == '[' {
		if err := json.Unmarshal(b, &next.parts); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("deepseek: content must be string, array, or null")
	}

	*c = next
	return nil
}

// UserMessage constructs a text-only user message.
func UserMessage(text string) Message { return Message{Role: RoleUser, Content: Text(text)} }

// SystemMessage constructs a text-only system message.
func SystemMessage(text string) Message { return Message{Role: RoleSystem, Content: Text(text)} }

// ToolMessage constructs a tool result associated with an assistant tool-call ID.
func ToolMessage(callID, text string) Message {
	return Message{Role: RoleTool, ToolCallID: callID, Content: Text(text)}
}

// ToolChoice selects auto, none, required, or a named function. Use the constructors.
type ToolChoice struct{ mode, name string }

// AutoToolChoice lets the model decide whether to call a tool.
func AutoToolChoice() *ToolChoice { return &ToolChoice{mode: "auto"} }

// NoToolChoice disables tool calls for the request.
func NoToolChoice() *ToolChoice { return &ToolChoice{mode: "none"} }

// RequiredToolChoice requires a tool call when the chosen model/mode supports it.
func RequiredToolChoice() *ToolChoice { return &ToolChoice{mode: "required"} }

// NamedToolChoice selects a function by name. An empty name fails request encoding.
func NamedToolChoice(name string) *ToolChoice { return &ToolChoice{name: name} }

type namedToolChoice struct {
	Type     string        `json:"type"`
	Function namedFunction `json:"function"`
}

type namedFunction struct {
	Name string `json:"name"`
}

func (c ToolChoice) MarshalJSON() ([]byte, error) {
	if c.name != "" {
		return json.Marshal(namedToolChoice{
			Type:     "function",
			Function: namedFunction{Name: c.name},
		})
	}

	switch c.mode {
	case "auto", "none", "required":
		return json.Marshal(c.mode)
	}

	return nil, &ValidationError{
		Field:   "tool_choice",
		Message: "requires a mode or function name",
	}
}

func (c *ToolChoice) UnmarshalJSON(b []byte) error {
	var mode string
	if json.Unmarshal(b, &mode) == nil {
		switch mode {
		case "auto", "none", "required":
			*c = ToolChoice{mode: mode}
			return nil
		}
	}

	var named namedToolChoice

	if err := json.Unmarshal(b, &named); err == nil && named.Type == "function" && named.Function.Name != "" {
		*c = ToolChoice{name: named.Function.Name}
		return nil
	}

	return &ValidationError{Field: "tool_choice", Message: "invalid selection"}
}

// MarshalJSON retains the required text field, including an empty text part.
func (part ContentPart) MarshalJSON() ([]byte, error) {
	type wirePart ContentPart

	var text *string
	if part.Type == "text" {
		text = &part.Text
	} else if part.Text != "" {
		text = &part.Text
	}

	return json.Marshal(struct {
		wirePart
		Text *string `json:"text,omitempty"`
	}{
		wirePart: wirePart(part),
		Text:     text,
	})
}
