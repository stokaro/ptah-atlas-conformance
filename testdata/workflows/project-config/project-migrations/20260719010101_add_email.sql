ALTER TABLE users ADD COLUMN email TEXT;
CREATE TABLE conformance_project_apply_delay AS
SELECT randomblob(8388608) AS payload;
DROP TABLE conformance_project_apply_delay;
