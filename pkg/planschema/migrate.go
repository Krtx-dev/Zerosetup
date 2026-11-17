package planschema

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Migrate upgrades a serialized plan to SchemaVersion. Plans from a newer
// major version are rejected rather than guessed at.
func Migrate(data []byte) ([]byte, error) {
	var head struct {
		SchemaVersion string `json:"schemaVersion"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("plan is not valid JSON: %w", err)
	}
	if head.SchemaVersion == "" {
		return nil, fmt.Errorf("plan has no schemaVersion")
	}
	major, _, _ := strings.Cut(head.SchemaVersion, ".")
	curMajor, _, _ := strings.Cut(SchemaVersion, ".")
	if major != curMajor {
		return nil, fmt.Errorf("plan schema version %s is not compatible with this ZEROSETUP (supports %s.x)", head.SchemaVersion, curMajor)
	}
	switch head.SchemaVersion {
	case SchemaVersion:
		return data, nil
	default:
		// Same major, different minor: minor versions are additive by
		// policy, so unknown newer fields would fail strict decoding and
		// older ones decode as-is.
		return data, nil
	}
}
