package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zerosetup/zerosetup/internal/runtime"
	"github.com/zerosetup/zerosetup/internal/zerr"
	ps "github.com/zerosetup/zerosetup/pkg/planschema"
)

// ServiceRecord records a managed service container.
type ServiceRecord struct {
	Kind        string `json:"kind"`
	ContainerID string `json:"containerId"`
	Image       string `json:"image"`
	Digest      string `json:"digest,omitempty"`
	ReadyMs     int64  `json:"readyMs"`
}

func serviceName(sid, kind string) string   { return "zerosetup-" + sid + "-" + kind }
func serviceVolume(sid, kind string) string { return "zerosetup-" + sid + "-" + kind + "-data" }

// startServices creates (or restarts) every managed service in the plan and
// waits until each answers its readiness command.
func (s *Service) startServices(ctx context.Context, b runtime.Backend, sid string, p *ps.Plan, f *Facts, fresh bool) error {
	if len(p.Services) == 0 {
		return nil
	}
	if fresh {
		f.Services = nil
	}
	for _, sv := range p.Services {
		start := time.Now()
		var rec *ServiceRecord
		for i := range f.Services {
			if f.Services[i].Kind == sv.Kind {
				rec = &f.Services[i]
			}
		}
		if rec == nil {
			s.emit(ctx, sid, "stage", "services", "acquiring image "+sv.Image, nil)
			img, err := b.EnsureImage(ctx, sv.Image, func(m string) { s.emit(ctx, sid, "progress", "services", m, nil) })
			if err != nil {
				return err
			}
			vol := serviceVolume(sid, sv.Kind)
			if err := b.EnsureVolume(ctx, vol, labels(sid, "service")); err != nil {
				return err
			}
			env := make([]string, 0, len(sv.Env))
			for _, e := range sv.Env {
				env = append(env, e.Name+"="+e.Value)
			}
			l := labels(sid, "service")
			l["io.zerosetup.service"] = sv.Kind
			id, err := b.Create(ctx, runtime.ContainerSpec{
				Name: serviceName(sid, sv.Kind), Image: sv.Image, Cmd: sv.Command, Env: env, User: sv.User,
				Mounts:  []runtime.VolumeMount{{Volume: vol, Target: sv.DataDir}},
				Network: netName(sid), Aliases: []string{sv.Alias}, Tmpfs: sv.Tmpfs, KeepEntrypoint: true,
				CPUs: 1, MemoryMB: 512, PidsLimit: 256, LogMaxMB: 5, Labels: l,
			})
			if err != nil {
				return err
			}
			f.Services = append(f.Services, ServiceRecord{Kind: sv.Kind, ContainerID: id, Image: sv.Image, Digest: img.Digest})
			rec = &f.Services[len(f.Services)-1]
		}
		s.emit(ctx, sid, "stage", "services", fmt.Sprintf("starting %s (%s) as %s on the private session network", sv.Kind, sv.Image, sv.Alias), nil)
		if err := b.Start(ctx, rec.ContainerID); err != nil {
			return err
		}
		if err := waitServiceReady(ctx, b, rec.ContainerID, sv); err != nil {
			return err
		}
		rec.ReadyMs = time.Since(start).Milliseconds()
		s.emit(ctx, sid, "milestone", "services", fmt.Sprintf("%s ready (%s answered) in %s", sv.Kind, strings.Join(sv.ReadyCommand, " "), time.Since(start).Round(100*time.Millisecond)),
			map[string]any{"service": sv.Kind})
	}
	return nil
}

func waitServiceReady(ctx context.Context, b runtime.Backend, id string, sv ps.Service) error {
	deadline := time.Now().Add(120 * time.Second)
	var last string
	for {
		st, err := b.Inspect(ctx, id)
		if err != nil {
			return zerr.Wrap(err, zerr.CodeContainer, "services", "cannot inspect "+sv.Kind)
		}
		if !st.Running {
			var sb strings.Builder
			_ = b.Logs(ctx, id, false, "40", &sb, &sb)
			return zerr.New(zerr.CodeNotReady, "services", fmt.Sprintf("%s exited with code %d before becoming ready", sv.Kind, st.ExitCode)).
				WithDetail("%s", strings.TrimSpace(sb.String()))
		}
		out, code, err := b.Exec(ctx, id, sv.ReadyCommand)
		if err == nil && code == 0 {
			return nil
		}
		last = strings.TrimSpace(string(out))
		if time.Now().After(deadline) {
			return zerr.New(zerr.CodeNotReady, "services", sv.Kind+" did not become ready within 120s").WithDetail("last readiness output: %s", last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// stopServices stops every managed service container of a session.
func stopServices(ctx context.Context, b runtime.Backend, f *Facts) {
	for _, r := range f.Services {
		_ = b.Stop(ctx, r.ContainerID, 10*time.Second)
	}
}
