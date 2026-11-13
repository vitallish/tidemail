package db

import "database/sql"

// MessageByMessageID returns a cached message with the given Message-ID (compared
// case-insensitively, brackets as stored), preferring one whose body is cached.
// ok is false when none is cached. Replies use it to find the message they quote.
func (db *DB) MessageByMessageID(messageID string) (m Message, ok bool, err error) {
	if messageID == "" {
		return Message{}, false, nil
	}
	var id int64
	err = db.QueryRow(`SELECT id FROM messages WHERE lower(message_id) = lower(?)
		ORDER BY (body_html != '' OR body_text != '') DESC, date DESC, id DESC LIMIT 1`, messageID).Scan(&id)
	if err == sql.ErrNoRows {
		return Message{}, false, nil
	}
	if err != nil {
		return Message{}, false, err
	}
	m, err = db.GetMessage(id)
	if err != nil {
		return Message{}, false, err
	}
	return m, true, nil
}
