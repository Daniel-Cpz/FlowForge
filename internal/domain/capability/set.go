// Package capability defines the shared, bounded Job and Worker contract.
package capability

import (
	"errors"
	"slices"
	"strings"
	"unicode"
)

func Normalize(values []string) ([]string, error) {
	if len(values) > 16 {
		return nil, errors.New("at most 16 capabilities")
	}
	out := make([]string, 0, len(values))
	for _, raw := range values {
		if strings.IndexFunc(raw, unicode.IsControl) >= 0 {
			return nil, errors.New("control character in capability")
		}
		v := strings.TrimSpace(raw)
		for _, c := range []byte(v) {
			if c >= 128 {
				return nil, errors.New("capabilities must be ASCII")
			}
		}
		v = strings.ToLower(v)
		if len(v) < 1 || len(v) > 32 {
			return nil, errors.New("capability length must be 1..32")
		}
		for i, c := range []byte(v) {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || i > 0 && (c == '.' || c == '_' || c == '-')) {
				return nil, errors.New("invalid capability token")
			}
		}
		out = append(out, v)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

func Canonical(values []string) bool {
	normalized, err := Normalize(values)
	return err == nil && slices.Equal(values, normalized)
}

func ParseConfig(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		if strings.IndexFunc(raw, unicode.IsControl) >= 0 {
			return nil, errors.New("invalid capability configuration")
		}
		return []string{}, nil
	}
	return Normalize(strings.Split(raw, ","))
}
