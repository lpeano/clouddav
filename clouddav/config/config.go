package config

import (
	"fmt"
	"log"
	"os" // MODIFICA: Aggiunto import per os.ReadFile
	"strings"
	"time"

	"gopkg.in/yaml.v2"
)

// LogLevel defines the logging level.
type LogLevel string

const (
	LogLevelDebug LogLevel = "DEBUG"
	LogLevelInfo  LogLevel = "INFO"
)

// Config represents the application configuration structure.
type Config struct {
	EnableAuth bool `yaml:"enable_auth" json:"enable_auth"`
	AzureAD    struct {
		TenantID      string   `yaml:"tenant_id" json:"tenant_id"`
		ClientID      string   `yaml:"client_id" json:"client_id"`
		ClientSecret  string   `yaml:"client_secret" json:"client_secret"`
		RedirectURL   string   `yaml:"redirect_url" json:"redirect_url"`
		AllowedGroups []string `yaml:"allowed_groups" json:"allowed_groups"`
	} `yaml:"azure_ad" json:"azure_ad"`
	GlobalAdminGroups []string        `yaml:"global_admin_groups" json:"global_admin_groups"`
	Storages          []StorageConfig `yaml:"storages" json:"storages"`
	Pagination        PaginationConfig `yaml:"pagination" json:"pagination"`
	Timeouts          TimeoutConfig    `yaml:"timeouts" json:"timeouts"`
	ClientPingIntervalMs int `yaml:"client_ping_interval_ms" json:"client_ping_interval_ms"`
	LogLevel             string `yaml:"log_level" json:"log_level"`
	UploadCleanupTimeout string `yaml:"upload_cleanup_timeout" json:"upload_cleanup_timeout"`
}

// StorageConfig ... (come prima)
type StorageConfig struct {
	Name                   string       `yaml:"name" json:"name"`
	Type                   string       `yaml:"type" json:"type"`
	FilesystemConfig       `yaml:",inline" json:",inline"`
	AzureBlobStorageConfig `yaml:",inline" json:",inline"`
	Permissions            []Permission `yaml:"permissions" json:"permissions"`
}

// FilesystemConfig ... (come prima)
type FilesystemConfig struct {
	Path string `yaml:"path" json:"path"`
}

// AzureBlobStorageConfig ... (come prima)
type AzureBlobStorageConfig struct {
	ConnectionString string `yaml:"connection_string,omitempty" json:"connection_string,omitempty"`
	AccountName      string `yaml:"account_name,omitempty" json:"account_name,omitempty"`
	ContainerName    string `yaml:"container_name" json:"container_name"`
}

// Permission ... (come prima)
type Permission struct {
	GroupID string `yaml:"group_id" json:"group_id"` // Adesso si assume sia un nome di gruppo
	Access  string `yaml:"access" json:"access"`
}

// PaginationConfig ... (come prima)
type PaginationConfig struct {
	ItemsPerPage int `yaml:"items_per_page" json:"items_per_page"`
}

// TimeoutConfig ... (come prima)
type TimeoutConfig struct {
	ReadTimeout  string `yaml:"read_timeout" json:"read_timeout"`
	WriteTimeout string `yaml:"write_timeout" json:"write_timeout"`
	IdleTimeout  string `yaml:"idle_timeout" json:"idle_timeout"`
}

var AppConfig Config
var CurrentLogLevel LogLevel = LogLevelInfo

// LoadConfig loads the configuration from the specified file.
func LoadConfig(filename string) error {
	// MODIFICA: Sostituito ioutil.ReadFile con os.ReadFile
	data, err := os.ReadFile(filename)
	if err != nil {
		log.Printf("Error reading configuration file %s: %v", filename, err)
		return fmt.Errorf("error reading configuration file %s: %w", filename, err)
	}

	err = yaml.Unmarshal(data, &AppConfig)
	if err != nil {
		log.Printf("Error parsing configuration file %s: %v", filename, err)
		return fmt.Errorf("error parsing configuration file %s: %w", filename, err)
	}

	if AppConfig.Pagination.ItemsPerPage <= 0 {
		AppConfig.Pagination.ItemsPerPage = 50
	}
	if AppConfig.Timeouts.ReadTimeout == "" {
		AppConfig.Timeouts.ReadTimeout = "5s"
	}
	if AppConfig.Timeouts.WriteTimeout == "" { // "" significa usa default di Go, "0s" per nessun timeout
		AppConfig.Timeouts.WriteTimeout = "0s" // Default a nessun timeout esplicito
	}
	if AppConfig.Timeouts.IdleTimeout == "" {
		AppConfig.Timeouts.IdleTimeout = "120s"
	}
	if AppConfig.ClientPingIntervalMs <= 0 {
		AppConfig.ClientPingIntervalMs = 10000
	}
	if AppConfig.UploadCleanupTimeout == "" {
		AppConfig.UploadCleanupTimeout = "10m"
	}

	switch strings.ToUpper(AppConfig.LogLevel) {
	case string(LogLevelDebug):
		CurrentLogLevel = LogLevelDebug
	case string(LogLevelInfo):
		CurrentLogLevel = LogLevelInfo
	default:
		log.Printf("Warning: Invalid log_level '%s' in config. Using default 'INFO'.", AppConfig.LogLevel)
		CurrentLogLevel = LogLevelInfo
	}
	log.Printf("Current log level set to: %s", CurrentLogLevel)
	log.Printf("Configuration loaded successfully from %s", filename)

	if IsLogLevel(LogLevelDebug) {
		yamlData, marshalErr := yaml.Marshal(&AppConfig)
		if marshalErr != nil {
			log.Printf("Warning: Failed to marshal config to YAML for logging: %v", marshalErr)
		} else {
			log.Printf("Configurazione caricata (YAML):\n%s", string(yamlData))
		}
	}

	validationErrors := validateConfig(&AppConfig)
	if len(validationErrors) > 0 {
		log.Println("--- Errori di Validazione Configurazione ---")
		for _, ve := range validationErrors {
			log.Printf("Errore: %v", ve)
		}
		log.Println("------------------------------------------")
		return fmt.Errorf("configuration validation failed with %d errors", len(validationErrors))
	}
	return nil
}

