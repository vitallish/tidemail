package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/mail"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/allisonhere/tidemail/internal/config"
	"github.com/allisonhere/tidemail/internal/db"
	imapClient "github.com/allisonhere/tidemail/internal/imap"
	"github.com/allisonhere/tidemail/internal/mailapp"
)

// RunMailRead exposes cached mail without starting the UI or contacting servers.
func RunMailRead(command string, args []string, out, errOut io.Writer) int {
	jsonMode := false
	for _, arg := range args {
		if arg == "--json" || arg == "--json=true" {
			jsonMode = true
		}
	}
	fail := func(code, message string, status int) int {
		return readFailure(out, errOut, jsonMode, code, message, status)
	}
	action := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet(command+" "+action, flag.ContinueOnError)
	fs.SetOutput(errOut)
	fs.BoolVar(&jsonMode, "json", jsonMode, "Print a versioned JSON envelope")
	agent := fs.Bool("agent", false, "Enforce configured agent capabilities")
	limit := fs.Int("limit", 50, "Maximum results (1–1000)")
	offset := fs.Int("offset", 0, "Results to skip")
	unread := fs.Bool("unread", false, "Only unread messages")
	server := fs.Bool("server", false, "search: also search the mail server and download matches not yet cached")
	accountName := fs.String("account", "", "search --server: only this account (name or ID)")
	usageOut := errOut
	if hasHelpFlag(args) {
		usageOut = out
	}
	fs.Usage = func() {
		extra := ""
		if command == "messages" && action == "search" {
			extra = " [--server [--account NAME]]"
		}
		_, _ = fmt.Fprintf(usageOut, "Usage: tidemail %s %s [ID or query] [--json] [--agent] [--limit N] [--offset N]%s\nCached reads do not synchronize with the server; search --server is the exception.\nUse --interactive for guided input, or --no-interactive to suppress prompts.\n", command, action, extra)
		if command == "outbox" {
			_, _ = fmt.Fprintln(usageOut, "Use outbox process to deliver queued messages while the TUI is closed.")
		}
		fs.SetOutput(usageOut)
		fs.PrintDefaults()
	}
	// Go's flag parser stops at positionals; permit flags after an ID/query.
	var options, positionals []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			options = append(options, a)
			if (a == "--limit" || a == "--offset" || a == "--account") && i+1 < len(args) {
				i++
				options = append(options, args[i])
			}
		} else {
			positionals = append(positionals, a)
		}
	}
	if err := fs.Parse(options); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return fail("invalid_arguments", "Invalid command options; use --help.", 2)
	}
	if *limit < 1 || *limit > 1000 || *offset < 0 {
		return fail("invalid_arguments", "--limit must be 1–1000 and --offset must be nonnegative.", 2)
	}
	if (command == "accounts" && action != "list") || (command == "messages" && action != "list" && action != "search" && action != "read") || (command == "outbox" && action != "list" && action != "status") {
		hint := map[string]string{"accounts": "list", "messages": "list, search, or read", "outbox": "list or status"}[command]
		message := fmt.Sprintf("Unknown subcommand %q; %s supports %s.", action, command, hint)
		if command == "messages" {
			message += ` Search with: tidemail messages search "query".`
		}
		return fail("invalid_arguments", message, 2)
	}
	if (*server || *accountName != "") && (command != "messages" || action != "search") {
		return fail("invalid_arguments", "--server and --account apply only to messages search.", 2)
	}
	if *accountName != "" && !*server {
		return fail("invalid_arguments", "--account applies to --server; cached searches cover every account.", 2)
	}
	needsArgument := action == "read" || action == "status" || action == "search"
	if (needsArgument && len(positionals) != 1) || (!needsArgument && len(positionals) != 0) {
		return fail("invalid_arguments", "Provide exactly one ID or quoted search query for read, status, or search; lists take no positional arguments.", 2)
	}
	cfg, err := config.Load()
	if err != nil {
		return fail("config_error", "Cannot load configuration; check config.toml.", 1)
	}
	capability := command + ".read"
	if command == "messages" {
		capability = "messages.metadata"
		if action == "read" {
			capability = "messages.read"
		}
		if action == "search" {
			capability = "messages.search"
		}
	}
	if *agent && !mailapp.Allowed(cfg, capability) {
		return fail("permission_denied", "Agent capability "+capability+" is not enabled in agent_permissions.", 3)
	}
	// Searching the server contacts it and writes the cache, so it is a separate
	// capability that the default agent permissions do not include.
	if *agent && *server && !mailapp.Allowed(cfg, "messages.search_server") {
		return fail("permission_denied", "Agent capability messages.search_server is not enabled in agent_permissions.", 3)
	}
	if command == "accounts" {
		items := make([]map[string]any, 0, len(cfg.Accounts))
		def, _ := cfg.DefaultAccount()
		for _, a := range cfg.Accounts {
			email := a.From
			if parsed, e := mail.ParseAddress(email); e == nil {
				email = parsed.Address
			}
			if email == "" {
				email = a.User
			}
			items = append(items, map[string]any{"id": a.ID, "name": a.Name, "email": email, "default": a.ID == def.ID})
		}
		return readSuccess(out, jsonMode, readPage(items, *limit, *offset))
	}
	database, err := db.Open()
	if err != nil {
		return fail("database_error", "Cannot open the mail cache.", 1)
	}
	defer database.Close() //nolint:errcheck
	if command == "outbox" {
		if action == "status" {
			id, e := strconv.ParseInt(positionals[0], 10, 64)
			if e != nil || id <= 0 {
				return fail("invalid_arguments", "Invalid outbox ID.", 2)
			}
			item, e := database.GetOutbox(id)
			if errors.Is(e, sql.ErrNoRows) {
				return fail("not_found", "Outbox item not found.", 1)
			}
			if e != nil {
				return fail("database_error", "Cannot read outbox item.", 1)
			}
			return readSuccess(out, jsonMode, outboxMetadata(item))
		}
		var total int
		if e := database.QueryRow("SELECT COUNT(*) FROM outbox").Scan(&total); e != nil {
			return fail("database_error", "Cannot count outbox items.", 1)
		}
		rows, e := database.Query("SELECT id FROM outbox ORDER BY CASE WHEN state='sent' THEN 1 ELSE 0 END, id DESC LIMIT ? OFFSET ?", *limit, *offset)
		if e != nil {
			return fail("database_error", "Cannot list outbox.", 1)
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if e = rows.Scan(&id); e != nil {
				_ = rows.Close()
				return fail("database_error", "Cannot list outbox.", 1)
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		_ = rows.Close()
		if e != nil {
			return fail("database_error", "Cannot list outbox.", 1)
		}
		safe := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			item, e := database.GetOutbox(id)
			if e != nil {
				return fail("database_error", "Cannot read outbox item.", 1)
			}
			safe = append(safe, outboxMetadata(item))
		}
		return readSuccess(out, jsonMode, readBoundedPage(safe, total, *limit, *offset))
	}
	metadata := func(m db.Message) (map[string]any, error) {
		var accountID, accountName, mailboxName string
		err := database.QueryRow(`SELECT accounts.config_id, accounts.name, mailboxes.name FROM mailboxes JOIN accounts ON accounts.id=mailboxes.account_id WHERE mailboxes.id=?`, m.MailboxID).Scan(&accountID, &accountName, &mailboxName)
		return map[string]any{"id": fmt.Sprintf("cache:%d", m.ID), "cache_id": m.ID, "account_id": accountID, "account_name": accountName, "mailbox_id": m.MailboxID, "mailbox": mailboxName, "uid": m.UID, "message_id": m.MessageID, "subject": m.Subject, "from": m.From, "to": m.To, "cc": m.CC, "date": m.Date.Format(time.RFC3339), "read": m.Read, "starred": m.Starred, "has_attachment": m.HasAttachment}, err
	}
	if action == "read" {
		id, e := readID(positionals[0])
		if e != nil {
			return fail("invalid_arguments", "Invalid message ID; use an ID from messages list.", 2)
		}
		m, e := database.GetMessage(id)
		if errors.Is(e, sql.ErrNoRows) {
			return fail("not_found", "Cached message not found.", 1)
		}
		if e != nil {
			return fail("database_error", "Cannot read cached message.", 1)
		}
		data, e := metadata(m)
		if e != nil {
			return fail("database_error", "Cannot resolve message identity.", 1)
		}
		data["body_text"] = m.BodyText
		data["body_html"] = m.BodyHTML
		return readSuccess(out, jsonMode, data)
	}
	var serverResult *mailapp.ServerSearchResult
	if *server {
		res, failure := runServerSearch(database, cfg, positionals[0], *accountName)
		if failure != nil {
			return fail(failure.code, failure.message, failure.status)
		}
		serverResult = &res
		if !jsonMode {
			text, _ := res.Summary()
			_, _ = fmt.Fprintln(errOut, safeTerminal(text))
		}
	}
	var messages []db.Message
	var total int
	if action == "search" {
		messages, total, err = database.SearchAllMessagesPage(positionals[0], *unread, *limit, *offset)
	} else {
		messages, total, err = database.ListUnifiedInboxPage(*unread, *limit, *offset)
	}
	if err != nil {
		return fail("database_error", "Cannot query cached messages.", 1)
	}
	items := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		if *unread && m.Read {
			continue
		}
		data, e := metadata(m)
		if e != nil {
			return fail("database_error", "Cannot resolve message identity.", 1)
		}
		items = append(items, data)
	}
	page := readBoundedPage(items, total, *limit, *offset)
	if serverResult != nil {
		page["server"] = map[string]any{"folders": serverResult.Folders, "found": serverResult.Found, "fetched": serverResult.Fetched, "not_downloaded": serverResult.Capped, "errors": serverResult.Errs, "checked_locally": len(serverResult.Skipped) > 0}
	}
	return readSuccess(out, jsonMode, page)
}

