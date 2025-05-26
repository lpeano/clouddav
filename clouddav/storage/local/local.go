package local

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic" // Import atomic for atomic.Value
	"time"

	"clouddav/auth"
	"clouddav/config"
	"clouddav/storage"
)

// NOTE: FileInfo and ListDirectoryResponse structs were removed as they are defined in clouddav/storage.

// LocalFilesystemProvider implements the StorageProvider interface for local filesystems.
type LocalFilesystemProvider struct {
	name string
	path string // Base path configured
}

// NewProvider creates a new LocalFilesystemProvider.
func NewProvider(cfg *config.StorageConfig) (*LocalFilesystemProvider, error) {
	if cfg.Type != "local" {
		return nil, errors.New("invalid storage config type for local provider")
	}
	if cfg.Path == "" {
		return nil, errors.New("local storage path is required")
	}
	return &LocalFilesystemProvider{
		name: cfg.Name,
		path: cfg.Path,
	}, nil
}

// Type returns the storage type.
func (p *LocalFilesystemProvider) Type() string {
	return "local"
}

// Name returns the configured name.
func (p *LocalFilesystemProvider) Name() string {
	return p.name
}

// validatePath ensures the requested path is within the configured base path.
// Prevents path traversal attacks. The path is relative to the provider's base path.
func (p *LocalFilesystemProvider) validatePath(requestedPath string) (string, error) {
	absBasePath, err := filepath.Abs(p.path)
	if err != nil {
		return "", fmt.Errorf("error determining absolute base path '%s': %w", p.path, err)
	}
	cleanedRequestedPath := filepath.Clean(requestedPath)
	if cleanedRequestedPath == "." {
		cleanedRequestedPath = ""
	}

	fullPath := filepath.Join(p.path, cleanedRequestedPath)
	absFullPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", fmt.Errorf("error determining absolute full path '%s': %w", fullPath, err)
	}

	if !strings.HasPrefix(absFullPath, absBasePath) {
		return "", errors.New("access denied: path outside allowed filesystem")
	}

	return absFullPath, nil
}