// GetTimeouts ... (come prima, ma assicurati che WriteTimeout "0s" sia gestito correttamente)
func (c *Config) GetTimeouts() (readTimeout, writeTimeout, idleTimeout time.Duration, err error) {
	readTimeout, err = time.ParseDuration(c.Timeouts.ReadTimeout)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid read_timeout format: %w", err)
	}
	if c.Timeouts.WriteTimeout == "0s" { // "0s" significa nessun timeout per il server http
		writeTimeout = 0
	} else {
		writeTimeout, err = time.ParseDuration(c.Timeouts.WriteTimeout)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("invalid write_timeout format: %w", err)
		}
	}
	idleTimeout, err = time.ParseDuration(c.Timeouts.IdleTimeout)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid idle_timeout format: %w", err)
	}
	return readTimeout, writeTimeout, idleTimeout, nil
}

// GetUploadCleanupTimeout ... (come prima)
func (c *Config) GetUploadCleanupTimeout() (time.Duration, error) {
	duration, err := time.ParseDuration(c.UploadCleanupTimeout)
	if err != nil {
		return 0, fmt.Errorf("invalid upload_cleanup_timeout format: %w", err)
	}
	return duration, nil
}

// validateConfig ... (come prima)
func validateConfig(cfg *Config) []error {
	var errors []error
	if cfg.EnableAuth {
		if cfg.AzureAD.TenantID == "" {
			errors = append(errors, fmt.Errorf("azure_ad.tenant_id is mandatory when enable_auth is true"))
		}
		if cfg.AzureAD.ClientID == "" {
			errors = append(errors, fmt.Errorf("azure_ad.client_id is mandatory when enable_auth is true"))
		}
		// ClientSecret non è più strettamente obbligatorio se si usa Workload Identity
		// if cfg.AzureAD.ClientSecret == "" && !isWorkloadIdentityConfigured(cfg) { // isWorkloadIdentityConfigured non è definito qui
		// errors = append(errors, fmt.Errorf("azure_ad.client_secret is mandatory when enable_auth is true and not using Workload Identity"))
		// }
		if cfg.AzureAD.RedirectURL == "" {
			errors = append(errors, fmt.Errorf("azure_ad.redirect_url is mandatory when enable_auth is true"))
		}
	}
	if cfg.Storages == nil {
		errors = append(errors, fmt.Errorf("storages list is mandatory"))
	}
	for i, storageCfg := range cfg.Storages {
		if storageCfg.Name == "" {
			errors = append(errors, fmt.Errorf("storages[%d].name is mandatory", i))
		}
		if storageCfg.Type == "" {
			errors = append(errors, fmt.Errorf("storages[%d].type is mandatory", i))
		} else {
			switch storageCfg.Type {
			case "local":
				if storageCfg.Path == "" {
					errors = append(errors, fmt.Errorf("storages[%d].path is mandatory for type 'local'", i))
				}
			case "azure-blob":
				if storageCfg.ConnectionString == "" && storageCfg.AccountName == "" {
					errors = append(errors, fmt.Errorf("storages[%d] requires either connection_string or account_name for type 'azure-blob'", i))
				}
				if storageCfg.ContainerName == "" {
					errors = append(errors, fmt.Errorf("storages[%d].container_name is mandatory for type 'azure-blob'", i))
				}
			default:
				errors = append(errors, fmt.Errorf("storages[%d] has unknown type '%s'", i, storageCfg.Type))
			}
		}
		for j, perm := range storageCfg.Permissions {
			if perm.GroupID == "" { // GroupID ora si assume sia un nome
				errors = append(errors, fmt.Errorf("storages[%d].permissions[%d].group_id (group name) is mandatory", i, j))
			}
			if perm.Access == "" {
				errors = append(errors, fmt.Errorf("storages[%d].permissions[%d].access is mandatory", i, j))
			} else if perm.Access != "read" && perm.Access != "write" {
				errors = append(errors, fmt.Errorf("storages[%d].permissions[%d].access must be 'read' or 'write', got '%s'", i, j, perm.Access))
			}
		}
	}
	return errors
}

// IsLogLevel ... (come prima)
func IsLogLevel(level LogLevel) bool {
	switch CurrentLogLevel {
	case LogLevelDebug:
		return true 
	case LogLevelInfo:
		return level == LogLevelInfo 
	default:
		return false
	}
}

// isWorkloadIdentityConfigured (aggiunta funzione helper se necessaria, o rimuovi il check da validateConfig se non applicabile)
// func isWorkloadIdentityConfigured(cfg *Config) bool {
// 	// Implementa la logica per determinare se Workload Identity è configurato
// 	// Ad esempio, basandosi sulla presenza/assenza di ClientSecret e/o variabili d'ambiente specifiche.
// 	return cfg.AzureAD.ClientSecret == "" // Esempio semplificato
// }

