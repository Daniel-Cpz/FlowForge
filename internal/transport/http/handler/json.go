package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"

	service "github.com/Daniel-Cpz/FlowForge/internal/service/job"
)

var errInvalidJSON = errors.New("invalid JSON object")

// decodeObject uses the standard parser, visiting raw values without rewriting
// payloads. Only envelope keys are checked for duplicates; nested payload keys
// retain PostgreSQL JSONB semantics. Input has already been size bounded.
func decodeObject(data []byte, visit func(string, json.RawMessage) error) error {
	if !utf8.Valid(data) {
		return errInvalidJSON
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return errInvalidJSON
	}
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return errInvalidJSON
		}
		name, ok := token.(string)
		if !ok || seen[name] {
			return errInvalidJSON
		}
		seen[name] = true
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return errInvalidJSON
		}
		if err := visit(name, raw); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return errInvalidJSON
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errInvalidJSON
	}
	return nil
}

func decodeCreateInput(data []byte) (service.CreateInput, error) {
	var in service.CreateInput
	err := decodeObject(data, func(name string, raw json.RawMessage) error {
		// Omitted numeric fields default; explicit null is not an integer.
		if bytes.Equal(raw, []byte("null")) && name != "payload" && name != "idempotency_key" {
			return errInvalidJSON
		}
		switch name {
		case "type":
			return decodeText(raw, &in.Type)
		case "payload":
			in.Payload = raw
			return nil
		case "priority":
			return json.Unmarshal(raw, &in.Priority)
		case "max_attempts":
			return json.Unmarshal(raw, &in.MaxAttempts)
		case "timeout":
			return json.Unmarshal(raw, &in.Timeout)
		case "idempotency_key":
			if bytes.Equal(raw, []byte("null")) {
				return nil
			}
			var key string
			if err := decodeText(raw, &key); err != nil {
				return err
			}
			in.IdempotencyKey = &key
			return nil
		default:
			return errInvalidJSON
		}
	})
	return in, err
}

// encoding/json replaces unpaired surrogate escapes with U+FFFD. Check these
// escapes in already parsed string values so client metadata is not silently
// changed. Payload stays raw and PostgreSQL validates its JSONB compatibility.
func decodeText(raw json.RawMessage, target *string) error {
	if err := json.Unmarshal(raw, target); err != nil {
		return err
	}
	for i := 1; i < len(raw)-1; i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if raw[i] != 'u' {
			continue
		}
		code, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return errInvalidJSON
		}
		i += 4
		if !utf16.IsSurrogate(rune(code)) {
			continue
		}
		if code >= 0xdc00 || i+6 >= len(raw)-1 || raw[i+1] != '\\' || raw[i+2] != 'u' {
			return errInvalidJSON
		}
		low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return errInvalidJSON
		}
		i += 6
	}
	return nil
}
