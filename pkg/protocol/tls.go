package protocol

// TLSSNIConfig routes an untouched TLS stream by its visible ClientHello SNI.
// Certificates and TLS policy belong to the target service.
type TLSSNIConfig struct {
	Domain             string   `json:"domain"`
	AllowedSourceCIDRs []string `json:"allowed_source_cidrs"`
}
