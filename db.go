package main

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const schemaV1 = `
CREATE TABLE IF NOT EXISTS participants (
	id          TEXT PRIMARY KEY,
	name        TEXT NOT NULL,
	api_url     TEXT NOT NULL,
	api_key     TEXT NOT NULL DEFAULT '',
	model       TEXT NOT NULL,
	system      TEXT NOT NULL,
	temperature REAL,
	created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS conversations (
	id            TEXT PRIMARY KEY,
	name          TEXT NOT NULL,
	part_a_id     TEXT NOT NULL REFERENCES participants(id),
	part_b_id     TEXT NOT NULL REFERENCES participants(id),
	topic         TEXT NOT NULL DEFAULT '',
	context_limit INTEGER NOT NULL DEFAULT 8192,
	created_at    TEXT NOT NULL DEFAULT (datetime('now')),
	updated_at    TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS sessions (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
	seq             INTEGER NOT NULL,
	summary         TEXT,
	started_at      TEXT NOT NULL DEFAULT (datetime('now')),
	UNIQUE(conversation_id, seq)
);

CREATE TABLE IF NOT EXISTS messages (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	seq        INTEGER NOT NULL,
	author_id  TEXT NOT NULL,
	content    TEXT NOT NULL,
	token_est  INTEGER NOT NULL DEFAULT 0,
	compacted  INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL DEFAULT (datetime('now')),
	UNIQUE(session_id, seq)
);

CREATE TABLE IF NOT EXISTS compaction_summaries (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id   INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	from_msg_seq INTEGER NOT NULL,
	to_msg_seq   INTEGER NOT NULL,
	summary      TEXT NOT NULL,
	created_at   TEXT NOT NULL DEFAULT (datetime('now'))
);`

// Store defines the persistence interface.
type Store interface {
	CreateParticipant(p Participant) error
	GetParticipant(id string) (Participant, error)
	ListParticipants() ([]Participant, error)
	UpdateParticipant(p Participant) error
	DeleteParticipant(id string) error

	CreateConversation(c Conversation) error
	GetConversation(id string) (Conversation, error)
	ListConversations() ([]Conversation, error)
	DeleteConversation(id string) error

	GetLatestSession(conversationID string) (*Session, error)
	CreateSession(conversationID string, seq int, summary *string) (Session, error)
	GetSessions(conversationID string) ([]Session, error)
	CountConversationMessages(conversationID string) (int, error)

	AppendMessage(sessionID int64, authorID string, content string, tokenEst int) (Message, error)
	GetNonCompactedMessages(sessionID int64) ([]Message, error)
	GetSessionMessages(sessionID int64) ([]Message, error)
	GetLastMessage(sessionID int64) (*Message, error)
	MarkMessagesCompacted(sessionID int64, fromSeq int, toSeq int) error

	SaveCompactionSummary(sessionID int64, fromSeq int, toSeq int, summary string) error
	GetCompactionSummaries(sessionID int64) ([]CompactionSummary, error)

	Close() error
}

func defaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "symposium.db"
	}
	return filepath.Join(home, ".symposium", "symposium.db")
}

// SQLiteStore implements Store using SQLite.
type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating db directory: %w", err)
	}

	// Apply pragmas via DSN so every pooled connection gets them.
	// database/sql may open multiple connections; a PRAGMA Exec'd after Open
	// would only land on whichever connection happened to serve it.
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	dsn := dbPath + "?" + q.Encode()

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	s := &SQLiteStore{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}
	return s, nil
}