// ListItems lists the contents of a specified directory, applying pagination and filters.
// The path is relative to the configured storage root. Includes claims parameter for logging.
// << MODIFICA: Aggiunto il parametro onlyDirectories
func (p *LocalFilesystemProvider) ListItems(ctx context.Context, claims *auth.UserClaims, path string, page int, itemsPerPage int, nameFilter string, timestampFilter *time.Time, onlyDirectories bool) (*storage.ListItemsResponse, error) {
	userIdent := "unauthenticated"
	if claims != nil {
		userIdent = claims.Email
	}
	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("LocalFilesystemProvider.ListItems chiamato da utente '%s' per storage '%s', path '%s', page %d, itemsPerPage %d, nameFilter '%s', onlyDirectories: %t", userIdent, p.name, path, page, itemsPerPage, nameFilter, onlyDirectories)
	}

	fullPath, err := p.validatePath(path)
	if err != nil {
		log.Printf("LocalFilesystemProvider.ListItems: Path validation error for '%s': %v", path, err)
		return nil, fmt.Errorf("path validation error: %w", err)
	}

	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("LocalFilesystemProvider.ListItems: Validated full path: '%s'", fullPath)
	}

	items, err := os.ReadDir(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("LocalFilesystemProvider.ListItems: Path not found: '%s'", fullPath)
			return nil, storage.ErrNotFound
		}
		log.Printf("LocalFilesystemProvider.ListItems: Error reading directory '%s': %v", fullPath, err)
		return nil, fmt.Errorf("error listing directory '%s': %w", fullPath, err)
	}

	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("LocalFilesystemProvider.ListItems: Found %d raw items in '%s'", len(items), fullPath)
	}

	filteredItems := []storage.ItemInfo{}
	for _, item := range items {
		select {
		case <-ctx.Done():
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Printf("LocalFilesystemProvider.ListItems: Context cancelled during filtering: %v", ctx.Err())
			}
			return nil, ctx.Err()
		default:
		}

		info, err := item.Info()
		if err != nil {
			log.Printf("Warning: Error getting info for item '%s' in '%s': %v", item.Name(), fullPath, err)
			continue
		}

		// << MODIFICA: Salta i file se onlyDirectories è true
		if onlyDirectories && !item.IsDir() {
			continue
		}

		itemInfo := storage.ItemInfo{
			Name:    item.Name(),
			IsDir:   item.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
			Path:    filepath.Join(path, item.Name()),
		}

		if nameFilter != "" {
			matched, _ := regexp.MatchString(nameFilter, itemInfo.Name)
			if !matched {
				continue
			}
		}

		if timestampFilter != nil {
			if !itemInfo.ModTime.After(*timestampFilter) {
				continue
			}
		}

		filteredItems = append(filteredItems, itemInfo)
	}

	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("LocalFilesystemProvider.ListItems: Found %d items after filtering (onlyDirectories: %t)", len(filteredItems), onlyDirectories)
	}

	sort.SliceStable(filteredItems, func(i, j int) bool {
		if filteredItems[i].IsDir != filteredItems[j].IsDir {
			return filteredItems[i].IsDir
		}
		return filteredItems[i].Name < filteredItems[j].Name
	})

	totalItems := len(filteredItems)

	startIndex := (page - 1) * itemsPerPage
	endIndex := startIndex + itemsPerPage

	if startIndex >= totalItems {
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("LocalFilesystemProvider.ListItems: Start index %d >= total items %d, returning empty page", startIndex, totalItems)
		}
		return &storage.ListItemsResponse{
			Items:        []storage.ItemInfo{},
			TotalItems:   totalItems,
			Page:         page,
			ItemsPerPage: itemsPerPage,
		}, nil
	}

	if endIndex > totalItems {
		endIndex = totalItems
	}

	paginatedItems := filteredItems[startIndex:endIndex]

	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("LocalFilesystemProvider.ListItems: Returning %d items for page %d (startIndex %d, endIndex %d)", len(paginatedItems), page, startIndex, endIndex)
	}

	return &storage.ListItemsResponse{
		Items:        paginatedItems,
		TotalItems:   totalItems,
		Page:         page,
		ItemsPerPage: itemsPerPage,
	}, nil
}

// GetItem retrieves information about a single item.
func (p *LocalFilesystemProvider) GetItem(ctx context.Context, claims *auth.UserClaims, path string) (*storage.ItemInfo, error) {
	userIdent := "unauthenticated"
	if claims != nil {
		userIdent = claims.Email
	}
	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("LocalFilesystemProvider.GetItem chiamato da utente '%s' per storage '%s', path '%s'", userIdent, p.name, path)
	}

	fullPath, err := p.validatePath(path)
	if err != nil {
		return nil, fmt.Errorf("path validation error: %w", err)
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, storage.ErrNotFound
		}
		return nil, fmt.Errorf("error getting item info '%s': %w", fullPath, err)
	}

	itemInfo := &storage.ItemInfo{
		Name:    info.Name(),
		IsDir:   info.IsDir(),
		Size:    info.Size(),
		ModTime: info.ModTime(),
		Path:    path,
	}

	return itemInfo, nil
}

// OpenReader opens a file for streaming.
func (p *LocalFilesystemProvider) OpenReader(ctx context.Context, claims *auth.UserClaims, path string) (io.ReadCloser, error) {
	userIdent := "unauthenticated"
	if claims != nil {
		userIdent = claims.Email
	}
	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("LocalFilesystemProvider.OpenReader chiamato da utente '%s' per storage '%s', path '%s'", userIdent, p.name, path)
	}

	fullPath, err := p.validatePath(path)
	if err != nil {
		return nil, fmt.Errorf("path validation error: %w", err)
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, storage.ErrNotFound
		}
		return nil, fmt.Errorf("error checking item '%s' before opening: %w", fullPath, err)
	}
	if info.IsDir() {
		return nil, errors.New("cannot open a directory for reading")
	}

	file, err := os.Open(fullPath)
	if err != nil {
		if os.IsPermission(err) {
			return nil, storage.ErrPermissionDenied
		}
		return nil, fmt.Errorf("error opening file '%s': %w", fullPath, err)
	}

	select {
	case <-ctx.Done():
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("Context cancelled after opening file '%s': %v", fullPath, ctx.Err())
		}
		file.Close()
		return nil, ctx.Err()
	default:
	}

	return file, nil
}

