package config

import (
	"fmt"
	"io/ioutil"
	"log"
	"strings" // Import the strings package
	"time"

	"gopkg.in/yaml.v2"
)

// LogLevel defines the logging level.
// LogLevel definisce il livello di logging.
type LogLevel string

const (
	LogLevelDebug LogLevel = "DEBUG"
	LogLevelInfo  LogLevel = "INFO"
	// Add other levels if needed (e.g., WARN, ERROR)
	// Aggiungi altri livelli se necessario (es. WARN, ERROR)
)

// Config represents the application configuration structure.
// Config rappresenta la struttura della configurazione dell'applicazione.
type Config struct {
	EnableAuth bool `yaml:"enable_auth" json:"enable_auth"` // Enable/Disable authentication and authorization
	AzureAD    struct {
		TenantID     string   `yaml:"tenant_id" json:"tenant_id"`     // Microsoft Entra ID Tenant ID
		ClientID     string   `yaml:"client_id" json:"client_id"`     // Microsoft Entra ID Client ID
		ClientSecret string   `yaml:"client_secret" json:"client_secret"` // Microsoft Entra ID Client Secret
		RedirectURL  string   `yaml:"redirect_url" json:"redirect_url"`  // OAuth2 Redirect URL
		AllowedGroups []string `yaml:"allowed_groups" json:"allowed_groups"` // Application-level allowed groups (Optional)
	} `yaml:"azure_ad" json:"azure_ad"`
	// Nuovo campo per i gruppi di amministratori globali
	// New field for global admin groups
	GlobalAdminGroups []string `yaml:"global_admin_groups" json:"global_admin_groups"`

	// Ora gestiamo gli storage in modo unificato
	// Now we handle storages in a unified way
	Storages []StorageConfig `yaml:"storages" json:"storages"` // List of configured storages (local filesystems and blob storages)

	Pagination PaginationConfig `yaml:"pagination" json:"pagination"` // Pagination configuration
	Timeouts   TimeoutConfig    `yaml:"timeouts" json:"timeouts"`   // HTTP Server Timeout configuration

	// Aggiunto campo per l'intervallo di ping del client (in millisecondi)
	// Added field for client ping interval (in milliseconds)
	ClientPingIntervalMs int `yaml:"client_ping_interval_ms" json:"client_ping_interval_ms"`

	// Added field for logging level
	// Aggiunto campo per il livello di logging
	LogLevel string `yaml:"log_level" json:"log_level"`

	// Nuovo campo per il timeout di pulizia degli upload orfani (es. "10m" per 10 minuti)
	// New field for orphaned upload cleanup timeout (e.g., "10m" for 10 minutes)
	UploadCleanupTimeout string `yaml:"upload_cleanup_timeout" json:"upload_cleanup_timeout"`
}

// StorageConfig è una struct generica per rappresentare un qualsiasi tipo di storage configurato.
// Contiene i campi comuni e i campi specifici per tipo (YAML inline/embedding).
// StorageConfig is a generic struct to represent any configured storage type.
// It contains common fields and type-specific fields (YAML inline/embedding).
type StorageConfig struct {
	Name string `yaml:"name" json:"name"` // Unique display name for this storage instance
	Type string `yaml:"type" json:"type"` // Type of storage: "local" or "azure-blob"

	// Campi specifici per filesystem locale (embedded)
	// Fields specific to local filesystem (embedded)
	FilesystemConfig `yaml:",inline" json:",inline"` // Aggiunto tag json:",inline"

	// Campi specifici per Azure Blob Storage (embedded)
	// Fields specific to Azure Blob Storage (embedded)
	AzureBlobStorageConfig `yaml:",inline" json:",inline"` // Aggiunto tag json:",inline"

	// Permissions moved here to be common for all storage types
	// I permessi sono stati spostati qui per essere comuni a tutti i tipi di storage
	Permissions []Permission `yaml:"permissions" json:"permissions"` // Group -> permissions mapping for this specific storage instance
}

// FilesystemConfig represents the configuration specific to a local filesystem.
// È ora embedded in StorageConfig e non ha più il campo Name o Permissions.
// FilesystemConfig represents the configuration specific to a local filesystem.
// It is now embedded in StorageConfig and no longer has the Name or Permissions field.
type FilesystemConfig struct {
	Path string `yaml:"path" json:"path"` // Physical path on the server
	// Name and Permissions moved to StorageConfig
}

// AzureBlobStorageConfig represents the configuration specific to an Azure Blob Storage.
// È ora embedded in StorageConfig e non ha più il campo Name o Permissions.
// AzureBlobStorageConfig represents the configuration specific to an Azure Blob Storage.
// It is now embedded in StorageConfig and no longer has the Name or Permissions field.
type AzureBlobStorageConfig struct {
	ConnectionString string `yaml:"connection_string,omitempty" json:"connection_string,omitempty"` // Azure Storage Connection String (optional if using Identity)
	AccountName      string `yaml:"account_name,omitempty" json:"account_name,omitempty"`      // Azure Storage Account Name (optional if using ConnectionString)
	ContainerName    string `yaml:"container_name" json:"container_name"`              // Azure Blob Container Name to expose
	// Name and Permissions moved to StorageConfig
	// Consider adding fields for AAD auth (TenantID, ClientID, ClientSecret if not global AzureAD config) or Managed Identity config
}

