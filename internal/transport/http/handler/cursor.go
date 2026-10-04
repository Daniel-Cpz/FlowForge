package handler

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	domain "github.com/Daniel-Cpz/FlowForge/internal/domain/job"
	"github.com/google/uuid"
)

const maxCursorLength = 512

var errInvalidCursor = errors.New("invalid cursor")

type cursorDocument struct {
	Version   int    `json:"v"`
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

func encodeCursor(cursor domain.PageCursor) (string, error) {
	if !cursor.Valid() {
		return "", errInvalidCursor
	}
	data, err := json.Marshal(cursorDocument{Version: 1,
		CreatedAt: cursor.CreatedAt.UTC().Format(time.RFC3339Nano), ID: cursor.ID.String()})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeCursor(encoded string) (*domain.PageCursor, error) {
	if len(encoded) == 0 || len(encoded) > maxCursorLength {
		return nil, errInvalidCursor
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || base64.RawURLEncoding.EncodeToString(data) != encoded {
		return nil, errInvalidCursor
	}
	var doc cursorDocument
	fields := 0
	err = decodeObject(data, func(name string, raw json.RawMessage) error {
		fields++
		switch name {
		case "v":
			return json.Unmarshal(raw, &doc.Version)
		case "created_at":
			return json.Unmarshal(raw, &doc.CreatedAt)
		case "id":
			return json.Unmarshal(raw, &doc.ID)
		default:
			return errInvalidCursor
		}
	})
	if err != nil || fields != 3 || doc.Version != 1 {
		return nil, errInvalidCursor
	}
	id, err := uuid.Parse(doc.ID)
	if err != nil || id.String() != doc.ID {
		return nil, errInvalidCursor
	}
	timestamp, err := time.Parse(time.RFC3339Nano, doc.CreatedAt)
	if err != nil || timestamp.UTC().Format(time.RFC3339Nano) != doc.CreatedAt {
		return nil, errInvalidCursor
	}
	cursor := &domain.PageCursor{CreatedAt: timestamp.UTC(), ID: id}
	if !cursor.Valid() {
		return nil, errInvalidCursor
	}
	return cursor, nil
}
