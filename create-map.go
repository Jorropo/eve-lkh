package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const JitaID = 30000142

func markAllReachables(reachables map[uint32]struct{}, systems map[uint32]system, edges map[uint32][]uint32, node uint32, onlyHighsec bool) {
	if onlyHighsec {
		if systems[node].SecurityStatus < 0.5 {
			return
		}
	}

	if _, ok := reachables[node]; ok {
		return
	}
	reachables[node] = struct{}{}

	for _, next := range edges[node] {
		markAllReachables(reachables, systems, edges, next, onlyHighsec)
	}
}

type D2 struct {
	RowSize uint
	Arr     []uint8 // using uint8 for distances since the longest are in the <~100 range and this makes the full dataset fit in the L3 cache of my CPU.
}

func NewD2(n uint) D2 {
	return D2{n, make([]uint8, n*n)}
}

func (d *D2) At(i, j uint) uint8 {
	return d.Arr[i*d.RowSize+j]
}

func (d *D2) Set(i, j uint, val uint8) {
	d.Arr[i*d.RowSize+j] = val
}

func (d *D2) String() string {
	var s strings.Builder
	var recycled []byte
	for i := uint(0); i < uint(d.RowSize); i++ {
		for j := uint(0); j < uint(d.RowSize); j++ {
			recycled = strconv.AppendUint(recycled[:0], uint64(d.At(i, j)), 10)
			s.Write(recycled)
			s.WriteByte('\t')
		}
		s.WriteByte('\n')
	}
	return s.String()
}

type systemJson struct {
	Name            string   `json:"name"`
	Stargates       []uint32 `json:"stargates"`
	ConstellationID uint32   `json:"constellation_id"`
	Stations        []uint32 `json:"stations"`
	SecurityStatus  float32  `json:"security_status"`
}

type constellationJson struct {
	RegionID uint32 `json:"region_id"`
}

type regionJson struct {
	Name string `json:"name"`
}

type stargateJson struct {
	Destination struct {
		SystemID uint32 `json:"system_id"`
	} `json:"destination"`
}

type system struct {
	Name           string
	Region         string
	Stations       []uint32
	SecurityStatus float32
}

type graph struct {
	Nodes              map[uint32]system
	Edges              map[uint32][]uint32
	Reachable          map[uint32]struct{}
	MatrixIndexesToIds []uint32
	IdsToMatrixIndexes map[uint32]uint
	Matrix             D2
}

const graphFile = "graph.json"

func loadGraph(onlyHighsec bool) (graph, error) {
	fileName := graphFile
	if onlyHighsec {
		fileName = "highsec-" + graphFile
	}
	f, err := os.Open(fileName)
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

var client = http.Client{
	Timeout: 10 * time.Second,
}

func init() {
	rt := http.DefaultTransport.(*http.Transport).Clone()
	rt.ForceAttemptHTTP2 = false
	rt.TLSClientConfig.NextProtos = []string{"http/1.1"}
	rt.TLSNextProto = make(map[string]func(authority string, c *tls.Conn) http.RoundTripper)
	client.Transport = rt
}

const baseUrl = "https://esi.evetech.net"

func fetch(url string, v interface{}) error {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(10*time.Second))
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	r, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("fetching %s: %w", url, err)
	}
	defer r.Body.Close()

	if r.StatusCode != http.StatusOK {
		return fmt.Errorf("fetching %s: %s", url, r.Status)
	}

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return fmt.Errorf("decoding %s: %w", url, err)
	}

	return nil
}

