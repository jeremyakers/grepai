package trace

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

const symbolColumns = `name,kind,file,line,end_line,signature,receiver,package_name,exported,language,docstring,feature_path`
const refColumns = `symbol_name,ref_type,file,line,col,context,caller,caller_file,caller_line`

func scanSymbols(rows pgx.Rows) ([]Symbol, error) {
	result := []Symbol{}
	for rows.Next() {
		var sym Symbol
		if err := rows.Scan(&sym.Name, &sym.Kind, &sym.File, &sym.Line, &sym.EndLine, &sym.Signature, &sym.Receiver, &sym.Package, &sym.Exported, &sym.Language, &sym.Docstring, &sym.FeaturePath); err != nil {
			return nil, err
		}
		result = append(result, sym)
	}
	return result, rows.Err()
}

func scanRefs(rows pgx.Rows) ([]Reference, error) {
	result := []Reference{}
	for rows.Next() {
		var ref Reference
		if err := rows.Scan(&ref.SymbolName, &ref.Kind, &ref.File, &ref.Line, &ref.Column, &ref.Context, &ref.CallerName, &ref.CallerFile, &ref.CallerLine); err != nil {
			return nil, err
		}
		result = append(result, ref)
	}
	return result, rows.Err()
}

func (s *PostgresSymbolStore) LookupSymbol(ctx context.Context, name string) ([]Symbol, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+symbolColumns+` FROM symbols WHERE project_id=$1 AND name=$2 ORDER BY file,line`, s.projectID, name)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup symbol: %w", err)
	}
	defer rows.Close()
	return scanSymbols(rows)
}

func (s *PostgresSymbolStore) LookupSymbolsBatch(ctx context.Context, names []string) (map[string][]Symbol, error) {
	result := make(map[string][]Symbol)
	if len(names) == 0 {
		return result, nil
	}
	unique := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		unique = append(unique, name)
	}
	rows, err := s.pool.Query(ctx, `SELECT `+symbolColumns+` FROM symbols WHERE project_id=$1 AND name=ANY($2::text[]) ORDER BY name,file,line`, s.projectID, unique)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup symbols batch: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var sym Symbol
		if err := rows.Scan(&sym.Name, &sym.Kind, &sym.File, &sym.Line, &sym.EndLine, &sym.Signature, &sym.Receiver, &sym.Package, &sym.Exported, &sym.Language, &sym.Docstring, &sym.FeaturePath); err != nil {
			return nil, fmt.Errorf("failed to scan symbol batch: %w", err)
		}
		result[sym.Name] = append(result[sym.Name], sym)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read symbol batch: %w", err)
	}
	return result, nil
}

func (s *PostgresSymbolStore) lookupRefs(ctx context.Context, symbolName, kind string) ([]Reference, error) {
	query := `SELECT ` + refColumns + ` FROM refs WHERE project_id=$1 AND symbol_name=$2`
	args := []any{s.projectID, symbolName}
	if kind == RefKindCall {
		query += ` AND (ref_type=$3 OR ref_type='')`
		args = append(args, kind)
	} else {
		query += ` AND ref_type=$3`
		args = append(args, kind)
	}
	query += ` ORDER BY file,line`
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup references: %w", err)
	}
	defer rows.Close()
	return scanRefs(rows)
}

func (s *PostgresSymbolStore) LookupCallers(ctx context.Context, symbolName string) ([]Reference, error) {
	return s.lookupRefs(ctx, symbolName, RefKindCall)
}
func (s *PostgresSymbolStore) LookupReaders(ctx context.Context, symbolName string) ([]Reference, error) {
	return s.lookupRefs(ctx, symbolName, RefKindRead)
}
func (s *PostgresSymbolStore) LookupWriters(ctx context.Context, symbolName string) ([]Reference, error) {
	return s.lookupRefs(ctx, symbolName, RefKindWrite)
}

func (s *PostgresSymbolStore) LookupCallees(ctx context.Context, symbolName, _ string) ([]Reference, error) {
	query := `SELECT DISTINCT ` + refColumns + ` FROM refs WHERE project_id=$1 AND caller=$2 AND (ref_type=$3 OR ref_type='')`
	args := []any{s.projectID, symbolName, RefKindCall}
	query += ` ORDER BY file,line`
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup callees: %w", err)
	}
	defer rows.Close()
	return scanRefs(rows)
}

func (s *PostgresSymbolStore) GetSymbolsForFile(ctx context.Context, filePath string) ([]Symbol, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+symbolColumns+` FROM symbols WHERE project_id=$1 AND file=$2 ORDER BY line,name`, s.projectID, filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get symbols for file: %w", err)
	}
	defer rows.Close()
	return scanSymbols(rows)
}

func (s *PostgresSymbolStore) GetCallEdges(ctx context.Context) ([]CallEdge, error) {
	rows, err := s.pool.Query(ctx, `SELECT caller,callee,file,line,call_type FROM call_edges WHERE project_id=$1 ORDER BY file,line`, s.projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to get call edges: %w", err)
	}
	defer rows.Close()
	edges := []CallEdge{}
	for rows.Next() {
		var edge CallEdge
		if err := rows.Scan(&edge.Caller, &edge.Callee, &edge.File, &edge.Line, &edge.CallType); err != nil {
			return nil, err
		}
		edges = append(edges, edge)
	}
	return edges, rows.Err()
}

func (s *PostgresSymbolStore) GetStats(ctx context.Context) (*SymbolStats, error) {
	var stats SymbolStats
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM symbols WHERE project_id=$1`, s.projectID).Scan(&stats.TotalSymbols); err != nil {
		return nil, err
	}
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM refs WHERE project_id=$1`, s.projectID).Scan(&stats.TotalReferences); err != nil {
		return nil, err
	}
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*),COALESCE(MAX(mod_time),'1970-01-01'::timestamptz) FROM symbol_files WHERE project_id=$1`, s.projectID).Scan(&stats.TotalFiles, &stats.LastUpdated); err != nil {
		return nil, err
	}
	return &stats, nil
}