// CreateDirectory creates a new directory.
func (p *LocalFilesystemProvider) CreateDirectory(ctx context.Context, claims *auth.UserClaims, path string) error {
	userIdent := "unauthenticated"
	if claims != nil {
		userIdent = claims.Email
	}
	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("LocalFilesystemProvider.CreateDirectory chiamato da utente '%s' per storage '%s', path '%s'", userIdent, p.name, path)
	}

	fullPath, err := p.validatePath(path)
	if err != nil {
		return fmt.Errorf("path validation error: %w", err)
	}

	if _, err := os.Stat(fullPath); err == nil {
		return storage.ErrAlreadyExists
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("error checking if directory '%s' exists: %w", fullPath, err)
	}

	select {
	case <-ctx.Done():
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("Context cancelled before creating directory '%s': %v", fullPath, ctx.Err())
		}
		return ctx.Err()
	default:
	}

	err = os.MkdirAll(fullPath, 0755)
	if err != nil {
		if os.IsPermission(err) {
			return storage.ErrPermissionDenied
		}
		return fmt.Errorf("error creating directory '%s': %w", fullPath, err)
	}

	return nil
}

// DeleteItem deletes a file or directory (recursively).
func (p *LocalFilesystemProvider) DeleteItem(ctx context.Context, claims *auth.UserClaims, path string) error {
	userIdent := "unauthenticated"
	if claims != nil {
		userIdent = claims.Email
	}
	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("LocalFilesystemProvider.DeleteItem chiamato da utente '%s' per storage '%s', path '%s'", userIdent, p.name, path)
	}

	fullPath, err := p.validatePath(path)
	if err != nil {
		return fmt.Errorf("path validation error: %w", err)
	}

	info, err := os.Stat(fullPath)
	if os.IsNotExist(err) {
		return storage.ErrNotFound
	} else if err != nil {
		return fmt.Errorf("error checking if item '%s' exists: %w", fullPath, err)
	}

	select {
	case <-ctx.Done():
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("Context cancelled before deleting item '%s': %v", fullPath, ctx.Err())
		}
		return ctx.Err()
	default:
	}

	if info.IsDir() {
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("LocalFilesystemProvider.DeleteItem: Deleting directory '%s' recursively with concurrency.", fullPath)
		}

		var itemsToDelete []string
		err := filepath.Walk(fullPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			itemsToDelete = append(itemsToDelete, path)
			return nil
		})
		if err != nil {
			return fmt.Errorf("error walking directory '%s' for deletion: %w", fullPath, err)
		}

		sort.Slice(itemsToDelete, func(i, j int) bool {
			return len(itemsToDelete[i]) > len(itemsToDelete[j])
		})

		var wg sync.WaitGroup
		errChan := make(chan error, len(itemsToDelete))

		maxConcurrency := runtime.NumCPU() * 4
		if maxConcurrency == 0 {
			maxConcurrency = 4
		}
		sem := make(chan struct{}, maxConcurrency)

		for _, itemPathToDelete := range itemsToDelete {
			if itemPathToDelete == fullPath {
				continue
			}

			select {
			case <-ctx.Done():
				if config.IsLogLevel(config.LogLevelDebug) {
					log.Printf("Context cancelled during local deletion of '%s': %v", itemPathToDelete, ctx.Err())
				}
				return ctx.Err()
			case sem <- struct{}{}:
				wg.Add(1)
				go func(name string) {
					defer wg.Done()
					defer func() { <-sem }()

					deleteErr := os.Remove(name)
					if deleteErr != nil {
						if os.IsPermission(deleteErr) {
							errChan <- storage.ErrPermissionDenied
						} else if !os.IsNotExist(deleteErr) {
							errChan <- fmt.Errorf("failed to delete item '%s': %w", name, deleteErr)
						}
					} else {
						if config.IsLogLevel(config.LogLevelDebug) {
							log.Printf("Local: Deleted item '%s'", name)
						}
					}
				}(itemPathToDelete)
			}
		}

		wg.Wait()
		close(errChan)

		for err := range errChan {
			if err != nil {
				return err
			}
		}

		err = os.Remove(fullPath)
		if err != nil {
			if os.IsPermission(err) {
				return storage.ErrPermissionDenied
			}
			return fmt.Errorf("error deleting root directory '%s': %w", fullPath, err)
		}
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("LocalFilesystemProvider.DeleteItem: Directory '%s' deleted successfully.", fullPath)
		}
		return nil

	} else {
		err = os.Remove(fullPath)
		if err != nil {
			if os.IsPermission(err) {
				return storage.ErrPermissionDenied
			}
			return fmt.Errorf("error deleting item '%s': %w", fullPath, err)
		}
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("LocalFilesystemProvider.DeleteItem: File '%s' deleted successfully.", fullPath)
		}
		return nil
	}
}

