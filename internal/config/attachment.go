package config

import (
	"fmt"
	"regexp"

	"github.com/xuanphongtran/gogo-dl/internal/scannerapi"
)

func (c *Config) loadAttachments() error {
	var err error
	c.AttachmentUploadEnabled, err = parseBool("ATTACHMENT_UPLOAD_ENABLED", false)
	if err != nil {
		return fmt.Errorf("config: invalid ATTACHMENT_UPLOAD_ENABLED: %w", err)
	}
	c.R2AccountID = getEnv("R2_ACCOUNT_ID", "")
	c.R2Bucket = getEnv("R2_BUCKET", "")
	c.R2AccessKeyID = getEnv("R2_ACCESS_KEY_ID", "")
	c.R2SecretAccessKey = getEnv("R2_SECRET_ACCESS_KEY", "")
	if c.R2AccountID == "" && c.R2Bucket == "" && c.R2AccessKeyID == "" && c.R2SecretAccessKey == "" {
		return c.loadScanner()
	}
	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(c.R2AccountID) || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`).MatchString(c.R2Bucket) || c.R2AccessKeyID == "" || c.R2SecretAccessKey == "" {
		return fmt.Errorf("config: all R2 credentials and a valid account ID/bucket are required")
	}
	return c.loadScanner()
}

func (c *Config) loadScanner() error {
	var err error
	c.AttachmentScanEnabled, err = parseBool("ATTACHMENT_SCAN_ENABLED", false)
	if err != nil {
		return fmt.Errorf("config: invalid ATTACHMENT_SCAN_ENABLED: %w", err)
	}
	c.ScannerURL = getEnv("SCANNER_URL", "")
	c.ScannerAPIToken = getEnv("SCANNER_API_TOKEN", "")
	if c.ScannerURL != "" || c.ScannerAPIToken != "" {
		if err := scannerapi.Validate(c.ScannerURL, c.ScannerAPIToken); err != nil {
			return fmt.Errorf("config: scanner settings: %w", err)
		}
		if c.ScannerAPIToken == c.JWTAccessSecret || c.ScannerAPIToken == c.JWTRefreshSecret {
			return fmt.Errorf("config: scanner credential must differ from JWT secrets")
		}
	}
	if c.AttachmentUploadEnabled && !c.AttachmentScanEnabled {
		return fmt.Errorf("config: upload admission requires ATTACHMENT_SCAN_ENABLED")
	}
	if c.AttachmentScanEnabled && (c.R2AccountID == "" || c.ScannerURL == "" || c.ScannerAPIToken == "") {
		return fmt.Errorf("config: scan worker requires R2 and scanner settings")
	}
	return nil
}
