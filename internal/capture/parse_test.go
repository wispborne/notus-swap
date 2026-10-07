package capture

import "testing"

func TestParseReadsVLLMReasoningField(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"reasoning\":\"The check\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"reasoning\":\" passes\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"Done\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	p := Parse([]byte(stream), "text/event-stream")
	if p.Reasoning != "The check passes" || p.Content != "Done" {
		t.Fatalf("got reasoning %q, content %q", p.Reasoning, p.Content)
	}

	whole := `{"choices":[{"message":{"content":"Done","reasoning":"Thought"},"finish_reason":"stop"}]}`
	if p := Parse([]byte(whole), "application/json"); p.Reasoning != "Thought" {
		t.Fatalf("got reasoning %q", p.Reasoning)
	}

	_, _, r, _ := chunk([]byte(`{"choices":[{"delta":{"reasoning":"live"}}]}`))
	if r != "live" {
		t.Fatalf("chunk reasoning %q", r)
	}
}