// --- Nuove strutture e variabili globali per la gestione degli upload locali ---

// chunkWriteRequest incapsula i dati di un chunk e la sua posizione.
type chunkWriteRequest struct {
	Data       []byte
	ChunkIndex int64
	ChunkSize  int64
}

// localUploadSession rappresenta lo stato di un upload di file in corso per lo storage locale.
type localUploadSession struct {
	TempFile        *os.File              // File temporaneo per scrivere i chunk
	ReceivedChunks  map[int64]bool        // Mappa per tracciare gli indici dei chunk ricevuti
	ExpectedChunks  int64                 // Numero totale di chunk attesi
	ExpectedFileSize int64                // Dimensione totale del file attesa
	FinalPath       string                // Percorso finale del file
	
	chunkBuffer     chan chunkWriteRequest // Canale bufferizzato per ricevere i chunk da scrivere
	done            chan struct{}         // Segnale per terminare la goroutine di scrittura
	writerWg        sync.WaitGroup        // WaitGroup per attendere la goroutine di scrittura
	writerError     atomic.Value          // Per propagare errori dalla goroutine di scrittura
	mu              sync.Mutex            // Mutex per proteggere l'accesso concorrente alla sessione
}

var localOngoingUploadSessions = make(map[string]*localUploadSession) // Mappa: uploadID -> sessione
var localUploadSessionsMutex sync.Mutex // Mutex per proteggere la mappa localOngoingUploadSessions

// writerGoroutine è la goroutine dedicata che scrive i chunk sul file temporaneo.
func (s *localUploadSession) writerGoroutine() {
	defer s.writerWg.Done()
	log.Printf("Local upload writerGoroutine started for temp file: %s", s.TempFile.Name())

	for {
		select {
		case req, ok := <-s.chunkBuffer:
			if !ok { // Canale chiuso, nessun altro chunk arriverà
				log.Printf("Local upload writerGoroutine: chunkBuffer closed for %s. Exiting.", s.TempFile.Name())
				return
			}

			// Calcola l'offset di scrittura
			offset := req.ChunkIndex * req.ChunkSize

			// Sposta il puntatore del file all'offset corretto
			_, err := s.TempFile.Seek(offset, io.SeekStart)
			if err != nil {
				s.writerError.Store(fmt.Errorf("writerGoroutine: error seeking in temporary file for chunk %d: %w", req.ChunkIndex, err))
				log.Printf("Local upload writerGoroutine error: %v", s.writerError.Load())
				return
			}

			// Scrivi il chunk nel file temporaneo
			n, err := s.TempFile.Write(req.Data)
			if err != nil {
				s.writerError.Store(fmt.Errorf("writerGoroutine: error writing chunk %d to temporary file: %w", req.ChunkIndex, err))
				log.Printf("Local upload writerGoroutine error: %v", s.writerError.Load())
				return
			}

			if n != len(req.Data) {
				s.writerError.Store(errors.New("writerGoroutine: partial write occurred during local upload chunk"))
				log.Printf("Local upload writerGoroutine error: %v", s.writerError.Load())
				return
			}

			// Marca il chunk come ricevuto (protetto da mutex se necessario, ma qui la goroutine è unica)
			// La mappa ReceivedChunks è protetta dal mutex della sessione in WriteChunk, non qui.
			// Quindi, non è necessario bloccare qui.
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Printf("Local upload writerGoroutine: Wrote chunk %d to %s", req.ChunkIndex, s.TempFile.Name())
			}

		case <-s.done: // Segnale di terminazione ricevuto
			log.Printf("Local upload writerGoroutine: Done signal received for %s. Exiting.", s.TempFile.Name())
			return
		}
	}
}


