-- name: CreateChirp :one
INSERT INTO chirps (id, body, user_id, created_at, updated_at)
VALUES (
        gen_random_uuid(),
        $1,
        $2,
        NOW(),
        NOW()
       )
RETURNING *;