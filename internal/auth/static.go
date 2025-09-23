package auth

import (
	"encoding/json"
	"fmt"
	"os"
)

// TokenProvider resolves principals for bearer tokens.
type TokenProvider interface {
	Principal(token string) (string, bool)
}

// StaticProvider loads tokens from a JSON map file {"token":"principal"}.
type StaticProvider struct {
	byToken map[string]string
}

// NewStaticProvider constructs a provider from a path.
func NewStaticProvider(path string) (*StaticProvider, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read token file: %w", err)
	}
	raw := map[string]string{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse token file: %w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("token file contained no entries")
	}
	return &StaticProvider{byToken: raw}, nil
}

// Principal returns the associated principal for a token.
func (s *StaticProvider) Principal(token string) (string, bool) {
	principal, ok := s.byToken[token]
	return principal, ok
}

// Size returns the number of configured tokens.
func (s *StaticProvider) Size() int {
	return len(s.byToken)
}

// InMemoryProvider allows programmatic registration (useful for tests).
type InMemoryProvider struct {
	byToken map[string]string
}

// NewInMemory constructs with provided map.
func NewInMemory(mapping map[string]string) *InMemoryProvider {
	cp := make(map[string]string, len(mapping))
	for k, v := range mapping {
		cp[k] = v
	}
	return &InMemoryProvider{byToken: cp}
}

// Principal implements TokenProvider.
func (m *InMemoryProvider) Principal(token string) (string, bool) {
	principal, ok := m.byToken[token]
	return principal, ok
}
