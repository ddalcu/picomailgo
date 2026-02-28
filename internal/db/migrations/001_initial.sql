-- Users
CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

-- Mailboxes (INBOX, Sent, Drafts, Trash, custom)
CREATE TABLE IF NOT EXISTS mailboxes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    uid_validity INTEGER NOT NULL DEFAULT 1,
    uid_next INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT (datetime('now')),
    UNIQUE(user_id, name)
);

-- Messages
CREATE TABLE IF NOT EXISTS messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    mailbox_id INTEGER NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
    uid INTEGER NOT NULL,
    from_addr TEXT NOT NULL DEFAULT '',
    to_addr TEXT NOT NULL DEFAULT '',
    subject TEXT NOT NULL DEFAULT '',
    date DATETIME,
    size INTEGER NOT NULL DEFAULT 0,
    raw BLOB NOT NULL,
    created_at DATETIME NOT NULL DEFAULT (datetime('now')),
    UNIQUE(mailbox_id, uid)
);

CREATE INDEX IF NOT EXISTS idx_messages_mailbox_date ON messages(mailbox_id, date DESC);
CREATE INDEX IF NOT EXISTS idx_messages_from ON messages(from_addr);
CREATE INDEX IF NOT EXISTS idx_messages_to ON messages(to_addr);

-- Message flags (\\Seen, \\Flagged, \\Deleted, etc.)
CREATE TABLE IF NOT EXISTS message_flags (
    message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    flag TEXT NOT NULL,
    PRIMARY KEY(message_id, flag)
);

-- DKIM keys
CREATE TABLE IF NOT EXISTS dkim_keys (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    domain TEXT NOT NULL,
    selector TEXT NOT NULL,
    private_key BLOB NOT NULL,
    public_key TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT (datetime('now')),
    UNIQUE(domain, selector)
);

-- Sessions (for web auth)
CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at DATETIME NOT NULL DEFAULT (datetime('now')),
    expires_at DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);

-- Outbound queue
CREATE TABLE IF NOT EXISTS outbound_queue (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    from_addr TEXT NOT NULL,
    to_addr TEXT NOT NULL,
    raw BLOB NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 10,
    next_retry DATETIME NOT NULL DEFAULT (datetime('now')),
    last_error TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_outbound_queue_retry ON outbound_queue(next_retry);
