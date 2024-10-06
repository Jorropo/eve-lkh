package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

func run() error {
	var onlySearchThatRegion string
	flag.StringVar(&onlySearchThatRegion, "region", "", "Only search for systems in this region. Note, the path finder will still route through other regions if it's faster.")
	var doNotSearchThatRegion string
	flag.StringVar(&doNotSearchThatRegion, "skip-region", "", "Region to from search. Note, the path finder will still route through this region if it's faster.")
	var onlyHighsec bool
	flag.BoolVar(&onlyHighsec, "highsec", false, "Only search for systems in highsec.")
	var gtsp bool
	flag.BoolVar(&gtsp, "gtsp", false, "Used colored TSP algorithm, clustering by region.")
	var rotateToClosestSystem bool
	flag.BoolVar(&rotateToClosestSystem, "rotate-closest", false, "Rotate the solution to the closest system you are at.")
	var onlyWithStations bool
	flag.BoolVar(&onlyWithStations, "stations", false, "Only search for systems with stations.")
	flag.Parse()

	g, err := loadOrCreateMap(onlyHighsec)
	if err != nil {
		return fmt.Errorf("failed to load graph: %w", err)
	}

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
		system := g.Nodes[v]
		if onlySearchThatRegion != "" {
			if system.Region != onlySearchThatRegion {
				continue
			}
		}
		if doNotSearchThatRegion != "" {
			if system.Region == doNotSearchThatRegion {
				continue
			}
		}
		if onlyWithStations && len(system.Stations) == 0 {
			continue
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

	if gtsp {
		err = copyFile("graph.par", "GLKH/graph.par")
		if err != nil {
			return fmt.Errorf("failed to copy graph.par: %w", err)
		}

		err = outputGtspFile("GLKH/graph.tsp", compute, gtspBuckets)
		if err != nil {
			return fmt.Errorf("failed to output GTSP file: %w", err)
		}

		cmd := exec.Command("./GLKH", "graph.par")
		cmd.Dir = "GLKH"
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err = cmd.Run()
		if err != nil {
			return fmt.Errorf("running GLKH: %w", err)
		}

		systemsAsMatrixIds, err := loadSolution("GLKH/output.tour", false)
		if err != nil {
			return fmt.Errorf("failed to load GLKH solution: %w", err)
		}

		// make a new compute matrix with the results of GLKH for HPP to improve further
		neededInComputeMatrixNarrow := make([]uint32, len(systemsAsMatrixIds))
		computeNarrow := NewD2(uint(len(systemsAsMatrixIds)))
		for i, v := range systemsAsMatrixIds {
			neededInComputeMatrixNarrow[i] = neededInComputeMatrix[v]
			for j, v2 := range systemsAsMatrixIds {
				computeNarrow.Set(uint(i), uint(j), compute.At(v, v2))
			}
		}
		neededInComputeMatrix = neededInComputeMatrixNarrow
		compute = computeNarrow
	}

	err = copyFile("graph.par", "LKH/graph.par")
	if err != nil {
		return fmt.Errorf("failed to copy graph.par: %w", err)
	}

	err = outputSopFile("LKH/graph.tsp", compute)
	if err != nil {
		return fmt.Errorf("failed to output TSP file: %w", err)
	}

	cmd := exec.Command("./LKH", "graph.par")
	cmd.Dir = "LKH"
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("running LKH: %w", err)
	}

	systemsAsMatrixIds, err := loadSolution("LKH/output.tour", true)
	if err != nil {
		return fmt.Errorf("failed to load GLKH solution: %w", err)
	}

	var solutionAsIds []uint32
	for _, v := range systemsAsMatrixIds {
		solutionAsIds = append(solutionAsIds, neededInComputeMatrix[v])
	}

	err = writeOutput(g, solutionAsIds)
	if err != nil {
		return fmt.Errorf("failed to write output: %w", err)
	}

	err = addWaypoints(g, solutionAsIds, rotateToClosestSystem)
	if err != nil {
		return fmt.Errorf("failed to add waypoints to UI: %w", err)
	}

	return nil
}

func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening src file: %w", err)
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("creating dst file: %w", err)
	}
	defer dstFile.Close()

	_, err = io.CopyBuffer(dstFile, srcFile, make([]byte, 1024*1024*32))
	if err != nil {
		os.Remove(dst)
		return fmt.Errorf("copying: %w", err)
	}

	return nil
}

