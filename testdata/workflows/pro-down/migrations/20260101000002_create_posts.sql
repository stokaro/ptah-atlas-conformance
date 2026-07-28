-- atlas:txtar

-- migration.sql --
CREATE TABLE posts (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users (id),
  title TEXT NOT NULL
);

-- down.sql --
DROP TABLE posts;
