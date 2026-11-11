package proxy

import (
	"encoding/json"
	"testing"
)

func TestTransformModelNamePreservesEndpointFields(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantFields map[string]any
	}{
		{
			name: "chat completions",
			body: `{
				"model": "local",
				"messages": [{"role": "user", "content": "hello"}],
				"temperature": 0.2,
				"stream": true
			}`,
			wantFields: map[string]any{
				"messages":    []any{map[string]any{"role": "user", "content": "hello"}},
				"temperature": float64(0.2),
				"stream":      true,
			},
		},
		{
			name: "chat completions vision content",
			body: `{
				"model": "local",
				"messages": [{
					"role": "user",
					"content": [
						{"type": "text", "text": "what is in this image?"},
						{"type": "image_url", "image_url": {"url": "data:image/png;base64,AAAA"}}
					]
				}],
				"stream": true
			}`,
			wantFields: map[string]any{
				"messages": []any{map[string]any{
					"role": "user",
					"content": []any{
						map[string]any{"type": "text", "text": "what is in this image?"},
						map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,AAAA"}},
					},
				}},
				"stream": true,
			},
		},
		{
			name: "legacy completions",
			body: `{
				"model": "local",
				"prompt": "write a haiku",
				"max_tokens": 32,
				"stream": false
			}`,
			wantFields: map[string]any{
				"prompt":     "write a haiku",
				"max_tokens": float64(32),
				"stream":     false,
			},
		},
		{
			name: "embeddings",
			body: `{
				"model": "local",
				"input": ["alpha", "beta"],
				"encoding_format": "float"
			}`,
			wantFields: map[string]any{
				"input":           []any{"alpha", "beta"},
				"encoding_format": "float",
			},
		},
		{
			name: "rerank",
			body: `{
				"model": "local",
				"query": "best match",
				"documents": ["one", "two"],
				"top_n": 1
			}`,
			wantFields: map[string]any{
				"query":     "best match",
				"documents": []any{"one", "two"},
				"top_n":     float64(1),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotBody, err := transformModelName([]byte(tt.body), "local", "remote")
			if err != nil {
				t.Fatalf("transformModelName returned error: %v", err)
			}

			var got map[string]any
			if err := json.Unmarshal(gotBody, &got); err != nil {
				t.Fatalf("transformed body is invalid JSON: %v", err)
			}

			if got["model"] != "remote" {
				t.Fatalf("expected model to be remote, got %#v", got["model"])
			}
			for key, want := range tt.wantFields {
				if !jsonEqual(got[key], want) {
					t.Errorf("field %s = %#v, want %#v", key, got[key], want)
				}
			}
		})
	}
}

func TestTransformModelNameLeavesOtherModelsAlone(t *testing.T) {
	body := []byte(`{"model":"other","prompt":"keep me"}`)
	gotBody, err := transformModelName(body, "local", "remote")
	if err != nil {
		t.Fatalf("transformModelName returned error: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(gotBody, &got); err != nil {
		t.Fatalf("transformed body is invalid JSON: %v", err)
	}
	if got["model"] != "other" {
		t.Fatalf("expected model to remain other, got %#v", got["model"])
	}
	if got["prompt"] != "keep me" {
		t.Fatalf("expected prompt to be preserved, got %#v", got["prompt"])
	}
}

func TestTransformRequestAppliesThinkingParams(t *testing.T) {
	body := []byte(`{
		"model":"qwen-local",
		"messages":[{"role":"user","content":"hello"}],
		"chat_template_kwargs":{"foo":"bar"}
	}`)
	params := `{"chat_template_kwargs":{"enable_thinking":false},"temperature":0.1}`

	gotBody, err := transformRequest(body, "qwen-local", "qwen3.6", params)
	if err != nil {
		t.Fatalf("transformRequest returned error: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(gotBody, &got); err != nil {
		t.Fatalf("transformed body is invalid JSON: %v", err)
	}
	if got["model"] != "qwen3.6" {
		t.Fatalf("model = %#v, want qwen3.6", got["model"])
	}
	if got["temperature"] != float64(0.1) {
		t.Fatalf("temperature = %#v, want 0.1", got["temperature"])
	}
	kwargs, ok := got["chat_template_kwargs"].(map[string]any)
	if !ok {
		t.Fatalf("chat_template_kwargs = %#v", got["chat_template_kwargs"])
	}
	if kwargs["enable_thinking"] != false {
		t.Fatalf("enable_thinking = %#v, want false", kwargs["enable_thinking"])
	}
	if kwargs["foo"] != "bar" {
		t.Fatalf("client nested kwargs were not preserved: %#v", kwargs)
	}
}

func TestExtractModelFromBody(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "chat", body: `{"model":"chat-model","messages":[]}`, want: "chat-model"},
		{name: "completion", body: `{"model":"completion-model","prompt":"x"}`, want: "completion-model"},
		{name: "embedding", body: `{"model":"embedding-model","input":["x"]}`, want: "embedding-model"},
		{name: "rerank", body: `{"model":"rerank-model","query":"x","documents":[]}`, want: "rerank-model"},
		{name: "missing", body: `{"prompt":"x"}`, want: ""},
		{name: "invalid", body: `{`, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractModelFromBody([]byte(tt.body)); got != tt.want {
				t.Fatalf("extractModelFromBody() = %q, want %q", got, tt.want)
			}
		})
	}
}

func jsonEqual(got, want any) bool {
	gotJSON, err := json.Marshal(got)
	if err != nil {
		return false
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		return false
	}
	return string(gotJSON) == string(wantJSON)
}
