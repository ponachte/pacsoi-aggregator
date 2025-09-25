package auth

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"

	"github.com/golang-jwt/jwt/v4"
)

// AS_ISSUER is read from environment variable
var AS_ISSUER string
var AS_DEFINED bool

func init() {
	AS_ISSUER, AS_DEFINED = os.LookupEnv("AS_ISSUER")
}

type Permission struct {
	ResourceID     string  `json:"resource_id"`
	ResourceScopes []Scope `json:"resource_scopes"`
}

// UmaClaims extends the standard JWT claims with an optional "permissions" array.
type UmaClaims struct {
	jwt.RegisteredClaims
	Permissions []Permission `json:"permissions,omitempty"`
}

func AuthorizeRequest(response http.ResponseWriter, request *http.Request, extraPermissions []Permission) bool {
	// ✅ If AS_ISSUER is not set, always authorize
	if !AS_DEFINED {
		return true
	}

	// check if Authorization header is present, if not create ticket
	if request.Header.Get("Authorization") == "" {
		// create ticket
		// Get the scheme
		scheme := "http"
		if request.TLS != nil {
			scheme = "https"
		}
		// Get the complete URL
		completeURL := fmt.Sprintf("%s://%s%s", scheme, request.Host, request.Pattern)

		ticketPermissions := make(map[string][]Scope)
		switch request.Method {
		case "POST", "PUT", "DELETE":
			ticketPermissions[completeURL] = []Scope{MODIFY}
		case "GET", "HEAD":
			ticketPermissions[completeURL] = []Scope{READ}
		default:
			fmt.Println(fmt.Errorf("method not supported by authorization: %v", request.Method).Error())
			http.Error(response, "method not supported by authorization", http.StatusMethodNotAllowed)
			return false
		}
		for _, permission := range extraPermissions {
			ticketPermissions[permission.ResourceID] = permission.ResourceScopes
		}
		ticket, err := fetchTicket(ticketPermissions, AS_ISSUER)
		if err != nil {
			fmt.Println(fmt.Errorf("error while retrieving ticket: %v", err).Error())
			http.Error(response, "error while retrieving ticket", http.StatusUnauthorized)
			return false
		}
		if ticket == "" {
			return true
		}
		response.Header().Set(
			"WWW-Authenticate",
			fmt.Sprintf(`UMA as_uri="%s", ticket="%s"`, AS_ISSUER, ticket),
		)
		response.WriteHeader(http.StatusUnauthorized)
		return false
	}

	permission, err := verifyTicket(request.Header.Get("Authorization"), []string{AS_ISSUER})
	if err != nil {
		fmt.Println("Error while verifying ticket: ", err)
		return false
	}

	for _, perm := range permission {
		if resourceMap.data[perm.ResourceID] == request.URL.Path {
			switch request.Method {
			case "POST", "PUT", "DELETE":
				return contains(perm.ResourceScopes, READ)
			case "GET", "HEAD":
				return contains(perm.ResourceScopes, MODIFY)
			default:
				fmt.Println(fmt.Errorf("authorize request method not supported: %v", request.Method).Error())
				return false
			}
		}
	}
	return false
}

func fetchTicket(permissions map[string][]Scope, issuer string) (string, error) {
	config, err := getUMAConfig(issuer)
	if err != nil {
		return "", fmt.Errorf("error while retrieving ticket: %v", err)
	}

	var body []Permission
	for target, scopes := range permissions {
		body = append(body, Permission{
			ResourceID:     target,
			ResourceScopes: scopes,
		})
	}

	jsonData, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", config.PermissionEndpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := DoSignedRequest(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	println(resp.StatusCode)

	if resp.StatusCode == http.StatusOK {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
		println(string(bodyBytes))
		return "", nil
	}

	if resp.StatusCode != http.StatusCreated {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
		bodyString := string(bodyBytes)
		return "", fmt.Errorf(
			"error while retrieving UMA Ticket: Received status %d with message \"%s\" from '%s'",
			resp.StatusCode,
			bodyString,
			config.PermissionEndpoint,
		)
	}

	var jsonResponse map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&jsonResponse); err != nil {
		return "", err
	}

	ticket, ok := jsonResponse["ticket"].(string)
	if !ok || ticket == "" {
		return "", errors.New("invalid response from UMA AS: missing or invalid 'ticket'")
	}

	return ticket, nil
}

