package privacyapi

import (
	"encoding/json"

	"github.com/aurimasniekis/go-harbor-pilot/internal/api"
	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
)

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func toFields(fs []cp.FieldError) []api.FieldError {
	out := make([]api.FieldError, 0, len(fs))
	for _, f := range fs {
		out = append(out, api.FieldError{Location: f.Location, Message: f.Message})
	}
	return out
}