// Permission defines the permissions for a group on a filesystem.
// Permission definisce i permessi per un gruppo su un filesystem.
type Permission struct {
	GroupID string `yaml:"group_id" json:"group_id"` // Microsoft Entra ID Group ID
	Access  string `yaml:"access" json:"access"`   // "read" or "write"
}

// PaginationConfig represents the pagination configuration.
// PaginationConfig rappresenta la configurazione della paginazione.
type PaginationConfig struct {
	ItemsPerPage int `yaml:"items_per_page" json:"items_per_page"` // Number of items per page
}

// TimeoutConfig represents the HTTP server timeout configuration.
// TimeoutConfig rappresenta la configurazione dei timeout del server HTTP.
type TimeoutConfig struct {
	ReadTimeout  string `yaml:"read_timeout" json:"read_timeout"`  // Read timeout for HTTP server
	WriteTimeout string `yaml:"write_timeout" json:"write_timeout"` // Write timeout for HTTP server
	IdleTimeout  string `yaml:"idle_timeout" json:"idle_timeout"`  // Idle timeout for HTTP server
}


var AppConfig Config // Global variable for the loaded configuration
// Variabile globale per la configurazione caricata

// CurrentLogLevel stores the parsed log level.
// CurrentLogLevel memorizza il livello di log parsificato.
var CurrentLogLevel LogLevel = LogLevelInfo // Default log level


// LoadConfig loads the configuration from the specified file.
// LoadConfig carica la configurazione dal file specificato.
func LoadConfig(filename string) error {
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		log.Printf("Error reading configuration file %s: %v", filename, err)
		return fmt.Errorf("error reading configuration file %s: %w", filename, err)
	}

	err = yaml.Unmarshal(data, &AppConfig)
	if err != nil {
		log.Printf("Error parsing configuration file %s: %v", filename, err)
		return fmt.Errorf("error parsing configuration file %s: %w", filename, err)
	}

	// Set default items_per_page if not specified or invalid
	// Imposta il valore di default per items_per_page se non specificato o non valido
	if AppConfig.Pagination.ItemsPerPage <= 0 {
		AppConfig.Pagination.ItemsPerPage = 50 // Default value
	}

	// Set default timeouts if not specified
	// Imposta i valori di default per i timeout se non specificati
	if AppConfig.Timeouts.ReadTimeout == "" {
		AppConfig.Timeouts.ReadTimeout = "5s"
	}
	// Imposta WriteTimeout e IdleTimeout solo se non specificati
	// Imposta WriteTimeout e IdleTimeout solo se non specificati
	if AppConfig.Timeouts.WriteTimeout == "0s" {
		// Usa il default del server Go (nessun timeout per Write)
		// Usa il default del server Go (nessun timeout per Write)
		AppConfig.Timeouts.WriteTimeout = "0s" // 0s significa nessun timeout
	}
	if AppConfig.Timeouts.IdleTimeout == "" {
		AppConfig.Timeouts.IdleTimeout = "120s" // Default Go http server idle timeout
	}

	// Set default client_ping_interval_ms if not specified or invalid
	// Imposta il valore di default per client_ping_interval_ms se non specificato o non valido
	if AppConfig.ClientPingIntervalMs <= 0 {
		AppConfig.ClientPingIntervalMs = 10000 // Default 10 seconds
	}

	// Set default for UploadCleanupTimeout if not specified
	// Imposta il valore di default per UploadCleanupTimeout se non specificato
	if AppConfig.UploadCleanupTimeout == "" {
		AppConfig.UploadCleanupTimeout = "10m" // Default 10 minutes
	}


	// Set and validate log level
	// Imposta e valida il livello di log
	switch strings.ToUpper(AppConfig.LogLevel) {
	case string(LogLevelDebug):
		CurrentLogLevel = LogLevelDebug
	case string(LogLevelInfo):
		CurrentLogLevel = LogLevelInfo
	default:
		log.Printf("Warning: Invalid log_level '%s' in config. Using default 'INFO'.", AppConfig.LogLevel)
		CurrentLogLevel = LogLevelInfo // Default to INFO if invalid
	}
	log.Printf("Current log level set to: %s", CurrentLogLevel)


	log.Printf("Configuration loaded successfully from %s", filename)

	// Log the loaded configuration in YAML format (only in DEBUG)
	// Logga la configurazione caricata in formato YAML (solo in DEBUG)
	if IsLogLevel(LogLevelDebug) {
		yamlData, marshalErr := yaml.Marshal(&AppConfig)
		if marshalErr != nil {
			log.Printf("Warning: Failed to marshal config to YAML for logging: %v", marshalErr)
		} else {
			log.Printf("Configurazione caricata (YAML):\n%s", string(yamlData))
		}
	}


	// Validate the configuration
	// Valida la configurazione
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

