package trace

import (
	"context"
	"fmt"
)

type calleeSite struct {
	name string
	file string
	line int
}

const calleeEdgesSQL = `SELECT caller,callee,file,line,call_type FROM call_edges WHERE project_id=$1 AND caller=$2 ORDER BY file,line,ordinal,callee`
const calleeRefsSQL = `SELECT ` + refColumns + ` FROM refs WHERE project_id=$1 AND caller=$2 AND (ref_type=$3 OR ref_type='') ORDER BY file,line,ordinal,symbol_name`

func (s *PostgresSymbolStore) LookupCallees(ctx context.Context, symbolName, _ string) ([]Reference, error) {
	edges, err := s.calleeEdges(ctx, symbolName)
	if err != nil {
		return nil, err
	}
	refs, err := s.callRefsByCaller(ctx, symbolName)
	if err != nil {
		return nil, err
	}
	refsBySite := make(map[calleeSite][]Reference)
	for _, ref := range refs {
		key := calleeSite{name: ref.SymbolName, file: ref.File, line: ref.Line}
		refsBySite[key] = append(refsBySite[key], ref)
	}

	result := []Reference{}
	seen := make(map[calleeSite]bool)
	for _, edge := range edges {
		key := calleeSite{file: edge.File, line: edge.Line}
		if seen[key] {
			continue
		}
		seen[key] = true
		refKey := calleeSite{name: edge.Callee, file: edge.File, line: edge.Line}
		if matches := refsBySite[refKey]; len(matches) > 0 {
			result = append(result, matches[0])
			continue
		}
		// Preserve GOBSymbolStore's existing fallback behavior exactly.
		if len(result) == 0 {
			result = append(result, Reference{SymbolName: edge.Callee, File: edge.File, Line: edge.Line, CallerName: symbolName})
		}
	}
	return result, nil
}

func (s *PostgresSymbolStore) calleeEdges(ctx context.Context, symbolName string) ([]CallEdge, error) {
	rows, err := s.pool.Query(ctx, calleeEdgesSQL, identityBytes(s.projectID), identityBytes(symbolName))
	if err != nil {
		return nil, fmt.Errorf("failed to lookup callee edges: %w", err)
	}
	defer rows.Close()
	edges := []CallEdge{}
	for rows.Next() {
		var edge CallEdge
		var caller, callee, file []byte
		if err := rows.Scan(&caller, &callee, &file, &edge.Line, &edge.CallType); err != nil {
			return nil, err
		}
		edge.Caller, edge.Callee, edge.File = string(caller), string(callee), string(file)
		edges = append(edges, edge)
	}
	return edges, rows.Err()
}

func (s *PostgresSymbolStore) callRefsByCaller(ctx context.Context, symbolName string) ([]Reference, error) {
	rows, err := s.pool.Query(ctx, calleeRefsSQL, identityBytes(s.projectID), identityBytes(symbolName), RefKindCall)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup callee references: %w", err)
	}
	defer rows.Close()
	return scanRefs(rows)
}