// InitiateUpload starts a new upload session or resumes an existing one for a local file.
// Ora accetta anche totalFileSize e chunkSize per una gestione più precisa.
func (p *LocalFilesystemProvider) InitiateUpload(ctx context.Context, claims *auth.UserClaims, filePath string, totalFileSize int64, chunkSize int64) (int64, error) {
	userIdent := "unauthenticated"
	if claims != nil {
		userIdent = claims.Email
	}
	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("LocalFilesystemProvider.InitiateUpload chiamato da utente '%s' per storage '%s', path '%s', totalFileSize %d, chunkSize %d", userIdent, p.name, filePath, totalFileSize, chunkSize)
	}

	fullPath, err := p.validatePath(filePath)
	if err != nil {
		return 0, fmt.Errorf("path validation error: %w", err)
	}

	dir := filepath.Dir(fullPath)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
		}
		err = os.MkdirAll(dir, 0755)
		if err != nil {
			return 0, fmt.Errorf("error creating directory '%s': %w", dir, err)
		}
	} else if err != nil {
		return 0, fmt.Errorf("error checking directory '%s': %w", dir, err)
	}

	uploadKey := fmt.Sprintf("%s:%s", p.name, filePath)

	localUploadSessionsMutex.Lock()
	session, exists := localOngoingUploadSessions[uploadKey]
	localUploadSessionsMutex.Unlock()

	var currentSize int64 = 0

	if !exists {
		// Crea un file temporaneo per l'upload
		// Utilizziamo il percorso della directory di destinazione per il file temporaneo
		tempDir := dir
		tempFile, err := os.CreateTemp(tempDir, "upload-*.tmp")
		if err != nil {
			return 0, fmt.Errorf("error creating temporary file for upload: %w", err)
		}

		// Pre-allocazione dello spazio del file temporaneo
		if err := tempFile.Truncate(totalFileSize); err != nil {
			tempFile.Close()
			os.Remove(tempFile.Name()) // Clean up on error
			return 0, fmt.Errorf("error pre-allocating temporary file space: %w", err)
		}

		expectedChunks := (totalFileSize + chunkSize - 1) / chunkSize // Calcola il numero totale di chunk attesi

		session = &localUploadSession{
			TempFile:        tempFile,
			ReceivedChunks:  make(map[int64]bool),
			ExpectedChunks:  expectedChunks,
			ExpectedFileSize: totalFileSize,
			FinalPath:       fullPath,
			chunkBuffer:     make(chan chunkWriteRequest, 100), // Buffer di 100 chunk (tunabile)
			done:            make(chan struct{}),
		}
		
		// Avvia la goroutine di scrittura per questa sessione
		session.writerWg.Add(1)
		go session.writerGoroutine()

		localUploadSessionsMutex.Lock()
		localOngoingUploadSessions[uploadKey] = session
		localUploadSessionsMutex.Unlock()

		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("Initiated new local upload session for storage '%s', path '%s'. Temp file: '%s', Expected chunks: %d, Total size: %d", p.name, filePath, tempFile.Name(), expectedChunks, totalFileSize)
		}
	} else {
		// Sessione esistente, riprendi l'upload
		// Qui non avviamo una nuova goroutine di scrittura, assumiamo che sia già attiva.
		// Se la sessione è stata recuperata da un crash, la writerGoroutine potrebbe non essere attiva.
		// Questo scenario di ripristino da crash è più complesso e richiederebbe una persistenza dello stato.
		// Per ora, assumiamo che la sessione esista solo se la writerGoroutine è già in esecuzione.
		session.mu.Lock() // Protegge l'accesso alla sessione per la lettura dello stato
		defer session.mu.Unlock()

		fileInfo, err := session.TempFile.Stat()
		if err != nil {
			session.TempFile.Close()
			delete(localOngoingUploadSessions, uploadKey) // Pulisci la sessione rotta
			return 0, fmt.Errorf("error getting temp file info for resuming upload '%s': %w", session.TempFile.Name(), err)
		}
		currentSize = fileInfo.Size()

		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("Resuming local upload session for storage '%s', path '%s'. Temp file: '%s', Current size: %d", p.name, filePath, session.TempFile.Name(), currentSize)
		}
	}

	return currentSize, nil
}

