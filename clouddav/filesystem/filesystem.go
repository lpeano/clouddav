package filesystem

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"io/ioutil" // Using ioutil for simplicity, consider os package for more control
	"log"
	"os"
	"path/filepath"
	"regexp" // Import the regexp package for regular expressions
	"sort"   // Import sort for sorting file list
	"strings"
	"sync" // Import sync for mutex
	"time"

	"clouddav/auth"   // Corrected import path
	"clouddav/config" // Corrected import path
)

// FileInfo represents information about a file or folder.
// FileInfo rappresenta le informazioni su un file o una cartella.
type FileInfo struct {
	Name    string      `json:"name"`
	IsDir   bool        `json:"is_dir"`
	Size    int64       `json:"size"`
	ModTime time.Time   `json:"mod_time"`
	Mode    fs.FileMode `json:"mode"`
}

// ListDirectoryResponse is the structure for the response of ListDirectory.
// ListDirectoryResponse è la struttura per la risposta di ListDirectory.
type ListDirectoryResponse struct {
	Files      []FileInfo `json:"files"`
	TotalItems int        `json:"total_items"` // Total number of items before pagination
	Page       int        `json:"page"`        // Current page number (1-based)
	ItemsPerPage int      `json:"items_per_page"` // Items per page
}


// In-memory map to track ongoing uploads (for simplified resumable logic)
// In-memory map per tracciare gli upload in corso (per logica resumable semplificata)
// In a production environment, use a persistent store (database, Redis)
var ongoingUploads = make(map[string]*os.File)
var uploadsMutex sync.Mutex // Mutex to protect ongoingUploads

// validatePath ensures the requested path is within the configured filesystem base path.
// Prevents path traversal attacks.
// validatePath assicura che il percorso richiesto sia all'interno del percorso base del filesystem configurato.
// Previene attacchi di path traversal.
func validatePath(basePath string, requestedPath string) (string, error) {
	absBasePath, err := filepath.Abs(basePath)
	if err != nil {
		return "", fmt.Errorf("error determining absolute base path: %w", err)
	}
	// Use filepath.Clean to normalize the path before joining
	// Usa filepath.Clean per normalizzare il percorso prima di unirlo
	cleanedRequestedPath := filepath.Clean(requestedPath)
	absRequestedPath, err := filepath.Abs(filepath.Join(basePath, cleanedRequestedPath))
	if err != nil {
		return "", fmt.Errorf("error determining absolute requested path: %w", err)
	}

	// Verify that the requested path is a sub-path of the base path
	// Verifica che il percorso richiesto sia un sotto-percorso del percorso base
	if !strings.HasPrefix(absRequestedPath, absBasePath) {
		return "", errors.New("access denied: path outside allowed filesystem")
	}

	return absRequestedPath, nil
}

