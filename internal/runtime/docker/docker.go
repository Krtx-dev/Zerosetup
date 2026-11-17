// Package docker implements runtime.Backend on the Docker Engine API using
// the official moby client SDK. It never shells out to the docker CLI.
package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/zerosetup/zerosetup/internal/runtime"
	"github.com/zerosetup/zerosetup/internal/zerr"
)

// Backend is the Docker implementation of runtime.Backend.
type Backend struct {
	cli      *client.Client
	endpoint Endpoint
}

var _ runtime.Backend = (*Backend)(nil)

// New connects to the first reachable Docker endpoint.
func New(ctx context.Context) (*Backend, error) {
	eps := ResolveEndpoint()
	if len(eps) == 0 {
		return nil, unavailable(errors.New("no Docker endpoint found (DOCKER_HOST unset, no docker context, no known socket)"))
	}
	var lastErr error
	for _, ep := range eps {
		cli, err := client.New(client.WithHost(ep.Host))
		if err != nil {
			lastErr = err
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err = cli.Ping(pctx, client.PingOptions{NegotiateAPIVersion: true})
		cancel()
		if err != nil {
			cli.Close()
			lastErr = fmt.Errorf("%s (%s): %w", ep.Host, ep.Source, err)
			continue
		}
		return &Backend{cli: cli, endpoint: ep}, nil
	}
	return nil, unavailable(lastErr)
}

func unavailable(cause error) error {
	return zerr.Wrap(cause, zerr.CodeDockerUnavailable, "runtime", "Docker daemon unavailable").
		WithDetail("ZEROSETUP can inspect repositories without Docker, but execution requires a running container engine.").
		WithSuggestion("Start Docker (Docker Desktop, OrbStack or `colima start`) and run `zerosetup doctor`.")
}

func (b *Backend) Name() string { return "docker" }

func (b *Backend) Close() error { return b.cli.Close() }

func (b *Backend) Check(ctx context.Context) (*runtime.Info, error) {
	v, err := b.cli.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		return nil, unavailable(err)
	}
	inf, err := b.cli.Info(ctx, client.InfoOptions{})
	if err != nil {
		return nil, unavailable(err)
	}
	i := inf.Info
	info := &runtime.Info{
		Backend: "docker", Endpoint: b.endpoint.Host, EndpointFrom: b.endpoint.Source,
		ServerVersion: v.Version, APIVersion: v.APIVersion, OS: i.OperatingSystem, Arch: i.Architecture,
		CgroupVersion: i.CgroupVersion, MemoryLimit: i.MemoryLimit, PidsLimit: i.PidsLimit,
		CPUs: i.NCPU, MemTotalBytes: i.MemTotal, SecurityOpts: i.SecurityOptions,
	}
	for _, o := range i.SecurityOptions {
		if strings.Contains(o, "rootless") {
			info.Rootless = true
		}
	}
	return info, nil
}

func (b *Backend) EnsureImage(ctx context.Context, ref string, progress func(string)) (*runtime.ImageInfo, error) {
	out := &runtime.ImageInfo{Reference: ref}
	if _, err := b.cli.ImageInspect(ctx, ref); err != nil {
		if !cerrdefs.IsNotFound(err) {
			return nil, zerr.Wrap(err, zerr.CodeImage, "image", "cannot inspect image "+ref)
		}
		resp, err := b.cli.ImagePull(ctx, ref, client.ImagePullOptions{})
		if err != nil {
			return nil, zerr.Wrap(err, zerr.CodeImage, "image", "cannot pull "+ref).
				WithSuggestion("Check your network connection; Docker Hub may also be rate-limiting anonymous pulls.")
		}
		last := ""
		for msg, err := range resp.JSONMessages(ctx) {
			if err != nil {
				resp.Close()
				return nil, zerr.Wrap(err, zerr.CodeImage, "image", "pull of "+ref+" failed")
			}
			if msg.Error != nil {
				resp.Close()
				return nil, zerr.New(zerr.CodeImage, "image", "pull of "+ref+" failed: "+msg.Error.Message)
			}
			if progress != nil && msg.Status != "" && msg.Status != last && msg.ID == "" {
				last = msg.Status
				progress(msg.Status)
			}
		}
		resp.Close()
		out.Pulled = true
	}
	ii, err := b.cli.ImageInspect(ctx, ref)
	if err != nil {
		return nil, zerr.Wrap(err, zerr.CodeImage, "image", "cannot inspect image "+ref)
	}
	out.ID = ii.ID
	out.OS = ii.Os
	out.Arch = ii.Architecture
	if len(ii.RepoDigests) > 0 {
		out.Digest = ii.RepoDigests[0]
	}
	return out, nil
}

