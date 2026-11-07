package config

// Executor is an isolated, machine-only mTLS listener. It has no user-token mode.
// ClientCertSHA256 contains SHA256 digests of the complete DER leaf certificate,
// not SPKI digests. Multiple pins permit a deliberate certificate rotation.
type Executor struct {
	Enabled          bool     `yaml:"enabled"`
	Addr             string   `yaml:"addr"`
	ServerCert       string   `yaml:"server_cert"`
	ServerKey        string   `yaml:"server_key"`
	ClientCA         string   `yaml:"client_ca"`
	ClientCertSHA256 []string `yaml:"client_cert_sha256"`
}
