package azureblob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"path/filepath" // Importato per filepath.Base
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"clouddav/auth"
	"clouddav/config"
	"clouddav/storage"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
)

// AzureBlobStorageProvider implements the StorageProvider interface for Azure Blob Storage.
type AzureBlobStorageProvider struct {
	name            string
	containerName   string
	containerClient *container.Client
}

// NewProvider creates a new AzureBlobStorageProvider.
func NewProvider(cfg *config.StorageConfig) (*AzureBlobStorageProvider, error) {
	if cfg.Type != "azure-blob" {
		return nil, errors.New("invalid storage config type for azure-blob provider")
	}
	if cfg.ContainerName == "" {
		return nil, errors.New("azure-blob storage container_name is required")
	}
	if cfg.AccountName == "" && cfg.ConnectionString == "" {
		return nil, errors.New("azure-blob storage requires either account_name (for AAD) or connection_string")
	}

	var cred azcore.TokenCredential
	var containerClient *container.Client
	var err error

	if cfg.ConnectionString != "" {
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("Azure Blob: Connecting to '%s' container '%s' using connection string (configured)...", cfg.Name, cfg.ContainerName)
		}
		containerClient, err = container.NewClientFromConnectionString(cfg.ConnectionString, cfg.ContainerName, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create Azure Blob container client from connection string: %w", err)
		}
	} else {
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("Using AAD Authentication")
		}
		accountURL := fmt.Sprintf("https://%s.blob.core.windows.net", cfg.AccountName)
		containerURL := fmt.Sprintf("%s/%s", accountURL, cfg.ContainerName)

		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("Azure Blob: Attempting authentication with Azure Identity (DefaultAzureCredential) for storage '%s'...", cfg.Name)
		}
		cred, err = azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create Azure Identity credential (DefaultAzureCredential) for storage '%s': %w", cfg.Name, err)
		}
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("Azure Blob: Azure Identity credential created successfully.")
		}

		containerClient, err = container.NewClient(containerURL, cred, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create Azure Blob container client with credential for storage '%s': %w", cfg.Name, err)
		}
	}

	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("Azure Blob: Provider '%s' initialized for container '%s'.", cfg.Name, cfg.ContainerName)
	}

	return &AzureBlobStorageProvider{
		name:            cfg.Name,
		containerName:   cfg.ContainerName,
		containerClient: containerClient,
	}, nil
}

// Type returns the storage type.
func (p *AzureBlobStorageProvider) Type() string {
	return "azure-blob"
}

// Name returns the configured name.
func (p *AzureBlobStorageProvider) Name() string {
	return p.name
}

