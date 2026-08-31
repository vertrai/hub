package starthermes

import (
	"encoding/base64"
	"testing"
)

func TestResetWeixinRejectsInvalidOrIncompletePayload(t *testing.T) {
	if err := ResetWeixin("not-base64!"); err == nil {
		t.Fatal("invalid Base64 should fail")
	}
	incomplete := base64.RawURLEncoding.EncodeToString([]byte(`{"accountId":"account"}`))
	if err := ResetWeixin(incomplete); err == nil {
		t.Fatal("incomplete credentials should fail")
	}
}
