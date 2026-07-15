-- atlas:txtar

-- migration.sql --
-- keep this marker-like SQL comment --
CREATE TABLE txtar_boundary_widgets (id INT PRIMARY KEY, name TEXT NOT NULL);

-- schema.sql --
SELECT 'ptah_conformance_txtar_extra_section_sentinel';

-- down.sql --
SELECT 'ptah_conformance_txtar_down_sentinel';
DROP TABLE txtar_boundary_widgets;
