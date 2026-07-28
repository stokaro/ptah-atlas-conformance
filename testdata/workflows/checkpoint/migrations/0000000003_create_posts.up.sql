CREATE TABLE posts (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id), title TEXT NOT NULL);
CREATE INDEX idx_posts_user_id ON posts(user_id);