// WriteChunk invia un chunk di dati alla goroutine di scrittura della sessione.
func (p *LocalFilesystemProvider) WriteChunk(ctx context.Context, claims *auth.UserClaims, filePath string, chunkData []byte, chunkIndex int64, chunkSize int64) error {
	userIdent := "unauthenticated"
	if claims != nil {
		userIdent = claims.Email
	}
	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("LocalFilesystemProvider.WriteChunk chiamato da utente '%s' per storage '%s', path '%s', chunkIndex %d", userIdent, p.name, filePath, chunkIndex)
	}

	uploadKey := fmt.Sprintf("%s:%s", p.name, filePath)
	localUploadSessionsMutex.Lock()
	session, ok := localOngoingUploadSessions[uploadKey]
	localUploadSessionsMutex.Unlock()

	if !ok || session == nil || session.TempFile == nil {
		return errors.New("local upload session not found or invalid")
	}

	// Controlla se la goroutine di scrittura ha segnalato un errore
	if errVal := session.writerError.Load(); errVal != nil {
		return errVal.(error) // Propaga l'errore dalla goroutine di scrittura
	}

	// Marca il chunk come ricevuto (protetto da mutex)
	session.mu.Lock()
	session.ReceivedChunks[chunkIndex] = true
	session.mu.Unlock()

	// Invia il chunk alla goroutine di scrittura tramite il canale bufferizzato
	select {
	case session.chunkBuffer <- chunkWriteRequest{Data: chunkData, ChunkIndex: chunkIndex, ChunkSize: chunkSize}:
		// Chunk inviato con successo al buffer
		return nil
	case <-ctx.Done():
		// Il contesto della richiesta è stato annullato
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("Context cancelled during local WriteChunk (sending to buffer) for '%s': %v", filePath, ctx.Err())
		}
		return ctx.Err()
	case <-session.done:
		// La sessione è stata terminata (es. annullata) mentre si tentava di inviare un chunk
		return errors.New("upload session terminated while writing chunk")
	case <-time.After(5 * time.Second): // Timeout per l'invio al buffer
		// Questo timeout si verifica se il buffer è pieno e la goroutine di scrittura è lenta.
		// Indica un problema di backpressure o una writerGoroutine bloccata.
		log.Printf("Warning: Timeout sending chunk %d to buffer for file '%s'. Buffer might be full or writer goroutine is stuck.", chunkIndex, filePath)
		return errors.New("timeout sending chunk to internal buffer")
	}
}