// ListDirectory lists the contents of a specified directory, applying access controls, pagination, and filters.
// ListDirectory elenca il contenuto di una directory specificata, applicando i controlli di accesso, paginazione e filtri.
func ListDirectory(ctx context.Context, claims *auth.UserClaims, fsName string, dirPath string, page int, itemsPerPage int, nameFilter string, timestampFilter *time.Time, cfg *config.Config) (*ListDirectoryResponse, error) {
	var fsConfig *config.FilesystemConfig
	for i := range cfg.Filesystems {
		if cfg.Filesystems[i].Name == fsName {
			fsConfig = &cfg.Filesystems[i]
			break
		}
	}

	if fsConfig == nil {
		return nil, errors.New("filesystem not found")
	}

	// Check read permissions
	// Verifica permessi di lettura
	// This check is now handled in the websocket handler before calling the provider
	// Questo controllo è ora gestito nell'handler websocket prima di chiamare il provider
	// if !auth.CheckFilesystemAccess(ctx, claims, fsConfig, "read", cfg) {
	// 	return nil, errors.New("access denied: read permission required")
	// }

	// Validate path to prevent path traversal
	// Validazione del percorso per prevenire path traversal
	fullPath, err := validatePath(fsConfig.Path, dirPath)
	if err != nil {
		return nil, fmt.Errorf("path validation error: %w", err)
	}

	files, err := ioutil.ReadDir(fullPath)
	if err != nil {
		return nil, fmt.Errorf("error listing directory: %w", err)
	}

	// Apply filters
	// Applica i filtri
	filteredFiles := []FileInfo{}
	for _, file := range files {
		// Check for context cancellation during iteration
		// Controlla la cancellazione del contesto durante l'iterazione
		select {
		case <-ctx.Done():
			// Changed to DEBUG level
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Printf("Context cancelled during ListDirectory filtering: %v", ctx.Err())
			}
			return nil, ctx.Err() // Return context error
		default:
			// Continue
			// Continua
		}

		fileInfo := FileInfo{
			Name:    file.Name(),
			IsDir:   file.IsDir(),
			Size:    file.Size(),
			ModTime: file.ModTime(),
			Mode:    file.Mode(),
		}

		// Name filter (Regular Expression)
		// Filtro per nome (Regular Expression)
		if nameFilter != "" {
			matched, _ := regexp.MatchString(nameFilter, fileInfo.Name)
			if !matched {
				continue // Skip if name doesn't match regex
			}
		}

		// Timestamp filter
		// Filtro per timestamp
		if timestampFilter != nil {
			// Compare timestamps (consider only date part if needed, or full time)
			// Confronta i timestamp (considera solo la parte data se necessario, o l'ora completa)
			// This example filters for files modified AFTER the given timestamp
			// Questo esempio filtra per file modificati DOPO il timestamp dato
			if !fileInfo.ModTime.After(*timestampFilter) {
				continue
			}
		}


		filteredFiles = append(filteredFiles, fileInfo)
	}

	// Sort files (optional but recommended for consistent pagination)
	// Ordina i file (opzionale ma raccomandato per paginazione consistente)
	// Sort by name, directories first
	// Ordina per nome, directory prima
	sort.SliceStable(filteredFiles, func(i, j int) bool {
		if filteredFiles[i].IsDir != filteredFiles[j].IsDir {
			return filteredFiles[i].IsDir // Directories come first
		}
		return filteredFiles[i].Name < filteredFiles[j].Name // Then sort by name
	})


	totalItems := len(filteredFiles)

	// Apply pagination
	// Applica la paginazione
	startIndex := (page - 1) * itemsPerPage
	endIndex := startIndex + itemsPerPage

	if startIndex >= totalItems {
		// Requested page is beyond the last page
		// La pagina richiesta è oltre l'ultima pagina
		return &ListDirectoryResponse{
			Files:      []FileInfo{},
			TotalItems: totalItems,
			Page:       page,
			ItemsPerPage: itemsPerPage,
		}, nil
	}

	if endIndex > totalItems {
		endIndex = totalItems
	}

	paginatedFiles := filteredFiles[startIndex:endIndex]

	return &ListDirectoryResponse{
		Files:      paginatedFiles,
		TotalItems: totalItems,
		Page:       page,
		ItemsPerPage: itemsPerPage,
	}, nil
}

// ReadFile reads the content of a file, applying access controls.
// ReadFile legge il contenuto di un file, applicando i controlli di accesso.
func ReadFile(ctx context.Context, claims *auth.UserClaims, fsName string, filePath string, cfg *config.Config) ([]byte, error) {
	var fsConfig *config.FilesystemConfig
	for i := range cfg.Filesystems {
		if cfg.Filesystems[i].Name == fsName {
			fsConfig = &cfg.Filesystems[i]
			break
		}
	}

	if fsConfig == nil {
		return nil, errors.New("filesystem not found")
	}

	// Check read permissions
	// Verifica permessi di lettura
	// This check is now handled in the websocket handler before calling the provider
	// if !auth.CheckFilesystemAccess(ctx, claims, fsConfig, "read", cfg) {
	// 	return nil, errors.New("access denied: read permission required")
	// }

	// Validate path
	// Validazione del percorso
	fullPath, err := validatePath(fsConfig.Path, filePath)
	if err != nil {
		return nil, fmt.Errorf("path validation error: %w", err)
	}

	// Use context-aware file reading if available (not directly in ioutil.ReadFile)
	// For large files, consider streaming with context checks.
	// Usa la lettura file consapevole del contesto se disponibile (non direttamente in ioutil.ReadFile)
	// Per file di grandi dimensioni, considera lo streaming con controlli di contesto.
	data, err := ioutil.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("error reading file: %w", err)
	}

	// Check context after reading
	// Controlla il contesto dopo la lettura
	select {
	case <-ctx.Done():
		// Changed to DEBUG level
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("Context cancelled after reading file: %v", ctx.Err())
		}
		return nil, ctx.Err()
	default:
		// Continue
		// Continua
	}


	return data, nil
}

