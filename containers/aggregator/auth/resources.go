package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
)

type Scope string

const (
	READ   Scope = "urn:example:css:modes:read"
	MODIFY Scope = "urn:example:css:modes:modify"
	CREATE Scope = "urn:example:css:modes:create"
	DELETE Scope = "urn:example:css:modes:delete"
)

type Resource struct {
	Name           string  `json:"name"`
	ResourceScopes []Scope `json:"resource_scopes"`
}

// Global map to track resource names → IDs
var resourceMap = struct {
	sync.RWMutex
	data map[string]string
}{data: make(map[string]string)}

// CreateResource sends a POST request and stores the resource location
func CreateResource(name string, scopes []Scope) {
	umaConfig, err := getUMAConfig(AS_ISSUER)
	if err != nil {
		panic(fmt.Sprintf("Failed to fetch UMA config: %v", err))
	}

	resourceMap.RLock()
	location, exists := resourceMap.data[name]
	resourceMap.RUnlock()

	method := "POST"
	endpoint := umaConfig.ResourceRegistrationEndpoint
	if exists {
		// Use the stored location for PUT
		endpoint = location
		method = "PUT"
	}

	resource := Resource{
		Name:           name,
		ResourceScopes: scopes,
	}

	body, err := json.Marshal(resource)
	if err != nil {
		panic(fmt.Sprintf("Failed to marshal resource: %v", err))
	}

	req, _ := http.NewRequest(method, endpoint, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := DoSignedRequest(req)
	if err != nil {
		fmt.Println("Request failed:", err)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// Use the Location header as the resource identifier
		locationHeader := resp.Header.Get("Location")
		if locationHeader == "" {
			fmt.Println("❌ Response missing 'Location' header")
			return
		}

		resourceMap.Lock()
		resourceMap.data[name] = locationHeader
		resourceMap.Unlock()

		action := "created"
		if exists {
			action = "updated"
		}
		fmt.Printf("✅ Resource '%s' %s successfully at %s\n", name, action, locationHeader)
	} else {
		fmt.Printf("❌ Failed to create/update resource '%s' (status %d): %s\n", name, resp.StatusCode, string(respBody))
	}
}

// DeleteResource deletes a resource by name
func DeleteResource(name string) {
	resourceMap.RLock()
	deleteURL, exists := resourceMap.data[name]
	resourceMap.RUnlock()

	if !exists {
		fmt.Printf("❌ Resource '%s' not found in local map\n", name)
		return
	}

	req, err := http.NewRequest("DELETE", deleteURL, bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	if err != nil {
		fmt.Println("Failed to create DELETE request:", err)
		return
	}

	req.Header.Set("Accept", "application/json")

	resp, err := DoSignedRequest(req)
	if err != nil {
		fmt.Println("Request failed:", err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		fmt.Printf("✅ Resource '%s' deleted successfully\n", name)

		// Remove from local map
		resourceMap.Lock()
		delete(resourceMap.data, name)
		resourceMap.Unlock()
	} else {
		fmt.Printf("❌ Failed to delete resource '%s' (status %d): %s\n", name, resp.StatusCode, string(body))
	}
}

// DeleteAllResources deletes all resources tracked in the local resourceMap
func DeleteAllResources() {
	resourceMap.RLock()
	names := make([]string, 0, len(resourceMap.data))
	for name := range resourceMap.data {
		names = append(names, name)
	}
	resourceMap.RUnlock()

	for _, name := range names {
		DeleteResource(name)
	}
}
