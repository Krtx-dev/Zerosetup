package docker

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Endpoint is a resolved Docker daemon address and how it was found.
type Endpoint struct {
	Host   string
	Source string
}

// ResolveEndpoint finds the Docker daemon without shelling out to the
// docker CLI. Order: DOCKER_HOST, the active docker CLI context, then
// well-known sockets of common engines. Only reads files.
func ResolveEndpoint() []Endpoint {
	var out []Endpoint
	if h := os.Getenv("DOCKER_HOST"); h != "" {
		return []Endpoint{{Host: h, Source: "DOCKER_HOST"}}
	}
	home, _ := os.UserHomeDir()
	if ep, ok := contextEndpoint(home); ok {
		out = append(out, ep)
	}
	candidates := []struct{ path, src string }{
		{"/var/run/docker.sock", "default socket"},
		{filepath.Join(home, ".colima/default/docker.sock"), "Colima"},
		{filepath.Join(home, ".orbstack/run/docker.sock"), "OrbStack"},
		{filepath.Join(home, ".docker/run/docker.sock"), "Docker Desktop"},
		{filepath.Join(home, ".rd/docker.sock"), "Rancher Desktop"},
	}
	for _, c := range candidates {
		if st, err := os.Stat(c.path); err == nil && st.Mode()&os.ModeSocket != 0 {
			out = append(out, Endpoint{Host: "unix://" + c.path, Source: c.src})
		}
	}
	return out
}

func contextEndpoint(home string) (Endpoint, bool) {
	cfgDir := os.Getenv("DOCKER_CONFIG")
	if cfgDir == "" {
		cfgDir = filepath.Join(home, ".docker")
	}
	name := os.Getenv("DOCKER_CONTEXT")
	if name == "" {
		b, err := os.ReadFile(filepath.Join(cfgDir, "config.json"))
		if err != nil {
			return Endpoint{}, false
		}
		var cfg struct {
			CurrentContext string `json:"currentContext"`
		}
		if json.Unmarshal(b, &cfg) != nil || cfg.CurrentContext == "" || cfg.CurrentContext == "default" {
			return Endpoint{}, false
		}
		name = cfg.CurrentContext
	}
	sum := sha256.Sum256([]byte(name))
	b, err := os.ReadFile(filepath.Join(cfgDir, "contexts", "meta", hex.EncodeToString(sum[:]), "meta.json"))
	if err != nil {
		return Endpoint{}, false
	}
	var meta struct {
		Endpoints map[string]struct {
			Host string `json:"Host"`
		} `json:"Endpoints"`
	}
	if json.Unmarshal(b, &meta) != nil {
		return Endpoint{}, false
	}
	h := meta.Endpoints["docker"].Host
	if !strings.HasPrefix(h, "unix://") && !strings.HasPrefix(h, "npipe://") {
		// Remote (tcp/ssh) contexts are not used automatically: published
		// ports would not be on this machine's loopback.
		return Endpoint{}, false
	}
	return Endpoint{Host: h, Source: "docker context " + name}, true
}
