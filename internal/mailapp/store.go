package mailapp

import "github.com/allisonhere/tidemail/internal/db"

// StoreFetchedMessages upserts msgs and returns the genuinely-new unread ones
// (previously-unseen UIDs), used to drive desktop notifications. Callers derive the
// "N new" count from len(newMsgs).
func StoreFetchedMessages(database *db.DB, mailboxID int64, msgs []db.Message) ([]db.Message, error) {
	var newMsgs []db.Message
	for _, msg := range msgs {
		msg.MailboxID = mailboxID
		deleted, delErr := database.MessageDeletedLocally(mailboxID, msg.UID, msg.MessageID)
		if delErr != nil {
			return newMsgs, delErr
		}
		if deleted {
			continue
		}
		// Determine novelty before the upsert: SINCE re-fetches mail we already hold,
		// and the upsert would otherwise make every poll look like new arrivals.
		existed, exErr := database.MessageExists(mailboxID, msg.UID)
		if err := database.UpsertMessage(msg); err != nil {
			return newMsgs, err
		}
		if exErr == nil && !existed && !msg.Read {
			// Carry the row ID so later consumers (plugin events) can reference
			// the stored message.
			if id, idErr := database.MessageIDByUID(mailboxID, msg.UID); idErr == nil {
				msg.ID = id
			}
			newMsgs = append(newMsgs, msg)
		}
	}
	return newMsgs, nil
}
