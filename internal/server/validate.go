package server

// ValidateServerAddr validates the management panel server address.
// Returns an error if the address is invalid (must be http:// or https:// URL).
func ValidateServerAddr(addr string) error {
	_, err := validateServerAddr(addr)
	return err
}

// ValidateAllowedPorts validates the allowed port ranges string.
// Returns an error if any range is invalid.
// Format: comma-separated list of "start-end" or single port numbers.
// Example: "10000-11000,8080"
func ValidateAllowedPorts(raw string) error {
	_, err := parseAllowedPorts(raw)
	return err
}
