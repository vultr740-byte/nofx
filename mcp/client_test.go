package mcp

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestReadStreamContent_DeepSeekReasoner_ContentOnly(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"reasoning_content":"think...","content":"{\"ok\":true}"}}]}`,
		`data: [DONE]`,
		"",
	}, "\n")

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	got, err := readStreamContent(resp, ProviderDeepSeek, "deepseek-reasoner", 8000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != `{"ok":true}` {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestReadStreamContent_DeepSeekReasoner_ReasoningOnly_HintsMaxTokens(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"reasoning_content":"think...","content":""}}]}`,
		`data: [DONE]`,
		"",
	}, "\n")

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	_, err := readStreamContent(resp, ProviderDeepSeek, "deepseek-reasoner", 2000)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "reasoning_content") || !strings.Contains(msg, "AI_MAX_TOKENS") {
		t.Fatalf("unexpected error message: %q", msg)
	}
}

func TestReadStreamContent_NonOKStatus_ReturnsBody(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(strings.NewReader(`{"error":"bad request"}`)),
	}

	_, err := readStreamContent(resp, ProviderDeepSeek, "deepseek-reasoner", 2000)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "status 400") {
		t.Fatalf("expected status in error, got: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "bad request") {
		t.Fatalf("expected body in error, got: %q", err.Error())
	}
}

