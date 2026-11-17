// Package services is the catalog of managed backing services. A service
// is only ever added to a plan when the user asks for it explicitly; a
// dependency name in a manifest is evidence, never a trigger.
package services

import (
	"fmt"
	"sort"

	ps "github.com/zerosetup/zerosetup/pkg/planschema"
)

// Definition is a catalog entry: the service and the environment the app
// receives to reach it.
type Definition struct {
	Service ps.Service
	AppEnv  []ps.EnvVar
}

// Postgres and Redis run as their images' unprivileged users with a
// read-only root filesystem. They are reachable only on the session's
// private network (never published to the host) and use no password:
// "trust" authentication is acceptable only because nothing but the app
// container can reach them. This is documented in the threat model.
var catalog = map[string]Definition{
	"postgres": {
		Service: ps.Service{
			Kind: "postgres", Image: "docker.io/library/postgres:17-alpine", Alias: "postgres", Port: 5432,
			User: "70:70", DataDir: "/var/lib/postgresql/data", Tmpfs: []string{"/var/run/postgresql"},
			Env: []ps.EnvVar{
				{Name: "POSTGRES_HOST_AUTH_METHOD", Value: "trust", Kind: ps.KindDefault, Reason: "network-private development database"},
				{Name: "POSTGRES_DB", Value: "app", Kind: ps.KindDefault},
			},
			ReadyCommand: []string{"pg_isready", "-h", "127.0.0.1", "-U", "postgres", "-d", "app"},
			Reason:       "requested with --with-service postgres",
		},
		AppEnv: []ps.EnvVar{
			{Name: "DATABASE_URL", Value: "postgres://postgres@postgres:5432/app", Kind: ps.KindInferred, Reason: "managed PostgreSQL on the session network"},
			{Name: "PGHOST", Value: "postgres", Kind: ps.KindInferred, Reason: "managed PostgreSQL"},
			{Name: "PGUSER", Value: "postgres", Kind: ps.KindInferred, Reason: "managed PostgreSQL"},
			{Name: "PGDATABASE", Value: "app", Kind: ps.KindInferred, Reason: "managed PostgreSQL"},
		},
	},
	"redis": {
		Service: ps.Service{
			Kind: "redis", Image: "docker.io/library/redis:8-alpine", Alias: "redis", Port: 6379,
			User: "999:1000", DataDir: "/data", Command: []string{"redis-server", "--appendonly", "no"},
			ReadyCommand: []string{"redis-cli", "ping"},
			Reason:       "requested with --with-service redis",
		},
		AppEnv: []ps.EnvVar{
			{Name: "REDIS_URL", Value: "redis://redis:6379", Kind: ps.KindInferred, Reason: "managed Redis on the session network"},
		},
	},
}

// Lookup returns the definition for kind.
func Lookup(kind string) (Definition, error) {
	d, ok := catalog[kind]
	if !ok {
		return Definition{}, fmt.Errorf("unsupported service %q", kind)
	}
	return d, nil
}

// Kinds lists supported service kinds.
func Kinds() []string {
	out := make([]string, 0, len(catalog))
	for k := range catalog {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Images returns the allowlisted service images.
func Images() map[string]bool {
	m := map[string]bool{}
	for _, d := range catalog {
		m[d.Service.Image] = true
	}
	return m
}
