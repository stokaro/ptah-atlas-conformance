CREATE TABLE users (
  id INTEGER PRIMARY KEY,
  email TEXT
);
CREATE INDEX idx_users_email ON users (email);