func (s *SQLiteStore) migrate() error {
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version < 1 {
		if _, err := s.db.Exec(schemaV1); err != nil {
			return fmt.Errorf("applying schema v1: %w", err)
		}
		if _, err := s.db.Exec("PRAGMA user_version = 1"); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// --- Participants ---

func (s *SQLiteStore) CreateParticipant(p Participant) error {
	_, err := s.db.Exec(
		`INSERT INTO participants (id, name, api_url, api_key, model, system, temperature) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.APIURL, p.APIKey, p.Model, p.System, p.Temperature,
	)
	return err
}

func (s *SQLiteStore) GetParticipant(id string) (Participant, error) {
	var p Participant
	var temp sql.NullFloat64
	err := s.db.QueryRow(
		`SELECT id, name, api_url, api_key, model, system, temperature FROM participants WHERE id = ?`, id,
	).Scan(&p.ID, &p.Name, &p.APIURL, &p.APIKey, &p.Model, &p.System, &temp)
	if err != nil {
		return p, err
	}
	if temp.Valid {
		p.Temperature = &temp.Float64
	}
	return p, nil
}

func (s *SQLiteStore) ListParticipants() ([]Participant, error) {
	rows, err := s.db.Query(
		`SELECT id, name, api_url, api_key, model, system, temperature FROM participants ORDER BY id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var participants []Participant
	for rows.Next() {
		var p Participant
		var temp sql.NullFloat64
		if err := rows.Scan(&p.ID, &p.Name, &p.APIURL, &p.APIKey, &p.Model, &p.System, &temp); err != nil {
			return nil, err
		}
		if temp.Valid {
			p.Temperature = &temp.Float64
		}
		participants = append(participants, p)
	}
	return participants, rows.Err()
}

func (s *SQLiteStore) UpdateParticipant(p Participant) error {
	res, err := s.db.Exec(
		`UPDATE participants SET name=?, api_url=?, api_key=?, model=?, system=?, temperature=? WHERE id=?`,
		p.Name, p.APIURL, p.APIKey, p.Model, p.System, p.Temperature, p.ID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("participant %q not found", p.ID)
	}
	return nil
}

func (s *SQLiteStore) DeleteParticipant(id string) error {
	res, err := s.db.Exec(`DELETE FROM participants WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("participant %q not found", id)
	}
	return nil
}

// --- Conversations ---

func (s *SQLiteStore) CreateConversation(c Conversation) error {
	_, err := s.db.Exec(
		`INSERT INTO conversations (id, name, part_a_id, part_b_id, topic, context_limit) VALUES (?, ?, ?, ?, ?, ?)`,
		c.ID, c.Name, c.PartAID, c.PartBID, c.Topic, c.ContextLimit,
	)
	return err
}

func (s *SQLiteStore) GetConversation(id string) (Conversation, error) {
	var c Conversation
	err := s.db.QueryRow(
		`SELECT id, name, part_a_id, part_b_id, topic, context_limit FROM conversations WHERE id = ?`, id,
	).Scan(&c.ID, &c.Name, &c.PartAID, &c.PartBID, &c.Topic, &c.ContextLimit)
	return c, err
}

func (s *SQLiteStore) ListConversations() ([]Conversation, error) {
	rows, err := s.db.Query(
		`SELECT id, name, part_a_id, part_b_id, topic, context_limit FROM conversations ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var convos []Conversation
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.Name, &c.PartAID, &c.PartBID, &c.Topic, &c.ContextLimit); err != nil {
			return nil, err
		}
		convos = append(convos, c)
	}
	return convos, rows.Err()
}

func (s *SQLiteStore) DeleteConversation(id string) error {
	res, err := s.db.Exec(`DELETE FROM conversations WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("conversation %q not found", id)
	}
	return nil
}

// --- Sessions ---

func (s *SQLiteStore) GetLatestSession(conversationID string) (*Session, error) {
	var sess Session
	var summary sql.NullString
	err := s.db.QueryRow(
		`SELECT id, conversation_id, seq, summary FROM sessions WHERE conversation_id = ? ORDER BY seq DESC LIMIT 1`,
		conversationID,
	).Scan(&sess.ID, &sess.ConversationID, &sess.Seq, &summary)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if summary.Valid {
		sess.Summary = &summary.String
	}
	return &sess, nil
}

func (s *SQLiteStore) CreateSession(conversationID string, seq int, summary *string) (Session, error) {
	res, err := s.db.Exec(
		`INSERT INTO sessions (conversation_id, seq, summary) VALUES (?, ?, ?)`,
		conversationID, seq, summary,
	)
	if err != nil {
		return Session{}, err
	}
	id, _ := res.LastInsertId()
	return Session{
		ID:             id,
		ConversationID: conversationID,
		Seq:            seq,
		Summary:        summary,
	}, nil
}

func (s *SQLiteStore) GetSessions(conversationID string) ([]Session, error) {
	rows, err := s.db.Query(
		`SELECT id, conversation_id, seq, summary FROM sessions WHERE conversation_id = ? ORDER BY seq`,
		conversationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var sess Session
		var summary sql.NullString
		if err := rows.Scan(&sess.ID, &sess.ConversationID, &sess.Seq, &summary); err != nil {
			return nil, err
		}
		if summary.Valid {
			sess.Summary = &summary.String
		}
		sessions = append(sessions, sess)
	}
	return sessions, rows.Err()
}

func (s *SQLiteStore) CountConversationMessages(conversationID string) (int, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM messages m
		 JOIN sessions s ON s.id = m.session_id
		 WHERE s.conversation_id = ?`,
		conversationID,
	).Scan(&n)
	return n, err
}

// --- Messages ---

func (s *SQLiteStore) AppendMessage(sessionID int64, authorID string, content string, tokenEst int) (Message, error) {
	res, err := s.db.Exec(
		`INSERT INTO messages (session_id, seq, author_id, content, token_est)
		 VALUES (?, (SELECT COALESCE(MAX(seq), 0) + 1 FROM messages WHERE session_id = ?), ?, ?, ?)`,
		sessionID, sessionID, authorID, content, tokenEst,
	)
	if err != nil {
		return Message{}, err
	}
	id, _ := res.LastInsertId()

	var msg Message
	err = s.db.QueryRow(
		`SELECT id, session_id, seq, author_id, content, token_est, compacted FROM messages WHERE id = ?`, id,
	).Scan(&msg.ID, &msg.SessionID, &msg.Seq, &msg.AuthorID, &msg.Content, &msg.TokenEst, &msg.Compacted)
	return msg, err
}

func (s *SQLiteStore) GetNonCompactedMessages(sessionID int64) ([]Message, error) {
	rows, err := s.db.Query(
		`SELECT id, session_id, seq, author_id, content, token_est, compacted
		 FROM messages WHERE session_id = ? AND compacted = 0 ORDER BY seq`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

func (s *SQLiteStore) GetSessionMessages(sessionID int64) ([]Message, error) {
	rows, err := s.db.Query(
		`SELECT id, session_id, seq, author_id, content, token_est, compacted
		 FROM messages WHERE session_id = ? ORDER BY seq`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

func (s *SQLiteStore) GetLastMessage(sessionID int64) (*Message, error) {
	var msg Message
	err := s.db.QueryRow(
		`SELECT id, session_id, seq, author_id, content, token_est, compacted
		 FROM messages WHERE session_id = ? ORDER BY seq DESC LIMIT 1`,
		sessionID,
	).Scan(&msg.ID, &msg.SessionID, &msg.Seq, &msg.AuthorID, &msg.Content, &msg.TokenEst, &msg.Compacted)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &msg, nil
}

func (s *SQLiteStore) MarkMessagesCompacted(sessionID int64, fromSeq int, toSeq int) error {
	_, err := s.db.Exec(
		`UPDATE messages SET compacted = 1 WHERE session_id = ? AND seq >= ? AND seq <= ?`,
		sessionID, fromSeq, toSeq,
	)
	return err
}

// --- Compaction Summaries ---

func (s *SQLiteStore) SaveCompactionSummary(sessionID int64, fromSeq int, toSeq int, summary string) error {
	_, err := s.db.Exec(
		`INSERT INTO compaction_summaries (session_id, from_msg_seq, to_msg_seq, summary) VALUES (?, ?, ?, ?)`,
		sessionID, fromSeq, toSeq, summary,
	)
	return err
}

func (s *SQLiteStore) GetCompactionSummaries(sessionID int64) ([]CompactionSummary, error) {
	rows, err := s.db.Query(
		`SELECT id, session_id, from_msg_seq, to_msg_seq, summary
		 FROM compaction_summaries WHERE session_id = ? ORDER BY from_msg_seq`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var summaries []CompactionSummary
	for rows.Next() {
		var cs CompactionSummary
		if err := rows.Scan(&cs.ID, &cs.SessionID, &cs.FromMsgSeq, &cs.ToMsgSeq, &cs.Summary); err != nil {
			return nil, err
		}
		summaries = append(summaries, cs)
	}
	return summaries, rows.Err()
}

func scanMessages(rows *sql.Rows) ([]Message, error) {
	var messages []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Seq, &m.AuthorID, &m.Content, &m.TokenEst, &m.Compacted); err != nil {
			return nil, err
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
}
