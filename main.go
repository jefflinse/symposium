package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	dbPath := defaultDBPath()
	if env := os.Getenv("SYMPOSIUM_DB"); env != "" {
		dbPath = env
	}

	args := os.Args[1:]

	// Parse global --db flag
	for i := 0; i < len(args); i++ {
		if args[i] == "--db" && i+1 < len(args) {
			dbPath = args[i+1]
			args = append(args[:i], args[i+2:]...)
			break
		}
	}

	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	switch args[0] {
	case "participant":
		cmdParticipant(dbPath, args[1:])
	case "conversation":
		cmdConversation(dbPath, args[1:])
	case "run":
		cmdRun(dbPath, args[1:])
	case "history":
		cmdHistory(dbPath, args[1:])
	case "handoff":
		cmdHandoff(dbPath, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", args[0])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `usage: symposium <command> [arguments]

commands:
  participant  manage participants
  conversation manage conversations
  run          run a conversation
  history      show conversation history
  handoff      manually trigger session handoff

global flags:
  --db <path>  database path (default: ~/.symposium/symposium.db)
               also settable via SYMPOSIUM_DB env var`)
}

// --- Participant commands ---

func cmdParticipant(dbPath string, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, `usage: symposium participant <subcommand>

subcommands:
  add     add a participant
  list    list participants
  show    show participant details
  update  update a participant
  remove  remove a participant`)
		os.Exit(1)
	}

	switch args[0] {
	case "add":
		cmdParticipantAdd(dbPath, args[1:])
	case "list":
		cmdParticipantList(dbPath)
	case "show":
		cmdParticipantShow(dbPath, args[1:])
	case "update":
		cmdParticipantUpdate(dbPath, args[1:])
	case "remove":
		cmdParticipantRemove(dbPath, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown participant command: %s\n", args[0])
		os.Exit(1)
	}
}

func cmdParticipantAdd(dbPath string, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: symposium participant add <id> --name <name> --api-url <url> --model <model> --system <prompt> [--api-key <key>] [--temperature <float>] [--system-file <path>]")
		os.Exit(1)
	}

	id := args[0]
	fs := flag.NewFlagSet("participant add", flag.ExitOnError)
	name := fs.String("name", "", "display name (required)")
	apiURL := fs.String("api-url", "", "API base URL (required)")
	model := fs.String("model", "", "model identifier (required)")
	system := fs.String("system", "", "system prompt")
	systemFile := fs.String("system-file", "", "read system prompt from file")
	apiKey := fs.String("api-key", "", "API key")
	temperature := fs.Float64("temperature", -1, "temperature (-1 for server default)")
	fs.Parse(args[1:])

	if *name == "" || *apiURL == "" || *model == "" {
		fmt.Fprintln(os.Stderr, "error: --name, --api-url, and --model are required")
		os.Exit(1)
	}

	sysPrompt := *system
	if *systemFile != "" {
		data, err := os.ReadFile(*systemFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading system file: %v\n", err)
			os.Exit(1)
		}
		sysPrompt = string(data)
	}
	if sysPrompt == "" {
		fmt.Fprintln(os.Stderr, "error: --system or --system-file is required")
		os.Exit(1)
	}

	var temp *float64
	if *temperature >= 0 {
		temp = temperature
	}

	store := mustOpenStore(dbPath)
	defer store.Close()

	p := Participant{
		ID:          id,
		Name:        *name,
		APIURL:      *apiURL,
		APIKey:      *apiKey,
		Model:       *model,
		System:      sysPrompt,
		Temperature: temp,
	}

	if err := store.CreateParticipant(p); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("participant %q created\n", id)
}

func cmdParticipantList(dbPath string) {
	store := mustOpenStore(dbPath)
	defer store.Close()

	participants, err := store.ListParticipants()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if len(participants) == 0 {
		fmt.Println("no participants")
		return
	}
	for _, p := range participants {
		fmt.Printf("  %-20s %-20s %s (%s)\n", p.ID, p.Name, p.Model, p.APIURL)
	}
}

func cmdParticipantShow(dbPath string, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: symposium participant show <id>")
		os.Exit(1)
	}

	store := mustOpenStore(dbPath)
	defer store.Close()

	p, err := store.GetParticipant(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("ID:          %s\n", p.ID)
	fmt.Printf("Name:        %s\n", p.Name)
	fmt.Printf("API URL:     %s\n", p.APIURL)
	fmt.Printf("Model:       %s\n", p.Model)
	if p.Temperature != nil {
		fmt.Printf("Temperature: %.2f\n", *p.Temperature)
	}
	fmt.Printf("System:      %s\n", p.System)
}

func cmdParticipantUpdate(dbPath string, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: symposium participant update <id> [--name <name>] [--api-url <url>] [--model <model>] [--system <prompt>] [--system-file <path>] [--api-key <key>] [--temperature <float>]")
		os.Exit(1)
	}

	id := args[0]
	fs := flag.NewFlagSet("participant update", flag.ExitOnError)
	name := fs.String("name", "", "display name")
	apiURL := fs.String("api-url", "", "API base URL")
	model := fs.String("model", "", "model identifier")
	system := fs.String("system", "", "system prompt")
	systemFile := fs.String("system-file", "", "read system prompt from file")
	apiKey := fs.String("api-key", "", "API key")
	temperature := fs.Float64("temperature", -1, "temperature (-1 for server default)")
	fs.Parse(args[1:])

	store := mustOpenStore(dbPath)
	defer store.Close()

	p, err := store.GetParticipant(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if *name != "" {
		p.Name = *name
	}
	if *apiURL != "" {
		p.APIURL = *apiURL
	}
	if *model != "" {
		p.Model = *model
	}
	if *system != "" {
		p.System = *system
	}
	if *systemFile != "" {
		data, err := os.ReadFile(*systemFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading system file: %v\n", err)
			os.Exit(1)
		}
		p.System = string(data)
	}
	if *apiKey != "" {
		p.APIKey = *apiKey
	}
	if *temperature >= 0 {
		p.Temperature = temperature
	}

	if err := store.UpdateParticipant(p); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("participant %q updated\n", id)
}

func cmdParticipantRemove(dbPath string, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: symposium participant remove <id>")
		os.Exit(1)
	}

	store := mustOpenStore(dbPath)
	defer store.Close()

	if err := store.DeleteParticipant(args[0]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("participant %q removed\n", args[0])
}

// --- Conversation commands ---

func cmdConversation(dbPath string, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, `usage: symposium conversation <subcommand>

subcommands:
  new     create a conversation
  list    list conversations
  show    show conversation details
  remove  remove a conversation`)
		os.Exit(1)
	}

	switch args[0] {
	case "new":
		cmdConversationNew(dbPath, args[1:])
	case "list":
		cmdConversationList(dbPath)
	case "show":
		cmdConversationShow(dbPath, args[1:])
	case "remove":
		cmdConversationRemove(dbPath, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown conversation command: %s\n", args[0])
		os.Exit(1)
	}
}

func cmdConversationNew(dbPath string, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: symposium conversation new <id> --name <name> --a <participant> --b <participant> [--topic <topic>] [--topic-file <path>] [--context-limit <tokens>]")
		os.Exit(1)
	}

	id := args[0]
	fs := flag.NewFlagSet("conversation new", flag.ExitOnError)
	name := fs.String("name", "", "display name (required)")
	partA := fs.String("a", "", "participant A ID (required)")
	partB := fs.String("b", "", "participant B ID (required)")
	topic := fs.String("topic", "", "conversation topic / opening prompt")
	topicFile := fs.String("topic-file", "", "read topic from file")
	contextLimit := fs.Int("context-limit", 8192, "context window token limit")
	fs.Parse(args[1:])

	if *name == "" || *partA == "" || *partB == "" {
		fmt.Fprintln(os.Stderr, "error: --name, --a, and --b are required")
		os.Exit(1)
	}

	topicText := *topic
	if *topicFile != "" {
		data, err := os.ReadFile(*topicFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading topic file: %v\n", err)
			os.Exit(1)
		}
		topicText = string(data)
	}

	store := mustOpenStore(dbPath)
	defer store.Close()

	if _, err := store.GetParticipant(*partA); err != nil {
		fmt.Fprintf(os.Stderr, "error: participant %q not found\n", *partA)
		os.Exit(1)
	}
	if _, err := store.GetParticipant(*partB); err != nil {
		fmt.Fprintf(os.Stderr, "error: participant %q not found\n", *partB)
		os.Exit(1)
	}

	c := Conversation{
		ID:           id,
		Name:         *name,
		PartAID:      *partA,
		PartBID:      *partB,
		Topic:        topicText,
		ContextLimit: *contextLimit,
	}

	if err := store.CreateConversation(c); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("conversation %q created\n", id)
}

func cmdConversationList(dbPath string) {
	store := mustOpenStore(dbPath)
	defer store.Close()

	convos, err := store.ListConversations()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if len(convos) == 0 {
		fmt.Println("no conversations")
		return
	}
	for _, c := range convos {
		fmt.Printf("  %-20s %-20s %s vs %s\n", c.ID, c.Name, c.PartAID, c.PartBID)
	}
}

func cmdConversationShow(dbPath string, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: symposium conversation show <id>")
		os.Exit(1)
	}

	store := mustOpenStore(dbPath)
	defer store.Close()

	c, err := store.GetConversation(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	sessions, err := store.GetSessions(c.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	totalMessages, err := store.CountConversationMessages(c.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("ID:            %s\n", c.ID)
	fmt.Printf("Name:          %s\n", c.Name)
	fmt.Printf("Participant A: %s\n", c.PartAID)
	fmt.Printf("Participant B: %s\n", c.PartBID)
	fmt.Printf("Context Limit: %d tokens\n", c.ContextLimit)
	fmt.Printf("Sessions:      %d\n", len(sessions))
	fmt.Printf("Messages:      %d\n", totalMessages)
	if c.Topic != "" {
		fmt.Printf("Topic:         %s\n", c.Topic)
	}
}

func cmdConversationRemove(dbPath string, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: symposium conversation remove <id>")
		os.Exit(1)
	}

	store := mustOpenStore(dbPath)
	defer store.Close()

	if err := store.DeleteConversation(args[0]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("conversation %q removed\n", args[0])
}

// --- Run command ---

func cmdRun(dbPath string, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: symposium run <conversation-id> [--pause <duration>] [--max-turns <n>] [--context-limit <tokens>]")
		os.Exit(1)
	}

	convID := args[0]
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	pauseStr := fs.String("pause", "0s", "pause between turns (e.g. 2s, 500ms)")
	maxTurns := fs.Int("max-turns", 0, "max turns (0 = unlimited)")
	contextLimit := fs.Int("context-limit", 0, "override context limit")
	fs.Parse(args[1:])

	pause, err := time.ParseDuration(*pauseStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid pause duration: %v\n", err)
		os.Exit(1)
	}

	store := mustOpenStore(dbPath)
	defer store.Close()

	conv, err := store.GetConversation(convID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if *contextLimit > 0 {
		conv.ContextLimit = *contextLimit
	}

	partA, err := store.GetParticipant(conv.PartAID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading participant %q: %v\n", conv.PartAID, err)
		os.Exit(1)
	}
	partB, err := store.GetParticipant(conv.PartBID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading participant %q: %v\n", conv.PartBID, err)
		os.Exit(1)
	}

	client := &LLMClient{HTTP: &http.Client{}}
	display := &Display{Out: os.Stdout}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	fmt.Printf("Starting conversation %q: %s vs %s\n", conv.Name, partA.Name, partB.Name)
	if conv.Topic != "" {
		fmt.Printf("Topic: %s\n", conv.Topic)
	}
	fmt.Println("Press Ctrl+C to stop.")

	if err := runConversation(ctx, store, conv, partA, partB, client, display, pause, *maxTurns); err != nil {
		fmt.Fprintf(os.Stderr, "\nerror: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\nConversation stopped.")
}

// --- History command ---

func cmdHistory(dbPath string, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: symposium history <conversation-id> [--session <n>] [--last <n>]")
		os.Exit(1)
	}

	convID := args[0]
	fs := flag.NewFlagSet("history", flag.ExitOnError)
	sessionNum := fs.Int("session", 0, "session number (0 = latest)")
	last := fs.Int("last", 0, "show last N messages (0 = all)")
	fs.Parse(args[1:])

	store := mustOpenStore(dbPath)
	defer store.Close()

	conv, err := store.GetConversation(convID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	partA, err := store.GetParticipant(conv.PartAID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading participant %q: %v\n", conv.PartAID, err)
		os.Exit(1)
	}
	partB, err := store.GetParticipant(conv.PartBID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading participant %q: %v\n", conv.PartBID, err)
		os.Exit(1)
	}
	nameMap := map[string]string{
		partA.ID: partA.Name,
		partB.ID: partB.Name,
	}

	var session *Session
	if *sessionNum > 0 {
		sessions, err := store.GetSessions(conv.ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		for i := range sessions {
			if sessions[i].Seq == *sessionNum {
				session = &sessions[i]
				break
			}
		}
		if session == nil {
			fmt.Fprintf(os.Stderr, "error: session %d not found\n", *sessionNum)
			os.Exit(1)
		}
	} else {
		session, err = store.GetLatestSession(conv.ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if session == nil {
			fmt.Println("no messages")
			return
		}
	}

	messages, err := store.GetSessionMessages(session.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if *last > 0 && len(messages) > *last {
		messages = messages[len(messages)-*last:]
	}

	display := &Display{Out: os.Stdout}
	fmt.Printf("Session %d (%d messages)\n", session.Seq, len(messages))
	if session.Summary != nil {
		fmt.Printf("\n[Session summary: %s...]\n", truncate(*session.Summary, 200))
	}

	for _, m := range messages {
		name := nameMap[m.AuthorID]
		if name == "" {
			name = m.AuthorID
		}
		display.PrintHeader(name)
		fmt.Println(m.Content)
	}
}

// --- Handoff command ---

func cmdHandoff(dbPath string, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: symposium handoff <conversation-id>")
		os.Exit(1)
	}

	store := mustOpenStore(dbPath)
	defer store.Close()

	conv, err := store.GetConversation(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	session, err := store.GetLatestSession(conv.ID)
	if err != nil || session == nil {
		fmt.Fprintln(os.Stderr, "error: no active session to hand off")
		os.Exit(1)
	}

	partA, err := store.GetParticipant(conv.PartAID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	client := &LLMClient{HTTP: &http.Client{}}
	display := &Display{Out: os.Stdout}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	newSession, err := performHandoff(ctx, store, client, conv, session, partA, display)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Handed off to session %d\n", newSession.Seq)
}

// --- Helpers ---

func mustOpenStore(dbPath string) *SQLiteStore {
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	return store
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen])
}