// ListItems lists blobs and virtual directories in a given path (prefix).
func (p *AzureBlobStorageProvider) ListItems(ctx context.Context, claims *auth.UserClaims, path string, page int, itemsPerPage int, nameFilter string, timestampFilter *time.Time, onlyDirectories bool) (*storage.ListItemsResponse, error) {
	userIdent := "unauthenticated"
	if claims != nil {
		userIdent = claims.Email
	}
	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("AzureBlobStorageProvider.ListItems chiamato da utente '%s' per storage '%s', path '%s', page %d, itemsPerPage %d, nameFilter '%s', onlyDirectories: %t", userIdent, p.name, path, page, itemsPerPage, nameFilter, onlyDirectories)
	}

	prefix := strings.TrimPrefix(path, "/")
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("Azure Blob: Listing items in container '%s' with prefix '%s' for storage '%s'", p.containerName, prefix, p.name)
	}

	azureMaxResults := int32(itemsPerPage * 2) // Richiedi un po' di più per permettere il filtraggio
	if azureMaxResults == 0 {
		azureMaxResults = 100 // Un default ragionevole
	}
	if onlyDirectories && azureMaxResults < 50 { // Se si richiedono solo directory, potrebbe essere necessario un buffer maggiore
		azureMaxResults = 50
	}

	h_pager := p.containerClient.NewListBlobsHierarchyPager("/", &container.ListBlobsHierarchyOptions{
		Prefix:     to.Ptr(prefix),
		MaxResults: to.Ptr(azureMaxResults),
	})

	allFilteredItems := []storage.ItemInfo{}
	// La variabile itemsCount non è più necessaria qui, la logica del loop sottostante è sufficiente.

	// Loop per raccogliere abbastanza items per la pagina richiesta, considerando il filtraggio
	// Questo loop esterno è per la paginazione logica dell'applicazione.
	// Il loop interno h_pager.More() è per la paginazione dell'SDK Azure.
	for len(allFilteredItems) < page*itemsPerPage && h_pager.More() {
		pageResponse, err := h_pager.NextPage(ctx)
		if err != nil {
			select {
			case <-ctx.Done():
				if config.IsLogLevel(config.LogLevelDebug) {
					log.Printf("Context cancelled during Azure Blob listing: %v", ctx.Err())
				}
				return nil, ctx.Err()
			default:
			}
			return nil, fmt.Errorf("failed to list blobs for prefix '%s': %w", prefix, err)
		}

		if pageResponse.Segment != nil {
			if pageResponse.Segment.BlobPrefixes != nil {
				for _, bp := range pageResponse.Segment.BlobPrefixes {
					name := strings.TrimPrefix(*bp.Name, prefix)
					name = strings.TrimSuffix(name, "/")
					if name == "" {
						continue
					}
					itemInfo := storage.ItemInfo{
						Name:    name,
						IsDir:   true,
						Size:    0,
						ModTime: time.Time{}, // No modification time for virtual directories
						Path:    strings.TrimSuffix(*bp.Name, "/"),
					}
					if nameFilter != "" {
						matched, _ := regexp.MatchString(nameFilter, itemInfo.Name)
						if !matched {
							continue
						}
					}
					// Il timestampFilter non si applica alle directory virtuali
					allFilteredItems = append(allFilteredItems, itemInfo)
				}
			}

			if !onlyDirectories {
				if pageResponse.Segment.BlobItems != nil {
					for _, blobItem := range pageResponse.Segment.BlobItems {
						name := strings.TrimPrefix(*blobItem.Name, prefix)
						if strings.Contains(name, "/") { // Ignora gli item che sono in "sottocartelle" rispetto al prefix corrente
							continue
						}

						itemInfo := storage.ItemInfo{
							Name:    name,
							IsDir:   false,
							Size:    *blobItem.Properties.ContentLength,
							ModTime: *blobItem.Properties.LastModified,
							Path:    *blobItem.Name,
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
						allFilteredItems = append(allFilteredItems, itemInfo)
					}
				}
			}
		}
	}

	// Ora che abbiamo tutti gli item filtrati (potenzialmente da più pagine Azure), ordiniamoli
	sort.SliceStable(allFilteredItems, func(i, j int) bool {
		if allFilteredItems[i].IsDir != allFilteredItems[j].IsDir {
			return allFilteredItems[i].IsDir
		}
		return allFilteredItems[i].Name < allFilteredItems[j].Name
	})

	// Applica la paginazione logica
	totalItems := len(allFilteredItems) // << MODIFICA: Dichiarazione corretta con :=

	startIndex := (page - 1) * itemsPerPage
	endIndex := startIndex + itemsPerPage

	if startIndex >= totalItems {
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

	paginatedItems := allFilteredItems[startIndex:endIndex]
	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("Azure Blob: Returning %d items for page %d (total filtered: %d, onlyDirs: %t)", len(paginatedItems), page, totalItems, onlyDirectories)
	}

	return &storage.ListItemsResponse{
		Items:        paginatedItems,
		TotalItems:   totalItems,
		Page:         page,
		ItemsPerPage: itemsPerPage,
	}, nil
}

