package report

import (
	"encoding/json"
	"io"
)

// WriteJSON renders s as indented JSON to w.
func WriteJSON(w io.Writer, s Summary) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}
