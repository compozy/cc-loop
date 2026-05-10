package loop

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeGoalVerdictAcceptsSupportedOutputShapes(t *testing.T) {
	t.Parallel()

	rawVerdict := `{"completed":true,"confidence":0.95,"reason":"verified","missing_work":[],"next_round_guidance":""}`
	verdictObject := map[string]any{
		"completed":           true,
		"confidence":          0.95,
		"reason":              "verified",
		"missing_work":        []any{},
		"next_round_guidance": "",
	}

	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "raw verdict",
			content: rawVerdict,
		},
		{
			name:    "fenced verdict",
			content: "```json\n" + rawVerdict + "\n```",
		},
		{
			name: "claude result envelope",
			content: mustMarshalJSONString(t, map[string]any{
				"type":    "result",
				"subtype": "success",
				"result":  "```json\n" + rawVerdict + "\n```",
			}),
		},
		{
			name: "structured output object",
			content: mustMarshalJSONString(t, map[string]any{
				"type": "structured_output",
				"data": verdictObject,
			}),
		},
		{
			name: "assistant structured output tool call",
			content: mustMarshalJSONString(t, map[string]any{
				"type": "assistant",
				"message": map[string]any{
					"content": []any{
						map[string]any{
							"type": "text",
							"text": `{"completed":true,"confidence":0.5,"reason":"draft","missing_work":[],"next_steps":["wrong field"]}`,
						},
						map[string]any{
							"type":  "tool_use",
							"name":  "StructuredOutput",
							"input": verdictObject,
						},
					},
				},
			}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			verdict, err := decodeGoalVerdict([]byte(tt.content))
			if err != nil {
				t.Fatalf("decode goal verdict: %v", err)
			}
			if !verdict.Completed {
				t.Fatalf("expected completed verdict, got %#v", verdict)
			}
			if verdict.Reason != "verified" {
				t.Fatalf("expected reason verified, got %q", verdict.Reason)
			}
			if verdict.Confidence != 0.95 {
				t.Fatalf("expected confidence 0.95, got %v", verdict.Confidence)
			}
		})
	}
}

func TestDecodeGoalVerdictReportsUsefulErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "unsupported envelope",
			content: `{"type":"result","subtype":"success"}`,
			want:    "unsupported Claude JSON output shape",
		},
		{
			name:    "missing required field",
			content: `{"completed":true,"confidence":0.9,"reason":"done","missing_work":[]}`,
			want:    `missing required field "next_round_guidance"`,
		},
		{
			name:    "blank reason",
			content: `{"completed":true,"confidence":0.9,"reason":"   ","missing_work":[],"next_round_guidance":""}`,
			want:    "missing reason",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := decodeGoalVerdict([]byte(tt.content))
			if err == nil {
				t.Fatal("expected decode error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func mustMarshalJSONString(t *testing.T, value any) string {
	t.Helper()
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal test JSON: %v", err)
	}
	return string(content)
}
