-- Created each time the app opens its file, so each statement may find it
-- already done.
CREATE TABLE IF NOT EXISTS books (
  id     INTEGER PRIMARY KEY,
  title  TEXT NOT NULL,
  author TEXT NOT NULL DEFAULT '',
  read   BOOLEAN NOT NULL DEFAULT 0,
  added  DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS books_added ON books (added);
