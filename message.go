package main

import "fmt"

// ChatMessage is the wire format for OpenAI-compatible chat completions.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Message is a persisted conversation message.
type Message struct {
	ID        int64
	SessionID int64
	Seq       int
	AuthorID  string
	Content   string
	TokenEst  int
	Compacted bool
}

// CompactionSummary replaces a range of compacted messages.
type CompactionSummary struct {
	ID         int64
	SessionID  int64
	FromMsgSeq int
	ToMsgSeq   int
	Summary    string
}

func estimateTokens(s string) int {
	n := len(s) / 4
	if n == 0 && len(s) > 0 {
		n = 1
	}
	return n
}

func estimateHistoryTokens(history []ChatMessage) int {
	total := 0
	for _, m := range history {
		total += estimateTokens(m.Content)
	}
	return total
}

// buildHistory constructs the chat message history for a given speaker.
// Messages authored by the speaker get role "assistant"; the other participant's
// messages get role "user". Compaction summaries replace compacted message ranges.
func buildHistory(speaker Participant, session Session, summaries []CompactionSummary, messages []Message, topic string) []ChatMessage {
	var history []ChatMessage

	// System prompt
	history = append(history, ChatMessage{Role: "system", Content: speaker.System})

	// Session summary carried forward from a prior session (handoff)
	if session.Summary != nil {
		history = append(history, ChatMessage{
			Role:    "user",
			Content: fmt.Sprintf("Summary of previous conversation:\n\n%s", *session.Summary),
		})
	}

	// Compaction summaries replace compacted message ranges
	for _, s := range summaries {
		history = append(history, ChatMessage{
			Role:    "user",
			Content: fmt.Sprintf("Summary of earlier discussion:\n\n%s", s.Summary),
		})
	}

	// Non-compacted messages with role swapping
	for _, m := range messages {
		role := "user"
		if m.AuthorID == speaker.ID {
			role = "assistant"
		}
		history = append(history, ChatMessage{Role: role, Content: m.Content})
	}

	// If this is the very first turn (no messages, no summary, no compaction),
	// inject the topic or a default kickoff message.
	if len(messages) == 0 && session.Summary == nil && len(summaries) == 0 {
		content := topic
		if content == "" {
			content = "Begin."
		}
		history = append(history, ChatMessage{Role: "user", Content: content})
	}

	return history
}