func fetchSystems(onlyHighsec bool) (nodes map[uint32]system, edges map[uint32][]uint32, err error) {
	nodes = make(map[uint32]system)
	edges = make(map[uint32][]uint32)

	var systems []uint32
	err = fetch(baseUrl+"/v1/universe/systems/", &systems)
	if err != nil {
		return nil, nil, err
	}

	constellationsToRegion := make(map[uint32]uint32)
	regionsToName := make(map[uint32]string)

	for len(systems) > 0 {
		failedSystems := systems[:0]
		// Don't multithread this, the API reacts poorly to being spiked
		for i, id := range systems {
			fmt.Printf("fetching system: %d/%d %.2f%%\n", i, len(systems), float64(i)/float64(len(systems))*100)

			var s systemJson
			err := fetch(baseUrl+"/v4/universe/systems/"+strconv.FormatUint(uint64(id), 10)+"/", &s)
			if err != nil {
				fmt.Println("failed to fetch system, will retry later", id, err)
				failedSystems = append(failedSystems, id)
				continue
			}

			if onlyHighsec && s.SecurityStatus < 0.5 {
				continue
			}

			region, ok := constellationsToRegion[s.ConstellationID]
			if !ok {
				var c constellationJson
				err := fetch(baseUrl+"/v1/universe/constellations/"+strconv.FormatUint(uint64(s.ConstellationID), 10)+"/", &c)
				if err != nil {
					fmt.Println("failed to fetch constellation, will retry later", s.ConstellationID, err)
					failedSystems = append(failedSystems, id)
					continue
				}
				region = c.RegionID
				constellationsToRegion[s.ConstellationID] = c.RegionID
			}

			regionName, ok := regionsToName[region]
			if !ok {
				var r regionJson
				err := fetch(baseUrl+"/v1/universe/regions/"+strconv.FormatUint(uint64(region), 10)+"/", &r)
				if err != nil {
					fmt.Println("failed to fetch region, will retry later", region, err)
					failedSystems = append(failedSystems, id)
					continue
				}
				regionName = r.Name
				regionsToName[region] = r.Name
			}

			nodes[id] = system{
				Name:           s.Name,
				Region:         regionName,
				Stations:       s.Stations,
				SecurityStatus: s.SecurityStatus,
			}

			stargates := s.Stargates
			for len(stargates) > 0 {
				failedStargates := stargates[:0]
				for _, stargate := range stargates {
					var sg stargateJson
					err := fetch(baseUrl+"/v1/universe/stargates/"+strconv.FormatUint(uint64(stargate), 10)+"/", &sg)
					if err != nil {
						fmt.Println("failed to fetch stargate, will retry later", stargate, err)
						failedStargates = append(failedStargates, stargate)
						continue
					}
					edges[id] = append(edges[id], sg.Destination.SystemID)
				}
				stargates = failedStargates
			}
		}
		systems = failedSystems
	}

	return nodes, edges, nil
}

func loadOrCreateMap(onlyHighsec bool) (graph, error) {
	g, err := loadGraph(onlyHighsec)
	if err == nil {
		return g, nil
	}
	fmt.Println("failed to load graph, creating a new one")

	nodes, edges, err := fetchSystems(onlyHighsec)
	if err != nil {
		return graph{}, fmt.Errorf("fetching systems: %w", err)
	}

	reachableNodes := make(map[uint32]struct{})
	markAllReachables(reachableNodes, nodes, edges, JitaID, onlyHighsec)

	reachableList := make([]uint32, 0, len(reachableNodes))
	nodeMap := make(map[uint32]uint) // Map from node ID to index in the matrix
	for node := range reachableNodes {
		nodeMap[node] = uint(len(reachableList))
		reachableList = append(reachableList, node)
	}

	distances := NewD2(uint(len(reachableList)))
	// Default to max
	for i := range distances.Arr {
		distances.Arr[i] = ^uint8(0)
	}
	// Setup the diagonal
	for i := range uint(len(reachableList)) {
		distances.Set(i, i, 0)
	}
	// Setup the edges
	for from, tos := range edges {
		fromIndex := nodeMap[from]
		for _, to := range tos {
			toIndex, ok := nodeMap[to]
			if !ok {
				continue
			}

			distances.Set(fromIndex, toIndex, 1)
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
					fmt.Printf("O(n³) full matrix solver: %d/%d %.2f%%\n", done, total, float64(done)/float64(total)*100)
				}

				new := uint(distances.At(i, k)) + uint(distances.At(k, j))
				if new >= uint(^uint8(0)) {
					continue
				}
				distances.Set(i, j, min(distances.At(i, j), uint8(new)))
			}
		}
	}

	g = graph{
		Nodes:              nodes,
		Edges:              edges,
		IdsToMatrixIndexes: nodeMap,
		MatrixIndexesToIds: reachableList,
		Reachable:          reachableNodes,
		Matrix:             distances,
	}

	fileName := graphFile
	if onlyHighsec {
		fileName = "highsec-" + graphFile
	}
	f, err := os.Create(fileName)
	if err != nil {
		return graph{}, fmt.Errorf("creating graph file: %w", err)
	}
	defer f.Close()

	w := bufio.NewWriterSize(f, 1024*1024*32)
	err = json.NewEncoder(w).Encode(g)
	if err != nil {
		return graph{}, fmt.Errorf("encoding graph: %w", err)
	}

	err = w.Flush()
	if err != nil {
		return graph{}, fmt.Errorf("flushing: %w", err)
	}

	return g, nil
}
