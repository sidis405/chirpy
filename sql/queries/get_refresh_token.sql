-- name: GetRefreshToken :one
SELECT * FROM refresh_tokens WHERE token = $1 and expires_at > NOW() and revoked_at IS NULL;