package abuse

import (
	"encoding/json"
	"fmt"
)

// ConfigKey carries a server's detection rules in the config an agent runs; the
// agent takes it out before Xray reads the rest.
const ConfigKey = "_piggerAbuse"

// Capability is what an agent able to detect abuse says in its hello; v4 grades
// bulk sign-ups, so an older agent shows as needing an update.
const Capability = "abuse-v4"

// SplitConfig takes the detection rules out of an agent config; nil rules mean
// detection is off on that server.
func SplitConfig(raw []byte) ([]byte, *Rules, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, nil, err
	}
	v, ok := top[ConfigKey]
	if !ok {
		return raw, nil, nil
	}
	var rules Rules
	if err := json.Unmarshal(v, &rules); err != nil {
		return nil, nil, fmt.Errorf("invalid abuse detection settings: %w", err)
	}
	delete(top, ConfigKey)
	core, err := json.Marshal(top)
	if err != nil {
		return nil, nil, err
	}
	rules = rules.Normalized()
	return core, &rules, nil
}

// JoinConfig adds a server's detection rules to the config an agent runs.
func JoinConfig(raw []byte, rules Rules) ([]byte, error) {
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, err
	}
	top[ConfigKey] = rules
	return json.Marshal(top)
}
