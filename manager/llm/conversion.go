// Adapted from llm-token-router/tokenrouter/codex_chat_conversion.go.
package llm

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

func transformOpenAICodexResponsesStreamToChat(src io.ReadCloser, model string, includeUsage bool) io.ReadCloser {
	pr, pw := io.Pipe()
	go func() {
		defer src.Close()
		defer pw.Close()
		state := newCodexChatStreamState(model, includeUsage)
		scanner := bufio.NewScanner(src)
		scanner.Buffer(make([]byte, 1024), 1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "" || payload == "[DONE]" {
				continue
			}
			chunks := state.handlePayload([]byte(payload))
			for _, chunk := range chunks {
				if _, err := pw.Write(chunk); err != nil {
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		if !state.finalized {
			_ = pw.CloseWithError(fmt.Errorf("Codex stream ended without a terminal event"))
			return
		}
		for _, chunk := range state.finalize() {
			if _, err := pw.Write(chunk); err != nil {
				return
			}
		}
	}()
	return pr
}

func chatMessageContentText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		parts := make([]string, 0, len(value))
		for _, item := range value {
			if itemMap, ok := item.(map[string]any); ok {
				if text, ok := itemMap["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	case nil:
		return ""
	default:
		body, err := json.Marshal(value)
		if err != nil {
			return ""
		}
		return string(body)
	}
}

type codexChatStreamState struct {
	id                string
	model             string
	created           int64
	sentRole          bool
	sawToolCall       bool
	finalized         bool
	failed            bool
	includeUsage      bool
	usage             map[string]any
	nextToolCallIndex int
	outputToolIndexes map[int]int
	content           strings.Builder
	toolCalls         []map[string]any
}

func newCodexChatStreamState(model string, includeUsage bool) *codexChatStreamState {
	return &codexChatStreamState{
		id:                "chatcmpl_" + newChatGPTMessageID(),
		model:             model,
		created:           time.Now().Unix(),
		includeUsage:      includeUsage,
		outputToolIndexes: make(map[int]int),
	}
}

func (s *codexChatStreamState) handlePayload(payload []byte) [][]byte {
	event := map[string]any{}
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil
	}
	eventType := stringValue(event["type"])
	switch eventType {
	case "response.created":
		if response, ok := event["response"].(map[string]any); ok {
			s.id = firstNonEmpty(stringValue(response["id"]), s.id)
			s.model = firstNonEmpty(stringValue(response["model"]), s.model)
		}
		return s.roleChunk()
	case "response.output_text.delta":
		delta := stringValue(event["delta"])
		if delta == "" {
			return nil
		}
		s.content.WriteString(delta)
		return [][]byte{s.chatChunk(map[string]any{"content": delta}, nil, false)}
	case "response.reasoning_summary_text.delta":
		delta := stringValue(event["delta"])
		if delta == "" {
			return nil
		}
		return [][]byte{s.chatChunk(map[string]any{"reasoning_content": delta}, nil, false)}
	case "response.output_item.added":
		item, _ := event["item"].(map[string]any)
		if stringValue(item["type"]) != "function_call" {
			return nil
		}
		s.sawToolCall = true
		outputIndex := int(int64Value(event["output_index"]))
		toolIndex := s.nextToolCallIndex
		s.nextToolCallIndex++
		s.outputToolIndexes[outputIndex] = toolIndex
		call := map[string]any{
			"index": toolIndex,
			"id":    stringValue(item["call_id"]),
			"type":  "function",
			"function": map[string]any{
				"name": stringValue(item["name"]),
			},
		}
		s.toolCalls = append(s.toolCalls, map[string]any{
			"id":   stringValue(item["call_id"]),
			"type": "function",
			"function": map[string]any{
				"name":      stringValue(item["name"]),
				"arguments": "",
			},
		})
		return [][]byte{s.chatChunk(map[string]any{"tool_calls": []any{call}}, nil, false)}
	case "response.function_call_arguments.delta":
		outputIndex := int(int64Value(event["output_index"]))
		toolIndex, ok := s.outputToolIndexes[outputIndex]
		if !ok {
			return nil
		}
		delta := stringValue(event["delta"])
		if toolIndex >= 0 && toolIndex < len(s.toolCalls) {
			fn, _ := s.toolCalls[toolIndex]["function"].(map[string]any)
			fn["arguments"] = stringValue(fn["arguments"]) + delta
		}
		call := map[string]any{
			"index": toolIndex,
			"function": map[string]any{
				"arguments": delta,
			},
		}
		return [][]byte{s.chatChunk(map[string]any{"tool_calls": []any{call}}, nil, false)}
	case "response.failed", "response.cancelled":
		s.failed, s.finalized = true, true
		errorPayload := map[string]any{"error": map[string]any{"type": "server_error", "code": "upstream_unavailable", "message": "Codex response failed"}}
		if response, ok := event["response"].(map[string]any); ok {
			if upstreamError, ok := response["error"].(map[string]any); ok {
				errorPayload["error"] = upstreamError
			}
		}
		encoded, _ := json.Marshal(errorPayload)
		return append(s.roleChunk(), []byte("data: "+string(encoded)+"\n\n"))
	case "response.completed", "response.done", "response.incomplete":
		s.captureUsage(event)
		finishReason := "stop"
		if s.sawToolCall {
			finishReason = "tool_calls"
		} else if eventType == "response.incomplete" {
			finishReason = "length"
		}
		s.finalized = true
		chunks := [][]byte{s.chatChunk(map[string]any{}, &finishReason, false)}
		if s.includeUsage && s.usage != nil {
			chunks = append(chunks, s.chatChunk(map[string]any{}, nil, true))
		}
		chunks = append(chunks, []byte("data: [DONE]\n\n"))
		return chunks
	default:
		return nil
	}
}

func (s *codexChatStreamState) roleChunk() [][]byte {
	if s.sentRole {
		return nil
	}
	s.sentRole = true
	return [][]byte{s.chatChunk(map[string]any{"role": "assistant"}, nil, false)}
}

func (s *codexChatStreamState) finalize() [][]byte {
	if s.finalized || s.failed {
		return nil
	}
	finishReason := "stop"
	if s.sawToolCall {
		finishReason = "tool_calls"
	}
	s.finalized = true
	chunks := append(s.roleChunk(), s.chatChunk(map[string]any{}, &finishReason, false))
	if s.includeUsage && s.usage != nil {
		chunks = append(chunks, s.chatChunk(map[string]any{}, nil, true))
	}
	chunks = append(chunks, []byte("data: [DONE]\n\n"))
	return chunks
}

func (s *codexChatStreamState) chatChunk(delta map[string]any, finishReason *string, usageOnly bool) []byte {
	payload := map[string]any{
		"id":      s.id,
		"object":  "chat.completion.chunk",
		"created": s.created,
		"model":   s.model,
	}
	if usageOnly {
		payload["choices"] = []any{}
		payload["usage"] = s.usage
	} else {
		payload["choices"] = []any{map[string]any{
			"index":         0,
			"delta":         delta,
			"finish_reason": finishReason,
		}}
	}
	data, _ := json.Marshal(payload)
	return []byte("data: " + string(data) + "\n\n")
}

func (s *codexChatStreamState) captureUsage(event map[string]any) {
	if usage, ok := event["usage"].(map[string]any); ok {
		s.usage = codexChatUsage(usage)
	}
	if response, ok := event["response"].(map[string]any); ok {
		if usage, ok := response["usage"].(map[string]any); ok {
			s.usage = codexChatUsage(usage)
		}
		if model := stringValue(response["model"]); model != "" {
			s.model = model
		}
		if id := stringValue(response["id"]); id != "" {
			s.id = id
		}
	}
}

func codexChatUsage(usage map[string]any) map[string]any {
	prompt := int64Value(usage["prompt_tokens"])
	completion := int64Value(usage["completion_tokens"])
	if prompt == 0 {
		prompt = int64Value(usage["input_tokens"])
	}
	if completion == 0 {
		completion = int64Value(usage["output_tokens"])
	}
	return map[string]any{
		"prompt_tokens":     prompt,
		"completion_tokens": completion,
		"total_tokens":      prompt + completion,
	}
}

func codexInputContentParts(content any, textType string) []any {
	switch value := content.(type) {
	case []any:
		parts := make([]any, 0, len(value))
		for _, raw := range value {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			switch stringValue(item["type"]) {
			case "text", "input_text", "output_text":
				if text := stringValue(item["text"]); text != "" {
					parts = append(parts, map[string]any{"type": textType, "text": text})
				}
			case "image_url":
				if imageURL, ok := item["image_url"].(map[string]any); ok {
					if urlValue := stringValue(imageURL["url"]); urlValue != "" {
						parts = append(parts, map[string]any{"type": "input_image", "image_url": urlValue})
					}
				}
			}
		}
		if len(parts) > 0 {
			return parts
		}
	}
	text := codexContentText(content)
	if text == "" {
		text = ""
	}
	return []any{map[string]any{"type": textType, "text": text}}
}

func codexContentText(content any) string {
	return chatMessageContentText(content)
}

func codexFunctionCallInput(raw any) map[string]any {
	call, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	fn, _ := call["function"].(map[string]any)
	name := stringValue(fn["name"])
	if name == "" {
		return nil
	}
	args := stringValue(fn["arguments"])
	if args == "" {
		args = "{}"
	}
	return map[string]any{
		"type":      "function_call",
		"call_id":   firstNonEmpty(stringValue(call["id"]), "call_"+newChatGPTMessageID()),
		"name":      name,
		"arguments": args,
	}
}

func codexResponsesTools(raw any) []any {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	tools := make([]any, 0, len(items))
	for _, rawTool := range items {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		if stringValue(tool["type"]) != "function" {
			tools = append(tools, tool)
			continue
		}
		fn, _ := tool["function"].(map[string]any)
		if len(fn) == 0 {
			continue
		}
		out := map[string]any{
			"type": "function",
			"name": stringValue(fn["name"]),
		}
		for _, key := range []string{"description", "parameters", "strict"} {
			if value, ok := fn[key]; ok {
				out[key] = value
			}
		}
		tools = append(tools, out)
	}
	return tools
}

func normalizeCodexToolChoiceValue(choice any) any {
	switch value := choice.(type) {
	case string:
		return value
	case map[string]any:
		if fn, ok := value["function"].(map[string]any); ok {
			return map[string]any{"type": "function", "name": stringValue(fn["name"])}
		}
		if name := stringValue(value["name"]); name != "" {
			return map[string]any{"type": "function", "name": name}
		}
	}
	return choice
}

func normalizeOpenAICodexModel(model string) string {
	return strings.TrimSpace(model)
}

func jwtNestedStringClaim(token, namespace, claim string) string {
	claims := jwtClaims(token)
	nested, _ := claims[namespace].(map[string]any)
	return stringValue(nested[claim])
}

func jwtClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	claims := map[string]any{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil
	}
	return claims
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func joinNonEmpty(a, b, sep string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + sep + b
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

func int64Value(value any) int64 {
	switch v := value.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	default:
		return 0
	}
}
