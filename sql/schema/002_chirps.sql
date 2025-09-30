-- +goose Up
CREATE TABLE chirps (
    id uuid PRIMARY KEY,
    body TEXT NOT NULL,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

-- +goose Down
DROP TABLE chirps;