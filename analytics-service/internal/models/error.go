package models

// ApiErrorResponse represents the uniform API error response format
// as defined in docs/API_SPEC.md (Section 8: Error Handling).
type ApiErrorResponse struct {
	Message string            `json:"message"`
	Code    string            `json:"code"`
	Details map[string]string `json:"details,omitempty"`
}
