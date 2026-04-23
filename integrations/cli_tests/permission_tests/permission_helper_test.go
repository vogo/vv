package permission_tests

import (
	"github.com/vogo/vage/schema"
)

// resultText extracts the first text content part from a ToolResult.
func resultText(r schema.ToolResult) string {
	for _, p := range r.Content {
		if p.Type == "text" {
			return p.Text
		}
	}

	return ""
}
