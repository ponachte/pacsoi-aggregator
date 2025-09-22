package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type UmaConfig struct {
	JwksUri                      string `json:"jwks_uri"`
	Issuer                       string `json:"issuer"`
	PermissionEndpoint           string `json:"permission_endpoint"`
	IntrospectionEndpoint        string `json:"introspection_endpoint"`
	ResourceRegistrationEndpoint string `json:"resource_registration_endpoint"`
}

func getUMAConfig(issuer string) (UmaConfig, error) {
	resp, err := http.Get(fmt.Sprintf("%s/uma/.well-known/uma2-configuration", issuer))
	if err != nil {
		return UmaConfig{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return UmaConfig{}, err
	}

	var cfg UmaConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		return UmaConfig{}, err
	}

	return cfg, nil
}
