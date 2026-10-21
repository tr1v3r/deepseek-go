package deepseek_test

import (
	"encoding/json"
	"fmt"

	deepseek "github.com/tr1v3r/deepseek-go"
)

func ExampleUserMessage() {
	b, _ := json.Marshal(deepseek.UserMessage("hello"))
	fmt.Println(string(b))
	// Output: {"role":"user","content":"hello"}
}

func ExampleContent() {
	content := deepseek.Text("")
	fmt.Println(content.IsNull())
	fmt.Println((deepseek.Content{}).IsNull())
	// Output:
	// false
	// true
}

func ExampleAccumulator() {
	var result deepseek.Accumulator
	for _, text := range []string{"hello", " world"} {
		_ = result.Add(deepseek.ChatCompletionChunk{ID: "example", Choices: []deepseek.ChunkChoice{{Index: 0, Delta: deepseek.Delta{Content: deepseek.Ptr(text)}}}})
	}

	fmt.Println(result.Completion().Choices[0].Message.Content.Text())
	// Output: hello world
}