type JWK struct {
	Kty string      `json:"kty"`
	Kid string      `json:"kid"`
	Alg string      `json:"alg,omitempty"`
	Use string      `json:"use,omitempty"`
	N   string      `json:"n,omitempty"` // Modulus
	E   string      `json:"e,omitempty"` // Exponent
	X   string      `json:"x,omitempty"`
	Y   string      `json:"y,omitempty"`
	Crv interface{} `json:"crv,omitempty"`
}

type JWKS struct {
	Keys []JWK `json:"keys"`
}

func verifyTicket(token string, validIssuers []string) ([]Permission, error) {
	payloadMap, err := decodeJwtPayload(token)
	if err != nil {
		return nil, fmt.Errorf("error decoding JWT: %w", err)
	}

	issVal, ok := payloadMap["iss"].(string)
	if !ok || issVal == "" {
		return nil, errors.New(`the JWT does not contain an "iss" parameter`)
	}

	hasValidIssuer := false
	for _, issuer := range validIssuers {
		if issuer == issVal {
			hasValidIssuer = true
		}
	}
	if !hasValidIssuer {
		return nil, errors.New(`the JWT wasn't issued by one of the target owners' issuers`)
	}

	config, err := getUMAConfig(issVal)
	if err != nil {
		return nil, fmt.Errorf("error fetching UMA config: %w", err)
	}

	// Parse and validate with our chosen public key.
	// We'll also check the issuer in the claims.
	claims := &UmaClaims{}
	parser := jwt.NewParser()
	parsedToken, err := parser.ParseWithClaims(token, claims, func(t *jwt.Token) (interface{}, error) {
		return fetchAndSelectKey(config.JwksUri, "TODO")

		// Auth server doesn't have kid's so we just return the first key
		/*
			// We'll look at t.Header["kid"] to figure out which key to use
			kidVal, hasKid := t.Header["kid"]
			if !hasKid {
				return nil, errors.New("token header missing 'kid'")
			}
			kid, ok := kidVal.(string)
			if !ok {
				return nil, fmt.Errorf("'kid' in header is not a string: %v", kidVal)
			}

			// Now fetch the JWKS from config.JwksUri and pick the correct public key.
			pubKey, err := fetchAndSelectKey(config.jwksUri, kid)
			if err != nil {
				return nil, fmt.Errorf("fetchAndSelectKey error: %w", err)
			}
			return pubKey, nil
		*/
	})

	if err != nil {
		return nil, err
	}

	if !parsedToken.Valid {
		return nil, errors.New("invalid token signature or claims")
	}

	if claims.Issuer != issVal {
		return nil, fmt.Errorf(`token "iss" (%s) does not match expected issuer (%s)`, claims.Issuer, issVal)
	}

	if claims.VerifyAudience("[solid]", true) {
		return nil, fmt.Errorf(`token "aud" (%s) does not match expected audience ("solid")`, claims.Audience)
	}

	// Check the permissions in the token
	if len(claims.Permissions) > 0 {
		for _, perm := range claims.Permissions {
			// resource_id must be a non-empty string
			if perm.ResourceID == "" {
				return nil, errors.New("invalid RPT: 'permissions[].resource_id' missing or not a string")
			}
			// resource_scopes must be an array of strings
			if len(perm.ResourceScopes) == 0 {
				return nil, errors.New("invalid RPT: 'permissions[].resource_scopes' missing or empty")
			}
			// Optionally check each scope is non-empty if needed
		}
	}

	// If we get here, the token is valid, and (if present) 'permissions' is well-formed.
	return claims.Permissions, nil
}

