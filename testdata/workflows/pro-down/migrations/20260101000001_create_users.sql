-- atlas:txtar

-- migration.sql --
CREATE TABLE users (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL
);

-- down.sql --
DROP TABLE users;
