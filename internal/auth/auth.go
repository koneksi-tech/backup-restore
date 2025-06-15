package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// Config holds authentication configuration
type Config struct {
	BaseURL string
}

// Client handles authentication operations
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new authentication client
func NewClient(baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// RegisterRequest represents user registration data
type RegisterRequest struct {
	FirstName       string  `json:"first_name"`
	MiddleName      *string `json:"middle_name"`
	LastName        string  `json:"last_name"`
	Suffix          *string `json:"suffix"`
	Email           string  `json:"email"`
	Password        string  `json:"password"`
	ConfirmPassword string  `json:"confirm_password"`
}

// LoginRequest represents login credentials
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// VerifyRequest represents account verification data
type VerifyRequest struct {
	VerificationCode string `json:"verification_code"`
}

// CreateKeyRequest represents API key creation data
type CreateKeyRequest struct {
	Name string `json:"name"`
}

// RevokeKeyRequest represents API key revocation data
type RevokeKeyRequest struct {
	ClientID string `json:"client_id"`
}

// MFASetupResponse represents MFA setup response
type MFASetupResponse struct {
	Secret string `json:"secret"`
	QRCode string `json:"qr_code"`
	URL    string `json:"url"`
}

// MFAEnableRequest represents MFA enable request
type MFAEnableRequest struct {
	OTP string `json:"otp"`
}

// MFALoginRequest represents step two of MFA login
type MFALoginRequest struct {
	LoginCode string `json:"login_code"`
	OTP       string `json:"otp"`
}

// MFADisableRequest represents MFA disable request
type MFADisableRequest struct {
	Password string `json:"password"`
}

// Register creates a new user account
func (c *Client) Register(req RegisterRequest) error {
	// Remove nil values for optional fields
	data := map[string]interface{}{
		"first_name":       req.FirstName,
		"last_name":        req.LastName,
		"email":            req.Email,
		"password":         req.Password,
		"confirm_password": req.ConfirmPassword,
	}

	if req.MiddleName != nil && *req.MiddleName != "" {
		data["middle_name"] = *req.MiddleName
	} else {
		data["middle_name"] = nil
	}

	if req.Suffix != nil && *req.Suffix != "" {
		data["suffix"] = *req.Suffix
	} else {
		data["suffix"] = nil
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal registration data: %w", err)
	}

	resp, err := c.doRequest("POST", "/api/users/register", jsonData, "")
	if err != nil {
		return err
	}

	fmt.Println("Registration successful!")
	if data, ok := resp["data"].(map[string]interface{}); ok {
		if id, ok := data["id"].(string); ok {
			fmt.Printf("User ID: %s\n", id)
		}
		if email, ok := data["email"].(string); ok {
			fmt.Printf("Email: %s\n", email)
		}
	}
	fmt.Println("\nIMPORTANT: A verification code has been sent to your email.")
	fmt.Println("\nNext steps:")
	fmt.Println("1. Check your email for the verification code")
	fmt.Printf("2. Login: koneksi-backup auth login -e %s -p <your-password>\n", req.Email)
	fmt.Println("3. Verify: koneksi-backup auth verify <verification-code> -t <access-token>")

	return nil
}

// Login authenticates a user and returns access token
func (c *Client) Login(req LoginRequest) error {
	jsonData, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal login data: %w", err)
	}

	resp, err := c.doRequest("POST", "/api/tokens/request", jsonData, "")
	if err != nil {
		return err
	}

	// Check if MFA is required
	if data, ok := resp["data"].(map[string]interface{}); ok {
		// Check for MFA login code
		if loginCode, ok := data["login_code"].(string); ok {
			fmt.Println("MFA is enabled for this account.")
			fmt.Printf("\nLogin Code: %s\n", loginCode)
			fmt.Println("\nComplete login with your authenticator OTP:")
			fmt.Printf("  koneksi-backup auth login-mfa %s <otp-code>\n", loginCode)
			return nil
		}

		// Normal login success
		fmt.Println("Login successful!")
		
		if accessToken, ok := data["access_token"].(string); ok {
			fmt.Printf("\nAccess Token:\n%s\n", accessToken)
			fmt.Println("\nUse this token to create/revoke API keys:")
			fmt.Printf("  koneksi-backup auth create-key \"My API Key\" -t \"%s\"\n", accessToken)
		}

		// Save refresh token if provided
		if refreshToken, ok := data["refresh_token"].(string); ok {
			fmt.Printf("\nRefresh Token (save for later use):\n%s\n", refreshToken)
		}
	}

	return nil
}

