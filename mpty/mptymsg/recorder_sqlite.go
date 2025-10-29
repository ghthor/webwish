package mptymsg

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"time"

	_ "modernc.org/sqlite"
)

type SqliteRecorder struct {
	ctx context.Context
	db  *sql.DB
}

func NewSqlite(ctx context.Context, filename string) (*SqliteRecorder, error) {
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_fk=1", filename))
	if err != nil {
		return nil, err
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS msgs (
			id INTEGER PRIMARY KEY,
			ts DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			msg JSON NOT NULL CHECK (json_valid(msg))
		);
	`)
	if err != nil {
		return nil, fmt.Errorf("error initializing sqlite table: %w", err)
	}

	return &SqliteRecorder{
		ctx: ctx,
		db:  db,
	}, nil
}

func (r *SqliteRecorder) Close() error {
	return r.db.Close()
}

func (r *SqliteRecorder) Save(msg Recordable) (Recordable, error) {
	b, err := JsonMarshal(msg)
	if err != nil {
		return nil, fmt.Errorf("error marshaling message: %w", err)
	}

	ts := msg.Ts()
	if ts.IsZero() {
		ts = time.Now()
	}

	res, err := r.db.ExecContext(r.ctx, `INSERT INTO msgs(ts, msg) VALUES (?, ?)`, ts, string(b))
	if err != nil {
		return nil, fmt.Errorf("error saving message: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("error reading last insert id: %w", err)
	}

	return msg.SetId(id), nil
}

func (r *SqliteRecorder) Read(n int) ([]Recordable, error) {
	rows, err := r.db.QueryContext(r.ctx, `
SELECT id, msg
FROM msgs
ORDER BY ts DESC, id DESC
LIMIT ?
`, n)
	if err != nil {
		return nil, fmt.Errorf("msgs query error: %w", err)
	}

	msgs := make([]Recordable, 0, n)
	for rows.Next() {
		var (
			id     int64
			rawMsg string
		)
		err = rows.Scan(&id, &rawMsg)
		if err != nil {
			break
		}

		var recMsg Recordable
		recMsg, err = JsonUnmarshal([]byte(rawMsg))
		if err != nil {
			err = fmt.Errorf("json decoding error: %w", err)
			// TODO: maybe just log undecodable msg?
			break
		}
		msgs = append(msgs, recMsg.SetId(id))
	}
	if closeErr := rows.Close(); closeErr != nil {
		return nil, fmt.Errorf("rows close error: %w", closeErr)
	}
	if err != nil {
		return nil, fmt.Errorf("rows scan error: %w", err)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("rows unexpected error: %w", rows.Err())
	}

	// TODO: maybe need to reverse msgs?
	slices.Reverse(msgs)

	return msgs, nil
}
