package trace

import (
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5/pgtype"
)

var reservedSymbolTables = []string{
	"symbol_files", "symbols", "refs", "call_edges", "symbol_migrations", "symbol_store_meta",
}

type columnRule struct {
	name, defaultKind string
	typeOIDs          []uint32
	nullable          bool
}

func col(name string, typeOID uint32, defaultKind string, nullable bool) columnRule {
	return columnRule{name: name, typeOIDs: []uint32{typeOID}, defaultKind: defaultKind, nullable: nullable}
}

func identity(name, defaultKind string) columnRule {
	return columnRule{name: name, typeOIDs: []uint32{pgtype.ByteaOID, pgtype.TextOID}, defaultKind: defaultKind}
}

var symbolFilesShape = []columnRule{
	identity("project_id", ""), identity("path", ""), col("content_hash", pgtype.TextOID, "empty", false),
	col("extractor_version", pgtype.TextOID, "empty", false), col("mod_time", pgtype.TimestamptzOID, "", false),
}

var symbolsShape = []columnRule{
	identity("project_id", ""), identity("name", ""), identity("file", ""), col("line", pgtype.Int4OID, "", false),
	col("end_line", pgtype.Int4OID, "zero", false), col("kind", pgtype.TextOID, "", false), col("signature", pgtype.TextOID, "empty", false),
	col("receiver", pgtype.TextOID, "empty", false), col("package_name", pgtype.TextOID, "empty", false), col("exported", pgtype.BoolOID, "false", false),
	col("language", pgtype.TextOID, "empty", false), col("docstring", pgtype.TextOID, "empty", false), col("feature_path", pgtype.TextOID, "empty", false),
}

var refsLegacyShape = []columnRule{
	identity("project_id", ""), identity("symbol_name", ""), identity("file", ""), col("line", pgtype.Int4OID, "", false),
	col("col", pgtype.Int4OID, "zero", false), col("ref_type", pgtype.TextOID, "empty", false), col("context", pgtype.TextOID, "empty", false),
	identity("caller", "empty"), identity("caller_file", "empty"), col("caller_line", pgtype.Int4OID, "zero", false),
}

var refsCurrentShape = append(slices.Clone(refsLegacyShape), col("ordinal", pgtype.Int4OID, "zero", false))

var callEdgesLegacyShape = []columnRule{
	identity("project_id", ""), identity("caller", ""), identity("callee", ""), identity("file", ""),
	col("line", pgtype.Int4OID, "", false), col("call_type", pgtype.TextOID, "empty", false),
}

var callEdgesCurrentShape = append(slices.Clone(callEdgesLegacyShape), col("ordinal", pgtype.Int4OID, "zero", false))

var migrationsLegacyShape = []columnRule{
	identity("project_id", ""), col("state", pgtype.TextOID, "", false), identity("source_path", ""),
	col("started_at", pgtype.TimestamptzOID, "", false), col("completed_at", pgtype.TimestamptzOID, "", true),
}

var migrationsCurrentShape = []columnRule{
	identity("project_id", ""), col("state", pgtype.TextOID, "", false), identity("source_path", ""),
	col("source_digest", pgtype.ByteaOID, "", true), col("source_size", pgtype.Int8OID, "", true),
	col("started_at", pgtype.TimestamptzOID, "", false), col("completed_at", pgtype.TimestamptzOID, "", true),
}

var metaShape = []columnRule{col("key", pgtype.TextOID, "", false), col("value", pgtype.Int4OID, "", false)}

type symbolIndexSpec struct {
	name, table string
	columns     []string
}

var symbolIndexSpecs = []symbolIndexSpec{
	{"idx_symbols_project_name", "symbols", []string{"project_id", "name"}},
	{"idx_symbols_project_file", "symbols", []string{"project_id", "file"}},
	{"idx_refs_project_name", "refs", []string{"project_id", "symbol_name"}},
	{"idx_refs_project_file", "refs", []string{"project_id", "file"}},
	{"idx_refs_project_caller", "refs", []string{"project_id", "caller"}},
	{"idx_call_edges_project_caller", "call_edges", []string{"project_id", "caller"}},
	{"idx_call_edges_project_callee", "call_edges", []string{"project_id", "callee"}},
	{"idx_call_edges_project_file", "call_edges", []string{"project_id", "file"}},
}

func reservedSymbolIndexes() []string {
	names := make([]string, len(symbolIndexSpecs))
	for i := range symbolIndexSpecs {
		names[i] = symbolIndexSpecs[i].name
	}
	return names
}

func (inv symbolSchemaInventory) validateTables(currentOnly bool) error {
	shapes := map[string]struct {
		allowed [][]columnRule
		pk      []string
	}{
		"symbol_files":      {[][]columnRule{symbolFilesShape}, []string{"project_id", "path"}},
		"symbols":           {[][]columnRule{symbolsShape}, nil},
		"refs":              {[][]columnRule{refsCurrentShape, refsLegacyShape}, nil},
		"call_edges":        {[][]columnRule{callEdgesCurrentShape, callEdgesLegacyShape}, nil},
		"symbol_migrations": {[][]columnRule{migrationsCurrentShape, migrationsLegacyShape}, []string{"project_id"}},
		"symbol_store_meta": {[][]columnRule{metaShape}, []string{"key"}},
	}
	if currentOnly {
		shapes["refs"] = struct {
			allowed [][]columnRule
			pk      []string
		}{[][]columnRule{refsCurrentShape}, nil}
		shapes["call_edges"] = struct {
			allowed [][]columnRule
			pk      []string
		}{[][]columnRule{callEdgesCurrentShape}, nil}
		shapes["symbol_migrations"] = struct {
			allowed [][]columnRule
			pk      []string
		}{[][]columnRule{migrationsCurrentShape}, []string{"project_id"}}
	}
	for name, table := range inv.tables {
		spec := shapes[name]
		if err := validateTable(name, table, spec.allowed, spec.pk); err != nil {
			return err
		}
	}
	return inv.validateIndexes()
}

type symbolSchemaOwnership uint8

const (
	symbolSchemaFresh symbolSchemaOwnership = iota
	symbolSchemaOwned
	symbolSchemaLegacy
)

func classifySymbolSchema(inv symbolSchemaInventory, markerPresent bool) (symbolSchemaOwnership, error) {
	if len(inv.tables) == 0 {
		if len(inv.indexes) != 0 {
			return 0, fmt.Errorf("reserved symbol index exists without an owned symbol schema")
		}
		return symbolSchemaFresh, nil
	}
	if markerPresent {
		if len(inv.tables) != len(reservedSymbolTables) {
			return 0, fmt.Errorf("versioned symbol schema has an incomplete reserved table footprint")
		}
		if err := inv.validateTables(true); err != nil {
			return 0, err
		}
		return symbolSchemaOwned, nil
	}
	if err := inv.validateTables(false); err != nil {
		return 0, err
	}
	for _, name := range []string{"symbol_files", "symbols", "refs", "call_edges"} {
		if _, ok := inv.tables[name]; !ok {
			return 0, fmt.Errorf("markerless symbol schema footprint is incomplete")
		}
	}
	for _, spec := range symbolIndexSpecs {
		if spec.name == "idx_refs_project_caller" {
			continue
		}
		if _, ok := inv.indexes[spec.name]; !ok {
			return 0, fmt.Errorf("markerless symbol schema lacks authoritative historical index %q", spec.name)
		}
	}
	return symbolSchemaLegacy, nil
}
