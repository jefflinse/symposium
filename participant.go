package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Participant represents an LLM endpoint in a conversation.
type Participant struct {
	ID          string
	Name        string
	APIURL      string
	APIKey      string
	Model       string
	System      string
	Temperature *float64
}

// LLMClient calls OpenAI-compatible chat completion endpoints.
type LLMClient struct {
	HTTP *http.Client
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature *float64      `json:"temperature,omitempty"`
	Stream      bool          `json:"stream"`
}

type chatResponseChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

// Complete sends a chat completion request and streams the response.
// Tokens are written to w as they arrive. The full response content is returned.
func (c *LLMClient) Complete(ctx context.Context, p Participant, messages []ChatMessage, w io.Writer) (string, error) {
	reqBody := chatRequest{
		Model:       p.Model,
		Messages:    messages,
		Temperature: p.Temperature,
		Stream:      true,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	url := strings.TrimRight(p.APIURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	// Bump from the default 64KB cap — a single SSE data line can carry a
	// large content delta or an error payload and would otherwise error with
	// bufio.ErrTooLong.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk chatResponseChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				result.WriteString(choice.Delta.Content)
				fmt.Fprint(w, choice.Delta.Content)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return result.String(), fmt.Errorf("reading stream: %w", err)
	}

	return result.String(), nil
}