func (b *Backend) EnsureNetwork(ctx context.Context, name string, labels map[string]string) error {
	if _, err := b.cli.NetworkInspect(ctx, name, client.NetworkInspectOptions{}); err == nil {
		return nil
	}
	_, err := b.cli.NetworkCreate(ctx, name, client.NetworkCreateOptions{Driver: "bridge", Labels: labels})
	if err != nil && !cerrdefs.IsConflict(err) && !cerrdefs.IsAlreadyExists(err) {
		return zerr.Wrap(err, zerr.CodeContainer, "prepare", "cannot create network "+name)
	}
	return nil
}

func (b *Backend) EnsureVolume(ctx context.Context, name string, labels map[string]string) error {
	if _, err := b.cli.VolumeCreate(ctx, client.VolumeCreateOptions{Name: name, Driver: "local", Labels: labels}); err != nil {
		return zerr.Wrap(err, zerr.CodeContainer, "prepare", "cannot create volume "+name)
	}
	return nil
}

// Create applies the non-negotiable hardening to every container.
func (b *Backend) Create(ctx context.Context, s runtime.ContainerSpec) (string, error) {
	if s.Labels[runtime.LabelManaged] != "true" || s.Labels[runtime.LabelSession] == "" && s.Labels[runtime.LabelRole] != "probe" {
		return "", zerr.New(zerr.CodePolicyViolation, "runtime", "refusing to create an unlabelled container")
	}
	if s.MemoryMB <= 0 || s.CPUs <= 0 || s.PidsLimit <= 0 {
		return "", zerr.New(zerr.CodePolicyViolation, "runtime", "refusing to create a container without resource limits")
	}
	exposed := network.PortSet{}
	bindings := network.PortMap{}
	for cport, hport := range s.Ports {
		p, err := network.ParsePort(strconv.Itoa(cport) + "/tcp")
		if err != nil {
			return "", err
		}
		exposed[p] = struct{}{}
		bindings[p] = []network.PortBinding{{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: strconv.Itoa(hport)}}
	}
	var mounts []mount.Mount
	for _, m := range s.Mounts {
		if m.Volume == "" || strings.ContainsAny(m.Volume, "/\\:") || !strings.HasPrefix(m.Target, "/") {
			return "", zerr.New(zerr.CodePolicyViolation, "runtime", "only named-volume mounts are allowed")
		}
		mounts = append(mounts, mount.Mount{Type: mount.TypeVolume, Source: m.Volume, Target: m.Target})
	}
	pids := s.PidsLimit
	init := true
	stopTimeout := 10
	logMax := s.LogMaxMB
	if logMax <= 0 {
		logMax = 10
	}
	hc := &container.HostConfig{
		Privileged:     false,
		CapDrop:        []string{"ALL"},
		SecurityOpt:    []string{"no-new-privileges:true"},
		ReadonlyRootfs: true,
		Tmpfs:          map[string]string{"/tmp": "rw,nosuid,nodev,size=512m"},
		Mounts:         mounts,
		PortBindings:   bindings,
		Init:           &init,
		NetworkMode:    container.NetworkMode(s.Network),
		LogConfig:      container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": fmt.Sprintf("%dm", logMax), "max-file": "2"}},
		RestartPolicy:  container.RestartPolicy{Name: container.RestartPolicyDisabled},
		Resources: container.Resources{
			Memory:     s.MemoryMB << 20,
			MemorySwap: s.MemoryMB << 20, // no extra swap
			NanoCPUs:   int64(s.CPUs * 1e9),
			PidsLimit:  &pids,
		},
	}
	if s.PrepChown {
		// The prep container runs only `chown` on ZEROSETUP's own volume
		// roots; it never runs repository code.
		hc.CapAdd = []string{"CHOWN", "FOWNER", "DAC_OVERRIDE"}
	}
	if s.Network == "" {
		hc.NetworkMode = "none"
	}
	for _, t := range s.Tmpfs {
		if !strings.HasPrefix(t, "/") || strings.Contains(t, "..") {
			return "", zerr.New(zerr.CodePolicyViolation, "runtime", "invalid tmpfs path "+t)
		}
		hc.Tmpfs[t] = "rw,nosuid,nodev,size=64m"
	}
	if s.User == "" || strings.HasPrefix(s.User, "0") && !s.PrepChown {
		return "", zerr.New(zerr.CodePolicyViolation, "runtime", "refusing to run a container as root")
	}
	cfg := &container.Config{
		Image: s.Image, Cmd: s.Cmd, Entrypoint: []string{}, WorkingDir: s.Workdir, Env: s.Env, User: s.User,
		Labels: s.Labels, ExposedPorts: exposed, StopTimeout: &stopTimeout,
	}
	if s.KeepEntrypoint {
		cfg.Entrypoint = nil
	}
	var nc *network.NetworkingConfig
	if len(s.Aliases) > 0 && s.Network != "" {
		nc = &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{s.Network: {Aliases: s.Aliases}}}
	}
	res, err := b.cli.ContainerCreate(ctx, client.ContainerCreateOptions{Name: s.Name, Config: cfg, HostConfig: hc, NetworkingConfig: nc})
	if err != nil {
		if cerrdefs.IsConflict(err) {
			return "", zerr.Wrap(err, zerr.CodeContainer, "runtime", "a container named "+s.Name+" already exists")
		}
		return "", zerr.Wrap(err, zerr.CodeContainer, "runtime", "cannot create container")
	}
	return res.ID, nil
}

