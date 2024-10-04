package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const JitaID = 30000142

type graph struct {
	Nodes map[uint32]struct {
		Name     string  `json:"name"`
		Security float64 `json:"security"`
		Region   string  `json:"region"`
	} `json:"nodes"`
	Edges []lk `json:"edges"`
}

type lk struct {
	From uint32 `json:"from"`
	To   uint32 `json:"to"`
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

func markAllReachables(reachables map[uint32]struct{}, edges map[uint32][]uint32, node uint32) {
	if _, ok := reachables[node]; ok {
		return
	}
	reachables[node] = struct{}{}

	for _, next := range edges[node] {
		markAllReachables(reachables, edges, next)
	}
}

type D2 struct {
	rowSize uint
	arr     []uint8
}

func NewD2(n uint) D2 {
	return D2{n, make([]uint8, n*n)}
}

func (d *D2) At(i, j uint) uint8 {
	return d.arr[i*d.rowSize+j]
}

func (d *D2) Set(i, j uint, val uint8) {
	d.arr[i*d.rowSize+j] = val
}

func (d *D2) String() string {
	var s strings.Builder
	var recycled []byte
	for i := uint(0); i < uint(d.rowSize); i++ {
		for j := uint(0); j < uint(d.rowSize); j++ {
			recycled = strconv.AppendUint(recycled[:0], uint64(d.At(i, j)), 10)
			s.Write(recycled)
			s.WriteByte('\t')
		}
		s.WriteByte('\n')
	}
	return s.String()
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

	var reachableNodes = make(map[uint32]struct{})
	markAllReachables(reachableNodes, edges, JitaID)

	reachableList := make([]uint32, 0, len(reachableNodes))
	nodeMap := make(map[uint32]uint) // Map from node ID to index in the matrix
	for node := range reachableNodes {
		nodeMap[node] = uint(len(reachableList))
		reachableList = append(reachableList, node)
	}

	distances := NewD2(uint(len(reachableList)))
	// Default to max
	for i := range distances.arr {
		distances.arr[i] = ^uint8(0)
	}
	// Setup the diagonal
	for i := range uint(len(reachableList)) {
		distances.Set(i, i, 0)
	}
	// Setup the edges
	for from, tos := range edges {
		fromIndex := nodeMap[from]
		for _, to := range tos {
			distances.Set(fromIndex, nodeMap[to], 1)
		}
	}
	// Run Floyd-Warshall
	oneRow := uint(len(reachableList))
	total := oneRow * oneRow * oneRow
	var done uint
	for k := range oneRow {
		for i := range oneRow {
			for j := range oneRow {
				done++
				if done%(1<<32) == 0 {
					fmt.Printf("Progress: %d/%d %.2f%%\n", done, total, float64(done)/float64(total)*100)
				}

				new := uint(distances.At(i, k)) + uint(distances.At(k, j))
				if new >= uint(^uint8(0)) {
					continue
				}
				distances.Set(i, j, min(distances.At(i, j), uint8(new)))
			}
		}
	}

	os.Stdout.WriteString(distances.String())
	return nil

	// Output the TSP file in FULL_MATRIX format
	file, err := os.Create("graph.tsp")
	if err != nil {
		return fmt.Errorf("failed to create TSP file: %w", err)
	}
	defer file.Close()

	w := bufio.NewWriterSize(file, 1024*1024*8)

	fmt.Fprintln(w, "NAME: graph")
	fmt.Fprintln(w, "TYPE: TSP")
	fmt.Fprintln(w, "EDGE_WEIGHT_TYPE: EXPLICIT")
	fmt.Fprintln(w, "EDGE_WEIGHT_FORMAT: FULL_MATRIX")
	fmt.Fprintf(w, "DIMENSION: %d\n", len(reachableList))
	fmt.Fprintln(w, "EDGE_WEIGHT_SECTION")

	// Output the full matrix
	var recycled []byte
	for i := range 1 {
		for j := range i {
			if j > 0 {
				err := w.WriteByte(' ')
				if err != nil {
					return fmt.Errorf("failed to write to TSP file: %w", err)
				}
			}
			//recycled = strconv.AppendUint(recycled[:0], uint64(distances[i][j]), 10)
			_, err := w.Write(recycled)
			if err != nil {
				return fmt.Errorf("failed to write to TSP file: %w", err)
			}
		}
		err := w.WriteByte('\n')
		if err != nil {
			return fmt.Errorf("failed to write to TSP file: %w", err)
		}
	}

	err = w.Flush()
	if err != nil {
		return fmt.Errorf("failed to flush TSP file: %w", err)
	}

	fmt.Println("TSP file created successfully!")

	return nil
}

func main() {
	if err := run(); err != nil {
		os.Stderr.WriteString(err.Error())
		os.Exit(1)
	}
}