// WriteFile writes content to a file, applying access controls.
// If the file exists, it is overwritten.
// WriteFile scrive contenuto in un file, applicando i controlli di accesso.
// Se il file esiste, viene sovrascritto.
// This function is now primarily for small files or initial creation.
// For large uploads, use InitiateUpload, WriteChunk, FinalizeUpload.
func WriteFile(ctx context.Context, claims *auth.UserClaims, fsName string, filePath string, content []byte, cfg *config.Config) error {
	var fsConfig *config.FilesystemConfig
	for i := range cfg.Filesystems {
		if cfg.Filesystems[i].Name == fsName {
			fsConfig = &cfg.Filesystems[i]
			break
		}
	}

	if fsConfig == nil {
		return errors.New("filesystem not found")
	}

	// Check write permissions
	// Verifica permessi di scrittura
	// This check is now handled in the HTTP handler before calling the provider
	// if !auth.CheckFilesystemAccess(ctx, claims, fsConfig, "write", cfg) {
	// 	return errors.New("access denied: write permission required")
	// }

	// Validate path
	// Validazione del percorso
	fullPath, err := validatePath(fsConfig.Path, filePath)
	if err != nil {
		return fmt.Errorf("path validation error: %w", err)
	}

	// Ensure the destination directory exists
	// Assicura che la directory di destinazione esista
	dir := filepath.Dir(fullPath)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		select {
		case <-ctx.Done():
			// Changed to DEBUG level
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Printf("Context cancelled before creating directory: %v", ctx.Err())
			}
			return ctx.Err()
		default:
			// Continue
		}
		err = os.MkdirAll(dir, 0755) // Create directory and any necessary parents
		if err != nil {
			return fmt.Errorf("error creating directory: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("error checking directory: %w", err)
	}

	// Use context-aware file writing if needed for large files or slow storage.
	// ioutil.WriteFile is simple but not context-aware for the write operation itself.
	// Usa la scrittura file consapevole del contesto se necessario per file di grandi dimensioni o storage lento.
	// ioutil.WriteFile è semplice ma non consapevole del contesto per l'operazione di scrittura stessa.
	err = ioutil.WriteFile(fullPath, content, 0644) // Default permissions for the new file
	if err != nil {
		return fmt.Errorf("error writing file: %w", err)
	}

	// Check context after writing
	// Controlla il contesto dopo la scrittura
	select {
	case <-ctx.Done():
		// Changed to DEBUG level
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("Context cancelled after writing file: %v", ctx.Err())
		}
		// Note: File might be partially written in this case.
		// Nota: Il file potrebbe essere parzialmente scritto in questo caso.
		return ctx.Err()
	default:
		// Continue
		// Continua
	}

	return nil
}

// CreateDirectory creates a new directory, applying access controls.
// CreateDirectory crea una nuova directory, applicando i controlli di accesso.
func CreateDirectory(ctx context.Context, claims *auth.UserClaims, fsName string, dirPath string, cfg *config.Config) error {
	var fsConfig *config.FilesystemConfig
	for i := range cfg.Filesystems {
		if cfg.Filesystems[i].Name == fsName {
			fsConfig = &cfg.Filesystems[i]
			break
		}
	}

	if fsConfig == nil {
		return errors.New("filesystem not found")
	}

	// Check write permissions (necessary to create directories)
	// Verifica permessi di scrittura (necessario per creare directory)
	// This check is now handled in the websocket handler before calling the provider
	// if !auth.CheckFilesystemAccess(ctx, claims, fsConfig, "write", cfg) {
	// 	return errors.New("access denied: write permission required")
	// }

	// Validate path
	// Validazione del percorso
	fullPath, err := validatePath(fsConfig.Path, dirPath)
	if err != nil {
		return fmt.Errorf("path validation error: %w", err)
	}

	// Check if it already exists before attempting creation
	// Controlla se esiste già prima di tentare la creazione
	if _, err := os.Stat(fullPath); err == nil {
		return errors.New("directory already exists")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("error checking if directory exists: %w", err)
	}

	// Check context before creating
	// Controlla il contesto prima di creare
	select {
	case <-ctx.Done():
		// Changed to DEBUG level
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("Context cancelled before creating directory: %v", ctx.Err())
		}
		return ctx.Err()
	default:
		// Continue
		// Continua
	}

	err = os.MkdirAll(fullPath, 0755) // Create the directory and any necessary parents
	if err != nil {
		return fmt.Errorf("error creating directory: %w", err)
	}

	return nil
}

