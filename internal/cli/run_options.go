package cli

import (
	"fmt"
	"strconv"
	"strings"

	hostruntime "github.com/MrQwerty13/Hostix/internal/runtime"
)

func parsePortBindings(values []string) ([]hostruntime.PortBinding, error) {
	bindings := make([]hostruntime.PortBinding, 0, len(values))
	for _, value := range values {
		parts := strings.Split(value, ":")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid port %q: expected HOST:CONTAINER", value)
		}

		containerValue := parts[1]
		protocol := "tcp"
		if portAndProtocol := strings.Split(containerValue, "/"); len(portAndProtocol) == 2 {
			containerValue = portAndProtocol[0]
			protocol = strings.ToLower(portAndProtocol[1])
		} else if len(portAndProtocol) > 2 {
			return nil, fmt.Errorf("invalid container port %q", parts[1])
		}
		if protocol != "tcp" && protocol != "udp" {
			return nil, fmt.Errorf("invalid protocol %q: expected tcp or udp", protocol)
		}

		hostPort, err := parsePort(parts[0])
		if err != nil {
			return nil, fmt.Errorf("invalid host port in %q: %w", value, err)
		}
		containerPort, err := parsePort(containerValue)
		if err != nil {
			return nil, fmt.Errorf("invalid container port in %q: %w", value, err)
		}

		bindings = append(bindings, hostruntime.PortBinding{
			HostPort:      hostPort,
			ContainerPort: containerPort,
			Protocol:      protocol,
		})
	}
	return bindings, nil
}

func parsePort(value string) (uint16, error) {
	number, err := strconv.ParseUint(value, 10, 16)
	if err != nil || number == 0 {
		return 0, fmt.Errorf("must be an integer between 1 and 65535")
	}
	return uint16(number), nil
}

func parseEnvironment(values []string) (map[string]string, error) {
	environment := make(map[string]string, len(values))
	for _, value := range values {
		key, item, ok := strings.Cut(value, "=")
		if !ok || !validEnvironmentKey(key) {
			return nil, fmt.Errorf("invalid environment variable %q: expected NAME=VALUE", value)
		}
		environment[key] = item
	}
	return environment, nil
}

func validEnvironmentKey(key string) bool {
	if key == "" {
		return false
	}
	for index, char := range key {
		letter := char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z'
		digit := char >= '0' && char <= '9'
		if char == '_' || letter || index > 0 && digit {
			continue
		}
		return false
	}
	return true
}