func (b *Backend) CopyTo(ctx context.Context, id, dest string, tar io.Reader) error {
	_, err := b.cli.CopyToContainer(ctx, id, client.CopyToContainerOptions{DestinationPath: dest, Content: tar})
	if err != nil {
		return zerr.Wrap(err, zerr.CodeContainer, "prepare", "cannot copy source into the workspace")
	}
	return nil
}

func (b *Backend) Start(ctx context.Context, id string) error {
	if _, err := b.cli.ContainerStart(ctx, id, client.ContainerStartOptions{}); err != nil {
		s := strings.ToLower(err.Error())
		if strings.Contains(s, "port is already allocated") || strings.Contains(s, "address already in use") {
			return zerr.Wrap(err, zerr.CodePortUnavailable, "start", "host port is already in use")
		}
		return zerr.Wrap(err, zerr.CodeContainer, "start", "cannot start container")
	}
	return nil
}

func (b *Backend) Wait(ctx context.Context, id string) (int, error) {
	w := b.cli.ContainerWait(ctx, id, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	select {
	case r := <-w.Result:
		if r.Error != nil && r.Error.Message != "" {
			return int(r.StatusCode), errors.New(r.Error.Message)
		}
		return int(r.StatusCode), nil
	case err := <-w.Error:
		return -1, err
	case <-ctx.Done():
		return -1, ctx.Err()
	}
}

func (b *Backend) Logs(ctx context.Context, id string, follow bool, tail string, stdout, stderr io.Writer) error {
	if tail == "" {
		tail = "all"
	}
	rc, err := b.cli.ContainerLogs(ctx, id, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Follow: follow, Tail: tail})
	if err != nil {
		return wrapNotFound(err, "cannot read logs")
	}
	defer rc.Close()
	_, err = stdcopy.StdCopy(stdout, stderr, rc)
	if err != nil && ctx.Err() != nil {
		return nil
	}
	return err
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	if t.Year() < 2000 {
		return time.Time{}
	}
	return t
}

func (b *Backend) Inspect(ctx context.Context, id string) (*runtime.ContainerState, error) {
	r, err := b.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return nil, wrapNotFound(err, "cannot inspect container")
	}
	c := r.Container
	st := &runtime.ContainerState{ID: c.ID, Name: strings.TrimPrefix(c.Name, "/"), HostPorts: map[int]int{}}
	if c.Config != nil {
		st.Labels = c.Config.Labels
	}
	if c.State != nil {
		st.Status = string(c.State.Status)
		st.Running = c.State.Running
		st.ExitCode = c.State.ExitCode
		st.OOMKilled = c.State.OOMKilled
		st.Error = c.State.Error
		st.StartedAt = parseTime(c.State.StartedAt)
		st.FinishedAt = parseTime(c.State.FinishedAt)
	}
	if c.NetworkSettings != nil {
		for p, bs := range c.NetworkSettings.Ports {
			for _, bnd := range bs {
				if hp, err := strconv.Atoi(bnd.HostPort); err == nil {
					st.HostPorts[int(p.Num())] = hp
				}
			}
		}
	}
	return st, nil
}

func (b *Backend) Exec(ctx context.Context, id string, argv []string) ([]byte, int, error) {
	ex, err := b.cli.ExecCreate(ctx, id, client.ExecCreateOptions{Cmd: argv, AttachStdout: true, AttachStderr: true})
	if err != nil {
		return nil, -1, wrapNotFound(err, "cannot exec probe")
	}
	att, err := b.cli.ExecAttach(ctx, ex.ID, client.ExecAttachOptions{})
	if err != nil {
		return nil, -1, err
	}
	defer att.Close()
	var out, errb strings.Builder
	if _, err := stdcopy.StdCopy(&out, &errb, io.LimitReader(att.Reader, 4<<20)); err != nil {
		return nil, -1, err
	}
	ins, err := b.cli.ExecInspect(ctx, ex.ID, client.ExecInspectOptions{})
	if err != nil {
		return nil, -1, err
	}
	return []byte(out.String()), ins.ExitCode, nil
}

