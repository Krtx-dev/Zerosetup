package detect

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/tailscale/hujson"
	"go.yaml.in/yaml/v3"

	ps "github.com/zerosetup/zerosetup/pkg/planschema"
)

// AnalyzeExisting parses environment specifications that already exist in
// the repository (Dockerfile, Compose, Dev Container). In this version they
// are analysed and reported but never executed: each can run arbitrary
// build steps, mount host paths or run host-side commands.
func AnalyzeExisting(repo Repo) []ps.ExistingConfig {
	var out []ps.ExistingConfig
	for _, f := range repo.FindByName(1, "Dockerfile") {
		out = append(out, analyzeDockerfile(repo, f))
	}
	for _, f := range repo.FindByName(0, "docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml") {
		out = append(out, analyzeCompose(repo, f))
	}
	for _, f := range []string{".devcontainer/devcontainer.json", ".devcontainer.json"} {
		if repo.Exists(f) {
			out = append(out, analyzeDevcontainer(repo, f))
		}
	}
	return out
}

func analyzeDockerfile(repo Repo, f string) ps.ExistingConfig {
	c := ps.ExistingConfig{Kind: "dockerfile", File: f, Used: false,
		Reason: "repository Dockerfiles are not built automatically: RUN steps execute arbitrary commands with network access during the build. ZEROSETUP generated its own hardened environment instead."}
	b, err := repo.ReadFile(f, 256<<10)
	if err != nil {
		c.Findings = append(c.Findings, "could not read: "+err.Error())
		return c
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		up := strings.ToUpper(l)
		switch {
		case strings.HasPrefix(up, "FROM "):
			c.Findings = append(c.Findings, "base image: "+strings.TrimSpace(l[5:]))
		case strings.HasPrefix(up, "EXPOSE "):
			c.Findings = append(c.Findings, "exposes: "+strings.TrimSpace(l[7:]))
		case strings.HasPrefix(up, "USER "):
			c.Findings = append(c.Findings, "runs as user: "+strings.TrimSpace(l[5:]))
		}
	}
	return c
}

type composeFile struct {
	Services map[string]struct {
		Image       string   `yaml:"image"`
		Build       any      `yaml:"build"`
		Privileged  bool     `yaml:"privileged"`
		NetworkMode string   `yaml:"network_mode"`
		Pid         string   `yaml:"pid"`
		CapAdd      []string `yaml:"cap_add"`
		Volumes     []any    `yaml:"volumes"`
		Devices     []any    `yaml:"devices"`
		Ports       []any    `yaml:"ports"`
		SecurityOpt []string `yaml:"security_opt"`
	} `yaml:"services"`
}

func analyzeCompose(repo Repo, f string) ps.ExistingConfig {
	c := ps.ExistingConfig{Kind: "compose", File: f, Used: false,
		Reason: "Compose orchestration is not supported yet; the file was parsed for evidence only and nothing in it was executed."}
	b, err := repo.ReadFile(f, 256<<10)
	if err != nil {
		c.Findings = append(c.Findings, "could not read: "+err.Error())
		return c
	}
	var cf composeFile
	if err := yaml.Unmarshal(b, &cf); err != nil {
		c.Findings = append(c.Findings, "could not parse YAML: "+err.Error())
		return c
	}
	names := make([]string, 0, len(cf.Services))
	for n := range cf.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		s := cf.Services[n]
		desc := "service " + n
		if s.Image != "" {
			desc += " (image " + s.Image + ")"
		} else if s.Build != nil {
			desc += " (built from source)"
		}
		c.Findings = append(c.Findings, desc)
		if s.Privileged {
			c.Findings = append(c.Findings, "UNSAFE: "+n+" requests privileged mode")
		}
		if s.NetworkMode == "host" {
			c.Findings = append(c.Findings, "UNSAFE: "+n+" uses host networking")
		}
		if s.Pid == "host" {
			c.Findings = append(c.Findings, "UNSAFE: "+n+" shares the host PID namespace")
		}
		for _, cap := range s.CapAdd {
			c.Findings = append(c.Findings, "UNSAFE: "+n+" adds capability "+cap)
		}
		if len(s.Devices) > 0 {
			c.Findings = append(c.Findings, "UNSAFE: "+n+" maps host devices")
		}
		for _, v := range s.Volumes {
			src := volumeSource(v)
			switch {
			case strings.Contains(src, "docker.sock"):
				c.Findings = append(c.Findings, "UNSAFE: "+n+" mounts the Docker socket")
			case strings.HasPrefix(src, "/") || strings.HasPrefix(src, "~") || strings.HasPrefix(src, "."):
				c.Findings = append(c.Findings, "host mount: "+n+" mounts "+src)
			}
		}
	}
	return c
}

func volumeSource(v any) string {
	switch t := v.(type) {
	case string:
		src, _, _ := strings.Cut(t, ":")
		return src
	case map[string]any:
		if s, ok := t["source"].(string); ok {
			return s
		}
	}
	return ""
}

func analyzeDevcontainer(repo Repo, f string) ps.ExistingConfig {
	c := ps.ExistingConfig{Kind: "devcontainer", File: f, Used: false,
		Reason: "Dev Container specs are parsed for evidence only; lifecycle commands and features are not executed."}
	b, err := repo.ReadFile(f, 256<<10)
	if err != nil {
		c.Findings = append(c.Findings, "could not read: "+err.Error())
		return c
	}
	std, err := hujson.Standardize(b)
	if err != nil {
		c.Findings = append(c.Findings, "could not parse JSONC: "+err.Error())
		return c
	}
	var dc map[string]any
	if err := json.Unmarshal(std, &dc); err != nil {
		c.Findings = append(c.Findings, "could not parse: "+err.Error())
		return c
	}
	if img, ok := dc["image"].(string); ok {
		c.Findings = append(c.Findings, "image: "+img)
	}
	if df, ok := dc["dockerFile"].(string); ok {
		c.Findings = append(c.Findings, "builds Dockerfile: "+path.Clean(df))
	}
	if ports, ok := dc["forwardPorts"].([]any); ok {
		c.Findings = append(c.Findings, fmt.Sprintf("forwardPorts: %v", ports))
	}
	if feats, ok := dc["features"].(map[string]any); ok {
		keys := make([]string, 0, len(feats))
		for k := range feats {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		c.Findings = append(c.Findings, "features: "+strings.Join(keys, ", "))
	}
	if _, ok := dc["initializeCommand"]; ok {
		c.Findings = append(c.Findings, "UNSAFE: initializeCommand runs on the HOST before the container exists")
	}
	for _, k := range []string{"onCreateCommand", "updateContentCommand", "postCreateCommand", "postStartCommand", "postAttachCommand"} {
		if _, ok := dc[k]; ok {
			c.Findings = append(c.Findings, "lifecycle command: "+k)
		}
	}
	if p, ok := dc["privileged"].(bool); ok && p {
		c.Findings = append(c.Findings, "UNSAFE: privileged container requested")
	}
	if args, ok := dc["runArgs"].([]any); ok && len(args) > 0 {
		c.Findings = append(c.Findings, fmt.Sprintf("runArgs: %v", args))
	}
	if m, ok := dc["mounts"].([]any); ok && len(m) > 0 {
		c.Findings = append(c.Findings, fmt.Sprintf("host mounts requested: %d", len(m)))
	}
	return c
}
