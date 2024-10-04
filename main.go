package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"slices"
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
	arr     []int8
}

func NewD2(n uint) D2 {
	return D2{n, make([]int8, n*n)}
}

func (d *D2) At(i, j uint) int8 {
	return d.arr[i*d.rowSize+j]
}

func (d *D2) Set(i, j uint, val int8) {
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

type dijkstraNode struct {
	path             []uint32
	current, atLeast uint8
}

type visitedNode struct {
	id    uint32
	depth uint8
}

// negative lengthes indicate it will take at least that many jumps (positive) to reach the target
// positive lengths indicate it will take this many jumps to reach the target
func writeDijkstraToMatrix(edges map[uint32][]uint32, distances D2, nodeMap map[uint32]uint, start, target uint32) {
	seen := make(map[uint32]struct{})
	var visited []visitedNode
	queue := []dijkstraNode{{path: []uint32{start}}}
	targetIndex := nodeMap[target]
	bestDirectToTarget := ^uint8(0)
	var length uint8

	for len(queue) > 0 {
		node := queue[0]

		if node.atLeast >= bestDirectToTarget {
			length = bestDirectToTarget
			goto write
		}

		nodeId := node.path[len(node.path)-1]
		if nodeId == target {
			length = node.current
			goto write
		}

		queue = queue[1:]

		if _, ok := seen[nodeId]; ok {
			continue
		}
		seen[nodeId] = struct{}{}
		visited = append(visited, visitedNode{id: nodeId, depth: node.current})

		var pathNeedsCopy bool
		for _, next := range edges[nodeId] {
			dist := distances.At(nodeMap[next], targetIndex)
			var nextAtLeast uint8
			nextDist := node.current + 1
			if dist > 0 {
				// we exactly know how long it will be
				bestDirectToTarget = min(bestDirectToTarget, nextDist+uint8(dist))
				continue
			} else {
				// we know it will take at least this many jumps
				nextAtLeast = max(node.atLeast, nextDist, nextDist+uint8(-dist))
			}
			path := node.path
			if pathNeedsCopy {
				path = path[:len(path):len(path)]
			} else {
				pathNeedsCopy = true
			}
			queue = append(queue, dijkstraNode{
				path:    append(path, next),
				current: nextDist,
				atLeast: nextAtLeast,
			})
		}
		slices.SortFunc(queue, func(a, b dijkstraNode) int {
			if a.atLeast == b.atLeast {
				if a.current == b.current {
					return 0
				}
				if a.current < b.current {
					return -1
				}
				return 1
			}
			if a.atLeast < b.atLeast {
				return -1
			}
			return 1
		})
	}
	panic("path not found")

write:
	// update all the visited
	for _, v := range visited {
		cur := distances.At(nodeMap[v.id], targetIndex)
		if cur > 0 {
			// we already know the exact length from that one
			continue
		}
		newAtLeast := -int8(length - v.depth) // assume from this node it would have been perfect path to the end
		if cur <= newAtLeast {
			// we already know a longer path
			continue
		}
		distances.Set(nodeMap[v.id], targetIndex, newAtLeast)
	}
	// update all the in path
	node := queue[0]
	for i, n := range node.path {
		distances.Set(nodeMap[n], targetIndex, int8(length)-int8(i))
	}
}

func abs(x int8) int8 {
	if x < 0 {
		return -x
	}
	return x
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
	total := uint(len(distances.arr))
	done := uint(0)
	for i := range uint(len(reachableList)) {
		for j := range uint(len(reachableList)) {
			done++
			if done%512 == 0 {
				fmt.Printf("%.2f%% %d\n", float64(done)/float64(total)*100, done)
			}
			if i == j {
				continue
			}
			if distances.At(i, j) != 0 {
				continue
			}

			writeDijkstraToMatrix(edges, distances, nodeMap, reachableList[i], reachableList[j])
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