// Verify verifies a user account with the verification code
func (c *Client) Verify(req VerifyRequest, authToken string) error {
	if authToken == "" {
		authToken = os.Getenv(EnvAuthToken)
		if authToken == "" {
			return fmt.Errorf("authentication token required. Use -t flag or set %s environment variable", EnvAuthToken)
		}
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request data: %w", err)
	}

	resp, err := c.doRequest("POST", "/api/users/verify-account", jsonData, authToken)
	if err != nil {
		return err
	}

	fmt.Println("Account verified successfully!")

	if data, ok := resp["data"].(map[string]interface{}); ok {
		if email, ok := data["email"].(string); ok {
			fmt.Printf("Email: %s\n", email)
		}
		if verified, ok := data["verified"].(bool); ok {
			fmt.Printf("Verified: %v\n", verified)
		}
	}

	fmt.Println("\nYou can now create API keys using:")
	fmt.Println("  koneksi-backup auth create-key \"My API Key\" -t <your-access-token>")

	return nil
}

// CreateKey creates a new API key
func (c *Client) CreateKey(req CreateKeyRequest, authToken string) error {
	if authToken == "" {
		authToken = os.Getenv(EnvAuthToken)
		if authToken == "" {
			return fmt.Errorf("authentication token required. Use -t flag or set %s environment variable", EnvAuthToken)
		}
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request data: %w", err)
	}
	fmt.Println(string(authToken))
	resp, err := c.doRequest("POST", "/api/service-accounts/generate", jsonData, authToken)
	if err != nil {
		fmt.Println("Failed to create API key:")
		return err
	}

	fmt.Printf("API Key '%s' created successfully!\n", req.Name)

	// Extract and display the API credentials
	if data, ok := resp["data"].(map[string]interface{}); ok {
		if clientID, ok := data["client_id"].(string); ok {
			fmt.Printf("\nClient ID:\n%s\n", clientID)
		}
		if clientSecret, ok := data["client_secret"].(string); ok {
			fmt.Printf("\nClient Secret (save this, it won't be shown again):\n%s\n", clientSecret)
		}

		fmt.Println("\nTo use these credentials with koneksi-backup:")
		fmt.Println("  export KONEKSI_API_CLIENT_ID=<client-id>")
		fmt.Println("  export KONEKSI_API_CLIENT_SECRET=<client-secret>")
		fmt.Println("\nOr add them to your config file (~/.koneksi-backup/config.yaml)")
	}

	return nil
}

// RevokeKey revokes an existing API key
func (c *Client) RevokeKey(req RevokeKeyRequest, authToken string) error {
	if authToken == "" {
		authToken = os.Getenv(EnvAuthToken)
		if authToken == "" {
			return fmt.Errorf("authentication token required. Use -t flag or set %s environment variable", EnvAuthToken)
		}
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request data: %w", err)
	}

	_, err = c.doRequest("DELETE", "/api/service-accounts/revoke", jsonData, authToken)
	if err != nil {
		return err
	}

	fmt.Printf("API Key '%s' has been revoked successfully.\n", req.ClientID)
	return nil
}

