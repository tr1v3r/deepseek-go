package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	deepseek "github.com/tr1v3r/deepseek-go"
)

func main() {
	client, err := deepseek.NewClient(deepseek.Config{
		APIKey: os.Getenv("DEEPSEEK_API_KEY"),
	})
	if err != nil {
		log.Fatal(err)
	}

	model := os.Getenv("DEEPSEEK_MODEL")
	if model == "" {
		log.Fatal("set DEEPSEEK_MODEL to a model returned by ListModels")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	result, err := client.CreateChatCompletion(ctx, deepseek.ChatCompletionRequest{
		Model:    model,
		Messages: []deepseek.Message{deepseek.UserMessage("你好")},
	})
	if err != nil {
		log.Fatal(err)
	}

	for _, choice := range result.Choices {
		fmt.Println(choice.Message.Content.Text())
	}
}
