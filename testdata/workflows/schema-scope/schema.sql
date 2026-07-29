CREATE TABLE scope_users (
  id INTEGER PRIMARY KEY,
  email TEXT
);
CREATE TABLE scope_groups (
  id INTEGER PRIMARY KEY,
  owner_id INTEGER REFERENCES scope_users(id)
);
CREATE TABLE scope_archive (
  id INTEGER PRIMARY KEY
);
