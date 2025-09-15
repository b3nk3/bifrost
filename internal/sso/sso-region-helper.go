/*
Copyright © 2025 Ben Szabo me@benszabo.co.uk
*/
package sso

import (
	"fmt"
	"net/http"
	"regexp"
)

// extractRegionFromSSO makes an HTTP request to the AWS SSO start URL
// and extracts the region from the Content-Security-Policy header
func ExtractRegionFromSSO(startURL string) (string, error) {
	// Create HTTP client
	client := &http.Client{
		// Don't follow redirects automatically
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Make HEAD request to get headers without body
	resp, err := client.Head(startURL)
	if err != nil {
		return "", fmt.Errorf("failed to make request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close() // Ignore error - this is cleanup
	}()

	// Get the Content-Security-Policy header
	csp := resp.Header.Get("Content-Security-Policy")
	if csp == "" {
		return "", fmt.Errorf("no Content-Security-Policy header found")
	}

	// Extract region from the report-uri in CSP
	// Looking for pattern: https://log.sso-portal.REGION.amazonaws.com/log
	re := regexp.MustCompile(`https://log\.sso-portal\.([a-z0-9-]+)\.amazonaws\.com/log`)
	matches := re.FindStringSubmatch(csp)

	if len(matches) < 2 {
		return "", fmt.Errorf("could not extract region from CSP header: %s", csp)
	}

	return matches[1], nil
}
