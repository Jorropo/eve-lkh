package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

func main() {
	if err := run(); err != nil {
		os.Stderr.WriteString(err.Error())
		os.Exit(1)
	}
}

const baseUrl = "https://esi.evetech.net"

var client = http.Client{
	Transport: http.DefaultTransport,
}

func init() {
	// disable HTTP2
	tpt := client.Transport.(*http.Transport).Clone()

	tpt.ForceAttemptHTTP2 = false
	tpt.TLSClientConfig = &tls.Config{
		NextProtos: []string{"http/1.1"},
	}
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

var systemsIdToNames = make(map[uint32]string)
var systemIdToDestinations = make(map[uint32]map[uint32]struct{})

func run() error {
	systems, err := getSystems()
	if err != nil {
		return fmt.Errorf("failed to fetch systems: %w", err)
	}

	for i, system := range systems {
		req, err := http.NewRequest("GET", baseUrl+"/v4/universe/systems/"+strconv.FormatUint(uint64(system), 10)+"/", nil)
		if err != nil {
			return fmt.Errorf("failed to create request for system %d: %w", system, err)
		}

		var sys systemT
		for {
			resp, err := client.Do(req)
			if err != nil {
				return fmt.Errorf("failed to fetch system %d: %w", system, err)
			}

			if resp.StatusCode != http.StatusOK {
				log.Println("got error code", resp.StatusCode, "for system", system)
				resp.Body.Close()
				time.Sleep(5 * time.Second)
				continue
			}

			if err := json.NewDecoder(resp.Body).Decode(&sys); err != nil {
				resp.Body.Close()
				return fmt.Errorf("failed to decode system %d: %w", system, err)
			}
			resp.Body.Close()
			break
		}

		systemsIdToNames[system] = sys.Name

		if len(sys.Stargates) == 0 {
			continue
		}

		destinationMap := make(map[uint32]struct{})

		for _, stargate := range sys.Stargates {
			req, err := http.NewRequest("GET", baseUrl+"/v1/universe/stargates/"+strconv.FormatUint(uint64(stargate), 10)+"/", nil)
			if err != nil {
				return fmt.Errorf("failed to create request for stargate %d: %w", stargate, err)
			}

			var gate stargateT
			for {
				resp, err := client.Do(req)
				if err != nil {
					return fmt.Errorf("failed to fetch stargate %d: %w", stargate, err)
				}

				if resp.StatusCode != http.StatusOK {
					log.Println(fmt.Sprintf("expected 200 status got %d for stargate %d", resp.StatusCode, stargate))
					resp.Body.Close()
					time.Sleep(5 * time.Second)
					continue
				}

				if err := json.NewDecoder(resp.Body).Decode(&gate); err != nil {
					resp.Body.Close()
					return fmt.Errorf("failed to decode stargate %d: %w", stargate, err)
				}
				resp.Body.Close()
				break
			}

			destinationMap[gate.Destination.SystemID] = struct{}{}
		}

		systemIdToDestinations[system] = destinationMap
		log.Println(i+1, "of", len(systems))
	}

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
