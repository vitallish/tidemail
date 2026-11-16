package cli

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/allisonhere/tidemail/internal/config"
	"github.com/allisonhere/tidemail/internal/db"
	"github.com/allisonhere/tidemail/internal/mailapp"
)

// openAttachment is mailapp.OpenAttachment, replaceable in tests.
var openAttachment = mailapp.OpenAttachment

// runAttachmentAction implements `messages attachments ID` (list the parts a
// person would open) and `messages open ID --attachment N` (open one with the
// configured program). Both use the same mailapp core as the TUI, and read
// only the cache: nothing is fetched from the server.
func runAttachmentAction(database *db.DB, cfg config.Config, action, idArg string, n int, jsonMode bool, out, errOut io.Writer) int {
	fail := func(code, message string, status int) int {
		return readFailure(out, errOut, jsonMode, code, message, status)
	}
	id, err := readID(idArg)
	if err != nil {
		return fail("invalid_arguments", "Invalid message ID; use an ID from messages list.", 2)
	}
	if _, err := database.GetMessage(id); errors.Is(err, sql.ErrNoRows) {
		return fail("not_found", "Cached message not found.", 1)
	} else if err != nil {
		return fail("database_error", "Cannot read cached message.", 1)
	}
	metas, err := database.GetAttachmentsMeta(id)
	if err != nil {
		return fail("database_error", "Cannot read attachments.", 1)
	}
	items := mailapp.OpenableAttachments(metas)
	if action == "attachments" {
		list := make([]map[string]any, 0, len(items))
		for i, a := range items {
			list = append(list, map[string]any{"number": i + 1, "filename": mailapp.AttachmentFileName(a), "content_type": a.ContentType, "size": a.Size})
		}
		if !jsonMode {
			for i, a := range items {
				if _, err := fmt.Fprintf(out, "%d  %s  %s  %s\n", i+1, safeTerminal(mailapp.AttachmentFileName(a)), safeTerminal(a.ContentType), formatBytes(a.Size)); err != nil {
					return 1
				}
			}
			if len(items) == 0 {
				_, _ = fmt.Fprintln(out, "No attachments.")
			}
			return 0
		}
		return readSuccess(out, jsonMode, map[string]any{"id": idArg, "attachments": list})
	}
	if n < 1 || n > len(items) {
		return fail("invalid_arguments", "Give --attachment N from 'messages attachments'; this message has "+countWord(len(items))+".", 2)
	}
	a := items[n-1]
	path, err := openAttachment(database, a, mailapp.OpenAttachmentOptions{Command: cfg.Display.AttachmentOpenCmd})
	if err != nil {
		return fail("open_failed", "Cannot open the attachment: "+err.Error(), 1)
	}
	return readSuccess(out, jsonMode, map[string]any{"id": idArg, "number": n, "filename": mailapp.AttachmentFileName(a), "path": path})
}

func countWord(n int) string {
	switch n {
	case 0:
		return "no attachments to open"
	case 1:
		return "1 attachment"
	}
	return strconv.Itoa(n) + " attachments"
}

func formatBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
