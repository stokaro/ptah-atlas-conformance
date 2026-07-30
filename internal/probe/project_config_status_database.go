package probe

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
)

func cloneProjectConfigDatabase(source, destination string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read brownfield database: %w", err)
	}
	if err := os.WriteFile(destination, data, 0o600); err != nil {
		return fmt.Errorf("write brownfield database clone: %w", err)
	}
	return nil
}

func inspectProjectConfigDatabase(path string) (projectConfigDatabaseFacts, error) {
	db, err := openSQLiteRuntimeDB(path)
	if err != nil {
		return projectConfigDatabaseFacts{}, err
	}
	defer func() { _ = db.Close() }()

	objects, err := projectConfigSchemaObjects(db)
	if err != nil {
		return projectConfigDatabaseFacts{}, fmt.Errorf("inspect schema objects: %w", err)
	}
	columns, err := projectConfigSchemaColumns(db)
	if err != nil {
		return projectConfigDatabaseFacts{}, fmt.Errorf("inspect schema columns: %w", err)
	}
	revisions, err := projectConfigRevisionMetadataFacts(db)
	if err != nil {
		return projectConfigDatabaseFacts{}, fmt.Errorf("inspect full revision metadata: %w", err)
	}
	return projectConfigDatabaseFacts{
		Objects:   objects,
		Columns:   columns,
		Revisions: revisions,
	}, nil
}

func projectConfigSchemaObjects(db *sql.DB) ([]projectConfigSchemaObjectFact, error) {
	rows, err := db.QueryContext(context.Background(), `
SELECT type, name, tbl_name
FROM sqlite_schema
WHERE name NOT LIKE 'sqlite_%'
  AND name <> 'atlas_schema_revisions'
ORDER BY type, name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var facts []projectConfigSchemaObjectFact
	for rows.Next() {
		var fact projectConfigSchemaObjectFact
		if err := rows.Scan(&fact.Type, &fact.Name, &fact.Table); err != nil {
			return nil, err
		}
		facts = append(facts, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return facts, nil
}

func projectConfigSchemaColumns(db *sql.DB) ([]projectConfigSchemaColumnFact, error) {
	rows, err := db.QueryContext(context.Background(), `
SELECT
    schema_object.name,
    column_info.cid,
    column_info.name,
    column_info.type,
    column_info.[notnull],
    column_info.dflt_value IS NULL,
    COALESCE(CAST(column_info.dflt_value AS TEXT), ''),
    column_info.pk,
    column_info.hidden
FROM sqlite_schema AS schema_object
JOIN pragma_table_xinfo(schema_object.name) AS column_info
WHERE schema_object.type = 'table'
  AND schema_object.name NOT LIKE 'sqlite_%'
  AND schema_object.name <> 'atlas_schema_revisions'
ORDER BY schema_object.name, column_info.cid`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var facts []projectConfigSchemaColumnFact
	for rows.Next() {
		var fact projectConfigSchemaColumnFact
		if err := rows.Scan(
			&fact.Table,
			&fact.Position,
			&fact.Name,
			&fact.Type,
			&fact.NotNull,
			&fact.DefaultIsNull,
			&fact.Default,
			&fact.PrimaryKey,
			&fact.Hidden,
		); err != nil {
			return nil, err
		}
		facts = append(facts, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return facts, nil
}

func projectConfigRevisionMetadataFacts(db *sql.DB) ([]projectConfigRevisionMetadata, error) {
	rows, err := db.QueryContext(context.Background(), `
SELECT
    version,
    description,
    type,
    applied,
    total,
    CAST(executed_at AS TEXT),
    typeof(executed_at),
    execution_time,
    error IS NULL,
    COALESCE(error, ''),
    typeof(error),
    error_stmt IS NULL,
    COALESCE(error_stmt, ''),
    typeof(error_stmt),
    hash,
    partial_hashes IS NULL,
    COALESCE(CAST(partial_hashes AS TEXT), ''),
    typeof(partial_hashes),
    operator_version
FROM atlas_schema_revisions
ORDER BY version`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var facts []projectConfigRevisionMetadata
	for rows.Next() {
		var fact projectConfigRevisionMetadata
		if err := rows.Scan(
			&fact.Version,
			&fact.Description,
			&fact.Type,
			&fact.Applied,
			&fact.Total,
			&fact.ExecutedAt,
			&fact.ExecutedAtStorageClass,
			&fact.ExecutionTime,
			&fact.ErrorIsNull,
			&fact.Error,
			&fact.ErrorStorageClass,
			&fact.ErrorStatementIsNull,
			&fact.ErrorStatement,
			&fact.ErrorStatementStorageClass,
			&fact.Hash,
			&fact.PartialHashesIsNull,
			&fact.PartialHashes,
			&fact.PartialHashesStorageClass,
			&fact.OperatorVersion,
		); err != nil {
			return nil, err
		}
		canonicalPartialHashes, err := canonicalProjectConfigJSON(fact.PartialHashes, fact.PartialHashesIsNull)
		if err != nil {
			return nil, fmt.Errorf("revision %s partial_hashes: %w", fact.Version, err)
		}
		fact.PartialHashes = canonicalPartialHashes
		facts = append(facts, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return facts, nil
}

func canonicalProjectConfigJSON(value string, sqlNull bool) (string, error) {
	if sqlNull {
		return "", nil
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(value)); err != nil {
		return "", err
	}
	return compact.String(), nil
}
