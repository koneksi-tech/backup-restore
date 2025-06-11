package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"time"

	"go.uber.org/zap"
)

type Client struct {
	BaseURL      string
	ClientID     string
	ClientSecret string
	DirectoryID  string
	HttpClient   *http.Client
	logger       *zap.Logger
	retryCount   int
}

type ErrorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

type HealthResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

type FileUploadRequest struct {
	FileName string `json:"file_name"`
	FilePath string `json:"file_path"`
	FileSize int64  `json:"file_size"`
	Checksum string `json:"checksum"`
}

type FileUploadResponse struct {
	FileID    string    `json:"file_id"`
	FileName  string    `json:"file_name"`
	Size      int64     `json:"size"`
	UploadedAt time.Time `json:"uploaded_at"`
	Status    string    `json:"status"`
}

type DirectoryCreateRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type DirectoryResponse struct {
	DirectoryID string `json:"directory_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

func NewClient(baseURL, clientID, clientSecret, directoryID string, timeout time.Duration, retryCount int, logger *zap.Logger) *Client {
	return &Client{
		BaseURL:      baseURL,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		DirectoryID:  directoryID,
		HttpClient: &http.Client{
			Timeout: timeout,
		},
		logger:     logger,
		retryCount: retryCount,
	}
}

func (c *Client) HealthCheck(ctx context.Context) error {
	resp, err := c.doRequest(ctx, "GET", "/api/check-health", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.parseError(resp)
	}

	var health HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return fmt.Errorf("failed to decode health response: %w", err)
	}

	c.logger.Info("API health check successful", zap.String("status", health.Status))
	return nil
}

func (c *Client) UploadFile(ctx context.Context, filePath string, fileData io.Reader, size int64, checksum string) (*FileUploadResponse, error) {
	// Using the correct files endpoint
	endpoint := "/api/clients/v1/files"
	
	// Create multipart form data
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	
	// Add file field
	fileName := filepath.Base(filePath)
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}
	
	// Copy file data
	if _, err := io.Copy(part, fileData); err != nil {
		return nil, fmt.Errorf("failed to copy file data: %w", err)
	}
	
	// Close writer to finalize the form
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}
	
	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+endpoint, &buf)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	
	// Set headers
	req.Header.Set("Client-ID", c.ClientID)
	req.Header.Set("Client-Secret", c.ClientSecret)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	
	// Debug log headers
	c.logger.Debug("upload request headers",
		zap.String("Client-ID", c.ClientID),
		zap.Bool("hasSecret", c.ClientSecret != ""),
		zap.String("Content-Type", writer.FormDataContentType()),
	)
	
	// Add directory_id query parameter if provided
	if c.DirectoryID != "" {
		req.URL.RawQuery = fmt.Sprintf("directory_id=%s", c.DirectoryID)
	}
	
	// Execute request
	resp, err := c.HttpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		// Log response for debugging
		body, _ := io.ReadAll(resp.Body)
		c.logger.Error("upload failed", 
			zap.Int("status", resp.StatusCode),
			zap.String("response", string(body)),
		)
		resp.Body = io.NopCloser(bytes.NewReader(body))
		return nil, c.parseError(resp)
	}

	// Parse response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	
	// Log the raw response for debugging
	c.logger.Debug("upload response", zap.String("body", string(respBody)))
	
	// Parse the actual response
	var apiResp struct {
		Data struct {
			ID          string `json:"id"`
			ContentType string `json:"content_type"`
			DirectoryID string `json:"directory_id"`
			Hash        string `json:"hash"`
			Name        string `json:"name"`
			Size        int    `json:"size"`
		} `json:"data"`
		Message string `json:"message"`
		Status  string `json:"status"`
	}
	
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	
	// Create response from API data
	// Use ID if available, otherwise fall back to hash
	fileID := apiResp.Data.ID
	if fileID == "" {
		fileID = apiResp.Data.Hash
	}
	
	uploadResp := &FileUploadResponse{
		FileID:     fileID,
		FileName:   apiResp.Data.Name,
		Size:       int64(apiResp.Data.Size),
		UploadedAt: time.Now(),
		Status:     apiResp.Status,
	}

	return uploadResp, nil
}

// GetFileIDByHash queries the directory to find the file ID by its hash
func (c *Client) GetFileIDByHash(ctx context.Context, hash string) (string, error) {
	if c.DirectoryID == "" {
		return "", fmt.Errorf("directory ID not set")
	}
	
	endpoint := fmt.Sprintf("/api/clients/v1/directories/%s", c.DirectoryID)
	resp, err := c.doRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return "", c.parseError(resp)
	}
	
	var dirResp struct {
		Data struct {
			Files []struct {
				ID   string `json:"id"`
				Hash string `json:"hash"`
			} `json:"files"`
		} `json:"data"`
	}
	
	if err := json.NewDecoder(resp.Body).Decode(&dirResp); err != nil {
		return "", fmt.Errorf("failed to decode directory response: %w", err)
	}
	
	// Find file by hash
	for _, file := range dirResp.Data.Files {
		if file.Hash == hash {
			return file.ID, nil
		}
	}
	
	return "", fmt.Errorf("file with hash %s not found in directory", hash)
}

func (c *Client) CreateDirectory(ctx context.Context, name, description string) (*DirectoryResponse, error) {
	endpoint := "/api/clients/v1/directories"
	
	reqBody := DirectoryCreateRequest{
		Name:        name,
		Description: description,
	}
	
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	
	resp, err := c.doRequest(ctx, "POST", endpoint, bytes.NewReader(jsonData))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, c.parseError(resp)
	}
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	
	// Parse the response
	var apiResp struct {
		Data struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			CreatedAt   string `json:"created_at"`
		} `json:"data"`
		Message string `json:"message"`
		Status  string `json:"status"`
	}
	
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	
	// Parse time
	createdAt, _ := time.Parse(time.RFC3339, apiResp.Data.CreatedAt)
	
	return &DirectoryResponse{
		DirectoryID: apiResp.Data.ID,
		Name:        apiResp.Data.Name,
		Description: apiResp.Data.Description,
		CreatedAt:   createdAt,
	}, nil
}

func (c *Client) DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, error) {
	// Try different endpoint formats
	endpoint := fmt.Sprintf("/api/clients/v1/files/%s", fileID)
	
	c.logger.Debug("downloading file", 
		zap.String("fileID", fileID),
		zap.String("endpoint", endpoint),
	)
	
	resp, err := c.doRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		// If the standard endpoint fails, try with /download suffix
		if resp.StatusCode == http.StatusBadRequest {
			endpoint = fmt.Sprintf("/api/clients/v1/files/%s/download", fileID)
			c.logger.Debug("trying alternative endpoint", zap.String("endpoint", endpoint))
			
			resp2, err2 := c.doRequest(ctx, "GET", endpoint, nil)
			if err2 != nil {
				return nil, c.parseError(resp)
			}
			if resp2.StatusCode == http.StatusOK {
				return resp2.Body, nil
			}
			resp2.Body.Close()
		}
		return nil, c.parseError(resp)
	}
	
	// Return the response body - caller is responsible for closing it
	return resp.Body, nil
}

func (c *Client) GetPeers(ctx context.Context) ([]interface{}, error) {
	resp, err := c.doRequest(ctx, "GET", "/api/peers", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}

	var peers []interface{}
	if err := json.NewDecoder(resp.Body).Decode(&peers); err != nil {
		return nil, fmt.Errorf("failed to decode peers response: %w", err)
	}

	return peers, nil
}

func (c *Client) doRequest(ctx context.Context, method, endpoint string, body io.Reader) (*http.Response, error) {
	url := c.BaseURL + endpoint
	
	var lastErr error
	for i := 0; i <= c.retryCount; i++ {
		if i > 0 {
			// Exponential backoff
			time.Sleep(time.Duration(i*i) * time.Second)
			c.logger.Info("retrying request", zap.String("url", url), zap.Int("attempt", i+1))
		}

		req, err := http.NewRequestWithContext(ctx, method, url, body)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Client-ID", c.ClientID)
		req.Header.Set("Client-Secret", c.ClientSecret)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.HttpClient.Do(req)
		if err != nil {
			lastErr = err
			c.logger.Error("request failed", zap.String("url", url), zap.Error(err))
			continue
		}

		// Don't retry on client errors
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return resp, nil
		}

		// Retry on server errors
		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("server error: %d", resp.StatusCode)
			resp.Body.Close()
			continue
		}

		return resp, nil
	}

	return nil, fmt.Errorf("request failed after %d attempts: %w", c.retryCount+1, lastErr)
}

func (c *Client) parseError(resp *http.Response) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("status %d: failed to read error response", resp.StatusCode)
	}

	// Log raw error response
	c.logger.Debug("API error response", 
		zap.Int("status", resp.StatusCode),
		zap.String("body", string(body)),
	)

	// Try to parse as JSON error
	var errResp ErrorResponse
	if err := json.Unmarshal(body, &errResp); err != nil {
		// Try alternative error format
		var altErr map[string]interface{}
		if err := json.Unmarshal(body, &altErr); err == nil {
			if msg, ok := altErr["message"].(string); ok {
				return fmt.Errorf("API error (status %d): %s", resp.StatusCode, msg)
			}
			if msg, ok := altErr["error"].(string); ok {
				return fmt.Errorf("API error (status %d): %s", resp.StatusCode, msg)
			}
		}
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	if errResp.Error != "" {
		return fmt.Errorf("API error %s: %s", errResp.Code, errResp.Error)
	}
	
	return fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
}