// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/shivangtanwar/kbark/internal/ai"
)

var (
	aiTestProvider string
	aiTestModel    string
)

// aiTestCmd is a developer-only smoke test of the streaming pipeline. It
// stays hidden from the top-level help; everyday users never need it.
// Used to validate M4 acceptance ("same prompt produces a streamed answer
// through each provider") without spinning up the full TUI.
var aiTestCmd = &cobra.Command{
	Use:    "ai-test [prompt]",
	Short:  "(internal) Smoke-test an AI provider end-to-end",
	Hidden: true,
	RunE:   runAITest,
}

func init() {
	aiTestCmd.Flags().StringVar(&aiTestProvider, "provider", "anthropic",
		"Provider name (anthropic|openai|ollama)")
	aiTestCmd.Flags().StringVar(&aiTestModel, "model", "claude-sonnet-4-6",
		"Model identifier for the chosen provider")
	rootCmd.AddCommand(aiTestCmd)
}

func runAITest(_ *cobra.Command, args []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	prompt := strings.TrimSpace(strings.Join(args, " "))
	if prompt == "" {
		prompt = "Say hello in exactly three words."
	}

	provider, err := ai.New(aiTestProvider)
	if err != nil {
		return err
	}

	fmt.Printf("provider: %s\nmodel:    %s\nprompt:   %s\n\n", provider.Name(), aiTestModel, prompt)

	start := time.Now()
	events, err := provider.Stream(ctx, ai.Request{
		Model:     aiTestModel,
		System:    "Respond concisely.",
		Messages:  []ai.Message{{Role: ai.RoleUser, Content: prompt}},
		MaxTokens: 256,
	})
	if err != nil {
		return fmt.Errorf("stream: %w", err)
	}

	var firstToken time.Duration
	for ev := range events {
		switch e := ev.(type) {
		case ai.TextDeltaEvent:
			if firstToken == 0 {
				firstToken = time.Since(start)
			}
			fmt.Print(e.Delta)
		case ai.ToolCallEvent:
			fmt.Printf("\n[tool call: %s args=%s]\n", e.Name, e.Arguments)
		case ai.DoneEvent:
			total := time.Since(start)
			fmt.Printf("\n\n[done: stop=%q first-token=%s total=%s]\n",
				e.StopReason, firstToken.Round(time.Millisecond), total.Round(time.Millisecond))
		case ai.ErrorEvent:
			fmt.Println()
			return e.Err
		}
	}
	return nil
}
