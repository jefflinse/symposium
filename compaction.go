package main

import (
	"context"
	"fmt"
	"io"
	"strings"
)

func compact(ctx context.Context, store Store, client *LLMClient, session *Session, participant Participant, display *Display) error {
	messages, err := store.GetNonCompactedMessages(session.ID)
	if err != nil {
		return err
	}
	if len(messages) < 4 {
		return nil
	}

	n := len(messages) / 2
	toCompact := messages[:n]

	display.PrintStatus(fmt.Sprintf("compacting %d messages...", n))

	var sb strings.Builder
	for _, m := range toCompact {
		fmt.Fprintf(&sb, "%s: %s\n\n", m.AuthorID, m.Content)
	}

	prompt := []ChatMessage{
		{Role: "system", Content: "Summarize the following conversation excerpt. Preserve key arguments, conclusions, agreements, disagreements, and important details. Be concise but thorough."},
		{Role: "user", Content: sb.String()},
	}

	summary, err := client.Complete(ctx, participant, prompt, io.Discard)
	if err != nil {
		return fmt.Errorf("compaction LLM call failed: %w", err)
	}

	fromSeq := toCompact[0].Seq
	toSeq := toCompact[len(toCompact)-1].Seq

	if err := store.SaveCompactionSummary(session.ID, fromSeq, toSeq, summary); err != nil {
		return err
	}
	return store.MarkMessagesCompacted(session.ID, fromSeq, toSeq)
}

func performHandoff(ctx context.Context, store Store, client *LLMClient, conv Conversation, session *Session, participant Participant, display *Display) (*Session, error) {
	display.PrintStatus("performing session handoff...")

	summaries, err := store.GetCompactionSummaries(session.ID)
	if err != nil {
		return nil, err
	}
	messages, err := store.GetNonCompactedMessages(session.ID)
	if err != nil {
		return nil, err
	}

	var sb strings.Builder
	if session.Summary != nil {
		fmt.Fprintf(&sb, "Previous context:\n%s\n\n", *session.Summary)
	}
	for _, s := range summaries {
		fmt.Fprintf(&sb, "Earlier discussion summary:\n%s\n\n", s.Summary)
	}
	for _, m := range messages {
		fmt.Fprintf(&sb, "%s: %s\n\n", m.AuthorID, m.Content)
	}

	prompt := []ChatMessage{
		{Role: "system", Content: "Create a comprehensive summary of the following conversation. This summary will be used to continue the conversation in a new session. Preserve all key arguments, conclusions, agreements, disagreements, open questions, and the current direction of discussion. Be thorough."},
		{Role: "user", Content: sb.String()},
	}

	summaryText, err := client.Complete(ctx, participant, prompt, io.Discard)
	if err != nil {
		return nil, fmt.Errorf("handoff summarization failed: %w", err)
	}

	newSession, err := store.CreateSession(conv.ID, session.Seq+1, &summaryText)
	if err != nil {
		return nil, err
	}

	display.PrintStatus(fmt.Sprintf("started session %d", newSession.Seq))
	return &newSession, nil
}
