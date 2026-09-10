package llm

import (
	"reflect"
	"testing"
)

func TestCodexImageContentPreserved(t *testing.T) {
	const image = "data:image/png;base64,aGVsbG8="
	got := codexInputContentParts([]any{
		map[string]any{"type": "text", "text": "Describe this image"},
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": image}},
	}, "input_text")
	want := []any{
		map[string]any{"type": "input_text", "text": "Describe this image"},
		map[string]any{"type": "input_image", "image_url": image},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
