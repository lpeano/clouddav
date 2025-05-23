package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"clouddav/auth"
	"clouddav/config"
)

// ItemInfo rappresenta le informazioni su un elemento (file o directory/blob virtuale) in uno storage.
type ItemInfo struct {
	Name    string      `json:"name"`
	IsDir   bool        `json:"is_dir"`
	Size    int64       `json:"size"`
	ModTime time.Time   `json:"mod_time"`
	Path    string      `json:"path"`
}

// ListItemsResponse è la struttura per la risposta del metodo ListItems.
type ListItemsResponse struct {
	Items        []ItemInfo `json:"items"`
	TotalItems   int        `json:"total_items"`
	Page         int        `json:"page"`
	ItemsPerPage int        `json:"items_per_page"`
}

// StorageProvider definisce l'interfaccia comune per l'interazione con diversi tipi di storage.
// I metodi di upload (InitiateUpload, WriteChunk, FinalizeUpload, CancelUpload, GetUploadedSize)
// NON sono inclusi in questa interfaccia perché la loro implementazione dipende fortemente
// dal tipo di storage e vengono gestiti specificamente negli handler HTTP.
type StorageProvider interface {
	Type() string
	Name() string

	ListItems(ctx context.Context, claims *auth.UserClaims, path string, page int, itemsPerPage int, nameFilter string, timestampFilter *time.Time) (*ListItemsResponse, error)
	GetItem(ctx context.Context, claims *auth.UserClaims, path string) (*ItemInfo, error)
	OpenReader(ctx context.Context, claims *auth.UserClaims, path string) (io.ReadCloser, error)
	CreateDirectory(ctx context.Context, claims *auth.UserClaims, path string) error
	DeleteItem(ctx context.Context, claims *auth.UserClaims, path string) error
}

// --- Registro degli Storage Provider ---

var (
	storageRegistry map[string]StorageProvider
	registryMutex   sync.RWMutex
)

func init() {
	storageRegistry = make(map[string]StorageProvider)
}

// RegisterProvider registra un'istanza di StorageProvider nel registro globale.
func RegisterProvider(provider StorageProvider) error {
	registryMutex.Lock()
	defer registryMutex.Unlock()

	if _, exists := storageRegistry[provider.Name()]; exists {
		return fmt.Errorf("storage provider with name '%s' already registered", provider.Name())
	}
	storageRegistry[provider.Name()] = provider
	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("Registered storage provider: Type='%s', Name='%s'", provider.Type(), provider.Name())
	}
	return nil
}

// GetProvider recupera un'istanza di StorageProvider per nome dal registro globale.
func GetProvider(name string) (StorageProvider, bool) {
	registryMutex.RLock()
	defer registryMutex.RUnlock()

	provider, ok := storageRegistry[name]
	return provider, ok
}

// GetAllProviders restituisce una slice di tutti gli StorageProvider registrati.
func GetAllProviders() []StorageProvider {
	registryMutex.RLock()
	defer registryMutex.RUnlock()

	providers := make([]StorageProvider, 0, len(storageRegistry))
	for _, provider := range storageRegistry {
		providers = append(providers, provider)
	}
	return providers
}

// ClearRegistry clears all registered storage providers.
func ClearRegistry() {
	registryMutex.Lock()
	defer registryMutex.Unlock()
	storageRegistry = make(map[string]StorageProvider)
	if config.IsLogLevel(config.LogLevelInfo) {
		log.Println("Storage registry cleared.")
	}
}


// --- Errori comuni ---

var ErrNotFound = errors.New("item not found")
var ErrPermissionDenied = errors.New("permission denied")
var ErrAlreadyExists = errors.New("item already exists")
var ErrNotImplemented = errors.New("operation not implemented for this storage type")
var ErrIntegrityCheckFailed = errors.New("file integrity check failed")