// cliFailure is an error as the command line reports it.
type cliFailure struct {
	code, message string
	status        int
}

// runServerSearch searches the configured accounts' servers (or the one named)
// and stores matches in the cache; the caller then searches the cache.
func runServerSearch(database *db.DB, cfg config.Config, query, accountName string) (mailapp.ServerSearchResult, *cliFailure) {
	accounts, err := database.ListAccounts()
	if err != nil {
		return mailapp.ServerSearchResult{}, &cliFailure{"database_error", "Cannot list accounts.", 1}
	}
	var targets []mailapp.SearchTarget
	for _, acc := range accounts {
		acfg, e := mailapp.AccountConfigFor(cfg.Accounts, acc)
		if e != nil {
			if accountName == "" {
				continue // an account with no settings is not searchable
			}
			if acc.Name == accountName {
				return mailapp.ServerSearchResult{}, &cliFailure{"invalid_arguments", fmt.Sprintf("Account %q has no server settings in config.toml.", accountName), 2}
			}
			continue
		}
		if accountName != "" && accountName != acc.Name && accountName != acfg.ID {
			continue
		}
		targets = append(targets, mailapp.SearchTarget{Account: acc, Config: acfg})
	}
	if len(targets) == 0 {
		if accountName != "" {
			return mailapp.ServerSearchResult{}, &cliFailure{"not_found", fmt.Sprintf("No account named %q; see tidemail accounts list.", accountName), 1}
		}
		return mailapp.ServerSearchResult{}, &cliFailure{"invalid_arguments", "No account to search.", 2}
	}
	sessions := imapClient.NewSessionPool()
	defer sessions.Close()
	ctx, cancel := context.WithTimeout(context.Background(), mailapp.ServerSearchTimeout)
	defer cancel()
	res, err := mailapp.ServerSearch(ctx, database, sessions, targets, query, time.Now(), cfg.Display.ServerSearchWindowDays)
	var refused *mailapp.NothingToSearchError
	switch {
	case errors.As(err, &refused):
		return res, &cliFailure{"invalid_arguments", refused.Error(), 2}
	case err != nil:
		return res, &cliFailure{"server_error", "Server search failed: " + safeTerminal(err.Error()), 1}
	}
	return res, nil
}

