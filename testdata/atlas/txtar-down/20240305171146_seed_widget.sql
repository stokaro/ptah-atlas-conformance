-- atlas:txtar

-- migration.sql --
CREATE TABLE widgets (id INT PRIMARY KEY, name TEXT NOT NULL);
INSERT INTO widgets (id, name) VALUES (1, 'Alice');

-- down.sql --
DROP TABLE widgets;
