package llm

import (
	"errors"
	"net/http"
)

var ErrInvalidRequest = errors.New("invalid request")

type Account struct {
	ID         string
	Credential []byte
}
type GatewayEndpoint string

const GatewayEndpointChat GatewayEndpoint = "chat"
const GatewayEndpointResponses GatewayEndpoint = "responses"

type OpenAIAdapterResult struct {
	StatusCode                int
	Header                    http.Header
	Body                      []byte
	InputTokens, OutputTokens *int64
	RawUsage                  []byte
	ErrorCode, ErrorMessage   string
	Err                       error
}
