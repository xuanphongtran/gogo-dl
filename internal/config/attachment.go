package config

import (
	"fmt"
	"regexp"
)

func (c *Config) loadAttachments() error {
	var err error
	c.AttachmentUploadEnabled, err = parseBool("ATTACHMENT_UPLOAD_ENABLED", false)
	if err != nil {
		return fmt.Errorf("config: invalid ATTACHMENT_UPLOAD_ENABLED: %w", err)
	}
	if c.AttachmentUploadEnabled {
		return fmt.Errorf("config: attachment upload admission requires scanner integration and R2 capability verification")
	}
	c.R2AccountID = getEnv("R2_ACCOUNT_ID", "")
	c.R2Bucket = getEnv("R2_BUCKET", "")
	c.R2AccessKeyID = getEnv("R2_ACCESS_KEY_ID", "")
	c.R2SecretAccessKey = getEnv("R2_SECRET_ACCESS_KEY", "")
	if c.R2AccountID == "" && c.R2Bucket == "" && c.R2AccessKeyID == "" && c.R2SecretAccessKey == "" {
		return nil
	}
	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(c.R2AccountID) || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`).MatchString(c.R2Bucket) || c.R2AccessKeyID == "" || c.R2SecretAccessKey == "" {
		return fmt.Errorf("config: all R2 credentials and a valid account ID/bucket are required")
	}
	return nil
}