func outputGtspFile(filepath string, distances D2, gtspBuckets [][]uint) error {
	file, err := os.Create(filepath)
	if err != nil {
		return fmt.Errorf("creating: %w", err)
	}
	defer file.Close()

	w := bufio.NewWriterSize(file, 1024*1024*32)

	_, err = fmt.Fprintf(w, `TYPE: GTSP
GTSP_SETS: %d
EDGE_WEIGHT_TYPE: EXPLICIT
EDGE_WEIGHT_FORMAT: FULL_MATRIX
DIMENSION: %d
EDGE_WEIGHT_SECTION
`, len(gtspBuckets), distances.RowSize)
	if err != nil {
		return fmt.Errorf("writing: %w", err)
	}

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

	_, err = w.WriteString("EOF\n")
	if err != nil {
		return fmt.Errorf("writing: %w", err)
	}

	err = w.Flush()
	if err != nil {
		return fmt.Errorf("flushing: %w", err)
	}

	return nil
}

func outputSopFile(filepath string, distances D2) error {
	file, err := os.Create(filepath)
	if err != nil {
		return fmt.Errorf("creating: %w", err)
	}
	defer file.Close()

	w := bufio.NewWriterSize(file, 1024*1024*32)

	distanceWithFakeStartAndEnd := distances.RowSize + 2
	_, err = fmt.Fprintf(w, `TYPE: SOP
EDGE_WEIGHT_TYPE: EXPLICIT
EDGE_WEIGHT_FORMAT: FULL_MATRIX
DIMENSION: %d
EDGE_WEIGHT_SECTION
%d
`, distanceWithFakeStartAndEnd, distanceWithFakeStartAndEnd)
	if err != nil {
		return fmt.Errorf("writing: %w", err)
	}

	// Add a fake start and end node with identical distances to all other nodes.
	// Start can go everywhere except the end.
	for range distanceWithFakeStartAndEnd - 1 {
		_, err = w.WriteString("0 ")
		if err != nil {
			return fmt.Errorf("writing: %w", err)
		}
	}
	_, err = w.WriteString("-1\n")
	if err != nil {
		return fmt.Errorf("writing: %w", err)
	}

	var recycled []byte
	for i := range distances.RowSize {
		_, err = w.WriteString("-1 ")
		if err != nil {
			return fmt.Errorf("writing: %w", err)
		}
		for j := range distances.RowSize {
			recycled = strconv.AppendUint(recycled[:0], uint64(distances.At(i, j)), 10)
			recycled = append(recycled, ' ')
			_, err = w.Write(recycled)
			if err != nil {
				return fmt.Errorf("writing: %w", err)
			}
		}
		_, err = w.WriteString("0\n")
		if err != nil {
			return fmt.Errorf("writing: %w", err)
		}
	}

	// End can go nowhere.
	for range distanceWithFakeStartAndEnd - 1 {
		_, err = w.WriteString("-1 ")
		if err != nil {
			return fmt.Errorf("writing: %w", err)
		}
	}
	_, err = w.WriteString("0\n")
	if err != nil {
		return fmt.Errorf("writing: %w", err)
	}

	_, err = w.WriteString("EOF\n")
	if err != nil {
		return fmt.Errorf("writing: %w", err)
	}

	err = w.Flush()
	if err != nil {
		return fmt.Errorf("flushing: %w", err)
	}

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

func loadSolution(filepath string, isSop bool) (solution []uint, err error) {
	f, err := os.Open(filepath)
	if err != nil {
		return nil, fmt.Errorf("opening solution file: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(bufio.NewReaderSize(f, 1024*1024*32))
	for scanner.Scan() {
		if string(scanner.Bytes()) == "TOUR_SECTION" {
			goto parse
		}
	}
	return nil, fmt.Errorf("TOUR_SECTION not found")
parse:
	for scanner.Scan() {
		b := scanner.Bytes()
		if string(b) == "-1" {
			break
		}

		matrixIndex, err := strconv.ParseUint(string(b), 10, 32)
		if err != nil {
			return nil, fmt.Errorf("parsing matrix index: %w", err)
		}
		matrixIndex-- // LKH is one-indexed
		if isSop {
			matrixIndex-- // SOP has a fake start node
		}
		solution = append(solution, uint(matrixIndex))
	}

	if isSop {
		// Remove the fake start and end nodes.
		solution = solution[1 : len(solution)-1]
	}

	return solution, nil
}

func writeOutput(g graph, solution []uint32) error {
	output, err := os.Create("output.txt")
	if err != nil {
		return fmt.Errorf("creating output file: %w", err)
	}
	defer output.Close()

	w := bufio.NewWriterSize(output, 1024*1024*32)
	for _, systemID := range solution {
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