// DeleteItem deletes a file or directory (recursively), applying access controls.
// DeleteItem elimina un file o una directory (ricorsivamente), applicando i controlli di accesso.
func DeleteItem(ctx context.Context, claims *auth.UserClaims, fsName string, itemPath string, cfg *config.Config) error {
	var fsConfig *config.FilesystemConfig
	for i := range cfg.Filesystems {
		if cfg.Filesystems[i].Name == fsName {
			fsConfig = &cfg.Filesystems[i]
			break
		}
	}

	if fsConfig == nil {
		return errors.New("filesystem not found")
	}

	// Check write permissions (necessary to delete)
	// Verifica permessi di scrittura (necessario per eliminare)
	// This check is now handled in the websocket handler before calling the provider
	// if !auth.CheckFilesystemAccess(ctx, claims, fsConfig, "write", cfg) {
	// 	return errors.New("access denied: write permission required")
	// }

	// Validate path
	// Validazione del percorso
	fullPath, err := validatePath(fsConfig.Path, itemPath)
	if err != nil {
		return fmt.Errorf("path validation error: %w", err)
	}

	// Verify that the item exists before attempting to delete it
	// Verifica che l'elemento esista prima di tentare di eliminarlo
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		return errors.New("item does not exist")
	} else if err != nil {
		return fmt.Errorf("error checking if item exists: %w", err)
	}

	// Check context before deleting
	// Controlla il contesto prima di eliminare
	select {
	case <-ctx.Done():
		// Changed to DEBUG level
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("Context cancelled before deleting item: %v", ctx.Err())
		}
		return ctx.Err()
	default:
		// Continue
		// Continua
	}


	err = os.RemoveAll(fullPath) // Remove file or directory (recursively)
	if err != nil {
		return fmt.Errorf("error deleting item: %w", err)
	}

	return nil
}

// GetFileStream opens a file for streaming, applying access controls.
// GetFileStream apre un file per lo streaming, applicando i controlli di accesso.
func GetFileStream(ctx context.Context, claims *auth.UserClaims, fsName string, filePath string, cfg *config.Config) (io.ReadCloser, error) {
	var fsConfig *config.FilesystemConfig
	for i := range cfg.Filesystems {
		if cfg.Filesystems[i].Name == fsName {
			fsConfig = &cfg.Filesystems[i]
			break
		}
	}

	if fsConfig == nil {
		return nil, errors.New("filesystem not found")
	}

	// Check read permissions
	// Verifica permessi di lettura
	// This check is now handled in the HTTP handler before calling the provider
	// if !auth.CheckFilesystemAccess(ctx, claims, fsConfig, "read", cfg) {
	// 	return nil, errors.New("access denied: read permission required")
	// }

	// Validate path
	// Validazione del percorso
	fullPath, err := validatePath(fsConfig.Path, filePath)
	if err != nil {
		return nil, fmt.Errorf("path validation error: %w", err)
	}

	// Use os.Open which might be context-aware depending on the underlying OS/filesystem.
	// For true context cancellation during large reads, you might need custom reader wrappers.
	// Usa os.Open che potrebbe essere consapevole del contesto a seconda del sistema operativo/filesystem sottostante.
	// Per una vera cancellazione del contesto durante letture di grandi dimensioni, potrebbe essere necessario usare wrapper reader personalizzati.
	file, err := os.Open(fullPath)
	if err != nil {
		return nil, fmt.Errorf("error opening file: %w", err)
	}

	// Check context after opening (quick check)
	// Controlla il contesto dopo l'apertura (controllo rapido)
	select {
	case <-ctx.Done():
		// Changed to DEBUG level
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("Context cancelled after opening file: %v", ctx.Err())
		}
		file.Close() // Close the opened file
		return nil, ctx.Err()
	default:
		// Continue
		// Continua
	}


	return file, nil
}