// GetItem retrieves information about a single blob.
func (p *AzureBlobStorageProvider) GetItem(ctx context.Context, claims *auth.UserClaims, path string) (*storage.ItemInfo, error) {
	userIdent := "unauthenticated"
	if claims != nil {
		userIdent = claims.Email
	}
	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("AzureBlobStorageProvider.GetItem chiamato da utente '%s' per storage '%s', path '%s'", userIdent, p.name, path)
	}

	blobPath := strings.TrimPrefix(path, "/")

	blobClient := p.containerClient.NewBlobClient(blobPath)

	props, err := blobClient.GetProperties(ctx, nil)
	if err != nil {
		var storageErr *azcore.ResponseError
		if errors.As(err, &storageErr) && storageErr.StatusCode == 404 {
			// Potrebbe essere una directory virtuale, controlla se esistono blob con quel prefisso
			prefixToCheck := blobPath
			if prefixToCheck != "" && !strings.HasSuffix(prefixToCheck, "/") {
				prefixToCheck += "/"
			}
			pager := p.containerClient.NewListBlobsHierarchyPager("/", &container.ListBlobsHierarchyOptions{
				Prefix:     to.Ptr(prefixToCheck),
				MaxResults: to.Ptr(int32(1)), // Basta uno per confermare l'esistenza
			})

			pageResponse, listErr := pager.NextPage(ctx)
			if listErr == nil && pageResponse.Segment != nil &&
				(len(pageResponse.Segment.BlobPrefixes) > 0 || len(pageResponse.Segment.BlobItems) > 0) {
				// Esiste qualcosa sotto questo prefisso, consideralo una directory
				return &storage.ItemInfo{
					Name:    filepath.Base(path), // Nome base del percorso
					IsDir:   true,
					Size:    0, // Le directory virtuali non hanno dimensione
					ModTime: time.Time{}, // Nessun ModTime per le directory virtuali
					Path:    path,
				}, nil
			}
			return nil, storage.ErrNotFound // Nessun blob e nessun prefisso trovato
		}
		return nil, fmt.Errorf("failed to get blob properties for '%s': %w", blobPath, err)
	}

	itemInfo := &storage.ItemInfo{
		Name:    filepath.Base(path), // Nome base del percorso
		IsDir:   false, // Se GetProperties ha successo, è un blob (file)
		Size:    *props.ContentLength,
		ModTime: *props.LastModified,
		Path:    path,
	}

	return itemInfo, nil
}

// OpenReader opens a blob for reading, returning an io.ReadCloser.
func (p *AzureBlobStorageProvider) OpenReader(ctx context.Context, claims *auth.UserClaims, path string) (io.ReadCloser, error) {
	userIdent := "unauthenticated"
	if claims != nil {
		userIdent = claims.Email
	}
	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("AzureBlobStorageProvider.OpenReader chiamato da utente '%s' per storage '%s', path '%s'", userIdent, p.name, path)
	}

	blobPath := strings.TrimPrefix(path, "/")

	// Verifica prima se è una directory virtuale, perché non può essere letta
	itemInfo, err := p.GetItem(ctx, claims, path) // Riusa GetItem per la logica di directory virtuale
	if err != nil {
		return nil, err // GetItem gestisce ErrNotFound e altri errori
	}
	if itemInfo.IsDir {
		return nil, errors.New("cannot open a directory for reading")
	}

	blobClient := p.containerClient.NewBlobClient(blobPath)
	downloadResponse, err := blobClient.DownloadStream(ctx, nil)
	if err != nil {
		var storageErr *azcore.ResponseError
		if errors.As(err, &storageErr) && storageErr.StatusCode == 403 {
			return nil, storage.ErrPermissionDenied
		}
		return nil, fmt.Errorf("failed to download blob stream for '%s': %w", blobPath, err)
	}

	return downloadResponse.Body, nil
}

// CreateDirectory simulates creating a virtual directory (a zero-byte blob ending with '/').
func (p *AzureBlobStorageProvider) CreateDirectory(ctx context.Context, claims *auth.UserClaims, path string) error {
	userIdent := "unauthenticated"
	if claims != nil {
		userIdent = claims.Email
	}
	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("AzureBlobStorageProvider.CreateDirectory chiamato da utente '%s' per storage '%s', path '%s'", userIdent, p.name, path)
	}

	dirBlobPath := strings.TrimPrefix(path, "/")
	if !strings.HasSuffix(dirBlobPath, "/") {
		dirBlobPath += "/"
	}

	// Controlla se esiste già come blob o come prefisso con contenuto
	itemInfo, err := p.GetItem(ctx, claims, strings.TrimSuffix(dirBlobPath, "/"))
	if err == nil && itemInfo != nil { // Se GetItem ha successo, qualcosa esiste
		return storage.ErrAlreadyExists
	}
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return fmt.Errorf("failed to check for existing virtual directory '%s': %w", dirBlobPath, err)
	}
	// Se ErrNotFound, possiamo procedere a creare il marker

	dirMarkerBlobClient := p.containerClient.NewBlockBlobClient(dirBlobPath)
	uploadResp, err := dirMarkerBlobClient.UploadBuffer(ctx, []byte{}, nil)
	if err != nil {
		var storageErr *azcore.ResponseError
		if errors.As(err, &storageErr) && storageErr.StatusCode == 403 {
			return storage.ErrPermissionDenied
		}
		return fmt.Errorf("failed to create virtual directory blob '%s': %w", dirBlobPath, err)
	}

	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("Azure Blob: Created virtual directory marker blob: %s", *uploadResp.ETag)
	}

	return nil
}

