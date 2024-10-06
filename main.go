package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

func run() error {
	var onlySearchThatRegion string
	flag.StringVar(&onlySearchThatRegion, "region", "", "Only search for systems in this region. Note, the path finder will still route through other regions if it's faster.")
	var onlyHighsec bool
	flag.BoolVar(&onlyHighsec, "highsec", false, "Only search for systems in highsec.")
	var gtsp bool
	flag.BoolVar(&gtsp, "gtsp", false, "Used colored TSP algorithm, clustering by region.")
	var biggestCut bool
	flag.BoolVar(&biggestCut, "biggest-cut", false, "Define the start position by the one giving the biggest cut.")
	flag.Parse()

	g, err := loadOrCreateMap(onlyHighsec)
	if err != nil {
		return fmt.Errorf("failed to load graph: %w", err)
	}

	err = loadSolution(g, biggestCut)
	if err == nil {
		return nil
	}
	fmt.Println("failed to load solution: ", err)
	fmt.Println("Generating new TSP file...")

	visited, err := parseAlreadyVisitedSystems(g)
	if err != nil {
		return fmt.Errorf("failed to parse already visited systems: %w", err)
	}

	// Now that we have the full matrix, remove all the systems we don't care about.
	var neededInComputeMatrix []uint32
	var gtspBuckets [][]uint
	var gtspSystemsToMatrix map[string]uint
	if gtsp {
		gtspSystemsToMatrix = make(map[string]uint)
	}
	for _, v := range g.MatrixIndexesToIds {
		if _, ok := visited[v]; ok {
			continue
		}
		if onlySearchThatRegion != "" {
			if g.Nodes[v].Region != onlySearchThatRegion {
				continue
			}
		}
		matrixId := uint(len(neededInComputeMatrix))
		neededInComputeMatrix = append(neededInComputeMatrix, v)
		if gtsp {
			region := g.Nodes[v].Region
			regionIndex, ok := gtspSystemsToMatrix[region]
			if !ok {
				regionIndex = uint(len(gtspBuckets))
				gtspSystemsToMatrix[region] = regionIndex
				gtspBuckets = append(gtspBuckets, nil)
			}
			gtspBuckets[regionIndex] = append(gtspBuckets[regionIndex], matrixId)
		}
	}

	computeMatrixIdToDistanceId := make([]uint, len(neededInComputeMatrix))
	for i, v := range neededInComputeMatrix {
		computeMatrixIdToDistanceId[i] = g.IdsToMatrixIndexes[v]
	}

	compute := NewD2(uint(len(neededInComputeMatrix)))
	for i := range uint(len(neededInComputeMatrix)) {
		for j := range uint(len(neededInComputeMatrix)) {
			compute.Set(i, j, g.Matrix.At(computeMatrixIdToDistanceId[i], computeMatrixIdToDistanceId[j]))
		}
	}

	err = outputTspFile(compute, gtspBuckets)
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

func outputTspFile(distances D2, gtspBuckets [][]uint) error {
	file, err := os.Create("graph.tsp")
	if err != nil {
		return fmt.Errorf("creating: %w", err)
	}
	defer file.Close()

	w := bufio.NewWriterSize(file, 1024*1024*32)

	typ := "TSP"
	if gtspBuckets != nil {
		typ = "GTSP"
	}
	_, err = fmt.Fprintf(w, `NAME: graph
TYPE: %s
GTSP_SETS: %d
EDGE_WEIGHT_TYPE: EXPLICIT
EDGE_WEIGHT_FORMAT: FULL_MATRIX
DIMENSION: %d
EDGE_WEIGHT_SECTION
`, typ, len(gtspBuckets), distances.RowSize)
	if err != nil {
		return fmt.Errorf("writing: %w", err)
	}

	// Output the full matrix
	var recycled []byte
	for i := range distances.RowSize {
		for j := range distances.RowSize {
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
	if gtspBuckets != nil {
		// Output the GTSP buckets
		_, err = w.WriteString("GTSP_SET_SECTION\n")
		if err != nil {
			return fmt.Errorf("writing: %w", err)
		}
		for i, bucket := range gtspBuckets {
			recycled = strconv.AppendUint(recycled[:0], uint64(i+1), 10) // LKH is one-indexed
			_, err := w.Write(recycled)
			if err != nil {
				return fmt.Errorf("writing: %w", err)
			}
			for _, v := range bucket {
				recycled = append(recycled[:0], ' ')
				recycled = strconv.AppendUint(recycled, uint64(v+1), 10) // LKH is one-indexed
				_, err := w.Write(recycled)
				if err != nil {
					return fmt.Errorf("writing: %w", err)
				}
			}
			_, err = w.WriteString(" -1\n")
			if err != nil {
				return fmt.Errorf("writing: %w", err)
			}
		}
	}

	_, err = w.WriteString("EOF\n")
	if err != nil {
		return fmt.Errorf("writing: %w", err)
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
func parseAlreadyVisitedSystems(g graph) (map[uint32]struct{}, error) {
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

	for _, logName := range flag.Args() {
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
				if _, ok := g.Reachable[from]; ok {
					visited[from] = struct{}{}
				}

				to, ok := nameToID[string(matches[2])]
				if !ok {
					return fmt.Errorf("unknown system: %s", matches[2])
				}
				if _, ok := g.Reachable[to]; ok {
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

func loadSolution(g graph, biggestCut bool) error {
	f, err := os.Open("output.tour")
	if err != nil {
		return fmt.Errorf("opening solution file: %w", err)
	}
	defer f.Close()

	var solution []uint
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
		solution = append(solution, uint(matrixIndex-1)) // LKH is one-indexed
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

	if biggestCut {
		var bestCut uint8
		var bestCutIndex int
		for i, matrixIndex := range solution {
			cut := g.Matrix.At(g.IdsToMatrixIndexes[matrixToSystemIds[matrixIndex]], g.IdsToMatrixIndexes[matrixToSystemIds[solution[(i+1)%len(solution)]]])
			if cut > bestCut {
				bestCut = cut
				bestCutIndex = (i + 1) % len(solution)
			}
		}
		solution = append(solution[bestCutIndex:], solution[:bestCutIndex]...)
	}

	output, err := os.Create("output.txt")
	if err != nil {
		return fmt.Errorf("creating output file: %w", err)
	}
	defer output.Close()

	w := bufio.NewWriterSize(output, 1024*1024*32)
	var solutionAsSystemIds []uint32
	for _, matrixIndex := range solution {
		systemID := matrixToSystemIds[matrixIndex]
		solutionAsSystemIds = append(solutionAsSystemIds, systemID)
		w.WriteString(g.Nodes[systemID].Name)
		w.WriteByte('\n')
	}
	err = w.Flush()
	if err != nil {
		return fmt.Errorf("flushing: %w", err)
	}

	fmt.Println("output.txt created successfully!")

	err = addWaypoints(g, solutionAsSystemIds, !biggestCut)
	if err != nil {
		return fmt.Errorf("adding waypoints: %w", err)
	}

	return nil
}