// InitiateUpload starts a new upload session or resumes an existing one.
// Returns the current size of the target file if it exists, for resuming.
// InitiateUpload avvia una nuova sessione di upload o riprende una esistente.
// Restituisce la dimensione corrente del file di destinazione se esiste, per la ripresa.
func InitiateUpload(ctx context.Context, claims *auth.UserClaims, fsName string, filePath string, cfg *config.Config) (int64, error) {
	var fsConfig *config.FilesystemConfig
	for i := range cfg.Filesystems {
		if cfg.Filesystems[i].Name == fsName {
			fsConfig = &cfg.Filesystems[i]
			break
		}
	}

	if fsConfig == nil {
		return 0, errors.New("filesystem not found")
	}

	// Check write permissions
	// Verifica permessi di scrittura
	// This check is now handled in the HTTP handler before calling the provider
	// if !auth.CheckFilesystemAccess(ctx, claims, fsConfig, "write", cfg) {
	// 	return 0, errors.New("access denied: write permission required")
	// }

	// Validate path
	// Validazione del percorso
	fullPath, err := validatePath(fsConfig.Path, filePath)
	if err != nil {
		return 0, fmt.Errorf("path validation error: %w", err)
	}

	// Ensure the destination directory exists
	// Assicura che la directory di destinazione esista
	dir := filepath.Dir(fullPath)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
		}
		err = os.MkdirAll(dir, 0755)
		if err != nil {
			return 0, fmt.Errorf("error creating directory: %w", err)
		}
	} else if err != nil {
		return 0, fmt.Errorf("error checking directory: %w", err)
	}

	// Open the file in append mode. If it exists, writing starts from the end.
	// If it doesn't exist, it's created.
	// Apri il file in modalità append. Se esiste, la scrittura inizia dalla fine.
	// Se non esiste, viene creato.
	file, err := os.OpenFile(fullPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return 0, fmt.Errorf("error opening file for upload: %w", err)
	}

	// Get the current size of the file for resuming
	// Ottieni la dimensione corrente del file per la ripresa
	fileInfo, err := file.Stat()
	if err != nil {
		file.Close() // Close the file before returning error
		return 0, fmt.Errorf("error getting file info for upload: %w", err)
	}

	// Store the file handle for subsequent chunk writes using a unique key
	// In a real system, you'd use a more robust session ID.
	// Memorizza il handle del file per le successive scritture di chunk usando una chiave univoca
	// In un un sistema reale, useresti un ID di sessione più robusto.
	uploadKey := fmt.Sprintf("%s:%s", fsName, filePath) // Simple key: filesystemName:filePath
	uploadsMutex.Lock()
	ongoingUploads[uploadKey] = file
	uploadsMutex.Unlock()

	// Changed to INFO level
	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("Initiated upload for %s. Current size: %d", fullPath, fileInfo.Size())
	}

	return fileInfo.Size(), nil // Return current size for resuming
}

// WriteChunk writes a chunk of data to an ongoing upload session.
// WriteChunk scrive un chunk di dati in una sessione di upload in corso.
func WriteChunk(ctx context.Context, fsName string, filePath string, chunk []byte) error {
	uploadKey := fmt.Sprintf("%s:%s", fsName, filePath)
	uploadsMutex.Lock()
	file, ok := ongoingUploads[uploadKey]
	uploadsMutex.Unlock()

	if !ok || file == nil {
		return errors.New("upload session not found or file handle is nil")
	}

	// Check context cancellation before writing
	// Controlla la cancellazione del contesto prima di scrivere
	select {
	case <-ctx.Done():
		// Changed to DEBUG level
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("Context cancelled during WriteChunk: %v", ctx.Err())
		}
		// Consider cleaning up the incomplete file here or in a separate process
		return ctx.Err()
	default:
		// Continue
	}


	// Write the chunk to the file (opened in append mode, so it writes at the end)
	// Scrivi il chunk nel file (aperto in modalità append, quindi scrive alla fine)
	n, err := file.Write(chunk)
	if err != nil {
		log.Printf("Error writing chunk to file %s: %v", filePath, err)
		// Consider removing the file from ongoingUploads on write error
		return fmt.Errorf("error writing chunk: %w", err)
	}

	if n != len(chunk) {
		log.Printf("Warning: Wrote %d bytes, but chunk size was %d for file %s", n, len(chunk), filePath)
		// This might indicate a partial write, which is an error in this context
		return errors.New("partial write occurred")
	}

	return nil
}

