package api

import "time"

// SendMessageRequest represents the request body for sending a text message
type SendMessageRequest struct {
	Recipient string `json:"recipient"`
	Message   string `json:"message"`
}

// SendFileRequest represents the request body for sending a file with optional caption
type SendFileRequest struct {
	Recipient string `json:"recipient"`
	FilePath  string `json:"file_path"`
	Caption   string `json:"caption,omitempty"`
}

// SendFileWithMessageRequest represents the request body for sending a file + separate message
type SendFileWithMessageRequest struct {
	Recipient   string `json:"recipient"`
	FilePath    string `json:"file_path"`
	Message     string `json:"message"`
	VoucherType string `json:"voucher_type,omitempty"`
}

// SendMessageResponse represents the response for message sending endpoints
type SendMessageResponse struct {
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	MessageID string `json:"message_id,omitempty"`
}

// HealthResponse represents the response for the health check endpoint
type HealthResponse struct {
	Status        string    `json:"status"`
	Authenticated bool      `json:"authenticated"`
	LastPing      time.Time `json:"last_ping,omitempty"`
}

// StatusResponse represents the response for the detailed status endpoint
type StatusResponse struct {
	Connected     bool      `json:"connected"`
	Authenticated bool      `json:"authenticated"`
	PhoneNumber   string    `json:"phone_number,omitempty"`
	DeviceName    string    `json:"device_name,omitempty"`
	LastPing      time.Time `json:"last_ping,omitempty"`
	ServerAddress string    `json:"server_address,omitempty"`
}

// ErrorResponse represents a standard error response
type ErrorResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
	Code    int    `json:"code,omitempty"`
}
