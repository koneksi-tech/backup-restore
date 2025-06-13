package auth

import (
	"testing"
)

func TestNewClient(t *testing.T) {
	tests := []struct {
		name        string
		baseURL     string
		wantBaseURL string
	}{
		{
			name:        "Default base URL",
			baseURL:     "",
			wantBaseURL: DefaultBaseURL,
		},
		{
			name:        "Custom base URL",
			baseURL:     "https://custom.example.com",
			wantBaseURL: "https://custom.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(tt.baseURL)
			if client.baseURL != tt.wantBaseURL {
				t.Errorf("NewClient() baseURL = %v, want %v", client.baseURL, tt.wantBaseURL)
			}
			if client.httpClient == nil {
				t.Error("NewClient() httpClient is nil")
			}
		})
	}
}

func TestRegisterRequest(t *testing.T) {
	middleName := "Middle"
	suffix := "Jr."
	
	req := RegisterRequest{
		FirstName:       "John",
		MiddleName:      &middleName,
		LastName:        "Doe",
		Suffix:          &suffix,
		Email:           "john.doe@example.com",
		Password:        "password123",
		ConfirmPassword: "password123",
	}

	if req.FirstName != "John" {
		t.Errorf("Expected FirstName to be 'John', got '%s'", req.FirstName)
	}
	if req.MiddleName == nil || *req.MiddleName != "Middle" {
		t.Error("Expected MiddleName to be 'Middle'")
	}
	if req.LastName != "Doe" {
		t.Errorf("Expected LastName to be 'Doe', got '%s'", req.LastName)
	}
	if req.Suffix == nil || *req.Suffix != "Jr." {
		t.Error("Expected Suffix to be 'Jr.'")
	}
}