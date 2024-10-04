package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
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

	err = loadSolution(g)
	if err == nil {
		return nil
	}
	fmt.Println("failed to load solution: ", err)
	fmt.Println("Generating new TSP file...")

	var edges = make(map[uint32][]uint32)

	for _, edge := range g.Edges {
		edges[edge.From] = append(edges[edge.From], edge.To)
	}

	var reachableNodes = make(map[uint32]struct{})
	markAllReachables(reachableNodes, edges, JitaID)

	visited, err := parseAlreadyVisitedSystems(reachableNodes, g)
	if err != nil {
		return fmt.Errorf("failed to parse already visited systems: %w", err)
	}

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

	// Now that we have the full matrix, remove all the systems we don't care about.
	neededInComputeMatrix := make([]uint32, 0, len(reachableList)-len(visited))
	for _, v := range reachableList {
		if _, ok := visited[v]; ok {
			continue
		}
		neededInComputeMatrix = append(neededInComputeMatrix, v)
	}

	computeMatrixIdToDistanceId := make([]uint, len(neededInComputeMatrix))
	for i, v := range neededInComputeMatrix {
		computeMatrixIdToDistanceId[i] = nodeMap[v]
	}

	compute := NewD2(uint(len(neededInComputeMatrix)))
	for i := range uint(len(neededInComputeMatrix)) {
		for j := range uint(len(neededInComputeMatrix)) {
			compute.Set(i, j, distances.At(computeMatrixIdToDistanceId[i], computeMatrixIdToDistanceId[j]))
		}
	}

	err = outputTspFile(compute)
	if err != nil {
		return fmt.Errorf("failed to output TSP file: %w", err)
	}

	err = outputMatrixToSystemIdsFile(neededInComputeMatrix)
	if err != nil {
		return fmt.Errorf("failed to output matrixToSystemIds file: %w", err)
	}

	return nil
}

func outputMatrixToSystemIdsFile(indexesToSystemIds []uint32) error {
	// Output the matrixToSystemIds file to convert from matrix index to system ID.
	file, err := os.Create("matrixToSystemIds.json")
	if err != nil {
		return fmt.Errorf("creating file: %w", err)
	}
	defer file.Close()

	w := bufio.NewWriterSize(file, 1024*1024*32)
	err = json.NewEncoder(w).Encode(indexesToSystemIds)
	if err != nil {
		return fmt.Errorf("encoding json: %w", err)
	}

	err = w.Flush()
	if err != nil {
		return fmt.Errorf("flushing: %w", err)
	}

	fmt.Println("matrixToSystemIds file created successfully!")
	return nil
}

func outputTspFile(distances D2) error {
	file, err := os.Create("graph.tsp")
	if err != nil {
		return fmt.Errorf("creating: %w", err)
	}
	defer file.Close()

	w := bufio.NewWriterSize(file, 1024*1024*32)

	fmt.Fprintln(w, "NAME: graph")
	fmt.Fprintln(w, "TYPE: TSP")
	fmt.Fprintln(w, "EDGE_WEIGHT_TYPE: EXPLICIT")
	fmt.Fprintln(w, "EDGE_WEIGHT_FORMAT: FULL_MATRIX")
	fmt.Fprintf(w, "DIMENSION: %d\n", distances.rowSize)
	fmt.Fprintln(w, "EDGE_WEIGHT_SECTION")

	// Output the full matrix
	var recycled []byte
	for i := range distances.rowSize {
		for j := range distances.rowSize {
			if j > 0 {
				err := w.WriteByte(' ')
				if err != nil {
					return fmt.Errorf("writing: %w", err)
				}
			}
			recycled = strconv.AppendUint(recycled[:0], uint64(distances.At(i, j)), 10)
			_, err := w.Write(recycled)
			if err != nil {
				return fmt.Errorf("writing: %w", err)
			}
		}
		err := w.WriteByte('\n')
		if err != nil {
			return fmt.Errorf("writing: %w", err)
		}
	}

	err = w.Flush()
	if err != nil {
		return fmt.Errorf("flushing: %w", err)
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

// parseAlreadyVisitedSystems only output systems in the reachable set.
func parseAlreadyVisitedSystems(reachable map[uint32]struct{}, g graph) (map[uint32]struct{}, error) {
	nameToID := make(map[string]uint32)
	var names []string
	for id, node := range g.Nodes {
		nameToID[node.Name] = id
		names = append(names, node.Name)
	}
	matchName := "(" + strings.Join(names, "|") + ")"
	r, err := regexp.Compile("Jumping from " + matchName + " to " + matchName)
	if err != nil {
		return nil, fmt.Errorf("compiling regexp: %w", err)
	}

	visited := make(map[uint32]struct{})

	for _, logName := range os.Args[1:] {
		fmt.Println("parsing:", logName)
		if err := func() error {
			f, err := os.Open(logName)
			if err != nil {
				return fmt.Errorf("opening log file: %w", err)
			}
			defer f.Close()

			scanner := bufio.NewScanner(bufio.NewReaderSize(f, 1024*1024*32))
			for scanner.Scan() {
				matches := r.FindSubmatch(scanner.Bytes())
				if matches == nil {
					continue
				}

				from, ok := nameToID[string(matches[1])]
				if !ok {
					return fmt.Errorf("unknown system: %s", matches[1])
				}
				if _, ok := reachable[from]; ok {
					visited[from] = struct{}{}
				}

				to, ok := nameToID[string(matches[2])]
				if !ok {
					return fmt.Errorf("unknown system: %s", matches[2])
				}
				if _, ok := reachable[to]; ok {
					visited[to] = struct{}{}
				}
			}
			return nil
		}(); err != nil {
			return nil, err
		}
	}

	return visited, nil
}

func loadSolution(g graph) error {
	f, err := os.Open("output.tour")
	if err != nil {
		return fmt.Errorf("opening solution file: %w", err)
	}
	defer f.Close()

	var solution []uint32
	scanner := bufio.NewScanner(bufio.NewReaderSize(f, 1024*1024*32))
	for scanner.Scan() {
		if string(scanner.Bytes()) == "TOUR_SECTION" {
			goto parse
		}
	}
	return fmt.Errorf("TOUR_SECTION not found")
parse:
	for scanner.Scan() {
		b := scanner.Bytes()
		if string(b) == "-1" {
			break
		}

		matrixIndex, err := strconv.ParseUint(string(b), 10, 32)
		if err != nil {
			return fmt.Errorf("parsing matrix index: %w", err)
		}
		solution = append(solution, uint32(matrixIndex-1)) // LKH is one-indexed
	}
	f.Close()

	// Load the matrixToSystemIds file
	f, err = os.Open("matrixToSystemIds.json")
	if err != nil {
		return fmt.Errorf("opening matrixToSystemIds file: %w", err)
	}
	defer f.Close()

	var matrixToSystemIds []uint32
	err = json.NewDecoder(f).Decode(&matrixToSystemIds)
	if err != nil {
		return fmt.Errorf("decoding matrixToSystemIds: %w", err)
	}

	output, err := os.Create("output.txt")
	if err != nil {
		return fmt.Errorf("creating output file: %w", err)
	}
	defer output.Close()

	w := bufio.NewWriterSize(output, 1024*1024*32)
	for _, matrixIndex := range solution {
		systemID := matrixToSystemIds[matrixIndex]
		w.WriteString(g.Nodes[systemID].Name)
		w.WriteByte('\n')
	}
	err = w.Flush()
	if err != nil {
		return fmt.Errorf("flushing: %w", err)
	}

	fmt.Println("output.txt created successfully!")

	return nil
}