// FinalizeUpload closes the file handle for an upload session.
// FinalizeUpload chiude il handle del file per una sessione di upload.
func FinalizeUpload(fsName string, filePath string) error {
	uploadKey := fmt.Sprintf("%s:%s", fsName, filePath)
	uploadsMutex.Lock()
	file, ok := ongoingUploads[uploadKey]
	if ok {
		delete(ongoingUploads, uploadKey) // Remove from map
	}
	uploadsMutex.Unlock()

	if !ok || file == nil {
		return errors.New("upload session not found or file handle is nil")
	}

	err := file.Close()
	if err != nil {
		return fmt.Errorf("error closing file after upload: %w", err)
	}

	// Changed to INFO level
	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("Upload finalized for %s", filePath)
	}

	return nil
}

// CancelUpload cancels an ongoing upload session and removes the incomplete file.
// CancelUpload cancella una sessione di upload in corso e rimuove il file incompleto.
func CancelUpload(fsName string, filePath string, cfg *config.Config) error {
	uploadKey := fmt.Sprintf("%s:%s", fsName, filePath)
	uploadsMutex.Lock()
	file, ok := ongoingUploads[uploadKey]
	if ok {
		delete(ongoingUploads, uploadKey) // Remove from map
	}
	uploadsMutex.Unlock()

	if ok && file != nil {
		file.Close() // Close the file handle
		// Changed to INFO level
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("Upload cancelled for %s. Attempting to remove incomplete file.", filePath)
		}

		// Attempt to remove the incomplete file
		var fsConfig *config.FilesystemConfig
		for i := range cfg.Filesystems {
			if cfg.Filesystems[i].Name == fsName {
				fsConfig = &cfg.Filesystems[i]
				break
			}
		}

		if fsConfig == nil {
			log.Printf("Warning: Filesystem config not found for cancelled upload %s, cannot remove file.", fsName)
			return errors.New("filesystem config not found for cleanup")
		}

		fullPath, err := validatePath(fsConfig.Path, filePath)
		if err != nil {
			log.Printf("Warning: Path validation error for cancelled upload %s: %v, cannot remove file.", filePath, err)
			return fmt.Errorf("path validation error during cancel: %w", err)
		}

		removeErr := os.Remove(fullPath)
		if removeErr != nil {
			log.Printf("Error removing incomplete file %s after cancellation: %v", fullPath, removeErr)
			return fmt.Errorf("error removing incomplete file: %w", removeErr)
		}
		// Changed to INFO level
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("Incomplete file %s removed after cancellation.", fullPath)
		}
		return nil // Successfully cancelled and removed
	}

	return errors.New("no ongoing upload session found for cancellation")
}

// GetUploadedSize returns the current size of a file being uploaded.
// GetUploadedSize restituisce la dimensione corrente di un file in upload.
func GetUploadedSize(fsName string, filePath string, cfg *config.Config) (int64, error) {
	var fsConfig *config.FilesystemConfig
	for i := range cfg.Filesystems {
		if cfg.Filesystems[i].Name == fsName {
			fsConfig = &cfg.Filesystems[i]
			break
		}
	}

	if fsConfig == nil {
		return 0, errors.New("filesystem not found")
	}

	// Validate path
	fullPath, err := validatePath(fsConfig.Path, filePath)
	if err != nil {
		return 0, fmt.Errorf("path validation error: %w", err)
	}

	fileInfo, err := os.Stat(fullPath)
	if os.IsNotExist(err) {
		return 0, nil // File doesn't exist yet, uploaded size is 0
	} else if err != nil {
		return 0, fmt.Errorf("error getting file info: %w", err)
	}

	return fileInfo.Size(), nil
}
