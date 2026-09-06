package rpg

func cloneNode(node *Node) *Node {
	if node == nil {
		return nil
	}
	cloned := *node
	cloned.Features = append([]string(nil), node.Features...)
	return &cloned
}

func cloneEdge(edge *Edge) *Edge {
	if edge == nil {
		return nil
	}
	cloned := *edge
	return &cloned
}

func cloneNodes(nodes []*Node) []*Node {
	cloned := make([]*Node, len(nodes))
	for i, node := range nodes {
		cloned[i] = cloneNode(node)
	}
	return cloned
}

func cloneEdges(edges []*Edge) []*Edge {
	cloned := make([]*Edge, len(edges))
	for i, edge := range edges {
		cloned[i] = cloneEdge(edge)
	}
	return cloned
}