// GetTimeouts parses the timeout strings from the config and returns time.Duration values.
// GetTimeouts parsifica le stringhe dei timeout dalla configurazione e restituisce valori time.Duration.
func (c *Config) GetTimeouts() (readTimeout, writeTimeout, idleTimeout time.Duration, err error) {
	readTimeout, err = time.ParseDuration(c.Timeouts.ReadTimeout)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid read_timeout format: %w", err)
	}
	// Aggiungi un controllo per 0s per il write timeout che significa nessun timeout
	// Aggiungi un controllo per 0s per il write timeout che significa nessun timeout
	if c.Timeouts.WriteTimeout == "0s" {
		writeTimeout = 0 // Nessun timeout
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

// GetUploadCleanupTimeout parses the UploadCleanupTimeout string from the config and returns time.Duration.
func (c *Config) GetUploadCleanupTimeout() (time.Duration, error) {
	duration, err := time.ParseDuration(c.UploadCleanupTimeout)
	if err != nil {
		return 0, fmt.Errorf("invalid upload_cleanup_timeout format: %w", err)
	}
	return duration, nil
}


// validateConfig checks for mandatory fields and logical consistency.
// It returns a slice of errors found.
// validateConfig controlla i campi obbligatori e la consistenza logica.
// Restituisce una slice di errori trovati.
func validateConfig(cfg *Config) []error {
	var errors []error

	// Global mandatory fields check
	// Controllo campi obbligatori globali
	// EnableAuth is considered mandatory for clarity in the config file
	// EnableAuth è considerato obbligatorio per chiarezza nel file di configurazione
	// No check needed as bool has default false

	// Azure AD configuration is mandatory if authentication is enabled
	// La configurazione di Azure AD è obbligatoria se l'autenticazione è abilitata
	if cfg.EnableAuth {
		if cfg.AzureAD.TenantID == "" {
			errors = append(errors, fmt.Errorf("azure_ad.tenant_id is mandatory when enable_auth is true"))
		}
		if cfg.AzureAD.ClientID == "" {
			errors = append(errors, fmt.Errorf("azure_ad.client_id is mandatory when enable_auth is true"))
		}
		if cfg.AzureAD.ClientSecret == "" && !isWorkloadIdentityConfigured(cfg) {
			errors = append(errors, fmt.Errorf("azure_ad.client_secret is mandatory when enable_auth is true"))
		}
		if cfg.AzureAD.RedirectURL == "" {
			errors = append(errors, fmt.Errorf("azure_ad.redirect_url is mandatory when enable_auth is true"))
		}
	}

	// Storages list is mandatory, but can be empty
	// La lista Storages è obbligatoria, ma può essere vuota
	if cfg.Storages == nil {
		errors = append(errors, fmt.Errorf("storages list is mandatory"))
	}

	// Validate each storage configuration
	// Valida ogni configurazione di storage
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

		// Validate permissions format (optional, but good practice)
		// Valida il formato dei permessi (opzionale, ma buona pratica)
		for j, perm := range storageCfg.Permissions {
			if perm.GroupID == "" {
				errors = append(errors, fmt.Errorf("storages[%d].permissions[%d].group_id is mandatory", i, j))
			}
			if perm.Access == "" {
				errors = append(errors, fmt.Errorf("storages[%d].permissions[%d].access is mandatory", i, j))
			} else if perm.Access != "read" && perm.Access != "write" {
				errors = append(errors, fmt.Errorf("storages[%d].permissions[%d].access must be 'read' or 'write', got '%s'", i, j, perm.Access))
			}
		}
	}

	// Pagination validation (optional, defaults are handled)
	// Validazione paginazione (opzionale, i default sono gestiti)
	// if cfg.Pagination.ItemsPerPage <= 0 {
	// 	errors = append(errors, fmt.Errorf("pagination.items_per_page must be positive"))
	// }

	// Timeouts validation (optional, defaults are handled)
	// Validazione timeout (opzionale, i default sono gestiti)
	// You could add checks here if you have specific constraints on timeout values.

	return errors
}

// IsLogLevel checks if the current log level is at or above the specified level.
// IsLogLevel controlla se il livello di log corrente è uguale o superiore al livello specificato.
func IsLogLevel(level LogLevel) bool {
	switch CurrentLogLevel {
	case LogLevelDebug:
		return true // DEBUG includes DEBUG and INFO
	case LogLevelInfo:
		return level == LogLevelInfo // INFO includes only INFO
	default:
		return false // Should not happen with current levels
	}
}

// isWorkloadIdentityConfigured checks if Workload Identity is configured.
// It returns true if the client secret is empty, which is the indicator for Workload Identity.
func isWorkloadIdentityConfigured(cfg *Config) bool {
	return cfg.AzureAD.ClientSecret == ""
}