// DeleteItem deletes a blob or all blobs under a prefix (for virtual directories).
func (p *AzureBlobStorageProvider) DeleteItem(ctx context.Context, claims *auth.UserClaims, path string) error {
	userIdent := "unauthenticated"
	if claims != nil {
		userIdent = claims.Email
	}
	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("AzureBlobStorageProvider.DeleteItem chiamato da utente '%s' per storage '%s', path '%s'", userIdent, p.name, path)
	}

	blobPath := strings.TrimPrefix(path, "/")

	// Determina se è un file o una directory (virtuale)
	itemInfo, err := p.GetItem(ctx, claims, path)
	if errors.Is(err, storage.ErrNotFound) {
		return storage.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("failed to get item properties before deleting '%s': %w", blobPath, err)
	}

	if !itemInfo.IsDir { // È un singolo blob (file)
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("Azure Blob: Deleting blob '%s' in container '%s'", blobPath, p.containerName)
		}
		blobClient := p.containerClient.NewBlobClient(blobPath)
		_, deleteErr := blobClient.Delete(ctx, nil)
		if deleteErr != nil {
			var deleteStorageErr *azcore.ResponseError
			if errors.As(deleteErr, &deleteStorageErr) && deleteStorageErr.StatusCode == 403 {
				return storage.ErrPermissionDenied
			}
			return fmt.Errorf("failed to delete blob '%s': %w", blobPath, deleteErr)
		}
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("Azure Blob: Deleted blob '%s'", blobPath)
		}
		return nil
	}

	// È una directory virtuale, elimina tutti i blob con quel prefisso
	prefix := blobPath
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("Azure Blob: Deleting virtual directory (blobs with prefix) '%s' in container '%s'", prefix, p.containerName)
	}

	pager := p.containerClient.NewListBlobsFlatPager(&container.ListBlobsFlatOptions{
		Prefix: to.Ptr(prefix),
	})

	blobsToDelete := []string{}
	for pager.More() {
		pageResponse, listErr := pager.NextPage(ctx)
		if listErr != nil {
			select {
			case <-ctx.Done():
				if config.IsLogLevel(config.LogLevelDebug) {
					log.Printf("Context cancelled during Azure Blob delete listing: %v", ctx.Err())
				}
				return ctx.Err()
			default:
			}
			return fmt.Errorf("failed to list blobs for deletion with prefix '%s': %w", prefix, listErr)
		}
		if pageResponse.Segment != nil {
			for _, blobItem := range pageResponse.Segment.BlobItems {
				blobsToDelete = append(blobsToDelete, *blobItem.Name)
			}
		}
	}

	// Aggiungi il marker della directory stessa se esiste (es. path/ a zero byte)
	dirMarkerPath := prefix
	if len(blobsToDelete) == 0 { // Se non ci sono blob sotto il prefisso, potrebbe esistere solo il marker
		markerClient := p.containerClient.NewBlobClient(dirMarkerPath)
		_, markerErr := markerClient.GetProperties(ctx, nil)
		if markerErr == nil { // Il marker esiste
			blobsToDelete = append(blobsToDelete, dirMarkerPath)
		} else {
			var markerStorageErr *azcore.ResponseError
			if !errors.As(markerErr, &markerStorageErr) || markerStorageErr.StatusCode != 404 {
				log.Printf("Warning: Failed to check for directory marker blob '%s' during delete: %v", dirMarkerPath, markerErr)
			}
		}
	}

	if len(blobsToDelete) == 0 {
		return storage.ErrNotFound // Nessun blob o marker trovato da eliminare
	}

	var wg sync.WaitGroup
	errChan := make(chan error, len(blobsToDelete))

	maxConcurrency := runtime.NumCPU() * 4
	if maxConcurrency == 0 {
		maxConcurrency = 4
	}
	sem := make(chan struct{}, maxConcurrency)

	for _, blobNameToDelete := range blobsToDelete {
		select {
		case <-ctx.Done():
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Printf("Context cancelled during Azure Blob deletion of '%s': %v", blobNameToDelete, ctx.Err())
			}
			return ctx.Err()
		case sem <- struct{}{}:
			wg.Add(1)
			go func(name string) {
				defer wg.Done()
				defer func() { <-sem }()

				blobClientToDelete := p.containerClient.NewBlobClient(name)
				_, deleteErr := blobClientToDelete.Delete(ctx, nil)
				if deleteErr != nil {
					var deleteStorageErr *azcore.ResponseError
					if errors.As(deleteErr, &deleteStorageErr) && deleteStorageErr.StatusCode == 403 {
						errChan <- storage.ErrPermissionDenied
					} else if errors.As(deleteErr, &deleteStorageErr) && deleteStorageErr.StatusCode == 404 {
						if config.IsLogLevel(config.LogLevelDebug) {
							log.Printf("Azure Blob: Blob '%s' not found during deletion, already deleted?", name)
						}
					} else {
						errChan <- fmt.Errorf("failed to delete blob '%s': %w", name, deleteErr)
					}
				} else {
					if config.IsLogLevel(config.LogLevelDebug) {
						log.Printf("Azure Blob: Deleted blob '%s'", name)
					}
				}
			}(blobNameToDelete)
		}
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		if err != nil {
			return err
		}
	}

	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("Azure Blob: Virtual directory deletion complete for prefix '%s'", prefix)
	}
	return nil
}

