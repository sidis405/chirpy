-- name: UpgradeUser :one
UPDATE users SET is_chirpy_red = true where id = $1 RETURNING *;