// doRequest performs an HTTP request and returns the response
func (c *Client) doRequest(method, endpoint string, body []byte, authToken string) (map[string]interface{}, error) {
	url := c.baseURL + endpoint
	req, err := http.NewRequest(method, url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errorResp map[string]interface{}
		if err := json.Unmarshal(respBody, &errorResp); err == nil {
			if msg, ok := errorResp["message"].(string); ok {
				return nil, fmt.Errorf("%s", msg)
			}
		}
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return result, nil
}

// SetupMFA initiates MFA setup and returns QR code and secret
func (c *Client) SetupMFA(authToken string) error {
	if authToken == "" {
		authToken = os.Getenv(EnvAuthToken)
		if authToken == "" {
			return fmt.Errorf("authentication token required. Use -t flag or set %s environment variable", EnvAuthToken)
		}
	}

	resp, err := c.doRequest("POST", "/api/settings/mfa/generate-otp", nil, authToken)
	if err != nil {
		return err
	}

	fmt.Println("MFA setup initiated successfully!")
	
	if data, ok := resp["data"].(map[string]interface{}); ok {
		if secret, ok := data["secret"].(string); ok {
			fmt.Printf("\nSecret Key: %s\n", secret)
			fmt.Println("Add this secret to your authenticator app (Google Authenticator, Authy, etc.)")
		}
		if qrCode, ok := data["qr_code"].(string); ok {
			fmt.Printf("\nQR Code URL: %s\n", qrCode)
			fmt.Println("Or scan this QR code with your authenticator app")
		}
		if url, ok := data["url"].(string); ok {
			fmt.Printf("\nManual Entry URL: %s\n", url)
		}
	}

	fmt.Println("\nAfter adding to your authenticator app, enable MFA with:")
	fmt.Println("  koneksi-auth mfa enable <otp-code> -t <access-token>")

	return nil
}

// EnableMFA enables MFA using the OTP from authenticator app
func (c *Client) EnableMFA(req MFAEnableRequest, authToken string) error {
	if authToken == "" {
		authToken = os.Getenv(EnvAuthToken)
		if authToken == "" {
			return fmt.Errorf("authentication token required. Use -t flag or set %s environment variable", EnvAuthToken)
		}
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request data: %w", err)
	}

	_, err = c.doRequest("POST", "/api/settings/mfa/enable", jsonData, authToken)
	if err != nil {
		return err
	}

	fmt.Println("MFA enabled successfully!")
	fmt.Println("\nIMPORTANT: Save your recovery codes in a safe place.")
	fmt.Println("You will now need to provide an OTP code when logging in.")

	return nil
}

// LoginWithMFA performs the second step of MFA login
func (c *Client) LoginWithMFA(req MFALoginRequest) error {
	jsonData, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request data: %w", err)
	}

	resp, err := c.doRequest("POST", "/api/tokens/verify-otp", jsonData, "")
	if err != nil {
		return err
	}

	fmt.Println("MFA login successful!")

	// Extract and display tokens
	if data, ok := resp["data"].(map[string]interface{}); ok {
		if accessToken, ok := data["access_token"].(string); ok {
			fmt.Printf("\nAccess Token:\n%s\n", accessToken)
			fmt.Println("\nUse this token to create/revoke API keys:")
			fmt.Printf("  koneksi-auth create-key \"My API Key\" -t \"%s\"\n", accessToken)
		}

		if refreshToken, ok := data["refresh_token"].(string); ok {
			fmt.Printf("\nRefresh Token (save for later use):\n%s\n", refreshToken)
		}
	}

	return nil
}

// DisableMFA disables MFA for the account
func (c *Client) DisableMFA(req MFADisableRequest, authToken string) error {
	if authToken == "" {
		authToken = os.Getenv(EnvAuthToken)
		if authToken == "" {
			return fmt.Errorf("authentication token required. Use -t flag or set %s environment variable", EnvAuthToken)
		}
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request data: %w", err)
	}

	_, err = c.doRequest("POST", "/api/settings/mfa/disable", jsonData, authToken)
	if err != nil {
		return err
	}

	fmt.Println("MFA disabled successfully!")
	fmt.Println("Your account no longer requires OTP codes for login.")

	return nil
}
