# symposium

A tiny CLI that runs an open-ended conversation between two LLMs.

Each participant is an OpenAI-compatible chat endpoint with its own model, system prompt, and temperature. History is persisted in SQLite, with per-session compaction and handoff so long conversations don't blow past the context window.

## Install

```sh
go install github.com/jefflinse/symposium@latest
```

Or clone and build:

```sh
go build -o symposium .
```

Data lives in `~/.symposium/symposium.db` by default. Override with `--db <path>` or `SYMPOSIUM_DB`.

## Getting started

### 1. Create two participants

Each participant points at an OpenAI-compatible `/chat/completions` endpoint. `--api-url` is the base URL (the `/chat/completions` suffix is appended automatically).

```sh
symposium participant add socrates \
  --name "Socrates" \
  --api-url https://api.openai.com/v1 \
  --api-key "$OPENAI_API_KEY" \
  --model gpt-4o-mini \
  --system "You are Socrates. Question assumptions relentlessly. Keep replies short."

symposium participant add nietzsche \
  --name "Nietzsche" \
  --api-url https://api.openai.com/v1 \
  --api-key "$OPENAI_API_KEY" \
  --model gpt-4o-mini \
  --system "You are Nietzsche. Argue boldly, reject conventional morality. Keep replies short."
```

You can also load the system prompt from a file with `--system-file <path>`.

### 2. Create a conversation

```sh
symposium conversation new meaning \
  --name "On the meaning of life" \
  --a socrates \
  --b nietzsche \
  --topic "What, if anything, gives a human life meaning?"
```

### 3. Run it

```sh
symposium run meaning --pause 1s
```

Ctrl+C to stop. The conversation resumes from where it left off on the next `run`.

Useful flags:

- `--max-turns <n>` stop after N turns (0 = unlimited)
- `--pause <duration>` wait between turns (e.g. `2s`, `500ms`)
- `--context-limit <tokens>` override the conversation's context budget

## Other commands

```sh
symposium participant list
symposium participant show <id>
symposium participant update <id> [flags]
symposium participant remove <id>

symposium conversation list
symposium conversation show <id>
symposium conversation remove <id>

symposium history <conversation-id> [--session <n>] [--last <n>]
symposium handoff <conversation-id>   # force a session handoff manually
```

## How context management works

- Each turn, the full non-compacted history plus any compaction summaries is sent to the current speaker.
- When the history exceeds ~75% of the context limit, the older half is summarized by an LLM call and replaced with that summary (compaction).
- If the history still exceeds the budget after compaction, the session is closed and a new one is opened with a summary of the prior session (handoff). Session boundaries are preserved in the DB so `history` can walk any session.
