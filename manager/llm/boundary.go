package llm

import (
	"encoding/json"
	"errors"
	"strings"
)

func convertChatRequestToResponses(body []byte) ([]byte, error) {
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, ErrInvalidRequest
	}
	messages, ok := request["messages"].([]any)
	if !ok {
		return nil, ErrInvalidRequest
	}
	delete(request, "messages")
	delete(request, "stream_options")
	input := make([]any, 0, len(messages))
	instructions := strings.TrimSpace(stringValue(request["instructions"]))
	for _, raw := range messages {
		message, _ := raw.(map[string]any)
		role := stringValue(message["role"])
		switch role {
		case "system", "developer":
			instructions = joinNonEmpty(instructions, codexContentText(message["content"]), "\n\n")
		case "assistant":
			input = append(input, map[string]any{"type": "message", "role": "assistant", "content": codexInputContentParts(message["content"], "output_text")})
			if calls, ok := message["tool_calls"].([]any); ok {
				for _, call := range calls {
					if converted := codexFunctionCallInput(call); converted != nil {
						input = append(input, converted)
					}
				}
			}
		case "tool", "function":
			input = append(input, map[string]any{"type": "function_call_output", "call_id": message["tool_call_id"], "output": codexContentText(message["content"])})
		default:
			input = append(input, map[string]any{"type": "message", "role": "user", "content": codexInputContentParts(message["content"], "input_text")})
		}
	}
	request["input"] = input
	if instructions != "" {
		request["instructions"] = instructions
	}
	if tools := codexResponsesTools(request["tools"]); len(tools) > 0 {
		request["tools"] = tools
	}
	if choice, exists := request["tool_choice"]; exists {
		request["tool_choice"] = normalizeCodexToolChoiceValue(choice)
	}
	if err := mapChatResponseFormat(request); err != nil {
		return nil, err
	}
	if effort := stringValue(request["reasoning_effort"]); effort != "" {
		request["reasoning"] = map[string]any{"effort": effort}
		delete(request, "reasoning_effort")
	}
	ensureCodexReasoningSummary(request)
	stripUnsupportedCodexTokenLimits(request)
	// Codex subscription models do not expose sampling controls. Keeping this
	// Chat-only field would reject otherwise compatible Hermes requests.
	delete(request, "temperature")
	return json.Marshal(request)
}

func ensureCodexReasoningSummary(request map[string]any) {
	raw, exists := request["reasoning"]
	if !exists {
		request["reasoning"] = map[string]any{"summary": "detailed"}
		return
	}
	reasoning, ok := raw.(map[string]any)
	if !ok || strings.EqualFold(stringValue(reasoning["effort"]), "none") {
		return
	}
	if _, explicit := reasoning["summary"]; !explicit {
		reasoning["summary"] = "detailed"
	}
}

func mapChatResponseFormat(request map[string]any) error {
	raw, exists := request["response_format"]
	if !exists {
		return nil
	}
	format, ok := raw.(map[string]any)
	if !ok {
		return errors.New("invalid response_format")
	}
	converted := make(map[string]any)
	switch stringValue(format["type"]) {
	case "json_schema":
		schemaFormat, ok := format["json_schema"].(map[string]any)
		if !ok {
			return errors.New("response_format.json_schema is required")
		}
		converted["type"] = "json_schema"
		for _, key := range []string{"name", "description", "schema", "strict"} {
			if value, present := schemaFormat[key]; present {
				converted[key] = value
			}
		}
	case "json_object":
		converted["type"] = "json_object"
	case "text", "":
		converted["type"] = "text"
	default:
		return errors.New("unsupported response_format type")
	}
	textConfig, _ := request["text"].(map[string]any)
	if textConfig == nil {
		textConfig = make(map[string]any)
	}
	textConfig["format"] = converted
	request["text"] = textConfig
	delete(request, "response_format")
	return nil
}

func stripUnsupportedCodexTokenLimits(request map[string]any) {
	delete(request, "max_tokens")
	delete(request, "max_completion_tokens")
	delete(request, "max_output_tokens")
}

func needsJSONResponseInstruction(body map[string]any) bool {
	format, ok := body["response_format"].(map[string]any)
	if !ok || stringValue(format["type"]) != "json_object" {
		return false
	}
	messages, ok := body["messages"].([]any)
	if !ok {
		return false
	}
	encodedMessages, _ := json.Marshal(messages)
	if strings.Contains(strings.ToLower(string(encodedMessages)), "json") {
		return false
	}
	return true
}

func numberPointer(value map[string]any, keys ...string) *int64 {
	for _, key := range keys {
		if number, ok := value[key].(float64); ok {
			converted := int64(number)
			return &converted
		}
	}
	return nil
}
