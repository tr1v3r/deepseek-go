package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	deepseek "github.com/tr1v3r/deepseek-go"
)

func run() error {
	client, err := deepseek.NewClient(deepseek.Config{
		APIKey: os.Getenv("DEEPSEEK_API_KEY"),
	})
	if err != nil {
		return err
	}

	model := os.Getenv("DEEPSEEK_MODEL")
	if model == "" {
		return errors.New("set DEEPSEEK_MODEL to a model returned by ListModels")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	stream, err := client.CreateChatCompletionStream(ctx, deepseek.ChatCompletionRequest{
		Model:         model,
		Messages:      []deepseek.Message{deepseek.UserMessage("你好")},
		StreamOptions: &deepseek.StreamOptions{IncludeUsage: true},
	})
	if err != nil {
		return err
	}

	defer stream.Close()

	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			fmt.Println()
			return nil
		}

		if err != nil {
			return err
		}

		for _, choice := range chunk.Choices {
			if choice.Delta.Content != nil {
				fmt.Print(*choice.Delta.Content)
			}
		}
	}
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
