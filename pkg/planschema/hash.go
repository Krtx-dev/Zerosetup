package planschema

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Canonical returns the deterministic JSON encoding of the plan. Go's
// encoder emits struct fields in declaration order and sorts map keys, and
// the Plan contains no maps or timestamps, so equal plans encode equally.
func Canonical(p *Plan) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(Normalize(p)); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// Hash returns "sha256:<hex>" of the canonical encoding.
func Hash(p *Plan) (string, error) {
	b, err := Canonical(p)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// ShortHash returns the first 12 hex characters of the plan hash, which is
// what users type for --approve.
func ShortHash(full string) string {
	const prefix = "sha256:"
	if len(full) > len(prefix)+12 && full[:len(prefix)] == prefix {
		return full[len(prefix) : len(prefix)+12]
	}
	return full
}

// Normalize replaces nil slices with empty ones so that a plan that went
// through a JSON round-trip hashes identically to the original.
func Normalize(p *Plan) *Plan {
	c := *p
	if c.Steps == nil {
		c.Steps = []Step{}
	}
	if c.Ports == nil {
		c.Ports = []Port{}
	}
	if c.Env == nil {
		c.Env = []EnvVar{}
	}
	if c.Readiness == nil {
		c.Readiness = []ReadinessCheck{}
	}
	if c.Decisions == nil {
		c.Decisions = []Decision{}
	}
	if c.Warnings == nil {
		c.Warnings = []Warning{}
	}
	if c.Conflicts == nil {
		c.Conflicts = []Conflict{}
	}
	if c.ExistingConfigs == nil {
		c.ExistingConfigs = []ExistingConfig{}
	}
	if c.Overrides == nil {
		c.Overrides = []string{}
	}
	if c.Start.Argv == nil {
		c.Start.Argv = []string{}
	}
	return &c
}

// Decode parses a plan, migrating older schema versions.
func Decode(data []byte) (*Plan, error) {
	migrated, err := Migrate(data)
	if err != nil {
		return nil, err
	}
	var p Plan
	dec := json.NewDecoder(bytes.NewReader(migrated))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, err
	}
	return Normalize(&p), nil
}
