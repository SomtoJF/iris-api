package jobapplication

import (
	"encoding/json"
	"testing"

	"github.com/SomtoJF/iris-api/model"
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

func TestValidateUserActionValuesRequiresExactFields(t *testing.T) {
	layout := model.UserActionLayout{
		{FieldName: "Email"},
		{FieldName: "One-time code"},
	}

	tests := []struct {
		name   string
		values []model.UserActionResultItem
		valid  bool
	}{
		{
			name: "all fields",
			values: []model.UserActionResultItem{
				{FieldName: "Email", Value: "person@example.test"},
				{FieldName: "One-time code", Value: "123456"},
			},
			valid: true,
		},
		{
			name: "missing field",
			values: []model.UserActionResultItem{
				{FieldName: "Email", Value: "person@example.test"},
			},
		},
		{
			name: "unknown field",
			values: []model.UserActionResultItem{
				{FieldName: "Email", Value: "person@example.test"},
				{FieldName: "Password", Value: "secret"},
			},
		},
		{
			name: "duplicate field",
			values: []model.UserActionResultItem{
				{FieldName: "Email", Value: "person@example.test"},
				{FieldName: "Email", Value: "other@example.test"},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateUserActionValues(layout, tc.values)
			if (err == nil) != tc.valid {
				t.Fatalf("validation error = %v, want valid=%v", err, tc.valid)
			}
		})
	}
}

func TestUserActionResumeQueuePayloadIsStableAcrossRetries(t *testing.T) {
	application := model.JobApplication{
		IdJobApplication: 61,
		UserId:           7,
		ResumeId:         12,
		Url:              "https://example.test/job",
	}
	action := model.UserAction{
		IdUserAction:     94,
		ResumeWorkflowID: "job-application-resume-stable",
	}
	first := buildUserActionResumeQueueItem(application, action, 7)
	retry := buildUserActionResumeQueueItem(application, action, 7)
	if first != retry {
		t.Fatalf("retry queue payload changed: first=%+v retry=%+v", first, retry)
	}
	if first.ResumeUserActionID != action.IdUserAction || first.ApplicationWorkflowId != action.ResumeWorkflowID {
		t.Fatalf("queue payload lost persisted idempotency keys: %+v", first)
	}
}
