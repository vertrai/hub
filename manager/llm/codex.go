// Adapted from llm-token-router/tokenrouter/adapter_codex.go.
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultCodexBackendURL = "https://chatgpt.com/backend-api/codex"

const (
	openAICodexOAuthClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	openAICodexOAuthTokenURL = "https://auth.openai.com/oauth/token"
)

type CodexCredential struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	IDToken      string    `json:"id_token,omitempty"`
	AccountID    string    `json:"account_id"`
	BaseURL      string    `json:"base_url,omitempty"`
	TokenURL     string    `json:"token_url,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
}

type CodexAdapterRequest struct {
	Account       Account
	UpstreamModel string
	Body          []byte
}

type CodexStreamRequest struct {
	CodexAdapterRequest
	Endpoint GatewayEndpoint
}

type CodexStreamResult struct {
	Response          *http.Response
	UpdatedCredential []byte
	ErrorCode         string
	Err               error
}

type CodexAdapterResult struct {
	OpenAIAdapterResult
	UpdatedCredential []byte
}

type CodexAdapter struct {
	Client      *http.Client
	mu          sync.Mutex
	credentials map[string]CodexCredential
	refreshLock map[string]*codexRefreshLock
}

type CodexRefreshError struct {
	Permanent bool
	Message   string
}

func (e *CodexRefreshError) Error() string { return e.Message }

type codexRefreshLock struct {
	mu   sync.Mutex
	refs int
}

func NewCodexAdapter(client *http.Client) *CodexAdapter {
	if client == nil {
		client = http.DefaultClient
	}
	return &CodexAdapter{Client: client, credentials: make(map[string]CodexCredential), refreshLock: make(map[string]*codexRefreshLock)}
}

func (a *CodexAdapter) Execute(ctx context.Context, input CodexAdapterRequest) CodexAdapterResult {
	var credential CodexCredential
	if err := json.Unmarshal(input.Account.Credential, &credential); err != nil {
		return codexResult(OpenAIAdapterResult{Err: err, ErrorCode: "upstream_reauth_required"})
	}
	credential, updated, err := a.validCredential(ctx, input.Account.ID, credential)
	if err != nil {
		return codexResult(OpenAIAdapterResult{Err: err, ErrorCode: codexRefreshErrorCode(err), ErrorMessage: err.Error()})
	}
	if strings.TrimSpace(credential.AccessToken) == "" || strings.TrimSpace(credential.AccountID) == "" {
		return codexResult(OpenAIAdapterResult{Err: errors.New("codex credential is incomplete"), ErrorCode: "upstream_reauth_required"})
	}
	normalizedBody, err := normalizeCodexResponsesBoundary(input.Body, false)
	if err != nil {
		return codexResult(OpenAIAdapterResult{Err: err, ErrorCode: "invalid_request", ErrorMessage: err.Error()})
	}
	var body map[string]any
	if err := json.Unmarshal(normalizedBody, &body); err != nil {
		return codexResult(OpenAIAdapterResult{Err: err, ErrorCode: "invalid_request"})
	}
	body["model"] = normalizeOpenAICodexModel(input.UpstreamModel)
	// The Codex subscription backend only accepts streaming Responses requests.
	// Non-streaming gateway calls are implemented by collecting the terminal
	// response event below and returning it as an ordinary JSON response.
	body["stream"] = true
	if _, exists := body["store"]; !exists {
		body["store"] = false
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return codexResult(OpenAIAdapterResult{Err: err})
	}
	baseURL := strings.TrimRight(credential.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultCodexBackendURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/responses", bytes.NewReader(encoded))
	if err != nil {
		return codexResult(OpenAIAdapterResult{Err: err})
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+credential.AccessToken)
	req.Header.Set("ChatGPT-Account-Id", credential.AccountID)
	req.Header.Set("Originator", "hub")
	resp, err := doCodexRequest(ctx, a.Client, req)
	if err != nil {
		return codexResult(OpenAIAdapterResult{Err: err, ErrorCode: "upstream_unavailable", ErrorMessage: err.Error()})
	}
	defer resp.Body.Close()
	responseBody, err := readBounded(resp.Body, 32<<20)
	if err != nil {
		return codexResult(OpenAIAdapterResult{Err: err, ErrorCode: "upstream_unavailable", ErrorMessage: err.Error()})
	}
	if resp.StatusCode < 400 && isEventStreamResponse(resp.Header, responseBody) {
		responseBody, err = collectCodexTerminalResponse(responseBody)
		if err != nil {
			return codexResult(OpenAIAdapterResult{Err: err, ErrorCode: "upstream_unavailable", ErrorMessage: err.Error()})
		}
	}
	if resp.StatusCode >= 400 {
		responseBody, _, _ = normalizeUpstreamErrorResponse(resp.StatusCode, responseBody, resp.Status)
	}
	result := OpenAIAdapterResult{StatusCode: resp.StatusCode, Header: resp.Header.Clone(), Body: responseBody}
	result.Header.Set("Content-Type", "application/json")
	var payload map[string]any
	if json.Unmarshal(responseBody, &payload) == nil {
		if usage, ok := payload["usage"].(map[string]any); ok {
			result.RawUsage, _ = json.Marshal(usage)
			result.InputTokens = numberPointer(usage, "input_tokens", "prompt_tokens")
			result.OutputTokens = numberPointer(usage, "output_tokens", "completion_tokens")
		}
		if upstreamError, ok := payload["error"].(map[string]any); ok {
			result.ErrorCode, _ = upstreamError["code"].(string)
			result.ErrorMessage, _ = upstreamError["message"].(string)
		}
	}
	return CodexAdapterResult{OpenAIAdapterResult: result, UpdatedCredential: updated}
}

func normalizeUpstreamErrorResponse(status int, body []byte, fallback string) ([]byte, string, string) {
	var payload map[string]any
	_ = json.Unmarshal(body, &payload)
	if upstreamError, ok := payload["error"].(map[string]any); ok {
		message := firstNonEmpty(stringValue(upstreamError["message"]), fallback)
		code := firstNonEmpty(stringValue(upstreamError["code"]), "upstream_error")
		errorType := firstNonEmpty(stringValue(upstreamError["type"]), "upstream_error")
		if status >= 400 && status < 500 && stringValue(upstreamError["type"]) == "" {
			errorType = "invalid_request_error"
		}
		upstreamError["message"], upstreamError["type"], upstreamError["code"] = message, errorType, code
		if _, exists := upstreamError["param"]; !exists {
			upstreamError["param"] = ""
		}
		normalized, err := json.Marshal(map[string]any{"error": upstreamError})
		if err != nil {
			return body, message, code
		}
		return normalized, message, code
	}
	message := firstNonEmpty(stringValue(payload["detail"]), strings.TrimSpace(string(body)), fallback)
	param, code := "", "upstream_error"
	const unsupportedPrefix = "Unsupported parameter:"
	if index := strings.Index(message, unsupportedPrefix); index >= 0 {
		param = strings.TrimSpace(message[index+len(unsupportedPrefix):])
		code = "unsupported_parameter"
	}
	errorType := "upstream_error"
	if status >= 400 && status < 500 {
		errorType = "invalid_request_error"
	}
	normalized, err := json.Marshal(map[string]any{"error": map[string]any{"message": message, "type": errorType, "code": code, "param": param}})
	if err != nil {
		return body, message, code
	}
	return normalized, message, code
}

func isEventStreamResponse(header http.Header, body []byte) bool {
	return strings.Contains(strings.ToLower(header.Get("Content-Type")), "text/event-stream") ||
		bytes.HasPrefix(bytes.TrimSpace(body), []byte("data:")) || bytes.HasPrefix(bytes.TrimSpace(body), []byte("event:"))
}

func collectCodexTerminalResponse(stream []byte) ([]byte, error) {
	scanner := bufio.NewScanner(bytes.NewReader(stream))
	scanner.Buffer(make([]byte, 64*1024), 8<<20)
	var outputText, refusal strings.Builder
	type pendingFunctionCall struct {
		Item      map[string]any
		Arguments strings.Builder
	}
	functionCalls := make(map[int]*pendingFunctionCall)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var event map[string]any
		if json.Unmarshal([]byte(payload), &event) != nil {
			continue
		}
		eventType, _ := event["type"].(string)
		switch eventType {
		case "response.output_text.delta":
			outputText.WriteString(stringValue(event["delta"]))
		case "response.refusal.delta":
			refusal.WriteString(stringValue(event["delta"]))
		case "response.output_item.added", "response.output_item.done":
			item, _ := event["item"].(map[string]any)
			if item["type"] == "function_call" {
				index := int(numberValue(event["output_index"]))
				call := functionCalls[index]
				if call == nil {
					call = &pendingFunctionCall{}
					functionCalls[index] = call
				}
				call.Item = item
				if arguments := stringValue(item["arguments"]); arguments != "" {
					call.Arguments.Reset()
					call.Arguments.WriteString(arguments)
				}
			}
		case "response.function_call_arguments.delta":
			index := int(numberValue(event["output_index"]))
			call := functionCalls[index]
			if call == nil {
				call = &pendingFunctionCall{}
				functionCalls[index] = call
			}
			call.Arguments.WriteString(stringValue(event["delta"]))
		}
		if eventType != "response.completed" && eventType != "response.failed" && eventType != "response.incomplete" {
			continue
		}
		response, ok := event["response"].(map[string]any)
		if !ok {
			return nil, errors.New("Codex terminal stream event did not include a response")
		}
		if !codexResponseHasMessageContent(response) && (outputText.Len() > 0 || refusal.Len() > 0) {
			content := make([]any, 0, 2)
			if outputText.Len() > 0 {
				content = append(content, map[string]any{"type": "output_text", "text": outputText.String()})
			}
			if refusal.Len() > 0 {
				content = append(content, map[string]any{"type": "refusal", "refusal": refusal.String()})
			}
			output, _ := response["output"].([]any)
			response["output"] = append(output, map[string]any{"type": "message", "role": "assistant", "content": content})
		}
		output, _ := response["output"].([]any)
		indexes := make([]int, 0, len(functionCalls))
		for index := range functionCalls {
			indexes = append(indexes, index)
		}
		sort.Ints(indexes)
		for _, index := range indexes {
			call := functionCalls[index]
			if call == nil || codexResponseHasFunctionCall(output, stringValue(call.Item["call_id"])) {
				continue
			}
			item := map[string]any{"type": "function_call", "call_id": call.Item["call_id"], "name": call.Item["name"], "arguments": call.Arguments.String()}
			if item["arguments"] == "" {
				item["arguments"] = "{}"
			}
			output = append(output, item)
		}
		response["output"] = output
		return json.Marshal(response)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("Codex stream ended without a terminal response event")
}

func numberValue(value any) float64 {
	number, _ := value.(float64)
	return number
}

func codexResponseHasFunctionCall(output []any, callID string) bool {
	for _, raw := range output {
		item, _ := raw.(map[string]any)
		if item["type"] == "function_call" && (callID == "" || stringValue(item["call_id"]) == callID) {
			return true
		}
	}
	return false
}

func codexResponseHasMessageContent(response map[string]any) bool {
	output, _ := response["output"].([]any)
	for _, raw := range output {
		item, _ := raw.(map[string]any)
		if item["type"] != "message" {
			continue
		}
		if content, ok := item["content"].([]any); ok && len(content) > 0 {
			return true
		}
	}
	return false
}

func (a *CodexAdapter) ExecuteChat(ctx context.Context, input CodexAdapterRequest) CodexAdapterResult {
	businessModel := ""
	var original map[string]any
	if err := json.Unmarshal(input.Body, &original); err != nil {
		return codexResult(OpenAIAdapterResult{Err: ErrInvalidRequest, ErrorCode: "invalid_request"})
	}
	businessModel, _ = original["model"].(string)
	converted, err := normalizeCodexResponsesBoundary(input.Body, true)
	if err != nil {
		return codexResult(OpenAIAdapterResult{Err: err, ErrorCode: "invalid_request"})
	}
	input.Body = converted
	result := a.Execute(ctx, input)
	if result.Err != nil || result.StatusCode >= 400 {
		return result
	}
	var response map[string]any
	if json.Unmarshal(result.Body, &response) == nil && response["status"] == "failed" {
		result.StatusCode = http.StatusBadGateway
		return result
	}
	result.Body, err = convertCodexResponseToChat(result.Body, businessModel)
	if err != nil {
		result.Err, result.ErrorCode, result.ErrorMessage = err, "upstream_unavailable", err.Error()
	}
	return result
}

func (a *CodexAdapter) OpenStream(ctx context.Context, input CodexStreamRequest) CodexStreamResult {
	var credential CodexCredential
	if err := json.Unmarshal(input.Account.Credential, &credential); err != nil {
		return CodexStreamResult{Err: err, ErrorCode: "upstream_reauth_required"}
	}
	credential, updated, err := a.validCredential(ctx, input.Account.ID, credential)
	if err != nil {
		return CodexStreamResult{Err: err, ErrorCode: codexRefreshErrorCode(err)}
	}
	businessModel := ""
	includeUsage := false
	var body map[string]any
	if err := json.Unmarshal(input.Body, &body); err != nil {
		return CodexStreamResult{Err: ErrInvalidRequest}
	}
	businessModel, _ = body["model"].(string)
	if streamOptions, ok := body["stream_options"].(map[string]any); ok {
		includeUsage, _ = streamOptions["include_usage"].(bool)
	}
	requestBody, err := normalizeCodexResponsesBoundary(input.Body, input.Endpoint == GatewayEndpointChat)
	if err != nil {
		return CodexStreamResult{Err: err, ErrorCode: "invalid_request"}
	}
	body = nil
	if err := json.Unmarshal(requestBody, &body); err != nil {
		return CodexStreamResult{Err: err}
	}
	body["model"], body["stream"] = normalizeOpenAICodexModel(input.UpstreamModel), true
	if _, exists := body["store"]; !exists {
		body["store"] = false
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return CodexStreamResult{Err: err}
	}
	baseURL := strings.TrimRight(credential.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultCodexBackendURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/responses", bytes.NewReader(encoded))
	if err != nil {
		return CodexStreamResult{Err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+credential.AccessToken)
	req.Header.Set("ChatGPT-Account-Id", credential.AccountID)
	req.Header.Set("Originator", "hub")
	resp, err := doCodexRequest(ctx, a.Client, req)
	if err != nil {
		return CodexStreamResult{Err: err}
	}
	if input.Endpoint == GatewayEndpointChat && resp.StatusCode < 400 {
		resp.Body = rewriteSSEModels(transformOpenAICodexResponsesStreamToChat(resp.Body, businessModel, includeUsage), businessModel)
	}
	if resp.StatusCode < 400 {
		resp.Header.Set("Content-Type", "text/event-stream; charset=utf-8")
	}
	return CodexStreamResult{Response: resp, UpdatedCredential: updated}
}

func normalizeCodexResponsesBoundary(body []byte, requireMessages bool) ([]byte, error) {
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, ErrInvalidRequest
	}
	_, hasMessages := request["messages"]
	_, hasInput := request["input"]
	if hasMessages && hasInput {
		return nil, errors.New("request cannot contain both messages and input")
	}
	if requireMessages && !hasMessages {
		return nil, ErrInvalidRequest
	}
	normalized := body
	injectJSONInputInstruction := false
	if hasMessages {
		injectJSONInputInstruction = needsJSONResponseInstruction(request)
		encoded, err := json.Marshal(request)
		if err != nil {
			return nil, err
		}
		normalized, err = convertChatRequestToResponses(encoded)
		if err != nil {
			return nil, err
		}
	}
	request = nil
	if err := json.Unmarshal(normalized, &request); err != nil {
		return nil, ErrInvalidRequest
	}
	if injectJSONInputInstruction {
		input, _ := request["input"].([]any)
		request["input"] = append([]any{map[string]any{
			"type": "message",
			"role": "user",
			"content": []any{map[string]any{
				"type": "input_text",
				"text": "You must respond with valid JSON only.",
			}},
		}}, input...)
	}
	stripUnsupportedCodexTokenLimits(request)
	request["store"] = false
	if _, ok := request["instructions"]; !ok {
		request["instructions"] = ""
	}
	return json.Marshal(request)
}

func doCodexRequest(ctx context.Context, client *http.Client, request *http.Request) (*http.Response, error) {
	return client.Do(request)
}

func rewriteSSEModels(source io.ReadCloser, model string) io.ReadCloser {
	reader, writer := io.Pipe()
	go func() {
		defer source.Close()
		defer writer.Close()
		scanner := bufio.NewScanner(source)
		scanner.Buffer(make([]byte, 64*1024), 8<<20)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data:") {
				payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				var event map[string]any
				if json.Unmarshal([]byte(payload), &event) == nil {
					if _, ok := event["model"]; ok {
						event["model"] = model
					}
					if encoded, err := json.Marshal(event); err == nil {
						line = "data: " + string(encoded)
					}
				}
			}
			if _, err := io.WriteString(writer, line+"\n"); err != nil {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			_ = writer.CloseWithError(err)
		}
	}()
	return reader
}

func convertCodexResponseToChat(body []byte, businessModel string) ([]byte, error) {
	var response map[string]any
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	message := map[string]any{"role": "assistant", "content": ""}
	content := strings.Builder{}
	toolCalls := make([]any, 0)
	annotations := make([]any, 0)
	reasoning := make([]any, 0)
	refusal := ""
	if output, ok := response["output"].([]any); ok {
		for _, raw := range output {
			item, _ := raw.(map[string]any)
			switch item["type"] {
			case "message":
				if parts, ok := item["content"].([]any); ok {
					for _, rawPart := range parts {
						part, _ := rawPart.(map[string]any)
						if part["type"] == "output_text" {
							content.WriteString(stringValue(part["text"]))
							if values, ok := part["annotations"].([]any); ok {
								annotations = append(annotations, values...)
							}
						} else if part["type"] == "refusal" {
							refusal = stringValue(part["refusal"])
						}
					}
				}
			case "function_call":
				toolCalls = append(toolCalls, map[string]any{"id": item["call_id"], "type": "function", "function": map[string]any{"name": item["name"], "arguments": item["arguments"]}})
			case "reasoning":
				reasoning = append(reasoning, item)
			}
		}
	}
	message["content"] = content.String()
	if len(annotations) > 0 {
		message["annotations"] = annotations
	}
	if refusal != "" {
		message["refusal"] = refusal
	}
	if len(reasoning) > 0 {
		message["reasoning"] = reasoning
	}
	finishReason := "stop"
	if len(toolCalls) > 0 {
		message["tool_calls"], message["content"], finishReason = toolCalls, nil, "tool_calls"
	} else if response["status"] == "incomplete" {
		finishReason = "length"
	} else if refusal != "" {
		finishReason = "content_filter"
	}
	chat := map[string]any{"id": response["id"], "object": "chat.completion", "created": time.Now().Unix(), "model": businessModel, "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finishReason}}}
	if value, ok := response["status"]; ok {
		chat["status"] = value
	}
	if value, ok := response["incomplete_details"]; ok {
		chat["incomplete_details"] = value
	}
	if usage, ok := response["usage"].(map[string]any); ok {
		chat["usage"] = codexChatUsage(usage)
	}
	return json.Marshal(chat)
}

func codexResult(result OpenAIAdapterResult) CodexAdapterResult {
	return CodexAdapterResult{OpenAIAdapterResult: result}
}

func (a *CodexAdapter) validCredential(ctx context.Context, accountID string, credential CodexCredential) (CodexCredential, []byte, error) {
	refreshLock := a.acquireAccountRefreshLock(accountID)
	refreshLock.mu.Lock()
	defer func() {
		refreshLock.mu.Unlock()
		a.releaseAccountRefreshLock(accountID, refreshLock)
	}()
	a.mu.Lock()
	if cached, ok := a.credentials[accountID]; ok && cached.ExpiresAt.After(credential.ExpiresAt) {
		credential = cached
	}
	a.mu.Unlock()
	if credential.ExpiresAt.IsZero() || credential.ExpiresAt.After(time.Now().Add(3*time.Minute)) {
		return credential, nil, nil
	}
	if credential.RefreshToken == "" || credential.TokenURL == "" {
		return credential, nil, &CodexRefreshError{Permanent: true, Message: "codex credential expired and cannot be refreshed"}
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {credential.RefreshToken}, "client_id": {openAICodexOAuthClientID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, credential.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return credential, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := a.Client.Do(req)
	if err != nil {
		return credential, nil, &CodexRefreshError{Message: "codex token refresh transport failed"}
	}
	defer resp.Body.Close()
	body, err := readBounded(resp.Body, 1<<20)
	if err != nil {
		return credential, nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &failure)
		permanent := resp.StatusCode == http.StatusBadRequest && (failure.Error == "invalid_grant" || failure.Error == "invalid_token")
		return credential, nil, &CodexRefreshError{Permanent: permanent, Message: "codex token refresh rejected"}
	}
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    any    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.AccessToken == "" {
		return credential, nil, errors.New("codex token refresh response is invalid")
	}
	credential.AccessToken = payload.AccessToken
	if payload.RefreshToken != "" {
		credential.RefreshToken = payload.RefreshToken
	}
	if payload.IDToken != "" {
		credential.IDToken = payload.IDToken
	}
	seconds := int64(3600)
	switch value := payload.ExpiresIn.(type) {
	case float64:
		seconds = int64(value)
	case string:
		if parsed, parseErr := strconv.ParseInt(value, 10, 64); parseErr == nil {
			seconds = parsed
		}
	}
	credential.ExpiresAt = time.Now().UTC().Add(time.Duration(seconds) * time.Second)
	a.cacheCredential(accountID, credential)
	encoded, err := json.Marshal(credential)
	return credential, encoded, err
}

func codexRefreshErrorCode(err error) string {
	var refreshErr *CodexRefreshError
	if errors.As(err, &refreshErr) && refreshErr.Permanent {
		return "upstream_reauth_required"
	}
	return "upstream_unavailable"
}

func (a *CodexAdapter) acquireAccountRefreshLock(accountID string) *codexRefreshLock {
	a.mu.Lock()
	defer a.mu.Unlock()
	lock := a.refreshLock[accountID]
	if lock == nil {
		lock = &codexRefreshLock{}
		a.refreshLock[accountID] = lock
	}
	lock.refs++
	return lock
}

func (a *CodexAdapter) releaseAccountRefreshLock(accountID string, lock *codexRefreshLock) {
	a.mu.Lock()
	defer a.mu.Unlock()
	lock.refs--
	if lock.refs == 0 && a.refreshLock[accountID] == lock {
		delete(a.refreshLock, accountID)
	}
}

func (a *CodexAdapter) DiscardCredential(accountID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.credentials, accountID)
}

func (a *CodexAdapter) cacheCredential(accountID string, credential CodexCredential) {
	a.mu.Lock()
	defer a.mu.Unlock()
	const maxCachedCredentials = 1024
	if len(a.credentials) >= maxCachedCredentials {
		now := time.Now()
		for id, cached := range a.credentials {
			if !cached.ExpiresAt.After(now) {
				delete(a.credentials, id)
			}
		}
	}
	if len(a.credentials) >= maxCachedCredentials {
		var oldestID string
		var oldestExpiry time.Time
		for id, cached := range a.credentials {
			if oldestID == "" || cached.ExpiresAt.Before(oldestExpiry) {
				oldestID, oldestExpiry = id, cached.ExpiresAt
			}
		}
		delete(a.credentials, oldestID)
	}
	a.credentials[accountID] = credential
}

func newChatGPTMessageID() string { return uuid.NewString() }

func readBounded(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if int64(len(body)) > limit {
		return nil, errors.New("upstream response exceeds size limit")
	}
	return body, err
}
