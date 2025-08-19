package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

var aggregatorOwner = "https://pod.playground.solidlab.be/user1/profile/card#me"

// claim represents the structure of the UMA token request payload.
type claim struct {
	GrantType        string `json:"grant_type"`
	Ticket           string `json:"ticket"`
	ClaimToken       string `json:"claim_token"`
	ClaimTokenFormat string `json:"claim_token_format"`
}

var client = &http.Client{}

// Do performs an HTTP request and handles UMA authorization if needed.
// If the initial request is unauthorized, it performs the UMA flow to obtain an access token
// and retries the request with the token.
//
// Parameters:
//
//	req - the original HTTP request to send.
//
// Returns:
//
//	*http.Response - the final response, either from the initial or retried request.
//	error - any error encountered during the process.
func Do(req *http.Request) (*http.Response, error) {
	// Send the initial request
	unauthenticatedResp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	// If unauthorized, begin UMA flow
	if unauthenticatedResp.StatusCode == http.StatusUnauthorized {
		defer unauthenticatedResp.Body.Close()

		// Extract authorization server URI and ticket from WWW-Authenticate header
		asUri, ticket, err := getTicketInfo(unauthenticatedResp.Header.Get("WWW-Authenticate"))
		if err != nil {
			return nil, err
		}

		// Construct UMA token request payload
		jsonBody, err := json.Marshal(claim{
			GrantType:        "urn:ietf:params:oauth:grant-type:uma-ticket",
			Ticket:           ticket,
			ClaimToken:       url.QueryEscape(aggregatorOwner),
			ClaimTokenFormat: "urn:solidlab:uma:claims:formats:webid",
		})
		if err != nil {
			return nil, err
		}

		// Send token request to the authorization server
		authReps, err := client.Post(asUri+"/token", "application/json", bytes.NewReader(jsonBody))
		if err != nil {
			return nil, err
		}
		defer authReps.Body.Close()

		// If token request fails, return unauthorized response
		if authReps.StatusCode != http.StatusOK {
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Status:     http.StatusText(http.StatusUnauthorized),
				Header:     make(http.Header),
			}, nil
		}

		// Parse token response
		var asResponse map[string]string
		err = json.NewDecoder(authReps.Body).Decode(&asResponse)
		if err != nil {
			return nil, err
		}

		accessToken, ok := asResponse["access_token"]
		if !ok {
			return nil, fmt.Errorf("access_token not found in response")
		}
		tokenType, ok := asResponse["token_type"]
		if !ok {
			return nil, fmt.Errorf("token_type not found in response")
		}

		// Decode JWT payload (optional, for inspection or logging)
		decodedToken, err := parseJwt(accessToken)
		if err != nil {
			return nil, err
		}

		// Add Authorization header and retry the original request
		req.Header.Set("Authorization", fmt.Sprintf("%s %s", tokenType, decodedToken))
		return client.Do(req)
	}

	// If no authorization is needed, return the original response
	fmt.Println("No authorization needed")
	return unauthenticatedResp, nil
}

// getTicketInfo parses the WWW-Authenticate header to extract the authorization server URI and ticket.
//
// Parameters:
//
//	headerString - the WWW-Authenticate header value.
//
// Returns:
//
//	asUri - the authorization server URI.
//	ticket - the UMA ticket.
//	error - if parsing fails.
func getTicketInfo(headerString string) (string, string, error) {
	header := strings.TrimPrefix(headerString, "Bearer ")
	params := strings.Split(header, ", ")
	var asUri string
	var ticket string
	for _, param := range params {
		keyValue := strings.Split(param, "=")
		if len(keyValue) != 2 {
			return "", "", fmt.Errorf("invalid parameter: %s", param)
		}
		key := strings.ReplaceAll(keyValue[0], "\"", "")
		value := strings.ReplaceAll(keyValue[1], "\"", "")
		switch key {
		case "as_uri":
			asUri = value
		case "ticket":
			ticket = value
		default:
			return "", "", fmt.Errorf("unknown parameter: %s", key)
		}
	}
	return asUri, ticket, nil
}

// parseJwt decodes the payload of a JWT token.
//
// Parameters:
//
//	token - the JWT string.
//
// Returns:
//
//	map[string]interface{} - the decoded payload.
//	error - if decoding fails.
func parseJwt(token string) (map[string]interface{}, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format")
	}

	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}

	var payload map[string]interface{}
	err = json.Unmarshal(decoded, &payload)
	if err != nil {
		return nil, err
	}

	return payload, nil
}