// FinalizeUpload closes the file handle for a local upload session, reassembles the file,
// performs SHA256 integrity check, and moves it to its final destination.
func (p *LocalFilesystemProvider) FinalizeUpload(claims *auth.UserClaims, filePath string, expectedSHA256 string) error {
	userIdent := "unauthenticated"
	if claims != nil {
		userIdent = claims.Email
	}
	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("LocalFilesystemProvider.FinalizeUpload chiamato da utente '%s' per storage '%s', path '%s'. SHA256 atteso: %s", userIdent, p.name, filePath, expectedSHA256)
	}

	uploadKey := fmt.Sprintf("%s:%s", p.name, filePath)
	localUploadSessionsMutex.Lock()
	session, ok := localOngoingUploadSessions[uploadKey]
	if ok {
		delete(localOngoingUploadSessions, uploadKey) // Rimuovi dalla mappa prima di finalizzare
	}
	localUploadSessionsMutex.Unlock()

	if !ok || session == nil || session.TempFile == nil {
		return errors.New("local upload session not found or invalid")
	}

	// Segnala alla goroutine di scrittura di terminare e chiudi il canale per assicurare che non vengano inviati più chunk
	close(session.chunkBuffer)
	close(session.done) // Segnala anche la terminazione esplicita
	session.writerWg.Wait() // Attendi che la goroutine di scrittura abbia terminato

	// Controlla se la goroutine di scrittura ha segnalato un errore
	if errVal := session.writerError.Load(); errVal != nil {
		// Se c'è stato un errore durante la scrittura asincrona, pulisci e restituisci l'errore
		session.TempFile.Close()
		os.Remove(session.TempFile.Name())
		return fmt.Errorf("error during asynchronous chunk writing: %w", errVal.(error))
	}

	session.mu.Lock() // Blocca la sessione durante la finalizzazione
	defer session.mu.Unlock()

	// Controlla se tutti i chunk sono stati ricevuti
	if int64(len(session.ReceivedChunks)) != session.ExpectedChunks {
		session.TempFile.Close() // Chiudi il file temporaneo
		os.Remove(session.TempFile.Name()) // Elimina il file temporaneo incompleto
		return fmt.Errorf("missing chunks for file '%s'. Expected %d, received %d", filePath, session.ExpectedChunks, len(session.ReceivedChunks))
	}

	// Assicurati che il file temporaneo sia sincronizzato su disco prima di leggerlo
	err := session.TempFile.Sync()
	if err != nil {
		session.TempFile.Close()
		os.Remove(session.TempFile.Name())
		return fmt.Errorf("error syncing temporary file '%s': %w", session.TempFile.Name(), err)
	}

	// Riporta il puntatore del file temporaneo all'inizio per la lettura e l'hashing
	_, err = session.TempFile.Seek(0, io.SeekStart)
	if err != nil {
		session.TempFile.Close()
		os.Remove(session.TempFile.Name())
		return fmt.Errorf("error seeking to start of temporary file '%s': %w", session.TempFile.Name(), err)
	}

	// Crea il file di destinazione finale
	finalFile, err := os.Create(session.FinalPath)
	if err != nil {
		session.TempFile.Close()
		os.Remove(session.TempFile.Name())
		if os.IsPermission(err) {
			return storage.ErrPermissionDenied
		}
		return fmt.Errorf("error creating final file '%s': %w", session.FinalPath, err)
	}
	defer finalFile.Close()

	// Inizializza l'hasher SHA256
	hasher := sha256.New()

	// Crea un MultiWriter per scrivere contemporaneamente all'hasher e al file finale
	mw := io.MultiWriter(finalFile, hasher)

	// Copia il contenuto dal file temporaneo al MultiWriter
	bytesCopied, err := io.Copy(mw, session.TempFile)
	if err != nil {
		session.TempFile.Close()
		os.Remove(session.TempFile.Name())
		os.Remove(session.FinalPath) // Rimuovi il file finale parziale
		return fmt.Errorf("error copying data from temporary file to final destination and hasher: %w", err)
	}

	// Verifica che la dimensione copiata corrisponda alla dimensione attesa
	if bytesCopied != session.ExpectedFileSize {
		session.TempFile.Close()
		os.Remove(session.TempFile.Name())
		os.Remove(session.FinalPath)
		return fmt.Errorf("copied bytes mismatch for '%s'. Expected %d, copied %d", filePath, session.ExpectedFileSize, bytesCopied)
	}

	// Chiudi il file temporaneo
	session.TempFile.Close()
	// Elimina il file temporaneo
	os.Remove(session.TempFile.Name())

	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("Local upload finalized for storage '%s', path '%s'. Starting integrity check.", p.name, filePath)
	}

	// Verifica di integrità SHA256
	if expectedSHA256 != "" {
		calculatedSHA256 := hex.EncodeToString(hasher.Sum(nil))

		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("Local: Calculated SHA256 for '%s': %s", filePath, calculatedSHA256)
			log.Printf("Local: Expected SHA256 for '%s': %s", filePath, expectedSHA256)
		}

		if calculatedSHA256 != expectedSHA256 {
			log.Printf("Error: SHA256 mismatch for local file '%s'. Calculated: %s, Expected: %s", filePath, calculatedSHA256, expectedSHA256)
			os.Remove(session.FinalPath) // Elimina il file finale se l'hash non corrisponde
			return storage.ErrIntegrityCheckFailed
		}
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("Local: SHA256 integrity check passed for file '%s'.", filePath)
		}
	} else {
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("Local: SHA256 integrity check skipped for file '%s' (no expected hash provided).", filePath)
		}
	}

	return nil
}

