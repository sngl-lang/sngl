-- sngl:schema schema.sql

-- name: Shelf :many
-- The books whose title or author holds search, unread first, newest first.
SELECT * FROM books
WHERE title LIKE '%' || sqlc.arg(search) || '%' OR author LIKE '%' || sqlc.arg(search) || '%'
ORDER BY read, added DESC, id DESC;

-- name: AddBook :execlastid
INSERT INTO books (title, author, added) VALUES (?, ?, ?);

-- name: SetRead :execrows
UPDATE books SET read = ? WHERE id = ?;

-- name: RemoveBook :execrows
DELETE FROM books WHERE id = ?;

-- name: Unread :one
SELECT count(*) FROM books WHERE NOT read;
