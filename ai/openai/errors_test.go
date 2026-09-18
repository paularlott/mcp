package openai

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

func TestResponseError_UnmarshalJSON_APIErrorObject(t *testing.T) {
	var re ResponseError
	data := []byte(`{"type":"invalid_request_error","message":"bad request"}`)
	if err := json.Unmarshal(data, &re); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if re.APIError == nil {
		t.Fatal("expected APIError to be set")
	}
	if re.APIError.Type != "invalid_request_error" {
		t.Errorf("Type = %q", re.APIError.Type)
	}
	if re.Error() != re.APIError.Error() {
		t.Errorf("Error() = %q", re.Error())
	}
}

func TestResponseError_UnmarshalJSON_String(t *testing.T) {
	var re ResponseError
	data := []byte(`"a plain string error"`)
	if err := json.Unmarshal(data, &re); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if re.Message != "a plain string error" {
		t.Errorf("Message = %q", re.Message)
	}
	if re.Error() != "a plain string error" {
		t.Errorf("Error() = %q", re.Error())
	}
}

func TestResponseError_UnmarshalJSON_Null(t *testing.T) {
	var re ResponseError
	if err := json.Unmarshal([]byte("null"), &re); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !re.IsNil() {
		t.Error("expected IsNil() = true for null")
	}
}

func TestResponseError_UnmarshalJSON_Invalid(t *testing.T) {
	var re ResponseError
	// A JSON number is neither an object nor a string nor null.
	err := re.UnmarshalJSON([]byte("12345"))
	if err == nil {
		t.Fatal("expected error for invalid format")
	}
}

func TestResponseError_IsNil(t *testing.T) {
	re := ResponseError{}
	if !re.IsNil() {
		t.Error("expected IsNil() = true for zero value")
	}
	re.Message = "x"
	if re.IsNil() {
		t.Error("expected IsNil() = false when Message set")
	}
}

func TestAPIError_Error_Formatting(t *testing.T) {
	tests := []struct {
		name string
		err  *APIError
		want string
	}{
		{"code and param", &APIError{Type: "t", Code: "c", Message: "m", Param: "p"}, "openai: t (c): m (param: p)"},
		{"code only", &APIError{Type: "t", Code: "c", Message: "m"}, "openai: t (c): m"},
		{"param only", &APIError{Type: "t", Message: "m", Param: "p"}, "openai: t: m (param: p)"},
		{"plain", &APIError{Type: "t", Message: "m"}, "openai: t: m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAPIError_IsTokenLimit(t *testing.T) {
	if !(&APIError{Code: "context_length_exceeded"}).IsTokenLimit() {
		t.Error("expected true for context_length_exceeded")
	}
	if !(&APIError{Code: "max_tokens_exceeded"}).IsTokenLimit() {
		t.Error("expected true for max_tokens_exceeded")
	}
	if (&APIError{Code: "other"}).IsTokenLimit() {
		t.Error("expected false for other code")
	}
}

func TestAPIError_IsInvalidRequest(t *testing.T) {
	if !(&APIError{StatusCode: http.StatusBadRequest}).IsInvalidRequest() {
		t.Error("expected true for 400 status")
	}
	if !(&APIError{Type: "invalid_request_error"}).IsInvalidRequest() {
		t.Error("expected true for type")
	}
	if (&APIError{StatusCode: 200}).IsInvalidRequest() {
		t.Error("expected false")
	}
}

func TestAPIError_IsAuthentication(t *testing.T) {
	if !(&APIError{StatusCode: http.StatusUnauthorized}).IsAuthentication() {
		t.Error("expected true for 401")
	}
	if !(&APIError{Type: "authentication_error"}).IsAuthentication() {
		t.Error("expected true for type")
	}
	if (&APIError{StatusCode: 200}).IsAuthentication() {
		t.Error("expected false")
	}
}

func TestAPIError_IsPermission(t *testing.T) {
	if !(&APIError{StatusCode: http.StatusForbidden}).IsPermission() {
		t.Error("expected true for 403")
	}
	if !(&APIError{Type: "permission_error"}).IsPermission() {
		t.Error("expected true for type")
	}
	if (&APIError{StatusCode: 200}).IsPermission() {
		t.Error("expected false")
	}
}

func TestAPIError_IsNotFound(t *testing.T) {
	if !(&APIError{StatusCode: http.StatusNotFound}).IsNotFound() {
		t.Error("expected true for 404")
	}
	if !(&APIError{Type: "not_found_error"}).IsNotFound() {
		t.Error("expected true for type")
	}
	if (&APIError{StatusCode: 200}).IsNotFound() {
		t.Error("expected false")
	}
}

func TestNewTokenLimitError(t *testing.T) {
	e := NewTokenLimitError("too long")
	if e.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d", e.StatusCode)
	}
	if e.Code != "context_length_exceeded" {
		t.Errorf("Code = %q", e.Code)
	}
	if !e.IsTokenLimit() {
		t.Error("expected IsTokenLimit() = true")
	}
	if e.Message != "too long" {
		t.Errorf("Message = %q", e.Message)
	}
}

func TestNewMaxToolIterationsError(t *testing.T) {
	e := NewMaxToolIterationsError(5)
	if e.Iterations != 5 {
		t.Errorf("Iterations = %d", e.Iterations)
	}
	if e.Error() != "maximum tool call iterations (5) reached" {
		t.Errorf("Error() = %q", e.Error())
	}
}

func TestNewToolExecutionError(t *testing.T) {
	inner := errors.New("inner failure")
	e := NewToolExecutionError("mytool", "call_1", inner)
	if e.ToolName != "mytool" || e.ToolID != "call_1" {
		t.Errorf("e = %+v", e)
	}
	wantMsg := `tool "mytool" (id: call_1) execution failed: inner failure`
	if e.Error() != wantMsg {
		t.Errorf("Error() = %q, want %q", e.Error(), wantMsg)
	}
	if !errors.Is(e, inner) && errors.Unwrap(e) != inner {
		t.Errorf("Unwrap() = %v, want %v", errors.Unwrap(e), inner)
	}
}

func TestNewStreamError(t *testing.T) {
	inner := errors.New("stream broke")
	e := NewStreamError(inner)
	want := "streaming error: stream broke"
	if e.Error() != want {
		t.Errorf("Error() = %q, want %q", e.Error(), want)
	}
	if errors.Unwrap(e) != inner {
		t.Errorf("Unwrap() = %v, want %v", errors.Unwrap(e), inner)
	}
}