// CancelUpload cancels an ongoing local upload session and removes the incomplete file.
func (p *LocalFilesystemProvider) CancelUpload(claims *auth.UserClaims, filePath string) error {
	userIdent := "unauthenticated"
	if claims != nil {
		userIdent = claims.Email
	}
	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("LocalFilesystemProvider.CancelUpload chiamato da utente '%s' per storage '%s', path '%s'", userIdent, p.name, filePath)
	}

	uploadKey := fmt.Sprintf("%s:%s", p.name, filePath)
	localUploadSessionsMutex.Lock()
	session, ok := localOngoingUploadSessions[uploadKey]
	if ok {
		delete(localOngoingUploadSessions, uploadKey) // Rimuovi dalla mappa
	}
	localUploadSessionsMutex.Unlock()

	if !ok || session == nil { // session.TempFile potrebbe essere nil se è già stato chiuso/rimosso
		return errors.New("no ongoing local upload session found for cancellation or session invalid")
	}

	// Segnala alla goroutine di scrittura di terminare e chiudi il canale
	close(session.chunkBuffer)
	close(session.done)
	session.writerWg.Wait() // Attendi che la goroutine di scrittura abbia terminato

	session.mu.Lock() // Blocca la sessione durante l'annullamento
	defer session.mu.Unlock()

	if session.TempFile != nil {
		session.TempFile.Close() // Chiudi il file handle del temporaneo
	}
	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("Local upload cancelled for storage '%s', path '%s'. Attempting to remove incomplete temporary file '%s'.", p.name, filePath, session.TempFile.Name())
	}

	removeErr := os.Remove(session.TempFile.Name()) // Rimuovi il file temporaneo
	if removeErr != nil {
		log.Printf("Error removing incomplete local temporary file '%s' after cancellation: %v", session.TempFile.Name(), removeErr)
		return fmt.Errorf("error removing incomplete local temporary file: %w", removeErr)
	}
	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("Incomplete local temporary file '%s' removed after cancellation.", session.TempFile.Name())
	}
	return nil
}

// GetUploadedSize returns the current size of a local file being uploaded.
func (p *LocalFilesystemProvider) GetUploadedSize(claims *auth.UserClaims, filePath string) (int64, error) {
	userIdent := "unauthenticated"
	if claims != nil {
		userIdent = claims.Email
	}
	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("LocalFilesystemProvider.GetUploadedSize chiamato da utente '%s' per storage '%s', path '%s'", userIdent, p.name, filePath)
	}

	uploadKey := fmt.Sprintf("%s:%s", p.name, filePath)
	localUploadSessionsMutex.Lock()
	session, ok := localOngoingUploadSessions[uploadKey]
	localUploadSessionsMutex.Unlock()

	if !ok || session == nil || session.TempFile == nil {
		// Se non c'è una sessione in corso, il file non è stato ancora caricato o è stato completato/annullato.
		// In questo caso, controlliamo la dimensione del file finale se esiste.
		fullPath, err := p.validatePath(filePath)
		if err != nil {
			return 0, fmt.Errorf("path validation error: %w", err)
		}
		fileInfo, err := os.Stat(fullPath)
		if os.IsNotExist(err) {
			return 0, nil // File non esiste, dimensione 0
		} else if err != nil {
			return 0, fmt.Errorf("error getting local file info '%s': %w", fullPath, err)
		}
		return fileInfo.Size(), nil
	}

	// Se c'è una sessione in corso, restituisci la dimensione del file temporaneo
	session.mu.Lock()
	defer session.mu.Unlock()

	fileInfo, err := session.TempFile.Stat()
	if err != nil {
		return 0, fmt.Errorf("error getting temp file info for upload status '%s': %w", session.TempFile.Name(), err)
	}

	return fileInfo.Size(), nil
}

var _ storage.StorageProvider = (*LocalFilesystemProvider)(nil)