// InitiateUpload starts a new upload session for a block blob.
func (p *AzureBlobStorageProvider) InitiateUpload(ctx context.Context, claims *auth.UserClaims, blobPath string, totalFileSize int64, chunkSize int64) (int64, error) {
	userIdent := "unauthenticated"
	if claims != nil {
		userIdent = claims.Email
	}
	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("AzureBlobStorageProvider.InitiateUpload chiamato da utente '%s' per storage '%s', path '%s'", userIdent, p.name, blobPath)
	}

	blobPath = strings.TrimPrefix(blobPath, "/")

	itemInfo, err := p.GetItem(ctx, claims, blobPath)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return 0, nil 
		}
		return 0, fmt.Errorf("failed to check existing blob for upload '%s': %w", blobPath, err)
	}

	if itemInfo.IsDir { 
		return 0, errors.New("cannot upload to a virtual directory path")
	}
	
	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("AzureBlob.InitiateUpload: Blob '%s' exists with size %d. Client should handle resume logic.", blobPath, itemInfo.Size)
	}
	return 0, nil 
}

// WriteChunk uploads a block to a block blob.
func (p *AzureBlobStorageProvider) WriteChunk(ctx context.Context, claims *auth.UserClaims, blobPath string, blockID string, chunk io.ReadSeekCloser, chunkIndex int64) error {
	userIdent := "unauthenticated"
	if claims != nil {
		userIdent = claims.Email
	}
	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("AzureBlobStorageProvider.WriteChunk chiamato da utente '%s' per storage '%s', path '%s', blockID '%s', chunkIndex %d", userIdent, p.name, blobPath, blockID, chunkIndex)
	}

	blobPath = strings.TrimPrefix(blobPath, "/")

	blockBlobClient := p.containerClient.NewBlockBlobClient(blobPath)

	_, err := blockBlobClient.StageBlock(ctx, blockID, chunk, nil)
	if err != nil {
		var storageErr *azcore.ResponseError
		if errors.As(err, &storageErr) && storageErr.StatusCode == 403 {
			return storage.ErrPermissionDenied
		}
		return fmt.Errorf("failed to stage block '%s' for blob '%s': %w", blockID, blobPath, err)
	}

	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("Azure Blob: Staged block '%s' for blob '%s'", blockID, blobPath)
	}
	return nil
}

