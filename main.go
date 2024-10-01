package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

func main() {
	if err := run(); err != nil {
		os.Stderr.WriteString(err.Error())
		os.Exit(1)
	}
}

const baseUrl = "https://esi.evetech.net"

var throttle = make(chan struct{}, 16)
var wg sync.WaitGroup

var client = http.Client{
	Transport: http.DefaultTransport,
}

func init() {
	// disable HTTP2
	tpt := client.Transport.(*http.Transport).Clone()

	tpt.ForceAttemptHTTP2 = false
	tpt.TLSClientConfig.NextProtos = []string{"http/1.1"}
	tpt.TLSNextProto = make(map[string]func(authority string, c *tls.Conn) http.RoundTripper)
	tpt.TLSHandshakeTimeout = 10 * time.Second

	client.Transport = tpt
}

func getSystems() (systems []uint32, _ error) {
	req, err := http.NewRequest("GET", baseUrl+"/v1/universe/systems/", nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("expected 200 status got %d", resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(&systems); err != nil {
		return nil, err
	}

	return systems, nil
}

type systemT struct {
	Name      string   `json:"name"`
	Stargates []uint32 `json:"stargates"`
}

type stargateT struct {
	Destination struct {
		SystemID uint32 `json:"system_id"`
	} `json:"destination"`
}

var dataLock sync.Mutex
var systemsIdToNames = make(map[uint32]string)
var systemIdToDestinations = make(map[uint32]map[uint32]struct{})
var fetchedSystems uint

func run() error {
	systems, err := getSystems()
	if err != nil {
		return fmt.Errorf("failed to fetch systems: %w", err)
	}

	wg.Add(len(systems))
	for _, system := range systems {
		throttle <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-throttle }()

		trySystem:

			req, err := http.NewRequest("GET", baseUrl+"/v4/universe/systems/"+strconv.FormatUint(uint64(system), 10)+"/", nil)
			if err != nil {
				panic(err)
			}

			resp, err := client.Do(req)
			if err != nil {
				panic(err)
			}

			if resp.StatusCode != http.StatusOK {
				log.Println("got error code", resp.StatusCode, "for system", system)
				resp.Body.Close()
				time.Sleep(5 * time.Second)
				goto trySystem
			}

			var sys systemT
			if err := json.NewDecoder(resp.Body).Decode(&sys); err != nil {
				panic(err)
			}

			resp.Body.Close()
			<-throttle

			if len(sys.Stargates) == 0 {
				return
			}

			destinationMap := make(map[uint32]struct{})
			var destinationLock sync.Mutex

			wg.Add(len(sys.Stargates))
			for _, stargate := range sys.Stargates {
				throttle <- struct{}{}
				go func() {
					defer wg.Done()
					defer func() { <-throttle }()

				try:

					req, err := http.NewRequest("GET", baseUrl+"/v1/universe/stargates/"+strconv.FormatUint(uint64(stargate), 10)+"/", nil)
					if err != nil {
						panic(err)
					}

					resp, err := client.Do(req)
					if err != nil {
						panic(err)
					}

					if resp.StatusCode != http.StatusOK {
						resp.Body.Close()
						log.Println(fmt.Sprintf("expected 200 status got %d for stargate %d", resp.StatusCode, stargate))
						time.Sleep(5 * time.Second)
						goto try
					}

					var gate stargateT
					if err := json.NewDecoder(resp.Body).Decode(&gate); err != nil {
						panic(err)
					}

					resp.Body.Close()

					destinationLock.Lock()
					defer destinationLock.Unlock()
					destinationMap[gate.Destination.SystemID] = struct{}{}
				}()
			}

			dataLock.Lock()
			defer dataLock.Unlock()
			systemsIdToNames[system] = sys.Name
			systemIdToDestinations[system] = destinationMap
			fetchedSystems++
			log.Println(fetchedSystems)
		}()
	}

	wg.Wait()

	if err := json.NewEncoder(os.Stdout).Encode(struct {
		Systems map[uint32]string
		Links   map[uint32]map[uint32]struct{}
	}{
		Systems: systemsIdToNames,
		Links:   systemIdToDestinations,
	}); err != nil {
		return fmt.Errorf("failed to encode data: %w", err)
	}

	return nil
}
