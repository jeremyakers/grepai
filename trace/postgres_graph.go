package trace

import (
	"context"
	"fmt"
)

func (s *PostgresSymbolStore) GetCallGraph(ctx context.Context, symbolName string, depth int) (*CallGraph, error) {
	graph := &CallGraph{Root: symbolName, Nodes: map[string]Symbol{}, Edges: []CallEdge{}, Depth: depth}
	visited, edgeSeen := map[string]bool{}, map[string]bool{}
	frontier := []string{symbolName}
	// Querying once per breadth level keeps the implementation simple. A future
	// optimization may replace this loop with a recursive CTE.
	for level := 0; level <= depth && len(frontier) > 0; level++ {
		for _, name := range frontier {
			if visited[name] {
				continue
			}
			visited[name] = true
			syms, err := s.LookupSymbol(ctx, name)
			if err != nil {
				return nil, err
			}
			if len(syms) > 0 {
				graph.Nodes[name] = syms[0]
			}
		}

		rows, err := s.pool.Query(ctx, `SELECT caller,callee,file,line,call_type FROM call_edges WHERE project_id=$1 AND (caller=ANY($2::text[]) OR ($3=0 AND callee=$4))`, s.projectID, frontier, level, symbolName)
		if err != nil {
			return nil, fmt.Errorf("failed to query call graph: %w", err)
		}
		edges := []CallEdge{}
		for rows.Next() {
			var edge CallEdge
			if err := rows.Scan(&edge.Caller, &edge.Callee, &edge.File, &edge.Line, &edge.CallType); err != nil {
				rows.Close()
				return nil, err
			}
			edges = append(edges, edge)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()

		next := []string{}
		for _, edge := range edges {
			if edge.Caller == edge.Callee {
				var declaration bool
				if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM symbols WHERE project_id=$1 AND name=$2 AND file=$3 AND line=$4)`, s.projectID, edge.Caller, edge.File, edge.Line).Scan(&declaration); err != nil {
					return nil, err
				}
				if declaration {
					continue
				}
			}
			key := edge.Caller + "->" + edge.Callee
			if !edgeSeen[key] {
				graph.Edges = append(graph.Edges, edge)
				edgeSeen[key] = true
			}
			if !visited[edge.Callee] && level < depth {
				calleeSymbols, err := s.LookupSymbol(ctx, edge.Callee)
				if err != nil {
					return nil, err
				}
				if len(calleeSymbols) == 1 {
					next = append(next, edge.Callee)
				}
			}
			if level == 0 && edge.Callee == symbolName {
				callerSymbols, err := s.LookupSymbol(ctx, edge.Caller)
				if err != nil {
					return nil, err
				}
				if len(callerSymbols) > 0 {
					graph.Nodes[edge.Caller] = callerSymbols[0]
				}
			}
		}
		frontier = next
	}
	return graph, nil
}