func readID(value string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimPrefix(value, "cache:"), 10, 64)
	if err == nil && id <= 0 {
		err = fmt.Errorf("invalid ID")
	}
	return id, err
}
func readPage(items []map[string]any, limit, offset int) map[string]any {
	start := min(offset, len(items))
	end := min(start+limit, len(items))
	var next any
	if end < len(items) {
		next = end
	}
	return map[string]any{"items": items[start:end], "limit": limit, "offset": offset, "total": len(items), "next_offset": next}
}
func outboxMetadata(item db.OutboxItem) map[string]any {
	return map[string]any{"id": item.ID, "account_id": item.AccountConfigID, "account_name": item.AccountName, "subject": item.Subject, "state": item.State, "attempts": item.Attempts, "max_attempts": item.MaxAttempts, "next_attempt": item.NextAttempt, "created_at": item.CreatedAt, "updated_at": item.UpdatedAt}
}
func readSuccess(out io.Writer, jsonMode bool, data any) int {
	if jsonMode {
		if err := json.NewEncoder(out).Encode(map[string]any{"schema_version": 1, "ok": true, "data": data}); err != nil {
			return 1
		}
		return 0
	}
	value, ok := data.(map[string]any)
	if !ok {
		return 1
	}
	if items, exists := value["items"].([]map[string]any); exists {
		for _, item := range items {
			id := fmt.Sprint(item["id"])
			label := fmt.Sprint(item["subject"])
			detail := fmt.Sprint(item["from"])
			if name, exists := item["name"]; exists {
				label = fmt.Sprint(name)
				detail = fmt.Sprint(item["email"])
			}
			if state, exists := item["state"]; exists {
				detail = fmt.Sprint(state)
			}
			line := func(s string) string { return strings.Join(strings.Fields(safeTerminal(s)), " ") }
			if _, err := fmt.Fprintf(out, "%s  %s  %s\n", paint(out, line(id), 124, 58, 237), line(label), paint(out, line(detail), 6, 182, 212)); err != nil {
				return 1
			}
		}
		if len(items) == 0 {
			if _, err := fmt.Fprintln(out, "No results."); err != nil {
				return 1
			}
		}
	} else {
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if _, err := fmt.Fprintf(out, "%s: %s\n", paint(out, key, 124, 58, 237), safeTerminal(fmt.Sprint(value[key]))); err != nil {
				return 1
			}
		}
	}
	return 0
}
func readFailure(out, errOut io.Writer, jsonMode bool, code, message string, status int) int {
	if jsonMode {
		_ = json.NewEncoder(out).Encode(map[string]any{"schema_version": 1, "ok": false, "error": map[string]string{"code": code, "message": message}})
	} else {
		_, _ = fmt.Fprintln(errOut, "tidemail: "+message)
	}
	return status
}

func readBoundedPage(items []map[string]any, total, limit, offset int) map[string]any {
	var next any
	if offset+len(items) < total {
		next = offset + len(items)
	}
	return map[string]any{"items": items, "limit": limit, "offset": offset, "total": total, "next_offset": next}
}
