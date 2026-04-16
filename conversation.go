package main

import (
	"context"
	"fmt"
	"time"
)

// Conversation is a long-running dialog between two participants.
type Conversation struct {
	ID           string
	Name         string
	PartAID      string
	PartBID      string
	Topic        string
	ContextLimit int
}

// Session represents a continuity boundary within a conversation.
// A new session is created on handoff, carrying a summary of the prior session.
type Session struct {
	ID             int64
	ConversationID string
	Seq            int
	Summary        *string
}

func runConversation(ctx context.Context, store Store, conv Conversation, partA, partB Participant, client *LLMClient, display *Display, pause time.Duration, maxTurns int) error {
	session, err := store.GetLatestSession(conv.ID)
	if err != nil {
		return fmt.Errorf("getting session: %w", err)
	}
	if session == nil {
		s, err := store.CreateSession(conv.ID, 1, nil)
		if err != nil {
			return fmt.Errorf("creating session: %w", err)
		}
		session = &s
	}

	// Determine whose turn it is
	speaker := partA
	lastMsg, err := store.GetLastMessage(session.ID)
	if err != nil {
		return fmt.Errorf("getting last message: %w", err)
	}
	if lastMsg != nil && lastMsg.AuthorID == partA.ID {
		speaker = partB
	}

	turns := 0
	consecutiveErrors := 0
	retryDelay := 2 * time.Second

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		if maxTurns > 0 && turns >= maxTurns {
			return nil
		}

		// Build history for current speaker
		summaries, err := store.GetCompactionSummaries(session.ID)
		if err != nil {
			return fmt.Errorf("getting summaries: %w", err)
		}
		messages, err := store.GetNonCompactedMessages(session.ID)
		if err != nil {
			return fmt.Errorf("getting messages: %w", err)
		}

		history := buildHistory(speaker, *session, summaries, messages, conv.Topic)
		totalTokens := estimateHistoryTokens(history)

		// Compact if over 75% of context limit
		if totalTokens > conv.ContextLimit*3/4 {
			if err := compact(ctx, store, client, session, partA, display); err != nil {
				return fmt.Errorf("compaction failed: %w", err)
			}

			// Rebuild after compaction
			summaries, err = store.GetCompactionSummaries(session.ID)
			if err != nil {
				return err
			}
			messages, err = store.GetNonCompactedMessages(session.ID)
			if err != nil {
				return err
			}
			history = buildHistory(speaker, *session, summaries, messages, conv.Topic)
			totalTokens = estimateHistoryTokens(history)

			// If still over budget, handoff to new session — but only if
			// there's something to consolidate. Otherwise we'd just be
			// summarizing the carried session summary into a smaller (or
			// larger!) summary on every turn, ping-ponging forever.
			if totalTokens > conv.ContextLimit*3/4 && (len(messages) > 0 || len(summaries) > 0) {
				newSession, err := performHandoff(ctx, store, client, conv, session, partA, display)
				if err != nil {
					return fmt.Errorf("handoff failed: %w", err)
				}
				session = newSession

				summaries, err = store.GetCompactionSummaries(session.ID)
				if err != nil {
					return err
				}
				messages, err = store.GetNonCompactedMessages(session.ID)
				if err != nil {
					return err
				}
				history = buildHistory(speaker, *session, summaries, messages, conv.Topic)
			}
		}

		// Display and call LLM
		display.PrintHeader(speaker.Name)

		response, err := client.Complete(ctx, speaker, history, display)
		if err != nil {
			if ctx.Err() != nil {
				return nil // graceful shutdown
			}
			consecutiveErrors++
			if consecutiveErrors >= 5 {
				return fmt.Errorf("too many consecutive errors: %w", err)
			}
			display.PrintError(fmt.Sprintf("%v, retrying in %v...", err, retryDelay))
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(retryDelay):
			}
			retryDelay *= 2
			if retryDelay > 60*time.Second {
				retryDelay = 60 * time.Second
			}
			continue
		}

		consecutiveErrors = 0
		retryDelay = 2 * time.Second
		display.PrintNewline()

		// Persist the message
		tokenEst := estimateTokens(response)
		if _, err := store.AppendMessage(session.ID, speaker.ID, response, tokenEst); err != nil {
			return fmt.Errorf("saving message: %w", err)
		}

		// Swap speakers
		if speaker.ID == partA.ID {
			speaker = partB
		} else {
			speaker = partA
		}
		turns++

		// Pause between turns
		if pause > 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(pause):
			}
		}
	}
}
