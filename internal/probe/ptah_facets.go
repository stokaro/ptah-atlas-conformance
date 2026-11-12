package probe

import (
	"fmt"
	"maps"

	"ptah.run/core/ast"
	"ptah.run/core/schemaext"
	"ptah.run/dialect/clickhouse/chschema"
	"ptah.run/dialect/mysql/mysqlschema"
	"ptah.run/dialect/postgres/pgtable"
	"ptah.run/dialect/sqlite/sqlitetable"
)

// facetValue makes an invalid owner payload fail the probe instead of quietly
// reading it as an absent option.
func facetValue(facets schemaext.Facets, kind schemaext.Kind) schemaext.Value {
	value, _, err := facets.Get(kind)
	if err != nil {
		panic(err)
	}
	return value
}

func mysqlTableOptions(facets schemaext.Facets) mysqlschema.DesiredTable {
	switch value := facetValue(facets, mysqlschema.TableKind).(type) {
	case nil:
		return mysqlschema.DesiredTable{}
	case *mysqlschema.DesiredTable:
		return *value
	case *mysqlschema.ObservedTable:
		return mysqlschema.DesiredTable{Charset: value.Charset}
	default:
		panic(fmt.Sprintf("unexpected MySQL table facet %T", value))
	}
}

func mysqlASTTableOptions(table *ast.CreateTableNode) map[string]string {
	options := maps.Clone(table.Options)
	if options == nil {
		options = map[string]string{}
	}
	owned := mysqlTableOptions(table.Facets)
	if owned.Engine != "" {
		options["ENGINE"] = owned.Engine
	}
	if owned.AutoIncrement != "" {
		options["AUTO_INCREMENT"] = owned.AutoIncrement
	}
	if owned.Charset != "" {
		options["CHARSET"] = owned.Charset
	}
	return options
}

func clearMySQLTableCharset(facets schemaext.Facets) schemaext.Facets {
	value := facetValue(facets, mysqlschema.TableKind)
	switch typed := value.(type) {
	case nil:
		return facets
	case *mysqlschema.DesiredTable:
		copy := *typed
		copy.Charset = ""
		updated, err := facets.Replace(&copy)
		if err != nil {
			panic(err)
		}
		return updated
	case *mysqlschema.ObservedTable:
		copy := *typed
		copy.Charset = ""
		updated, err := facets.Replace(&copy)
		if err != nil {
			panic(err)
		}
		return updated
	default:
		panic(fmt.Sprintf("unexpected MySQL table facet %T", value))
	}
}

func mysqlColumnOptions(facets schemaext.Facets) mysqlschema.ColumnSettings {
	switch value := facetValue(facets, mysqlschema.ColumnSettingsKind).(type) {
	case nil:
		return mysqlschema.ColumnSettings{}
	case *mysqlschema.DesiredColumnSettings:
		return mysqlschema.ColumnSettings{Charset: value.Charset, OnUpdate: value.OnUpdate}
	case *mysqlschema.ObservedColumnSettings:
		return mysqlschema.ColumnSettings{Charset: value.Charset, OnUpdate: value.OnUpdate}
	default:
		panic(fmt.Sprintf("unexpected MySQL column facet %T", value))
	}
}

func clearMySQLColumnCharset(facets schemaext.Facets) schemaext.Facets {
	value := facetValue(facets, mysqlschema.ColumnSettingsKind)
	switch typed := value.(type) {
	case nil:
		return facets
	case *mysqlschema.DesiredColumnSettings:
		copy := *typed
		copy.Charset = ""
		updated, err := facets.Replace(&copy)
		if err != nil {
			panic(err)
		}
		return updated
	case *mysqlschema.ObservedColumnSettings:
		copy := *typed
		copy.Charset = ""
		updated, err := facets.Replace(&copy)
		if err != nil {
			panic(err)
		}
		return updated
	default:
		panic(fmt.Sprintf("unexpected MySQL column facet %T", value))
	}
}

func mysqlIndexParser(facets schemaext.Facets) string {
	switch value := facetValue(facets, mysqlschema.IndexKind).(type) {
	case nil:
		return ""
	case *mysqlschema.DesiredIndex:
		return value.Parser
	case *mysqlschema.ObservedIndex:
		return value.Parser
	default:
		panic(fmt.Sprintf("unexpected MySQL index facet %T", value))
	}
}

func sqliteTableOptions(facets schemaext.Facets) sqlitetable.Options {
	switch value := facetValue(facets, sqlitetable.TableKind).(type) {
	case nil:
		return sqlitetable.Options{}
	case *sqlitetable.DesiredTable:
		return value.Options
	case *sqlitetable.ObservedTable:
		return value.Options
	default:
		panic(fmt.Sprintf("unexpected SQLite table facet %T", value))
	}
}

func clickhouseIndexGranularity(facets schemaext.Facets) uint64 {
	switch value := facetValue(facets, chschema.IndexKind).(type) {
	case nil:
		return 0
	case *chschema.DesiredIndex:
		return value.Granularity.Value
	case *chschema.ObservedIndex:
		return value.Granularity
	default:
		panic(fmt.Sprintf("unexpected ClickHouse index facet %T", value))
	}
}

func txtarPartition(table *ast.CreateTableNode) *pgtable.Partition {
	switch value := facetValue(table.Facets, pgtable.PartitionKind).(type) {
	case nil:
		return nil
	case *pgtable.DesiredPartition:
		return &pgtable.Partition{Type: value.Type, Parts: value.Parts}
	case *pgtable.ObservedPartition:
		return (*pgtable.Partition)(value)
	default:
		panic(fmt.Sprintf("unexpected PostgreSQL partition facet %T", value))
	}
}