// FinalizeUpload commits the blocks to form the final block blob and performs SHA256 integrity check.
func (p *AzureBlobStorageProvider) FinalizeUpload(ctx context.Context, claims *auth.UserClaims, blobPath string, blockIDs []string, expectedSHA256 string) error {
	userIdent := "unauthenticated"
	if claims != nil {
		userIdent = claims.Email
	}
	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("AzureBlobStorageProvider.FinalizeUpload chiamato da utente '%s' per storage '%s', path '%s' con %d blocchi. SHA256 atteso: %s", userIdent, p.name, blobPath, len(blockIDs), expectedSHA256)
	}

	blobPath = strings.TrimPrefix(blobPath, "/")

	blockBlobClient := p.containerClient.NewBlockBlobClient(blobPath)

	_, err := blockBlobClient.CommitBlockList(ctx, blockIDs, nil)
	if err != nil {
		var storageErr *azcore.ResponseError
		if errors.As(err, &storageErr) && storageErr.StatusCode == 403 {
			return storage.ErrPermissionDenied
		}
		return fmt.Errorf("failed to commit block list for blob '%s': %w", blobPath, err)
	}

	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("Azure Blob: Committed block list for blob '%s'. Starting integrity check.", blobPath)
	}

	if expectedSHA256 != "" {
		downloadResponse, err := blockBlobClient.DownloadStream(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to download blob for SHA256 verification: %w", err)
		}
		defer downloadResponse.Body.Close()

		hasher := sha256.New()
		if _, err := io.Copy(hasher, downloadResponse.Body); err != nil {
			return fmt.Errorf("failed to hash downloaded blob for SHA256 verification: %w", err)
		}
		calculatedSHA256 := hex.EncodeToString(hasher.Sum(nil))

		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("Azure Blob: Calculated SHA256 for '%s': %s", blobPath, calculatedSHA256)
			log.Printf("Azure Blob: Expected SHA256 for '%s': %s", blobPath, expectedSHA256)
		}

		if calculatedSHA256 != expectedSHA256 {
			log.Printf("Error: SHA256 mismatch for blob '%s'. Calculated: %s, Expected: %s", blobPath, calculatedSHA256, expectedSHA256)
			return storage.ErrIntegrityCheckFailed
		}
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("Azure Blob: SHA256 integrity check passed for blob '%s'.", blobPath)
		}
	} else {
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("Azure Blob: SHA256 integrity check skipped for blob '%s' (no expected hash provided).", blobPath)
		}
	}

	return nil
}

// CancelUpload aborts an ongoing block blob upload.
func (p *AzureBlobStorageProvider) CancelUpload(ctx context.Context, claims *auth.UserClaims, blobPath string) error {
	userIdent := "unauthenticated"
	if claims != nil {
		userIdent = claims.Email
	}
	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("AzureBlobStorageProvider.CancelUpload chiamato da utente '%s' per storage '%s', path '%s'", userIdent, p.name, blobPath)
	}

	blobPath = strings.TrimPrefix(blobPath, "/")
	blobClient := p.containerClient.NewBlobClient(blobPath)

	_, err := blobClient.Delete(ctx, nil)
	if err != nil {
		var storageErr *azcore.ResponseError
		if errors.As(err, &storageErr) && storageErr.StatusCode == 404 {
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Printf("Azure Blob: No existing blob found to delete during cancel for '%s'", blobPath)
			}
			return nil
		}
		if errors.As(err, &storageErr) && storageErr.StatusCode == 403 {
			return storage.ErrPermissionDenied
		}
		return fmt.Errorf("failed to delete blob during cancel for '%s': %w", blobPath, err)
	}

	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("Azure Blob: Deleted existing blob '%s' during cancel (staged blocks will expire).", blobPath)
	}
	return nil
}

// GetUploadedSize returns the current size of a blob.
func (p *AzureBlobStorageProvider) GetUploadedSize(ctx context.Context, claims *auth.UserClaims, blobPath string) (int64, error) {
	userIdent := "unauthenticated"
	if claims != nil {
		userIdent = claims.Email
	}
	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("AzureBlobStorageProvider.GetUploadedSize chiamato da utente '%s' per storage '%s', path '%s'", userIdent, p.name, blobPath)
	}

	blobPath = strings.TrimPrefix(blobPath, "/")

	itemInfo, err := p.GetItem(ctx, claims, blobPath)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return 0, nil 
		}
		return 0, fmt.Errorf("failed to get blob size for upload status '%s': %w", blobPath, err)
	}

	if itemInfo.IsDir {
		return 0, errors.New("cannot get size for a virtual directory path")
	}

	return itemInfo.Size, nil
}

var _ storage.StorageProvider = (*AzureBlobStorageProvider)(nil)
