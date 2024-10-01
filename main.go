package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	if err := run(); err != nil {
		os.Stderr.WriteString(err.Error())
		os.Exit(1)
	}
}

const JitaID = 30000142

type graph struct {
	Nodes map[uint32]struct {
		Name     string  `json:"name"`
		Security float64 `json:"security"`
		Region   string  `json:"region"`
	} `json:"nodes"`
	Edges []lk `json:"edges"`
}

func loadGraph() (graph, error) {
	f, err := os.Open("eve-map.json")
	if err != nil {
		return graph{}, err
	}
	defer f.Close()

	var g graph
	if err := json.NewDecoder(f).Decode(&g); err != nil {
		return graph{}, err
	}

	return g, nil
}

type lk struct {
	From uint32 `json:"from"`
	To   uint32 `json:"to"`
}

func markAllReachables(reachables map[uint32]struct{}, edges map[uint32][]uint32, node uint32) {
	if _, ok := reachables[node]; ok {
		return
	}
	reachables[node] = struct{}{}

	for _, next := range edges[node] {
		markAllReachables(reachables, edges, next)
	}
}

func run() error {
	g, err := loadGraph()
	if err != nil {
		return fmt.Errorf("failed to load graph: %w", err)
	}

	var edges = make(map[uint32][]uint32)
	for _, edge := range g.Edges {
		edges[edge.From] = append(edges[edge.From], edge.To)
	}
	var reachAbleNodes = make(map[uint32]struct{})
	markAllReachables(reachAbleNodes, edges, JitaID)

	file, err := os.Create("graph.tsp")
	if err != nil {
		return fmt.Errorf("failed to create TSP file: %w", err)
	}
	defer file.Close()

	fmt.Fprintln(file, "NAME: graph")
	fmt.Fprintln(file, "TYPE: TSP")
	fmt.Fprintln(file, "EDGE_WEIGHT_TYPE: EXPLICIT")
	fmt.Fprintln(file, "EDGE_WEIGHT_FORMAT: EDGE_LIST")
	fmt.Fprintf(file, "DIMENSION: %d\n", len(g.Nodes))
	fmt.Fprintln(file, "EDGE_DATA_FORMAT: EDGE_LIST")
	fmt.Fprintln(file, "EDGE_DATA_SECTION")

	for _, edge := range g.Edges {
		if _, ok := reachAbleNodes[edge.From]; !ok {
			continue
		}
		fmt.Fprintf(file, "%d %d 1\n", edge.From, edge.To)
	}

	fmt.Fprintln(file, "-1")

	fmt.Println("TSP file created successfully!")

	return nil
}