func fetchAndSelectKey(jwksUri, kid string) (interface{}, error) {
	// 1) Fetch the JWKS JSON
	resp, err := http.Get(jwksUri)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch JWKS from %s: %w", jwksUri, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JWKS endpoint returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read JWKS body: %w", err)
	}

	var jwks JWKS
	if err := json.Unmarshal(body, &jwks); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JWKS: %w", err)
	}

	if len(jwks.Keys) == 0 {
		return nil, errors.New("no keys found in JWKS")
	}

	return parsePublicKeyFromJWK(jwks.Keys[0])

	// the authentication server currently doesn't support kid's so we just return the first key
	/*

		// 3) Find the key with matching kid
		for _, jwk := range jwks.Keys {
			if jwk.Kid == kid {
				// We found the correct JWK. Let's parse it as an RSA key.
				return parseRSAPublicKeyFromJWK(jwk)
			}
		}

		return nil, fmt.Errorf("no matching key found for kid=%s in JWKS", kid)
	*/
}

func parsePublicKeyFromJWK(jwk JWK) (interface{}, error) {
	switch jwk.Kty {
	case "RSA":
		return parseRSAPublicKeyFromJWK(jwk)
	case "EC":
		return parseECPublicKeyFromJWK(jwk)
	default:
		return nil, fmt.Errorf("unsupported key type: %s", jwk.Kty)
	}
}

func parseRSAPublicKeyFromJWK(jwk JWK) (*rsa.PublicKey, error) {
	if jwk.Kty != "RSA" {
		return nil, fmt.Errorf("expected RSA kty but got %s", jwk.Kty)
	}
	nBytes, err := base64.RawURLEncoding.DecodeString(jwk.N)
	if err != nil {
		return nil, fmt.Errorf("failed to decode 'n' in JWK: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(jwk.E)
	if err != nil {
		return nil, fmt.Errorf("failed to decode 'e' in JWK: %w", err)
	}

	// Convert eBytes to int
	var eInt int
	for _, b := range eBytes {
		eInt = eInt<<8 | int(b)
	}

	pubKey := &rsa.PublicKey{
		N: bytesToBigInt(nBytes),
		E: eInt,
	}
	return pubKey, nil
}

func parseECPublicKeyFromJWK(jwk JWK) (*ecdsa.PublicKey, error) {
	xBytes, err := base64.RawURLEncoding.DecodeString(jwk.X)
	if err != nil {
		return nil, fmt.Errorf("failed to decode 'x' in JWK: %w", err)
	}
	yBytes, err := base64.RawURLEncoding.DecodeString(jwk.Y)
	if err != nil {
		return nil, fmt.Errorf("failed to decode 'y' in JWK: %w", err)
	}

	var curve elliptic.Curve
	switch jwk.Crv {
	case "P-256":
		curve = elliptic.P256()
	case "P-384":
		curve = elliptic.P384()
	case "P-521":
		curve = elliptic.P521()
	default:
		return nil, fmt.Errorf("unsupported curve: %s", jwk.Crv)
	}

	pubKey := &ecdsa.PublicKey{
		Curve: curve,
		X:     bytesToBigInt(xBytes),
		Y:     bytesToBigInt(yBytes),
	}
	return pubKey, nil
}

func bytesToBigInt(b []byte) *big.Int {
	bi := new(big.Int)
	bi.SetBytes(b)
	return bi
}

func decodeJwtPayload(tokenString string) (map[string]interface{}, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) < 2 {
		return nil, errors.New("invalid JWT format (missing segments)")
	}
	// The second part is the payload
	payloadSegment := parts[1]

	decoded, err := base64.RawURLEncoding.DecodeString(payloadSegment)
	if err != nil {
		// Some libraries use regular base64 with or without padding; you might need to handle that
		return nil, fmt.Errorf("failed to base64-decode JWT payload: %w", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return nil, fmt.Errorf("failed to JSON-decode JWT payload: %w", err)
	}
	return payload, nil
}