func (b *Backend) Stop(ctx context.Context, id string, timeout time.Duration) error {
	secs := int(timeout.Seconds())
	_, err := b.cli.ContainerStop(ctx, id, client.ContainerStopOptions{Timeout: &secs})
	if err != nil && !cerrdefs.IsNotFound(err) {
		return zerr.Wrap(err, zerr.CodeContainer, "stop", "cannot stop container")
	}
	return nil
}

func (b *Backend) Remove(ctx context.Context, id string) error {
	_, err := b.cli.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true})
	if err != nil && !cerrdefs.IsNotFound(err) {
		return zerr.Wrap(err, zerr.CodeCleanup, "cleanup", "cannot remove container "+short(id))
	}
	return nil
}

func (b *Backend) RemoveVolume(ctx context.Context, name string) error {
	_, err := b.cli.VolumeRemove(ctx, name, client.VolumeRemoveOptions{Force: false})
	if err != nil && !cerrdefs.IsNotFound(err) {
		return zerr.Wrap(err, zerr.CodeCleanup, "cleanup", "cannot remove volume "+name)
	}
	return nil
}

func (b *Backend) RemoveNetwork(ctx context.Context, name string) error {
	_, err := b.cli.NetworkRemove(ctx, name, client.NetworkRemoveOptions{})
	if err != nil && !cerrdefs.IsNotFound(err) {
		return zerr.Wrap(err, zerr.CodeCleanup, "cleanup", "cannot remove network "+name)
	}
	return nil
}

func labelFilters(filter map[string]string) client.Filters {
	f := client.Filters{}
	for k, v := range filter {
		f.Add("label", k+"="+v)
	}
	return f
}

func (b *Backend) ListOwned(ctx context.Context, filter map[string]string) (*runtime.Owned, error) {
	if filter[runtime.LabelManaged] != "true" {
		return nil, errors.New("ListOwned requires the managed label filter")
	}
	out := &runtime.Owned{}
	cs, err := b.cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: labelFilters(filter)})
	if err != nil {
		return nil, unavailable(err)
	}
	for _, c := range cs.Items {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		out.Containers = append(out.Containers, runtime.ContainerState{ID: c.ID, Name: name, Status: string(c.State),
			Running: c.State == container.StateRunning, Labels: c.Labels})
	}
	vs, err := b.cli.VolumeList(ctx, client.VolumeListOptions{Filters: labelFilters(filter)})
	if err != nil {
		return nil, unavailable(err)
	}
	for _, v := range vs.Items {
		out.Volumes = append(out.Volumes, v.Name)
	}
	ns, err := b.cli.NetworkList(ctx, client.NetworkListOptions{Filters: labelFilters(filter)})
	if err != nil {
		return nil, unavailable(err)
	}
	for _, n := range ns.Items {
		out.Networks = append(out.Networks, n.Name)
	}
	return out, nil
}

func (b *Backend) Stats(ctx context.Context, id string) (*runtime.Stats, error) {
	r, err := b.cli.ContainerStats(ctx, id, client.ContainerStatsOptions{Stream: false, IncludePreviousSample: true})
	if err != nil {
		return nil, wrapNotFound(err, "cannot read stats")
	}
	defer r.Body.Close()
	var s container.StatsResponse
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		return nil, err
	}
	out := &runtime.Stats{MemoryBytes: s.MemoryStats.Usage, MemoryLimit: s.MemoryStats.Limit, Pids: s.PidsStats.Current, SampledAt: s.Read}
	cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage) - float64(s.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(s.CPUStats.SystemUsage) - float64(s.PreCPUStats.SystemUsage)
	if sysDelta > 0 && cpuDelta > 0 {
		n := float64(s.CPUStats.OnlineCPUs)
		if n == 0 {
			n = 1
		}
		out.CPUPercent = cpuDelta / sysDelta * n * 100
	}
	for _, n := range s.Networks {
		out.NetRxBytes += n.RxBytes
		out.NetTxBytes += n.TxBytes
	}
	return out, nil
}

type notFound struct{ error }

func (notFound) NotFound() bool  { return true }
func (n notFound) Unwrap() error { return n.error }

func wrapNotFound(err error, msg string) error {
	if cerrdefs.IsNotFound(err) {
		return notFound{fmt.Errorf("%s: %w", msg, err)}
	}
	return fmt.Errorf("%s: %w", msg, err)
}

// IsNotFound reports whether err means the resource does not exist.
func IsNotFound(err error) bool {
	var nf interface{ NotFound() bool }
	return errors.As(err, &nf) && nf.NotFound() || cerrdefs.IsNotFound(err)
}

func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
