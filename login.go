package main

import (
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const appId = "bac8e360dacc4dad85a1cc7173e78cb3"

var neededPerms = []string{
	"esi-ui.write_waypoint.v1",
	"esi-location.read_location.v1",
}

var exitPage = []byte(`<!doctypehtml><title>EVE-LKH</title><style>body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;display:flex;justify-content:center;align-items:center;height:100vh;margin:0;font-size:20px;color:#4a4a4a;background-color:#fafafa;line-height:1.6;letter-spacing:.2px}p{padding:20px;max-width:600px;text-align:center}</style><p>You can close this tab now.</p><script async>window.close()</script>`)

type ssoResponseJson struct {
	AccessToken string `json:"access_token"`
}

func grabUserToken() (authToken string, characterId uint32, err error) {
	listener, err := net.Listen("tcp", "localhost:13377")
	if err != nil {
		return "", 0, fmt.Errorf("listening: %w", err)
	}
	defer listener.Close()

	var state, codeVerifier [32]byte
	var rng [len(state) + len(codeVerifier)]byte
	_, err = io.ReadFull(crand.Reader, rng[:])
	if err != nil {
		return "", 0, fmt.Errorf("getting randomness: %w", err)
	}

	copy(state[:], rng[:len(state)])
	stateStr := base64.RawURLEncoding.EncodeToString(state[:])
	copy(codeVerifier[:], rng[len(state):])
	codeVerifierStr := base64.RawURLEncoding.EncodeToString(codeVerifier[:])
	challenge := sha256.Sum256([]byte(codeVerifierStr))
	challengeStr := base64.RawURLEncoding.EncodeToString(challenge[:])

	exitServer := make(chan struct{})
	token := make(chan string)
	go http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := r.URL.Query().Get("state")
		if state != stateStr {
			http.Error(w, "invalid state", http.StatusBadRequest)
			return
		}

		code := r.URL.Query().Get("code")
		resp, err := client.PostForm("https://login.eveonline.com/v2/oauth/token", url.Values{
			"grant_type":    {"authorization_code"},
			"client_id":     {appId},
			"code":          {code},
			"code_verifier": {codeVerifierStr},
		})
		if err != nil {
			http.Error(w, "error getting token", http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			w.WriteHeader(http.StatusInternalServerError)
			io.Copy(w, resp.Body)
			return
		}

		var ssoResp ssoResponseJson
		err = json.NewDecoder(resp.Body).Decode(&ssoResp)
		if err != nil {
			http.Error(w, "error decoding token", http.StatusInternalServerError)
			return
		}

		select {
		case <-exitServer:
		case token <- ssoResp.AccessToken:
		}

		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write(exitPage)
	}))

	// get user token
	userUrl := "https://login.eveonline.com/v2/oauth/authorize/?" +
		"response_type=code&" +
		"redirect_uri=" + url.QueryEscape("http://localhost:13377/") +
		"&client_id=" + appId +
		"&scope=" + url.QueryEscape(strings.Join(neededPerms, " ")) +
		"&code_challenge_method=S256" +
		"&code_challenge=" + challengeStr +
		"&state=" + stateStr

	err = exec.Command("xdg-open", userUrl).Run()
	if err != nil {
		return "", 0, fmt.Errorf("opening browser: %w", err)
	}

	authToken = <-token
	close(exitServer)

	jwtSections := strings.Split(authToken, ".")
	if len(jwtSections) != 3 {
		return "", 0, fmt.Errorf("invalid JWT")
	}
	payload, err := base64.RawStdEncoding.DecodeString(jwtSections[1])
	if err != nil {
		return "", 0, fmt.Errorf("decoding JWT payload: %w", err)
	}

	var jwt jwtJson
	err = json.Unmarshal(payload, &jwt)
	if err != nil {
		return "", 0, fmt.Errorf("decoding JWT json: %w", err)
	}
	id := strings.TrimPrefix(jwt.Sub, "CHARACTER:EVE:")
	if id == jwt.Sub {
		return "", 0, fmt.Errorf("invalid JWT sub")
	}

	characterId64, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		return "", 0, fmt.Errorf("parsing character ID: %w", err)
	}

	return authToken, uint32(characterId64), nil
}

type locationJson struct {
	SolarSystemID uint32 `json:"solar_system_id"`
}

func getLocation(auth string, characterId uint32) (uint32, error) {
	req, err := http.NewRequest(http.MethodGet, baseUrl+"/v2/characters/"+strconv.FormatUint(uint64(characterId), 10)+"/location/", nil)
	if err != nil {
		return 0, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+auth)

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("getting location: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("getting location: %s", resp.Status)
	}

	var loc locationJson
	err = json.NewDecoder(resp.Body).Decode(&loc)
	if err != nil {
		return 0, fmt.Errorf("decoding location: %w", err)
	}

	return loc.SolarSystemID, nil
}

type jwtJson struct {
	Sub string `json:"sub"`
}

func addWaypoints(g graph, token string, route []uint32) error {
	const minBackoff = time.Second
	backoff := minBackoff
	for i, system := range route {
		err := addWaypoint(token, system, i == 0)
		if err != nil {
			fmt.Println("failed to add waypoint to", system, ":", err)
			backoff *= 2
		} else {
			backoff = max(backoff/2, minBackoff)
		}
		time.Sleep(backoff)
	}

	return nil
}

func addWaypoint(auth string, system uint32, overwriteRoute bool) error {
	overwrite := "false"
	if overwriteRoute {
		// overwrite the existing route at the beginning
		overwrite = "true"
	}

	url := baseUrl + "/v2/ui/autopilot/waypoint/" +
		"?add_to_beginning=false" +
		"&clear_other_waypoints=" + overwrite +
		"&destination_id=" + strconv.FormatUint(uint64(system), 10)
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+auth)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("adding waypoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("adding waypoint: %s", resp.Status)
	}

	return nil
}
