package jobapplication

import (
	"encoding/json"
	"testing"
)

func TestApplicationResponsesIncludeAppliedUsingExtension(t *testing.T) {
	responses := []struct {
		name             string
		response         any
		appliedViaExtVal bool
	}{
		{
			name: "list true",
			response: JobApplication{
				AppliedUsingExtension: true,
			},
			appliedViaExtVal: true,
		},
		{
			name:     "list false",
			response: JobApplication{},
		},
		{
			name: "comprehensive true",
			response: JobApplicationComprehensiveResponse{
				AppliedUsingExtension: true,
			},
			appliedViaExtVal: true,
		},
		{
			name:     "comprehensive false",
			response: JobApplicationComprehensiveResponse{},
		},
	}

	for _, tc := range responses {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.response)
			if err != nil {
				t.Fatalf("marshal response: %v", err)
			}

			var payload map[string]json.RawMessage
			if err := json.Unmarshal(data, &payload); err != nil {
				t.Fatalf("unmarshal response: %v", err)
			}

			value, ok := payload["appliedUsingExtension"]
			if !ok {
				t.Fatal("appliedUsingExtension missing from response")
			}

			var appliedUsingExtension bool
			if err := json.Unmarshal(value, &appliedUsingExtension); err != nil {
				t.Fatalf("decode appliedUsingExtension: %v", err)
			}
			if appliedUsingExtension != tc.appliedViaExtVal {
				t.Fatalf("appliedUsingExtension = %v, want %v", appliedUsingExtension, tc.appliedViaExtVal)
			}
		})
	}
}
