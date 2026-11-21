package model

import (
	"fmt"
	"strconv"
)

type StateVersion int

const StateProtocolKey = "state_protocol"
const StateAuthorityKey = "state_authority"

const (
	StateVersionLegacy   StateVersion = 2
	StateVersionPortable StateVersion = 3
)

func (v StateVersion) String() string { return strconv.Itoa(int(v)) }

func (v StateVersion) Validate() error {
	switch v {
	case StateVersionLegacy, StateVersionPortable:
		return nil
	default:
		return fmt.Errorf("model: unsupported state version %d", v)
	}
}

func ParseStateVersion(value string) (StateVersion, error) {
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("model: invalid state version %q: %w", value, err)
	}
	v := StateVersion(n)
	return v, v.Validate()
}

type DiscoveryVersion int

const DiscoveryVersionInitial DiscoveryVersion = 1

func (v DiscoveryVersion) String() string { return strconv.Itoa(int(v)) }

func (v DiscoveryVersion) Validate() error {
	if v != DiscoveryVersionInitial {
		return fmt.Errorf("model: unsupported discovery version %d", v)
	}
	return nil
}

func ParseDiscoveryVersion(value string) (DiscoveryVersion, error) {
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("model: invalid discovery version %q: %w", value, err)
	}
	v := DiscoveryVersion(n)
	return v, v.Validate()
}
